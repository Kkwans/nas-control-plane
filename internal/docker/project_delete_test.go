package docker

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestProjectDeleterRejectsRunningProjectBeforeMutation(t *testing.T) {
	gateway := newFakeProjectDeleteGateway(
		ContainerSnapshot{ID: "running", Name: "/running", Running: true},
		ContainerSnapshot{ID: "stopped", Name: "/stopped"},
	)
	registry := newFakeComposeRegistry(true)
	result, err := NewProjectDeleter(gateway, registry).Delete(context.Background(), composeDeleteRequest())
	if ErrorCode(err) != "DOCKER_PROJECT_DELETE_RUNNING" {
		t.Fatalf("error code = %q, want DOCKER_PROJECT_DELETE_RUNNING", ErrorCode(err))
	}
	if len(gateway.removed) != 0 || registry.backupCalls != 0 {
		t.Fatalf("mutation happened: removed=%#v backupCalls=%d", gateway.removed, registry.backupCalls)
	}
	if result.Containers[0].ErrorCode != "DOCKER_PROJECT_DELETE_RUNNING" {
		t.Fatalf("result = %#v", result)
	}
}

func TestProjectDeleterRejectsMissingRegistryEntryBeforeContainerMutation(t *testing.T) {
	gateway := newFakeProjectDeleteGateway(ContainerSnapshot{ID: "one", Name: "/one"})
	registry := newFakeComposeRegistry(false)
	_, err := NewProjectDeleter(gateway, registry).Delete(context.Background(), composeDeleteRequest())
	if ErrorCode(err) != "DOCKER_PROJECT_DELETE_REGISTRY_NOT_FOUND" {
		t.Fatalf("error code = %q, want DOCKER_PROJECT_DELETE_REGISTRY_NOT_FOUND", ErrorCode(err))
	}
	if len(gateway.removed) != 0 || registry.tx.rollbacks != 1 {
		t.Fatalf("missing row still mutated: removed=%#v rollbacks=%d", gateway.removed, registry.tx.rollbacks)
	}
}

func TestProjectDeleterResolvesAllContainersFromLiveInventory(t *testing.T) {
	gateway := newFakeProjectDeleteGateway(
		ContainerSnapshot{ID: "two", Name: "/two"},
		ContainerSnapshot{ID: "one", Name: "/one"},
	)
	registry := newFakeComposeRegistry(true)
	result, err := NewProjectDeleter(gateway, registry).Delete(context.Background(), composeDeleteRequest())
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if !result.Completed || result.Partial || !result.RegistryDeleted || result.RegistryRolledBack {
		t.Fatalf("result = %#v", result)
	}
	if len(gateway.removed) != 2 || gateway.removed[0] != "one" || gateway.removed[1] != "two" {
		t.Fatalf("removed = %#v", gateway.removed)
	}
	if registry.tx.key != registry.tx.entry.Key || registry.backupCalls != 1 || registry.integrityCalls != 1 || registry.tx.commits != 1 {
		t.Fatalf("registry operations = %#v", registry)
	}
}

func TestProjectDeleterKeepsRegistryAfterPartialContainerFailure(t *testing.T) {
	gateway := newFakeProjectDeleteGateway(
		ContainerSnapshot{ID: "one", Name: "/one"},
		ContainerSnapshot{ID: "two", Name: "/two"},
	)
	gateway.removeErrors = map[string]error{"two": errors.New("engine refused removal")}
	registry := newFakeComposeRegistry(true)
	result, err := NewProjectDeleter(gateway, registry).Delete(context.Background(), composeDeleteRequest())
	if ErrorCode(err) != "DOCKER_PROJECT_DELETE_FAILED" {
		t.Fatalf("error code = %q, want DOCKER_PROJECT_DELETE_FAILED", ErrorCode(err))
	}
	if !result.Partial || !result.RegistryRolledBack || result.Completed || !result.Containers[0].Deleted || result.Containers[1].Deleted {
		t.Fatalf("result = %#v", result)
	}
	if registry.tx.commits != 0 || registry.tx.rollbacks != 1 || registry.restoreCalls != 0 || registry.tx.deleted {
		t.Fatalf("rollback operations = %#v", registry)
	}
}

