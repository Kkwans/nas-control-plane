package system

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protowire"
)

const (
	ugosDNSPreviewTTL       = 5 * time.Minute
	ugosGetGeneralConfigRPC = "/ugidl.netserv.general.GeneralService/GetGeneralConfig"
	ugosSetGeneralConfigRPC = "/ugidl.netserv.general.GeneralService/SetGeneralConfig"
	ugosDNSModeAuto         = 1
	ugosDNSModeManual       = 2
)

type ugosRawMessage []byte

type ugosRawCodec struct{}

func (ugosRawCodec) Name() string { return "proto" }

func (ugosRawCodec) Marshal(value any) ([]byte, error) {
	message, ok := value.(*ugosRawMessage)
	if !ok {
		return nil, errors.New("UGOS_DNS_MESSAGE_INVALID")
	}
	return append([]byte(nil), (*message)...), nil
}

func (ugosRawCodec) Unmarshal(content []byte, value any) error {
	message, ok := value.(*ugosRawMessage)
	if !ok {
		return errors.New("UGOS_DNS_MESSAGE_INVALID")
	}
	*message = append((*message)[:0], content...)
	return nil
}

type ugosDNSClient interface {
	GetGeneralConfig(context.Context) ([]byte, error)
	SetGeneralConfig(context.Context, []byte) error
}

type liveUGOSDNSClient struct {
	socketPath string
}

func (c liveUGOSDNSClient) invoke(ctx context.Context, method string, request []byte) ([]byte, error) {
	connection, err := grpc.NewClient(
		"passthrough:///ugos-net-serv",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(ugosRawCodec{})),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", c.socketPath)
		}),
	)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	input := ugosRawMessage(append([]byte(nil), request...))
	var output ugosRawMessage
	if err := connection.Invoke(ctx, method, &input, &output); err != nil {
		return nil, err
	}
	return append([]byte(nil), output...), nil
}

func (c liveUGOSDNSClient) GetGeneralConfig(ctx context.Context) ([]byte, error) {
	return c.invoke(ctx, ugosGetGeneralConfigRPC, nil)
}

func (c liveUGOSDNSClient) SetGeneralConfig(ctx context.Context, config []byte) error {
	response, err := c.invoke(ctx, ugosSetGeneralConfigRPC, config)
	if err != nil {
		return err
	}
	return validateUGOSSetGeneralConfigResponse(response)
}

type ugosDNSPreview struct {
	before      []byte
	after       []byte
	afterDNS    []string
	expiresAt   time.Time
	beforeState DNSState
}

type ugosDNSChange struct {
	beforeHash string
	afterHash  string
	backupPath string
	appliedAt  time.Time
}

type ugosDNSChangeManifest struct {
	ChangeID   string    `json:"changeId"`
	BeforeHash string    `json:"beforeHash"`
	AfterHash  string    `json:"afterHash"`
	AppliedAt  time.Time `json:"appliedAt"`
}

// UGOSNetworkDNSController changes DNS through the vendor network service.
// It preserves the complete GeneralConfig response, changes only the DNS
// fields, rejects concurrent edits, and persists enough state for rollback
// after an Agent restart.
type UGOSNetworkDNSController struct {
	client    ugosDNSClient
	backupDir string
	now       func() time.Time

	mu       sync.Mutex
	previews map[string]ugosDNSPreview
	changes  map[string]ugosDNSChange
}

func NewUGOSNetworkDNSController(socketPath, backupDir string) (*UGOSNetworkDNSController, error) {
	socketPath = filepath.Clean(strings.TrimSpace(socketPath))
	backupDir = filepath.Clean(strings.TrimSpace(backupDir))
	if !filepath.IsAbs(socketPath) || !filepath.IsAbs(backupDir) || socketPath == string(filepath.Separator) || backupDir == string(filepath.Separator) {
		return nil, errors.New("UGOS_DNS_PATH_INVALID")
	}
	info, err := os.Stat(socketPath)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return nil, errors.New("UGOS_DNS_SOCKET_UNAVAILABLE")
	}
	client := liveUGOSDNSClient{socketPath: socketPath}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	config, err := client.GetGeneralConfig(ctx)
	if err != nil {
		return nil, errors.New("UGOS_DNS_READ_FAILED")
	}
	if _, _, err := parseUGOSDNSConfig(config); err != nil {
		return nil, err
	}
	return newUGOSNetworkDNSController(client, backupDir), nil
}

