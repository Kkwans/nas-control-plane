package system

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	staticDNSPreviewTTL = 5 * time.Minute
	staticDNSMaxSize    = 64 * 1024
)

type staticDNSPreview struct {
	before    []byte
	after     []byte
	original  os.FileMode
	expiresAt time.Time
}

type staticDNSChange struct {
	beforeHash string
	afterHash  string
	backupPath string
	appliedAt  time.Time
}

type staticDNSChangeManifest struct {
	ChangeID   string    `json:"changeId"`
	BeforeHash string    `json:"beforeHash"`
	AfterHash  string    `json:"afterHash"`
	AppliedAt  time.Time `json:"appliedAt"`
}

// StaticResolvDNSController manages one explicitly configured regular
// resolv.conf file. It rejects symlinks and concurrent edits and keeps every
// applied change recoverable from a root-only backup.
type StaticResolvDNSController struct {
	path      string
	backupDir string
	now       func() time.Time

	mu       sync.Mutex
	previews map[string]staticDNSPreview
	changes  map[string]staticDNSChange
}

func NewStaticResolvDNSController(path, backupDir string) (*StaticResolvDNSController, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	backupDir = filepath.Clean(strings.TrimSpace(backupDir))
	if !filepath.IsAbs(path) || !filepath.IsAbs(backupDir) || path == string(filepath.Separator) || backupDir == string(filepath.Separator) {
		return nil, errors.New("DNS_STATIC_PATH_INVALID")
	}
	if _, _, err := readStaticDNSFile(path); err != nil {
		return nil, err
	}
	return &StaticResolvDNSController{
		path: path, backupDir: backupDir, now: time.Now,
		previews: make(map[string]staticDNSPreview), changes: make(map[string]staticDNSChange),
	}, nil
}

func (c *StaticResolvDNSController) CurrentDNSState(ctx context.Context) (DNSState, error) {
	if err := ctx.Err(); err != nil {
		return DNSState{}, err
	}
	before, _, err := readStaticDNSFile(c.path)
	if err != nil {
		return DNSState{}, err
	}
	return DNSState{
		Nameservers:   parseNameservers(string(before)),
		SearchDomains: parseSearchDomains(string(before)),
	}, nil
}

func (c *StaticResolvDNSController) Preview(ctx context.Context, request DNSChangeRequest) (DNSChangePreview, error) {
	if err := ctx.Err(); err != nil {
		return DNSChangePreview{Backend: DNSBackendStaticResolv, ErrorCode: "DNS_CHANGE_CANCELED"}, err
	}
	nameservers, searchDomains, err := normalizeStaticDNSRequest(request)
	if err != nil {
		return DNSChangePreview{Backend: DNSBackendStaticResolv, ErrorCode: err.Error()}, err
	}
	before, mode, err := readStaticDNSFile(c.path)
	if err != nil {
		return DNSChangePreview{Backend: DNSBackendStaticResolv, ErrorCode: errorCode(err)}, err
	}
	currentSearch := parseSearchDomains(string(before))
	if request.SearchDomains == nil {
		searchDomains = currentSearch
	}
	after := renderStaticDNSFile(before, nameservers, searchDomains, request.SearchDomains != nil)
	previewID, err := randomDNSID("dns-preview")
	if err != nil {
		return DNSChangePreview{Backend: DNSBackendStaticResolv, ErrorCode: "DNS_CHANGE_ID_FAILED"}, err
	}
	expiresAt := c.now().UTC().Add(staticDNSPreviewTTL)
	preview := DNSChangePreview{
		PreviewID: previewID, Backend: DNSBackendStaticResolv,
		Before:          DNSState{Nameservers: parseNameservers(string(before)), SearchDomains: currentSearch},
		After:           DNSState{Nameservers: nameservers, SearchDomains: searchDomains},
		RequiresConfirm: true, RollbackAvailable: true, ExpiresAt: expiresAt,
	}
	c.mu.Lock()
	c.pruneExpiredPreviewsLocked()
	c.previews[previewID] = staticDNSPreview{
		before: append([]byte(nil), before...), after: append([]byte(nil), after...),
		original: mode, expiresAt: expiresAt,
	}
	c.mu.Unlock()
	return preview, nil
}

