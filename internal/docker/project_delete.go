package docker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	// DefaultUGREENComposeRegistryPath is the verified UGREEN Docker registry
	// location on supported NAS installations. Runtime wiring can override it.
	DefaultUGREENComposeRegistryPath = "/volume1/@appstore/com.ugreen.docker/db/docker_info_log.db"
	projectDeleteTimeout             = 2 * time.Minute
	protectedNCPProjectName          = "nas-control-plane"
)

var (
	ErrComposeRegistryEntryNotFound = errors.New("compose registry entry was not found")
	composeRegistryNamePattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@/-]{0,255}$`)
)

// ComposeRegistryKey is always populated from the Root Agent's registry read,
// never from a browser-provided app_id. Empty AppID represents UGREEN's NULL
// app_id used by user-created Compose projects.
type ComposeRegistryKey struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	AppID string `json:"appId,omitempty"`
}

func (k ComposeRegistryKey) Normalize() (ComposeRegistryKey, error) {
	k.Name = strings.TrimSpace(k.Name)
	k.Path = strings.TrimSpace(k.Path)
	k.AppID = strings.TrimSpace(k.AppID)
	if k.Name == "" || k.Path == "" {
		return ComposeRegistryKey{}, coded("DOCKER_PROJECT_DELETE_INVALID", errors.New("compose registry name and path are required"))
	}
	if !composeRegistryNamePattern.MatchString(k.Name) {
		return ComposeRegistryKey{}, coded("DOCKER_PROJECT_DELETE_INVALID", errors.New("compose registry name is invalid"))
	}
	if k.AppID != "" && !composeRegistryNamePattern.MatchString(k.AppID) {
		return ComposeRegistryKey{}, coded("DOCKER_PROJECT_DELETE_INVALID", errors.New("compose registry app id is invalid"))
	}
	if !validComposeRegistryPath(k.Path) {
		return ComposeRegistryKey{}, coded("DOCKER_PROJECT_DELETE_INVALID", errors.New("compose registry path is invalid"))
	}
	return k, nil
}

type ComposeRegistryEntry struct {
	Key          ComposeRegistryKey `json:"key"`
	State        string             `json:"state,omitempty"`
	Content      string             `json:"content,omitempty"`
	ContainerNum int                `json:"containerNum,omitempty"`
}

type ComposeRegistryBackup struct {
	Path string
	Mode os.FileMode
	Data []byte
}

// ComposeRegistry models the controlled UGREEN registry transaction. The
// lookup discovers the real path and app_id from a unique project name; the
// delete then matches all three fields exactly.
type ComposeRegistry interface {
	Backup(context.Context) (ComposeRegistryBackup, error)
	Begin(context.Context) (ComposeRegistryTx, error)
	IntegrityCheck(context.Context) error
	Restore(context.Context, ComposeRegistryBackup) error
}

type ComposeRegistryTx interface {
	FindByName(context.Context, string) (ComposeRegistryEntry, error)
	DeleteExact(context.Context, ComposeRegistryKey) error
	Commit(context.Context) error
	Rollback(context.Context) error
}

type SQLiteComposeRegistry struct {
	path string
	mu   sync.Mutex
}

func NewSQLiteComposeRegistry(path string) *SQLiteComposeRegistry {
	if strings.TrimSpace(path) == "" {
		path = DefaultUGREENComposeRegistryPath
	}
	return &SQLiteComposeRegistry{path: filepath.Clean(path)}
}

func NewUGREENComposeRegistry(path string) *SQLiteComposeRegistry {
	return NewSQLiteComposeRegistry(path)
}

func (r *SQLiteComposeRegistry) Backup(ctx context.Context) (ComposeRegistryBackup, error) {
	if err := ctx.Err(); err != nil {
		return ComposeRegistryBackup{}, err
	}
	if r == nil || r.path == "" {
		return ComposeRegistryBackup{}, errors.New("compose registry path is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	info, err := os.Stat(r.path)
	if err != nil {
		return ComposeRegistryBackup{}, err
	}
	if !info.Mode().IsRegular() {
		return ComposeRegistryBackup{}, errors.New("compose registry is not a regular file")
	}
	data, err := os.ReadFile(r.path)
	if err != nil {
		return ComposeRegistryBackup{}, err
	}
	return ComposeRegistryBackup{Path: r.path, Mode: info.Mode().Perm(), Data: data}, nil
}

func (r *SQLiteComposeRegistry) Begin(ctx context.Context) (ComposeRegistryTx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil || r.path == "" {
		return nil, errors.New("compose registry path is required")
	}
	r.mu.Lock()
	database, err := sql.Open("sqlite", r.path)
	if err != nil {
		r.mu.Unlock()
		return nil, err
	}
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		_ = database.Close()
		r.mu.Unlock()
		return nil, err
	}
	return &sqliteComposeRegistryTx{registry: r, database: database, transaction: transaction}, nil
}

func (r *SQLiteComposeRegistry) IntegrityCheck(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil || r.path == "" {
		return errors.New("compose registry path is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	database, err := sql.Open("sqlite", r.path)
	if err != nil {
		return err
	}
	defer database.Close()
	var result string
	if err := database.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if strings.ToLower(strings.TrimSpace(result)) != "ok" {
		return fmt.Errorf("sqlite integrity check returned %q", result)
	}
	return nil
}

func (r *SQLiteComposeRegistry) Restore(ctx context.Context, backup ComposeRegistryBackup) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil || r.path == "" || backup.Path != "" && filepath.Clean(backup.Path) != r.path {
		return errors.New("compose registry backup does not match target")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	mode := backup.Mode
	if mode == 0 {
		mode = 0o600
	}
	temporary, err := os.CreateTemp(filepath.Dir(r.path), ".ncp-compose-registry-restore-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(backup.Data); err != nil {
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
	return os.Rename(temporaryName, r.path)
}

type sqliteComposeRegistryTx struct {
	registry    *SQLiteComposeRegistry
	database    *sql.DB
	transaction *sql.Tx
	closed      bool
}

func (t *sqliteComposeRegistryTx) FindByName(ctx context.Context, name string) (ComposeRegistryEntry, error) {
	if err := t.ensureOpen(); err != nil {
		return ComposeRegistryEntry{}, err
	}
	name = strings.TrimSpace(name)
	if !composeRegistryNamePattern.MatchString(name) {
		return ComposeRegistryEntry{}, coded("DOCKER_PROJECT_DELETE_INVALID", errors.New("compose registry name is invalid"))
	}
	rows, err := t.transaction.QueryContext(ctx, `SELECT name, path, COALESCE(app_id, ''), COALESCE(CAST(state AS TEXT), ''), COALESCE(content, ''), COALESCE(container_num, 0) FROM compose WHERE name = ?`, name)
	if err != nil {
		return ComposeRegistryEntry{}, err
	}
	defer rows.Close()
	var entry ComposeRegistryEntry
	count := 0
	for rows.Next() {
		if count > 0 {
			return ComposeRegistryEntry{}, errors.New("compose registry project name is not unique")
		}
		if err := rows.Scan(&entry.Key.Name, &entry.Key.Path, &entry.Key.AppID, &entry.State, &entry.Content, &entry.ContainerNum); err != nil {
			return ComposeRegistryEntry{}, err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return ComposeRegistryEntry{}, err
	}
	if count == 0 {
		return ComposeRegistryEntry{}, ErrComposeRegistryEntryNotFound
	}
	key, err := entry.Key.Normalize()
	if err != nil {
		return ComposeRegistryEntry{}, err
	}
	entry.Key = key
	return entry, nil
}

func (t *sqliteComposeRegistryTx) DeleteExact(ctx context.Context, key ComposeRegistryKey) error {
	if err := t.ensureOpen(); err != nil {
		return err
	}
	key, err := key.Normalize()
	if err != nil {
		return err
	}
	result, err := t.transaction.ExecContext(ctx, `DELETE FROM compose WHERE name = ? AND path = ? AND COALESCE(app_id, '') = ?`, key.Name, key.Path, key.AppID)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrComposeRegistryEntryNotFound
	}
	if rowsAffected != 1 {
		return errors.New("compose registry delete matched more than one row")
	}
	return nil
}

func (t *sqliteComposeRegistryTx) Commit(ctx context.Context) error {
	if err := t.ensureOpen(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := t.transaction.Commit()
	t.close()
	return err
}

func (t *sqliteComposeRegistryTx) Rollback(ctx context.Context) error {
	if t == nil || t.closed {
		return nil
	}
	if err := ctx.Err(); err != nil {
		t.close()
		return err
	}
	err := t.transaction.Rollback()
	t.close()
	return err
}

func (t *sqliteComposeRegistryTx) ensureOpen() error {
	if t == nil || t.closed || t.transaction == nil {
		return errors.New("compose registry transaction is closed")
	}
	return nil
}

func (t *sqliteComposeRegistryTx) close() {
	if t == nil || t.closed {
		return
	}
	t.closed = true
	_ = t.database.Close()
	if t.registry != nil {
		t.registry.mu.Unlock()
	}
}

// ProjectDeleteRequest deliberately contains no container IDs, registry path,
// or app_id. The Root Agent resolves those values from live Docker and UGREEN
// state so a stale or forged browser payload cannot narrow or redirect deletion.
type ProjectDeleteRequest struct {
	ProjectID    string      `json:"projectId"`
	Kind         ProjectKind `json:"kind,omitempty"`
	RegistryName string      `json:"registryName,omitempty"`
	Name         string      `json:"name,omitempty"`
}

type DeleteProjectRequest = ProjectDeleteRequest

func (r ProjectDeleteRequest) Normalize() (ProjectDeleteRequest, error) {
	r.ProjectID = strings.TrimSpace(r.ProjectID)
	if r.ProjectID == "" {
		return ProjectDeleteRequest{}, coded("DOCKER_PROJECT_DELETE_INVALID", errors.New("project id is required"))
	}
	if r.Kind == "" {
		r.Kind = ProjectKindCompose
	}
	if r.Kind != ProjectKindCompose {
		return ProjectDeleteRequest{}, coded("DOCKER_PROJECT_DELETE_PROTECTED", errors.New("standalone project deletion is not supported"))
	}
	if strings.TrimSpace(r.RegistryName) == "" {
		r.RegistryName = strings.TrimSpace(r.Name)
	}
	r.RegistryName = strings.TrimSpace(r.RegistryName)
	if !composeRegistryNamePattern.MatchString(r.RegistryName) {
		return ProjectDeleteRequest{}, coded("DOCKER_PROJECT_DELETE_INVALID", errors.New("compose registry name is invalid"))
	}
	if r.ProjectID != "compose:"+r.RegistryName {
		return ProjectDeleteRequest{}, coded("DOCKER_PROJECT_DELETE_INVALID", errors.New("project id does not match compose project name"))
	}
	if strings.EqualFold(r.RegistryName, protectedNCPProjectName) {
		return ProjectDeleteRequest{}, coded("DOCKER_PROJECT_DELETE_PROTECTED", errors.New("the NCP project is protected from self-deletion"))
	}
	return r, nil
}

func (r ProjectDeleteRequest) Validate() error {
	_, err := r.Normalize()
	return err
}

type ProjectDeleteContainerResult struct {
	ContainerID string `json:"containerId"`
	Name        string `json:"name"`
	State       string `json:"state"`
	Deleted     bool   `json:"deleted"`
	Success     bool   `json:"success"`
	ErrorCode   string `json:"errorCode,omitempty"`
}

type ProjectDeleteResult struct {
	ProjectID          string                         `json:"projectId"`
	Kind               ProjectKind                    `json:"kind"`
	Completed          bool                           `json:"completed"`
	Partial            bool                           `json:"partial"`
	RegistryDeleted    bool                           `json:"registryDeleted"`
	RegistryRolledBack bool                           `json:"registryRolledBack"`
	Containers         []ProjectDeleteContainerResult `json:"containers"`
}

type ProjectDeleteGateway interface {
	ListInventoryContainers(context.Context) ([]InventoryContainer, error)
	InspectContainer(context.Context, string) (ContainerSnapshot, error)
	RemoveContainer(context.Context, string) error
}

type ProjectDeleter struct {
	gateway  ProjectDeleteGateway
	registry ComposeRegistry
	timeout  time.Duration
}

func NewProjectDeleter(gateway ProjectDeleteGateway, registry ComposeRegistry) *ProjectDeleter {
	return &ProjectDeleter{gateway: gateway, registry: registry, timeout: projectDeleteTimeout}
}

func (c *ContainerController) DeleteProject(ctx context.Context, request ProjectDeleteRequest) (ProjectDeleteResult, error) {
	if c == nil {
		return ProjectDeleteResult{}, coded("DOCKER_PROJECT_DELETE_UNAVAILABLE", errors.New("container controller is not configured"))
	}
	gateway, ok := c.gateway.(ProjectDeleteGateway)
	if !ok {
		return ProjectDeleteResult{}, coded("DOCKER_PROJECT_DELETE_UNAVAILABLE", errors.New("project delete gateway is not configured"))
	}
	registryPath := c.composeRegistryPath
	if strings.TrimSpace(registryPath) == "" {
		registryPath = DefaultUGREENComposeRegistryPath
	}
	return NewProjectDeleter(gateway, NewSQLiteComposeRegistry(registryPath)).Delete(ctx, request)
}

func (d *ProjectDeleter) Delete(ctx context.Context, request ProjectDeleteRequest) (ProjectDeleteResult, error) {
	request, err := request.Normalize()
	if err != nil {
		return ProjectDeleteResult{}, err
	}
	result := ProjectDeleteResult{ProjectID: request.ProjectID, Kind: ProjectKindCompose}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if d == nil || d.gateway == nil || d.registry == nil {
		return result, coded("DOCKER_PROJECT_DELETE_UNAVAILABLE", errors.New("project deletion is not configured"))
	}
	operationContext, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	containers, err := d.resolveProjectContainers(operationContext, request.ProjectID)
	if err != nil {
		return result, err
	}
	result.Containers = make([]ProjectDeleteContainerResult, 0, len(containers))
	for _, container := range containers {
		result.Containers = append(result.Containers, ProjectDeleteContainerResult{ContainerID: container.ID, Name: container.Name, State: "unknown"})
	}

	// Inspect every live-resolved container before the first mutation. If any
	// container is running or disappeared, the whole operation is rejected.
	for index, container := range containers {
		snapshot, inspectErr := d.gateway.InspectContainer(operationContext, container.ID)
		if inspectErr != nil {
			result.Containers[index].ErrorCode = "DOCKER_CONTAINER_INSPECT_FAILED"
			return result, coded("DOCKER_PROJECT_DELETE_INSPECT_FAILED", inspectErr)
		}
		result.Containers[index].ContainerID = strings.TrimSpace(snapshot.ID)
		if result.Containers[index].ContainerID == "" {
			result.Containers[index].ContainerID = container.ID
		}
		result.Containers[index].Name = strings.TrimPrefix(strings.TrimSpace(snapshot.Name), "/")
		result.Containers[index].State = containerState(snapshot.Running)
		if snapshot.Running {
			result.Containers[index].ErrorCode = "DOCKER_PROJECT_DELETE_RUNNING"
			return result, coded("DOCKER_PROJECT_DELETE_RUNNING", errors.New("project contains a running container"))
		}
	}

	backup, err := d.registry.Backup(operationContext)
	if err != nil {
		return result, coded("DOCKER_PROJECT_DELETE_REGISTRY_BACKUP_FAILED", err)
	}
	transaction, err := d.registry.Begin(operationContext)
	if err != nil {
		return result, coded("DOCKER_PROJECT_DELETE_REGISTRY_UNAVAILABLE", err)
	}
	entry, err := transaction.FindByName(operationContext, request.RegistryName)
	if err != nil {
		rollbackErr := transaction.Rollback(context.Background())
		if rollbackErr != nil {
			return result, coded("DOCKER_PROJECT_DELETE_ROLLBACK_FAILED", rollbackErr)
		}
		if errors.Is(err, ErrComposeRegistryEntryNotFound) || errors.Is(err, sql.ErrNoRows) {
			return result, coded("DOCKER_PROJECT_DELETE_REGISTRY_NOT_FOUND", err)
		}
		return result, coded("DOCKER_PROJECT_DELETE_REGISTRY_FAILED", err)
	}

	for index := range result.Containers {
		id := result.Containers[index].ContainerID
		if err := d.gateway.RemoveContainer(operationContext, id); err != nil {
			result.Containers[index].ErrorCode = errorCodeOr(err, "DOCKER_PROJECT_DELETE_FAILED")
			result.Partial = anyDeletedProjectContainer(result.Containers)
			if rollbackErr := transaction.Rollback(context.Background()); rollbackErr != nil {
				return result, coded("DOCKER_PROJECT_DELETE_ROLLBACK_FAILED", rollbackErr)
			}
			result.RegistryRolledBack = true
			return result, coded("DOCKER_PROJECT_DELETE_FAILED", err)
		}
		result.Containers[index].Deleted = true
		result.Containers[index].Success = true
	}

	if err := transaction.DeleteExact(operationContext, entry.Key); err != nil {
		result.Partial = true
		if rollbackErr := transaction.Rollback(context.Background()); rollbackErr != nil {
			return result, coded("DOCKER_PROJECT_DELETE_ROLLBACK_FAILED", rollbackErr)
		}
		result.RegistryRolledBack = true
		return result, coded("DOCKER_PROJECT_DELETE_REGISTRY_FAILED", err)
	}
	if err := transaction.Commit(operationContext); err != nil {
		result.Partial = true
		result.RegistryRolledBack = true
		if restoreErr := d.registry.Restore(context.Background(), backup); restoreErr != nil {
			return result, coded("DOCKER_PROJECT_DELETE_ROLLBACK_FAILED", restoreErr)
		}
		return result, coded("DOCKER_PROJECT_DELETE_REGISTRY_FAILED", err)
	}
	if err := d.registry.IntegrityCheck(operationContext); err != nil {
		result.Partial = true
		result.RegistryRolledBack = true
		if restoreErr := d.registry.Restore(context.Background(), backup); restoreErr != nil {
			return result, coded("DOCKER_PROJECT_DELETE_ROLLBACK_FAILED", restoreErr)
		}
		return result, coded("DOCKER_PROJECT_DELETE_INTEGRITY_FAILED", err)
	}
	result.RegistryDeleted = true
	result.Completed = true
	return result, nil
}

func (d *ProjectDeleter) resolveProjectContainers(ctx context.Context, projectID string) ([]InventoryContainer, error) {
	containers, err := d.gateway.ListInventoryContainers(ctx)
	if err != nil {
		return nil, coded("DOCKER_PROJECT_DELETE_INVENTORY_FAILED", err)
	}
	result := make([]InventoryContainer, 0)
	for index := range containers {
		normalizeInventoryContainer(&containers[index])
		if containers[index].ProjectID == projectID {
			result = append(result, containers[index])
		}
	}
	if len(result) == 0 {
		return nil, coded("DOCKER_PROJECT_DELETE_NOT_FOUND", errors.New("compose project has no current containers"))
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return result, nil
}

func (g *mobyContainerControlGateway) ListInventoryContainers(ctx context.Context) ([]InventoryContainer, error) {
	return (&mobyInventoryGateway{client: g.client}).ListInventoryContainers(ctx)
}

func anyDeletedProjectContainer(items []ProjectDeleteContainerResult) bool {
	for _, item := range items {
		if item.Deleted {
			return true
		}
	}
	return false
}

func errorCodeOr(err error, fallback string) string {
	if code := ErrorCode(err); code != "" {
		return code
	}
	return fallback
}

func validComposeRegistryPath(value string) bool {
	return value != "" && filepath.IsAbs(value) && filepath.Clean(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}