func TestProjectDeleterProtectsStandaloneAndNCPProjects(t *testing.T) {
	tests := []ProjectDeleteRequest{
		{ProjectID: standaloneProjectID, Kind: ProjectKindStandalone, RegistryName: "standalone"},
		{ProjectID: "compose:nas-control-plane", Kind: ProjectKindCompose, RegistryName: "nas-control-plane"},
	}
	for _, request := range tests {
		if _, err := request.Normalize(); ErrorCode(err) != "DOCKER_PROJECT_DELETE_PROTECTED" {
			t.Fatalf("request %#v error code = %q", request, ErrorCode(err))
		}
	}
}

func TestProjectDeleterRejectsProjectIdentityMismatch(t *testing.T) {
	request := ProjectDeleteRequest{ProjectID: "compose:other", Kind: ProjectKindCompose, RegistryName: "demo"}
	if _, err := request.Normalize(); ErrorCode(err) != "DOCKER_PROJECT_DELETE_INVALID" {
		t.Fatalf("error code = %q", ErrorCode(err))
	}
}

func TestProjectDeleterRestoresRegistryWhenIntegrityCheckFails(t *testing.T) {
	gateway := newFakeProjectDeleteGateway(ContainerSnapshot{ID: "one", Name: "/one"})
	registry := newFakeComposeRegistry(true)
	registry.integrityErr = errors.New("integrity failed")
	result, err := NewProjectDeleter(gateway, registry).Delete(context.Background(), composeDeleteRequest())
	if ErrorCode(err) != "DOCKER_PROJECT_DELETE_INTEGRITY_FAILED" {
		t.Fatalf("error code = %q, want DOCKER_PROJECT_DELETE_INTEGRITY_FAILED", ErrorCode(err))
	}
	if !result.Partial || !result.RegistryRolledBack || result.Completed || registry.restoreCalls != 1 || !registry.exists {
		t.Fatalf("result = %#v registry = %#v", result, registry)
	}
}