func newUGOSNetworkDNSController(client ugosDNSClient, backupDir string) *UGOSNetworkDNSController {
	return &UGOSNetworkDNSController{
		client: client, backupDir: backupDir, now: time.Now,
		previews: make(map[string]ugosDNSPreview), changes: make(map[string]ugosDNSChange),
	}
}

func (c *UGOSNetworkDNSController) Preview(ctx context.Context, request DNSChangeRequest) (DNSChangePreview, error) {
	if err := ctx.Err(); err != nil {
		return DNSChangePreview{Backend: DNSBackendUGOSNetwork, ErrorCode: "DNS_CHANGE_CANCELED"}, err
	}
	nameservers, searchDomains, err := normalizeStaticDNSRequest(request)
	if err != nil {
		return DNSChangePreview{Backend: DNSBackendUGOSNetwork, ErrorCode: err.Error()}, err
	}
	if len(searchDomains) > 0 {
		return DNSChangePreview{Backend: DNSBackendUGOSNetwork, ErrorCode: "DNS_SEARCH_DOMAINS_UNSUPPORTED"}, errors.New("DNS_SEARCH_DOMAINS_UNSUPPORTED")
	}
	before, err := c.client.GetGeneralConfig(ctx)
	if err != nil {
		return DNSChangePreview{Backend: DNSBackendUGOSNetwork, ErrorCode: "UGOS_DNS_READ_FAILED"}, errors.New("UGOS_DNS_READ_FAILED")
	}
	currentDNS, _, err := parseUGOSDNSConfig(before)
	if err != nil {
		return DNSChangePreview{Backend: DNSBackendUGOSNetwork, ErrorCode: errorCode(err)}, err
	}
	after, err := rewriteUGOSDNSConfig(before, nameservers)
	if err != nil {
		return DNSChangePreview{Backend: DNSBackendUGOSNetwork, ErrorCode: errorCode(err)}, err
	}
	previewID, err := randomDNSID("dns-preview")
	if err != nil {
		return DNSChangePreview{Backend: DNSBackendUGOSNetwork, ErrorCode: "DNS_CHANGE_ID_FAILED"}, err
	}
	expiresAt := c.now().UTC().Add(ugosDNSPreviewTTL)
	beforeState := DNSState{Nameservers: currentDNS, SearchDomains: []string{}}
	preview := DNSChangePreview{
		PreviewID: previewID, Backend: DNSBackendUGOSNetwork,
		Before: beforeState, After: DNSState{Nameservers: append([]string(nil), nameservers...), SearchDomains: []string{}},
		RequiresConfirm: true, RollbackAvailable: true, ExpiresAt: expiresAt,
	}
	c.mu.Lock()
	c.pruneExpiredPreviewsLocked()
	c.previews[previewID] = ugosDNSPreview{
		before: append([]byte(nil), before...), after: append([]byte(nil), after...),
		afterDNS: append([]string(nil), nameservers...), expiresAt: expiresAt, beforeState: beforeState,
	}
	c.mu.Unlock()
	return preview, nil
}