func (c *StaticResolvDNSController) Confirm(ctx context.Context, confirmation DNSChangeConfirmation) (DNSChangeResult, error) {
	result := DNSChangeResult{Backend: DNSBackendStaticResolv}
	if !confirmation.Confirmed || strings.TrimSpace(confirmation.PreviewID) == "" {
		result.ErrorCode = "DNS_CONFIRMATION_REQUIRED"
		return result, errors.New(result.ErrorCode)
	}
	if err := ctx.Err(); err != nil {
		result.ErrorCode = "DNS_CHANGE_CANCELED"
		return result, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	preview, ok := c.previews[confirmation.PreviewID]
	if !ok {
		result.ErrorCode = "DNS_PREVIEW_NOT_FOUND"
		return result, errors.New(result.ErrorCode)
	}
	if !c.now().UTC().Before(preview.expiresAt) {
		delete(c.previews, confirmation.PreviewID)
		result.ErrorCode = "DNS_PREVIEW_EXPIRED"
		return result, errors.New(result.ErrorCode)
	}
	current, _, err := readStaticDNSFile(c.path)
	if err != nil {
		result.ErrorCode = errorCode(err)
		return result, err
	}
	if !bytes.Equal(current, preview.before) {
		result.ErrorCode = "DNS_SOURCE_CHANGED"
		return result, errors.New(result.ErrorCode)
	}
	changeID, err := randomDNSID("dns-change")
	if err != nil {
		result.ErrorCode = "DNS_CHANGE_ID_FAILED"
		return result, err
	}
	backupPath, err := c.writeBackup(changeID, preview.before)
	if err != nil {
		result.ErrorCode = "DNS_BACKUP_FAILED"
		return result, err
	}
	appliedAt := c.now().UTC()
	change := staticDNSChange{
		beforeHash: contentHash(preview.before), afterHash: contentHash(preview.after),
		backupPath: backupPath, appliedAt: appliedAt,
	}
	// Persist the rollback record before replacing the live file. If the Agent
	// exits immediately after the rename, the next process can still restore it.
	if err := c.writeChangeManifest(changeID, change); err != nil {
		result.ErrorCode = "DNS_CHANGE_RECORD_FAILED"
		return result, err
	}
	if err := atomicWriteStaticDNSFile(c.path, preview.after, preview.original); err != nil {
		result.ErrorCode = "DNS_APPLY_FAILED"
		return result, err
	}
	verified, _, err := readStaticDNSFile(c.path)
	if err != nil || !bytes.Equal(verified, preview.after) {
		_ = atomicWriteStaticDNSFile(c.path, preview.before, preview.original)
		result.ErrorCode = "DNS_APPLY_VERIFICATION_FAILED"
		return result, errors.New(result.ErrorCode)
	}
	c.changes[changeID] = change
	delete(c.previews, confirmation.PreviewID)
	return DNSChangeResult{
		ChangeID: changeID, Backend: DNSBackendStaticResolv, Applied: true,
		RollbackAvailable: true, AppliedAt: appliedAt,
	}, nil
}

func (c *StaticResolvDNSController) Rollback(ctx context.Context, request DNSRollbackRequest) (DNSChangeResult, error) {
	result := DNSChangeResult{Backend: DNSBackendStaticResolv, ChangeID: strings.TrimSpace(request.ChangeID)}
	if result.ChangeID == "" {
		result.ErrorCode = "DNS_CHANGE_NOT_FOUND"
		return result, errors.New(result.ErrorCode)
	}
	if err := ctx.Err(); err != nil {
		result.ErrorCode = "DNS_CHANGE_CANCELED"
		return result, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if !validDNSChangeID(result.ChangeID) {
		result.ErrorCode = "DNS_CHANGE_NOT_FOUND"
		return result, errors.New(result.ErrorCode)
	}
	change, ok := c.changes[result.ChangeID]
	if !ok {
		var err error
		change, err = c.loadChange(result.ChangeID)
		if err != nil {
			result.ErrorCode = "DNS_CHANGE_NOT_FOUND"
			return result, errors.New(result.ErrorCode)
		}
	}
	current, mode, err := readStaticDNSFile(c.path)
	if err != nil {
		result.ErrorCode = errorCode(err)
		return result, err
	}
	if contentHash(current) != change.afterHash {
		result.ErrorCode = "DNS_SOURCE_CHANGED"
		return result, errors.New(result.ErrorCode)
	}
	backup, err := os.ReadFile(change.backupPath)
	if err != nil || contentHash(backup) != change.beforeHash {
		result.ErrorCode = "DNS_BACKUP_INVALID"
		return result, errors.New(result.ErrorCode)
	}
	if err := atomicWriteStaticDNSFile(c.path, backup, mode); err != nil {
		result.ErrorCode = "DNS_ROLLBACK_FAILED"
		return result, err
	}
	verified, _, err := readStaticDNSFile(c.path)
	if err != nil || contentHash(verified) != change.beforeHash {
		result.ErrorCode = "DNS_ROLLBACK_VERIFICATION_FAILED"
		return result, errors.New(result.ErrorCode)
	}
	delete(c.changes, result.ChangeID)
	return DNSChangeResult{
		ChangeID: result.ChangeID, Backend: DNSBackendStaticResolv,
		Applied: false, RollbackAvailable: false, AppliedAt: change.appliedAt,
	}, nil
}

func (c *StaticResolvDNSController) pruneExpiredPreviewsLocked() {
	now := c.now().UTC()
	for key, preview := range c.previews {
		if !now.Before(preview.expiresAt) {
			delete(c.previews, key)
		}
	}
}

func (c *StaticResolvDNSController) writeBackup(changeID string, content []byte) (string, error) {
	if err := os.MkdirAll(c.backupDir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(c.backupDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(c.backupDir, changeID+".resolv.conf.bak")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	writeErr := error(nil)
	if _, err = file.Write(content); err != nil {
		writeErr = err
	} else if err = file.Sync(); err != nil {
		writeErr = err
	}
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return "", writeErr
	}
	return path, nil
}

func (c *StaticResolvDNSController) writeChangeManifest(changeID string, change staticDNSChange) error {
	manifest := staticDNSChangeManifest{
		ChangeID: changeID, BeforeHash: change.beforeHash, AfterHash: change.afterHash, AppliedAt: change.appliedAt,
	}
	content, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	content = append(content, '\n')
	path := filepath.Join(c.backupDir, changeID+".json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	writeErr := error(nil)
	if _, err = file.Write(content); err != nil {
		writeErr = err
	} else if err = file.Sync(); err != nil {
		writeErr = err
	}
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	return writeErr
}

func (c *StaticResolvDNSController) loadChange(changeID string) (staticDNSChange, error) {
	content, err := os.ReadFile(filepath.Join(c.backupDir, changeID+".json"))
	if err != nil || len(content) > 4096 {
		return staticDNSChange{}, errors.New("DNS_CHANGE_NOT_FOUND")
	}
	var manifest staticDNSChangeManifest
	if err := json.Unmarshal(content, &manifest); err != nil || manifest.ChangeID != changeID || manifest.BeforeHash == "" || manifest.AfterHash == "" {
		return staticDNSChange{}, errors.New("DNS_CHANGE_NOT_FOUND")
	}
	return staticDNSChange{
		beforeHash: manifest.BeforeHash, afterHash: manifest.AfterHash,
		backupPath: filepath.Join(c.backupDir, changeID+".resolv.conf.bak"), appliedAt: manifest.AppliedAt,
	}, nil
}

func readStaticDNSFile(path string) ([]byte, os.FileMode, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, 0, fmt.Errorf("DNS_SOURCE_UNAVAILABLE: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, 0, errors.New("DNS_STATIC_FILE_UNSAFE")
	}
	if info.Size() > staticDNSMaxSize {
		return nil, 0, errors.New("DNS_STATIC_FILE_TOO_LARGE")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, fmt.Errorf("DNS_SOURCE_UNAVAILABLE: %w", err)
	}
	return content, info.Mode().Perm(), nil
}

func atomicWriteStaticDNSFile(path string, content []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".ncp-resolv-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(mode.Perm()); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	directoryHandle, err := os.Open(directory)
	if err == nil {
		_ = directoryHandle.Sync()
		_ = directoryHandle.Close()
	}
	return nil
}

func normalizeStaticDNSRequest(request DNSChangeRequest) ([]string, []string, error) {
	if len(request.Nameservers) == 0 || len(request.Nameservers) > 6 {
		return nil, nil, errors.New("DNS_NAMESERVERS_INVALID")
	}
	nameservers := make([]string, 0, len(request.Nameservers))
	seen := make(map[string]struct{}, len(request.Nameservers))
	for _, value := range request.Nameservers {
		value = strings.TrimSpace(value)
		if net.ParseIP(value) == nil {
			return nil, nil, errors.New("DNS_NAMESERVERS_INVALID")
		}
		if _, exists := seen[value]; exists {
			return nil, nil, errors.New("DNS_NAMESERVERS_INVALID")
		}
		seen[value] = struct{}{}
		nameservers = append(nameservers, value)
	}
	searchDomains := make([]string, 0, len(request.SearchDomains))
	for _, value := range request.SearchDomains {
		value = strings.TrimSpace(strings.TrimSuffix(value, "."))
		if value == "" || strings.ContainsAny(value, " /\\\x00\r\n") {
			return nil, nil, errors.New("DNS_SEARCH_DOMAINS_INVALID")
		}
		searchDomains = append(searchDomains, value)
	}
	return nameservers, searchDomains, nil
}

func renderStaticDNSFile(before []byte, nameservers, searchDomains []string, replaceSearch bool) []byte {
	lines := strings.Split(strings.ReplaceAll(string(before), "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines)+len(nameservers)+1)
	insertAt := -1
	for _, line := range lines {
		fields := strings.Fields(line)
		isNameserver := len(fields) > 0 && fields[0] == "nameserver"
		isSearch := len(fields) > 0 && (fields[0] == "search" || fields[0] == "domain")
		if isNameserver || (replaceSearch && isSearch) {
			if insertAt < 0 {
				insertAt = len(kept)
			}
			continue
		}
		kept = append(kept, line)
	}
	for len(kept) > 0 && kept[len(kept)-1] == "" {
		kept = kept[:len(kept)-1]
	}
	if insertAt < 0 || insertAt > len(kept) {
		insertAt = len(kept)
	}
	directives := make([]string, 0, len(nameservers)+1)
	for _, nameserver := range nameservers {
		directives = append(directives, "nameserver "+nameserver)
	}
	if replaceSearch && len(searchDomains) > 0 {
		directives = append(directives, "search "+strings.Join(searchDomains, " "))
	}
	result := append([]string{}, kept[:insertAt]...)
	result = append(result, directives...)
	result = append(result, kept[insertAt:]...)
	return []byte(strings.Join(result, "\n") + "\n")
}

func parseSearchDomains(content string) []string {
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 && (fields[0] == "search" || fields[0] == "domain") {
			return append([]string(nil), fields[1:]...)
		}
	}
	return []string{}
}

func randomDNSID(prefix string) (string, error) {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(value), nil
}

func validDNSChangeID(value string) bool {
	const prefix = "dns-change-"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+24 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, prefix))
	return err == nil
}

func contentHash(content []byte) string {
	value := sha256.Sum256(content)
	return hex.EncodeToString(value[:])
}

func errorCode(err error) string {
	if err == nil {
		return ""
	}
	value := err.Error()
	if index := strings.IndexByte(value, ':'); index >= 0 {
		value = value[:index]
	}
	return value
}