func TestSQLiteComposeRegistryDiscoversNullableAppIDAndDeletesExactKey(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "docker_info_log.db")
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	_, err = database.Exec(`CREATE TABLE compose (created_at TEXT, updated_at TEXT, name TEXT UNIQUE, state INTEGER, path TEXT, content TEXT, app_id TEXT, container_num INTEGER)`)
	if err != nil {
		database.Close()
		t.Fatalf("create schema error = %v", err)
	}
	_, err = database.Exec(`INSERT INTO compose (name, path, app_id, state, content, container_num) VALUES ('demo', '/volume2/demo/compose.yaml', NULL, 1, 'content', 1), ('other', '/volume2/other/compose.yaml', 'app-2', 1, 'other', 1)`)
	if err != nil {
		database.Close()
		t.Fatalf("insert rows error = %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("database.Close() error = %v", err)
	}

	registry := NewSQLiteComposeRegistry(databasePath)
	tx, err := registry.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	entry, err := tx.FindByName(context.Background(), "demo")
	if err != nil {
		t.Fatalf("FindByName() error = %v", err)
	}
	if entry.Key != (ComposeRegistryKey{Name: "demo", Path: "/volume2/demo/compose.yaml"}) {
		t.Fatalf("entry key = %#v", entry.Key)
	}
	if err := tx.DeleteExact(context.Background(), entry.Key); err != nil {
		t.Fatalf("DeleteExact() error = %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if err := registry.IntegrityCheck(context.Background()); err != nil {
		t.Fatalf("IntegrityCheck() error = %v", err)
	}
	check, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer check.Close()
	var remainingName, remainingApp string
	if err := check.QueryRow(`SELECT name, app_id FROM compose`).Scan(&remainingName, &remainingApp); err != nil {
		t.Fatalf("remaining row error = %v", err)
	}
	if remainingName != "other" || remainingApp != "app-2" {
		t.Fatalf("remaining row = %q %q", remainingName, remainingApp)
	}
}

func composeDeleteRequest() ProjectDeleteRequest {
	return ProjectDeleteRequest{ProjectID: "compose:demo", Kind: ProjectKindCompose, RegistryName: "demo"}
}

type fakeProjectDeleteGateway struct {
	inventory    []InventoryContainer
	snapshots    map[string]ContainerSnapshot
	removeErrors map[string]error
	removed      []string
}

func newFakeProjectDeleteGateway(snapshots ...ContainerSnapshot) *fakeProjectDeleteGateway {
	result := &fakeProjectDeleteGateway{snapshots: make(map[string]ContainerSnapshot), removeErrors: make(map[string]error)}
	for _, snapshot := range snapshots {
		result.snapshots[snapshot.ID] = snapshot
		result.inventory = append(result.inventory, InventoryContainer{
			ID: snapshot.ID, Name: snapshot.Name, State: containerState(snapshot.Running),
			Labels: map[string]string{composeProjectLabel: "demo"},
		})
	}
	return result
}

func (f *fakeProjectDeleteGateway) ListInventoryContainers(context.Context) ([]InventoryContainer, error) {
	return append([]InventoryContainer(nil), f.inventory...), nil
}

func (f *fakeProjectDeleteGateway) InspectContainer(_ context.Context, id string) (ContainerSnapshot, error) {
	snapshot, ok := f.snapshots[id]
	if !ok {
		return ContainerSnapshot{}, errors.New("container not found")
	}
	return snapshot, nil
}

func (f *fakeProjectDeleteGateway) RemoveContainer(_ context.Context, id string) error {
	if err := f.removeErrors[id]; err != nil {
		return err
	}
	f.removed = append(f.removed, id)
	return nil
}

type fakeComposeRegistry struct {
	exists         bool
	backupCalls    int
	beginCalls     int
	integrityCalls int
	restoreCalls   int
	integrityErr   error
	tx             *fakeComposeRegistryTx
}

func newFakeComposeRegistry(exists bool) *fakeComposeRegistry {
	return &fakeComposeRegistry{exists: exists}
}

func (f *fakeComposeRegistry) Backup(context.Context) (ComposeRegistryBackup, error) {
	f.backupCalls++
	return ComposeRegistryBackup{Path: "fake", Data: []byte("backup")}, nil
}

func (f *fakeComposeRegistry) Begin(context.Context) (ComposeRegistryTx, error) {
	f.beginCalls++
	f.tx = &fakeComposeRegistryTx{
		registry: f,
		found:    f.exists,
		entry: ComposeRegistryEntry{Key: ComposeRegistryKey{
			Name: "demo", Path: "/volume2/demo/compose.yaml", AppID: "app-1",
		}},
	}
	return f.tx, nil
}

func (f *fakeComposeRegistry) IntegrityCheck(context.Context) error {
	f.integrityCalls++
	return f.integrityErr
}

func (f *fakeComposeRegistry) Restore(context.Context, ComposeRegistryBackup) error {
	f.restoreCalls++
	f.exists = true
	return nil
}

type fakeComposeRegistryTx struct {
	registry  *fakeComposeRegistry
	found     bool
	entry     ComposeRegistryEntry
	key       ComposeRegistryKey
	deleted   bool
	commits   int
	rollbacks int
}

func (f *fakeComposeRegistryTx) FindByName(_ context.Context, name string) (ComposeRegistryEntry, error) {
	if !f.found || name != f.entry.Key.Name {
		return ComposeRegistryEntry{}, ErrComposeRegistryEntryNotFound
	}
	return f.entry, nil
}

func (f *fakeComposeRegistryTx) DeleteExact(_ context.Context, key ComposeRegistryKey) error {
	f.key = key
	if !f.found || key != f.entry.Key {
		return ErrComposeRegistryEntryNotFound
	}
	f.deleted = true
	f.registry.exists = false
	return nil
}

func (f *fakeComposeRegistryTx) Commit(context.Context) error {
	f.commits++
	return nil
}

func (f *fakeComposeRegistryTx) Rollback(context.Context) error {
	f.rollbacks++
	if f.deleted {
		f.registry.exists = true
		f.deleted = false
	}
	return nil
}