func (c *UGOSNetworkDNSController) Confirm(ctx context.Context, confirmation DNSChangeConfirmation) (DNSChangeResult, error) {
	result := DNSChangeResult{Backend: DNSBackendUGOSNetwork}
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
	current, err := c.client.GetGeneralConfig(ctx)
	if err != nil {
		result.ErrorCode = "UGOS_DNS_READ_FAILED"
		return result, errors.New(result.ErrorCode)
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
	change := ugosDNSChange{
		beforeHash: contentHash(preview.before), afterHash: contentHash(preview.after),
		backupPath: backupPath, appliedAt: appliedAt,
	}
	if err := c.writeChangeManifest(changeID, change, true); err != nil {
		result.ErrorCode = "DNS_CHANGE_RECORD_FAILED"
		return result, err
	}
	if err := c.client.SetGeneralConfig(ctx, preview.after); err != nil {
		result.ErrorCode = errorCode(err)
		return result, err
	}
	verified, err := c.client.GetGeneralConfig(ctx)
	if err != nil {
		_ = c.client.SetGeneralConfig(context.Background(), preview.before)
		result.ErrorCode = "DNS_APPLY_VERIFICATION_FAILED"
		return result, errors.New(result.ErrorCode)
	}
	verifiedDNS, manual, parseErr := parseUGOSDNSConfig(verified)
	if parseErr != nil || !manual || !equalStringSlices(verifiedDNS, preview.afterDNS) {
		_ = c.client.SetGeneralConfig(context.Background(), preview.before)
		result.ErrorCode = "DNS_APPLY_VERIFICATION_FAILED"
		return result, errors.New(result.ErrorCode)
	}
	change.afterHash = contentHash(verified)
	if err := c.writeChangeManifest(changeID, change, false); err != nil {
		_ = c.client.SetGeneralConfig(context.Background(), preview.before)
		result.ErrorCode = "DNS_CHANGE_RECORD_FAILED"
		return result, err
	}
	c.changes[changeID] = change
	delete(c.previews, confirmation.PreviewID)
	return DNSChangeResult{
		ChangeID: changeID, Backend: DNSBackendUGOSNetwork, Applied: true,
		RollbackAvailable: true, AppliedAt: appliedAt,
	}, nil
}

func (c *UGOSNetworkDNSController) Rollback(ctx context.Context, request DNSRollbackRequest) (DNSChangeResult, error) {
	result := DNSChangeResult{Backend: DNSBackendUGOSNetwork, ChangeID: strings.TrimSpace(request.ChangeID)}
	if result.ChangeID == "" || !validDNSChangeID(result.ChangeID) {
		result.ErrorCode = "DNS_CHANGE_NOT_FOUND"
		return result, errors.New(result.ErrorCode)
	}
	if err := ctx.Err(); err != nil {
		result.ErrorCode = "DNS_CHANGE_CANCELED"
		return result, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	change, ok := c.changes[result.ChangeID]
	if !ok {
		var err error
		change, err = c.loadChange(result.ChangeID)
		if err != nil {
			result.ErrorCode = "DNS_CHANGE_NOT_FOUND"
			return result, errors.New(result.ErrorCode)
		}
	}
	current, err := c.client.GetGeneralConfig(ctx)
	if err != nil {
		result.ErrorCode = "UGOS_DNS_READ_FAILED"
		return result, errors.New(result.ErrorCode)
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
	if err := c.client.SetGeneralConfig(ctx, backup); err != nil {
		result.ErrorCode = errorCode(err)
		return result, err
	}
	verified, err := c.client.GetGeneralConfig(ctx)
	if err != nil || contentHash(verified) != change.beforeHash {
		result.ErrorCode = "DNS_ROLLBACK_VERIFICATION_FAILED"
		return result, errors.New(result.ErrorCode)
	}
	delete(c.changes, result.ChangeID)
	return DNSChangeResult{
		ChangeID: result.ChangeID, Backend: DNSBackendUGOSNetwork,
		Applied: false, RollbackAvailable: false, AppliedAt: change.appliedAt,
	}, nil
}

func (c *UGOSNetworkDNSController) pruneExpiredPreviewsLocked() {
	now := c.now().UTC()
	for key, preview := range c.previews {
		if !now.Before(preview.expiresAt) {
			delete(c.previews, key)
		}
	}
}

func (c *UGOSNetworkDNSController) writeBackup(changeID string, content []byte) (string, error) {
	if err := os.MkdirAll(c.backupDir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(c.backupDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(c.backupDir, changeID+".ugos.pb.bak")
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
	return path, writeErr
}

func (c *UGOSNetworkDNSController) writeChangeManifest(changeID string, change ugosDNSChange, create bool) error {
	manifest := ugosDNSChangeManifest{
		ChangeID: changeID, BeforeHash: change.beforeHash, AfterHash: change.afterHash, AppliedAt: change.appliedAt,
	}
	content, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	content = append(content, '\n')
	path := filepath.Join(c.backupDir, changeID+".ugos.json")
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if create {
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, 0o600)
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

func (c *UGOSNetworkDNSController) loadChange(changeID string) (ugosDNSChange, error) {
	content, err := os.ReadFile(filepath.Join(c.backupDir, changeID+".ugos.json"))
	if err != nil || len(content) > 4096 {
		return ugosDNSChange{}, errors.New("DNS_CHANGE_NOT_FOUND")
	}
	var manifest ugosDNSChangeManifest
	if err := json.Unmarshal(content, &manifest); err != nil || manifest.ChangeID != changeID || manifest.BeforeHash == "" || manifest.AfterHash == "" {
		return ugosDNSChange{}, errors.New("DNS_CHANGE_NOT_FOUND")
	}
	return ugosDNSChange{
		beforeHash: manifest.BeforeHash, afterHash: manifest.AfterHash,
		backupPath: filepath.Join(c.backupDir, changeID+".ugos.pb.bak"), appliedAt: manifest.AppliedAt,
	}, nil
}

func parseUGOSDNSConfig(config []byte) ([]string, bool, error) {
	general, ok, err := firstBytesField(config, 1)
	if err != nil || !ok {
		return nil, false, errors.New("UGOS_DNS_CONFIG_INVALID")
	}
	nameservers := []string{}
	manual := false
	for len(general) > 0 {
		number, kind, tagLength := protowire.ConsumeTag(general)
		if tagLength < 0 {
			return nil, false, errors.New("UGOS_DNS_CONFIG_INVALID")
		}
		valueLength := protowire.ConsumeFieldValue(number, kind, general[tagLength:])
		if valueLength < 0 {
			return nil, false, errors.New("UGOS_DNS_CONFIG_INVALID")
		}
		value := general[tagLength : tagLength+valueLength]
		if number == 3 && kind == protowire.BytesType {
			item, consumed := protowire.ConsumeBytes(value)
			if consumed < 0 || net.ParseIP(string(item)) == nil {
				return nil, false, errors.New("UGOS_DNS_CONFIG_INVALID")
			}
			nameservers = append(nameservers, string(item))
		}
		if number == 4 && kind == protowire.VarintType {
			item, consumed := protowire.ConsumeVarint(value)
			if consumed < 0 {
				return nil, false, errors.New("UGOS_DNS_CONFIG_INVALID")
			}
			manual = item == ugosDNSModeManual
		}
		general = general[tagLength+valueLength:]
	}
	return nameservers, manual, nil
}

// parseUGOSSingleBool decodes ugidl.common.SingleBool. SetGeneralConfig uses
// this value as a reboot-required flag, not an applied/success flag. A false
// value is encoded as an empty payload and is a valid successful response.
func parseUGOSSingleBool(content []byte) (bool, error) {
	result := false
	for len(content) > 0 {
		number, kind, tagLength := protowire.ConsumeTag(content)
		if tagLength < 0 {
			return false, errors.New("UGOS_DNS_RESPONSE_INVALID")
		}
		valueLength := protowire.ConsumeFieldValue(number, kind, content[tagLength:])
		if valueLength < 0 {
			return false, errors.New("UGOS_DNS_RESPONSE_INVALID")
		}
		if number == 1 {
			if kind != protowire.VarintType {
				return false, errors.New("UGOS_DNS_RESPONSE_INVALID")
			}
			value, consumed := protowire.ConsumeVarint(content[tagLength : tagLength+valueLength])
			if consumed < 0 {
				return false, errors.New("UGOS_DNS_RESPONSE_INVALID")
			}
			result = value != 0
		}
		content = content[tagLength+valueLength:]
	}
	return result, nil
}

func validateUGOSSetGeneralConfigResponse(content []byte) error {
	_, err := parseUGOSSingleBool(content)
	return err
}

func rewriteUGOSDNSConfig(config []byte, nameservers []string) ([]byte, error) {
	general, ok, err := firstBytesField(config, 1)
	if err != nil || !ok {
		return nil, errors.New("UGOS_DNS_CONFIG_INVALID")
	}
	rewrittenGeneral, err := rewriteUGOSGeneralDNS(general, nameservers)
	if err != nil {
		return nil, err
	}
	return replaceFirstBytesField(config, 1, rewrittenGeneral)
}

func rewriteUGOSGeneralDNS(general []byte, nameservers []string) ([]byte, error) {
	result := make([]byte, 0, len(general)+64)
	for len(general) > 0 {
		number, kind, tagLength := protowire.ConsumeTag(general)
		if tagLength < 0 {
			return nil, errors.New("UGOS_DNS_CONFIG_INVALID")
		}
		valueLength := protowire.ConsumeFieldValue(number, kind, general[tagLength:])
		if valueLength < 0 {
			return nil, errors.New("UGOS_DNS_CONFIG_INVALID")
		}
		if number != 3 && number != 4 {
			result = append(result, general[:tagLength+valueLength]...)
		}
		general = general[tagLength+valueLength:]
	}
	for _, nameserver := range nameservers {
		result = protowire.AppendTag(result, 3, protowire.BytesType)
		result = protowire.AppendString(result, nameserver)
	}
	result = protowire.AppendTag(result, 4, protowire.VarintType)
	result = protowire.AppendVarint(result, ugosDNSModeManual)
	return result, nil
}

func firstBytesField(content []byte, target protowire.Number) ([]byte, bool, error) {
	for len(content) > 0 {
		number, kind, tagLength := protowire.ConsumeTag(content)
		if tagLength < 0 {
			return nil, false, errors.New("UGOS_DNS_CONFIG_INVALID")
		}
		valueLength := protowire.ConsumeFieldValue(number, kind, content[tagLength:])
		if valueLength < 0 {
			return nil, false, errors.New("UGOS_DNS_CONFIG_INVALID")
		}
		if number == target && kind == protowire.BytesType {
			value, consumed := protowire.ConsumeBytes(content[tagLength : tagLength+valueLength])
			if consumed < 0 {
				return nil, false, errors.New("UGOS_DNS_CONFIG_INVALID")
			}
			return append([]byte(nil), value...), true, nil
		}
		content = content[tagLength+valueLength:]
	}
	return nil, false, nil
}

func replaceFirstBytesField(content []byte, target protowire.Number, replacement []byte) ([]byte, error) {
	result := make([]byte, 0, len(content)+len(replacement))
	replaced := false
	for len(content) > 0 {
		number, kind, tagLength := protowire.ConsumeTag(content)
		if tagLength < 0 {
			return nil, errors.New("UGOS_DNS_CONFIG_INVALID")
		}
		valueLength := protowire.ConsumeFieldValue(number, kind, content[tagLength:])
		if valueLength < 0 {
			return nil, errors.New("UGOS_DNS_CONFIG_INVALID")
		}
		if number == target && kind == protowire.BytesType && !replaced {
			result = protowire.AppendTag(result, target, protowire.BytesType)
			result = protowire.AppendBytes(result, replacement)
			replaced = true
		} else {
			result = append(result, content[:tagLength+valueLength]...)
		}
		content = content[tagLength+valueLength:]
	}
	if !replaced {
		return nil, errors.New("UGOS_DNS_CONFIG_INVALID")
	}
	return result, nil
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
