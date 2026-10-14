package database

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// 本文件只使用合成凭据、临时 SQLite 和内存回调，不启动监听器或连接远端数据库。
type activationTester func(context.Context, Connection) (ConnectionDiagnostic, error)

func (f activationTester) TestConnection(ctx context.Context, connection Connection) (ConnectionDiagnostic, error) {
	return f(ctx, connection)
}

type activationStore struct {
	CredentialStore
	afterRead      func(CredentialSnapshot)
	beforeRotate   func()
	beforeDisable  func(CredentialBaseline) error
	reads, lists   int
	writes, resets int
}

func (s *activationStore) ReadCredentialSnapshot(ctx context.Context, id string) (CredentialSnapshot, error) {
	s.reads++
	snapshot, err := s.CredentialStore.ReadCredentialSnapshot(ctx, id)
	if err == nil && s.afterRead != nil {
		s.afterRead(snapshot)
	}
	return snapshot, err
}

func (s *activationStore) ListCredentials(ctx context.Context) ([]StoredCredential, error) {
	s.lists++
	return s.CredentialStore.ListCredentials(ctx)
}

func (s *activationStore) CompareAndSwapCredential(ctx context.Context, baseline CredentialBaseline, record StoredCredential) error {
	s.writes++
	return s.CredentialStore.CompareAndSwapCredential(ctx, baseline, record)
}

func (s *activationStore) ApplyRotation(ctx context.Context, updates []CredentialRotationUpdate, disables []CredentialRotationDisable) error {
	if s.beforeRotate != nil {
		s.beforeRotate()
	}
	return s.CredentialStore.ApplyRotation(ctx, updates, disables)
}

func (s *activationStore) DisableAutomaticIfUnchanged(ctx context.Context, baseline CredentialBaseline, code ErrorCode) error {
	s.resets++
	if s.beforeDisable != nil {
		if err := s.beforeDisable(baseline); err != nil {
			return err
		}
	}
	return s.CredentialStore.DisableAutomaticIfUnchanged(ctx, baseline, code)
}

func activationKeys(t *testing.T) *MemoryKeyProvider {
	t.Helper()
	keys, err := NewMemoryKeyProvider(1, bytes.Repeat([]byte{0x51}, deploymentKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func activationVault(t *testing.T, store CredentialStore, keys KeyProvider) *CredentialVault {
	t.Helper()
	vault, err := NewCredentialVault(store, keys)
	if err != nil {
		t.Fatal(err)
	}
	vault.now = func() time.Time { return time.Unix(1730000000, 0).UTC() }
	return vault
}

func activationSource(id string) Source {
	return Source{ID: id, Driver: DriverPostgreSQL, Host: "synthetic.invalid", Port: 5432, DefaultDatabase: "fixture"}
}

func activationCredentials(label string) Credentials {
	return Credentials{Username: "fixture-user", Password: "synthetic-" + label, Token: "synthetic-token-" + label, Database: "fixture"}
}

func activationSave(t *testing.T, vault *CredentialVault, source Source, credentials Credentials) {
	t.Helper()
	coordinator, err := NewConnectionCoordinator(vault, activationTester(func(context.Context, Connection) (ConnectionDiagnostic, error) {
		return ConnectionDiagnostic{Connected: true, Code: "connected"}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.ManualConnect(context.Background(), source, credentials); err != nil {
		t.Fatal(err)
	}
}

func activationAssertCredentials(t *testing.T, keys KeyProvider, record StoredCredential, want Credentials) {
	t.Helper()
	got, err := DecryptCredentials(context.Background(), keys, credentialAAD(record.SourceID, record.Driver, record.Endpoint), record.Ciphertext)
	if err != nil || got != want {
		t.Fatalf("synthetic credentials were not retained: error=%v", err)
	}
	var envelope encryptedCredentialEnvelope
	if err := json.Unmarshal(record.Ciphertext, &envelope); err != nil || envelope.Version != 1 {
		t.Fatal("A1 must continue writing v1 envelopes")
	}
}

func TestCredentialActivationLateManualCannotOverwrite(t *testing.T) {
	for _, scenario := range []string{"initially-absent", "same-second", "state-aba", "delete-reinsert", "absent-insert-delete"} {
		t.Run(scenario, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "shared.db")
			first, other := revisionOpenStore(t, path), revisionOpenStore(t, path)
			keys := activationKeys(t)
			writer := activationVault(t, other, keys)
			source := activationSource("source")
			if scenario != "initially-absent" && scenario != "absent-insert-delete" {
				activationSave(t, writer, source, activationCredentials("initial"))
			}
			original := revisionSnapshot(t, first, source.ID)
			counted := &activationStore{CredentialStore: first}
			vault := activationVault(t, counted, keys)
			started, release := make(chan struct{}), make(chan struct{})
			tests := 0
			coordinator, err := NewConnectionCoordinator(vault, activationTester(func(_ context.Context, request Connection) (ConnectionDiagnostic, error) {
				tests++
				close(started)
				<-release
				return ConnectionDiagnostic{Connected: true, Code: "connected"}, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			type outcome struct {
				diagnostic ConnectionDiagnostic
				err        error
			}
			done := make(chan outcome, 1)
			go func() {
				diagnostic, err := coordinator.ManualConnect(context.Background(), source, activationCredentials("late-a"))
				done <- outcome{diagnostic, err}
			}()
			<-started
			switch scenario {
			case "initially-absent", "same-second":
				activationSave(t, writer, source, activationCredentials("winner-b"))
			case "state-aba":
				if err := writer.DisableAutomatic(context.Background(), source.ID, CodeCredentialCorrupt); err != nil {
					t.Fatal(err)
				}
				if err := revisionPut(t, other, original.Record); err != nil {
					t.Fatal(err)
				}
			case "delete-reinsert":
				if err := writer.Delete(context.Background(), source.ID); err != nil {
					t.Fatal(err)
				}
				if err := revisionPut(t, other, original.Record); err != nil {
					t.Fatal(err)
				}
			case "absent-insert-delete":
				activationSave(t, writer, source, activationCredentials("winner-b"))
				if err := writer.Delete(context.Background(), source.ID); err != nil {
					t.Fatal(err)
				}
			}
			winner := revisionSnapshot(t, other, source.ID)
			close(release)
			result := <-done
			if ErrorCodeOf(result.err) != CodeCredentialConflict || result.err.Error() != "credential_conflict" ||
				!result.diagnostic.Connected || result.diagnostic.Code != "credential_conflict" || result.diagnostic.Operation != "manual_persist" {
				t.Fatalf("late manual outcome lost test/persist distinction: %#v %v", result.diagnostic, result.err)
			}
			var databaseErr *DatabaseError
			if !errors.As(result.err, &databaseErr) || databaseErr.Operation != "manual_persist" {
				t.Fatal("persistence conflict context is missing")
			}
			if tests != 1 || counted.reads != 1 || counted.writes != 1 || counted.resets != 0 {
				t.Fatalf("late manual retried or disabled: tests=%d reads=%d writes=%d resets=%d", tests, counted.reads, counted.writes, counted.resets)
			}
			revisionAssertUnchanged(t, first, winner)
			if scenario == "same-second" {
				if original.Record.UpdatedAt != winner.Record.UpdatedAt {
					t.Fatal("same-second fixture timestamps differ")
				}
				activationAssertCredentials(t, keys, winner.Record, activationCredentials("winner-b"))
			}
		})
	}
}

func TestCredentialActivationFreezesValuesAndSkipsWritesOnTestFailure(t *testing.T) {
	for _, failedCode := range []ErrorCode{"", CodeAuthFailed, CodeCredentialConflict} {
		t.Run(string(failedCode), func(t *testing.T) {
			store := revisionTestStore(t)
			counted := &activationStore{CredentialStore: store}
			keys := activationKeys(t)
			vault := activationVault(t, counted, keys)
			source, credentials := activationSource("source"), activationCredentials("original")
			tests := 0
			coordinator, err := NewConnectionCoordinator(vault, activationTester(func(_ context.Context, request Connection) (ConnectionDiagnostic, error) {
				tests++
				if counted.reads != 1 {
					t.Fatal("tester ran before snapshot")
				}
				request.Credentials = activationCredentials("provider-mutation")
				source.Host, credentials.Password = "changed.invalid", "changed"
				if failedCode != "" {
					return ConnectionDiagnostic{}, &DatabaseError{Code: failedCode, Operation: "test_connection"}
				}
				return ConnectionDiagnostic{Connected: true, Code: "connected"}, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			diagnostic, err := coordinator.ManualConnect(context.Background(), source, credentials)
			if tests != 1 || counted.reads != 1 {
				t.Fatal("test or snapshot repeated")
			}
			if failedCode != "" {
				if ErrorCodeOf(err) != failedCode || diagnostic.Connected || diagnostic.Operation != "manual_connect" || counted.writes != 0 {
					t.Fatalf("failed tester wrote or claimed success: %#v %v", diagnostic, err)
				}
				if revisionSnapshot(t, store, "source").Baseline.Exists {
					t.Fatal("failed tester saved a record")
				}
				return
			}
			if err != nil || !diagnostic.Connected || counted.writes != 1 {
				t.Fatalf("successful tester did not persist: %v", err)
			}
			record := revisionSnapshot(t, store, "source").Record
			if record.Endpoint != "synthetic.invalid:5432/fixture" {
				t.Fatal("source was not frozen before test")
			}
			activationAssertCredentials(t, keys, record, activationCredentials("original"))
		})
	}
}

func TestCredentialActivationResolveFailureCannotDisableReplacement(t *testing.T) {
	for _, scenario := range []string{"corrupt", "key-unavailable", "source-mismatch", "already-disabled"} {
		t.Run(scenario, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "shared.db")
			reader, writer := revisionOpenStore(t, path), revisionOpenStore(t, path)
			keys := activationKeys(t)
			goodVault := activationVault(t, writer, keys)
			source := activationSource("source")
			activationSave(t, goodVault, source, activationCredentials("old"))
			wantCode := CodeCredentialsRequired
			switch scenario {
			case "corrupt":
				revisionExec(t, writer, `UPDATE ncp_database_credentials SET ciphertext = X'01' WHERE source_id = 'source'`)
				wantCode = CodeCredentialCorrupt
			case "key-unavailable":
				wantCode = CodeKeyUnavailable
			case "source-mismatch":
				revisionExec(t, writer, `UPDATE ncp_database_credentials SET endpoint = 'old.invalid:5432/fixture' WHERE source_id = 'source'`)
			case "already-disabled":
				if err := goodVault.DisableAutomatic(context.Background(), source.ID, CodeCredentialCorrupt); err != nil {
					t.Fatal(err)
				}
			}
			counted := &activationStore{CredentialStore: reader}
			var replacement CredentialSnapshot
			counted.afterRead = func(CredentialSnapshot) {
				// 模拟仍可写旧形状的并发 writer，保持 A1 的 v1 胜者断言。
				// C 的受保护手动入口不允许 nil TLS 修复损坏的旧记录。
				credentials := activationCredentials("replacement")
				before := revisionSnapshot(t, writer, source.ID)
				record := before.Record
				record.Driver, record.Endpoint = source.Driver, sourceEndpoint(source, credentials.Database)
				record.Username, record.Database = credentials.Username, credentials.Database
				record.AutomaticEnabled, record.LastErrorCode = true, ""
				var err error
				record.Ciphertext, record.KeyVersion, err = EncryptCredentials(context.Background(), keys,
					credentialAAD(source.ID, source.Driver, record.Endpoint), credentials)
				if err != nil {
					t.Fatal(err)
				}
				if err := writer.CompareAndSwapCredential(context.Background(), before.Baseline, record); err != nil {
					t.Fatal(err)
				}
				replacement = revisionSnapshot(t, writer, source.ID)
			}
			var resolverKeys KeyProvider = keys
			if scenario == "key-unavailable" {
				resolverKeys = &activationKeyProvider{MemoryKeyProvider: keys, lookupError: &DatabaseError{Code: CodeKeyUnavailable}}
			}
			vault := activationVault(t, counted, resolverKeys)
			credentials, found, err := vault.Resolve(context.Background(), source)
			if ErrorCodeOf(err) != wantCode || found || credentials != (Credentials{}) || counted.resets != 1 || counted.reads != 1 {
				t.Fatalf("resolve failure changed its error or retried: found=%v err=%v reads=%d disables=%d", found, err, counted.reads, counted.resets)
			}
			revisionAssertUnchanged(t, reader, replacement)
			activationAssertCredentials(t, keys, replacement.Record, activationCredentials("replacement"))
		})
	}
}

type activationKeyProvider struct {
	*MemoryKeyProvider
	rotateHook  func()
	rotateError error
	lookupError error
	invalidKey  bool
}

func (p *activationKeyProvider) Rotate(ctx context.Context) (KeyMaterial, error) {
	if p.rotateHook != nil {
		p.rotateHook()
	}
	if p.rotateError != nil {
		return KeyMaterial{}, p.rotateError
	}
	key, err := p.MemoryKeyProvider.Rotate(ctx)
	if p.invalidKey {
		key.Key = nil
	}
	return key, err
}

func (p *activationKeyProvider) ForVersion(ctx context.Context, version int) (KeyMaterial, error) {
	if p.lookupError != nil {
		return KeyMaterial{}, p.lookupError
	}
	return p.MemoryKeyProvider.ForVersion(ctx, version)
}

func TestCredentialActivationRotationConflictRetainsPublishedKeys(t *testing.T) {
	for _, concurrentOperation := range []string{"manual-save", "rotation"} {
		t.Run(concurrentOperation, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "shared.db")
			first, second := revisionOpenStore(t, path), revisionOpenStore(t, path)
			keys := activationKeys(t)
			writer := activationVault(t, second, keys)
			for _, id := range []string{"a", "b"} {
				activationSave(t, writer, activationSource(id), activationCredentials(id))
			}
			oldA := revisionSnapshot(t, first, "a")
			counted := &activationStore{CredentialStore: first}
			vault := activationVault(t, counted, keys)
			var winnerA, winnerB CredentialSnapshot
			counted.beforeRotate = func() {
				if concurrentOperation == "manual-save" {
					activationSave(t, writer, activationSource("b"), activationCredentials("winner-b"))
				} else if err := writer.Rotate(context.Background()); err != nil {
					t.Fatal(err)
				}
				winnerA, winnerB = revisionSnapshot(t, second, "a"), revisionSnapshot(t, second, "b")
			}
			err := vault.Rotate(context.Background())
			if ErrorCodeOf(err) != CodeKeyRotationFailed || !errors.Is(err, errCredentialConflict) {
				t.Fatalf("rotation conflict was not retained as safe cause: %v", err)
			}
			if counted.lists != 1 || counted.reads != 0 || counted.resets != 0 {
				t.Fatalf("rotation conflict refreshed or disabled: lists=%d reads=%d resets=%d", counted.lists, counted.reads, counted.resets)
			}
			revisionAssertUnchanged(t, first, winnerA)
			revisionAssertUnchanged(t, first, winnerB)
			current, err := keys.Current(context.Background())
			wantVersion := 2
			if concurrentOperation == "rotation" {
				wantVersion = 3
			}
			if err != nil || current.Version != wantVersion {
				t.Fatal("published key was rolled back")
			}
			for version := 1; version <= wantVersion; version++ {
				if _, err := keys.ForVersion(context.Background(), version); err != nil {
					t.Fatal("retained key was removed")
				}
			}
			activationAssertCredentials(t, keys, oldA.Record, activationCredentials("a"))
			activationAssertCredentials(t, keys, winnerA.Record, activationCredentials("a"))
			wantB := activationCredentials("b")
			if concurrentOperation == "manual-save" {
				wantB = activationCredentials("winner-b")
				if !reflect.DeepEqual(oldA, winnerA) || winnerB.Record.KeyVersion != 2 {
					t.Fatal("rotation transaction partially committed or concurrent save did not use published key")
				}
			}
			activationAssertCredentials(t, keys, winnerB.Record, wantB)
		})
	}
}

func TestCredentialActivationRotationFailureDisablesOnlyFrozenSnapshots(t *testing.T) {
	for _, scenario := range []string{"publish-failure", "lookup-failure", "invalid-new-key"} {
		t.Run(scenario, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "shared.db")
			first, second := revisionOpenStore(t, path), revisionOpenStore(t, path)
			keys := activationKeys(t)
			writer := activationVault(t, second, keys)
			for _, id := range []string{"a", "b"} {
				activationSave(t, writer, activationSource(id), activationCredentials(id))
			}
			oldB := revisionSnapshot(t, first, "b")
			provider := &activationKeyProvider{MemoryKeyProvider: keys}
			if scenario == "publish-failure" {
				provider.rotateError = &DatabaseError{Code: CodeKeyUnavailable}
			} else if scenario == "lookup-failure" {
				provider.lookupError = &DatabaseError{Code: CodeKeyUnavailable}
			} else {
				provider.invalidKey = true
			}
			var replacement, inserted CredentialSnapshot
			provider.rotateHook = func() {
				activationSave(t, writer, activationSource("a"), activationCredentials("replacement"))
				activationSave(t, writer, activationSource("new"), activationCredentials("new"))
				replacement = revisionSnapshot(t, second, "a")
				inserted = revisionSnapshot(t, second, "new")
			}
			counted := &activationStore{CredentialStore: first}
			vault := activationVault(t, counted, provider)
			if err := vault.Rotate(context.Background()); ErrorCodeOf(err) != CodeKeyRotationFailed {
				t.Fatalf("rotation failure = %v", err)
			}
			if counted.lists != 1 || counted.reads != 0 || counted.resets != 2 {
				t.Fatal("rotation failure did not use the original frozen record list")
			}
			revisionAssertUnchanged(t, first, replacement)
			revisionAssertUnchanged(t, first, inserted)
			afterB := revisionSnapshot(t, first, "b").Record
			if afterB.AutomaticEnabled || afterB.LastErrorCode != CodeKeyRotationFailed || !bytes.Equal(afterB.Ciphertext, oldB.Record.Ciphertext) {
				t.Fatal("unchanged affected record was not disabled without changing ciphertext")
			}
		})
	}
}

func TestCredentialActivationRotationDisableFailureIsRecorded(t *testing.T) {
	store := revisionTestStore(t)
	keys := activationKeys(t)
	activationSave(t, activationVault(t, store, keys), activationSource("source"), activationCredentials("old"))
	before := revisionSnapshot(t, store, "source")
	counted := &activationStore{CredentialStore: store, beforeDisable: func(CredentialBaseline) error {
		return errors.New("synthetic-storage-failure")
	}}
	provider := &activationKeyProvider{MemoryKeyProvider: keys, rotateError: &DatabaseError{Code: CodeKeyUnavailable}}
	err := activationVault(t, counted, provider).Rotate(context.Background())
	var rotationErr *DatabaseError
	if !errors.As(err, &rotationErr) || rotationErr.Code != CodeKeyRotationFailed {
		t.Fatalf("rotation error = %v", err)
	}
	joined, ok := rotationErr.Cause.(interface{ Unwrap() []error })
	if !ok || len(joined.Unwrap()) != 2 || ErrorCodeOf(joined.Unwrap()[1]) != CodeCredentialStoreUnavailable {
		t.Fatal("best-effort disable failure was silently ignored")
	}
	if err.Error() != "key_rotation_failed" || counted.resets != 1 {
		t.Fatal("failure leaked storage detail or retried")
	}
	revisionAssertUnchanged(t, store, before)
}

func TestCredentialActivationKeyRotationRejectsVersionReuse(t *testing.T) {
	for _, providerKind := range []string{"memory", "file"} {
		for _, scenario := range []string{"max-int", "occupied-next"} {
			t.Run(providerKind+"/"+scenario, func(t *testing.T) {
				version := 1
				if scenario == "max-int" {
					version = math.MaxInt
				}
				key := bytes.Repeat([]byte{0x53}, deploymentKeyBytes)
				var provider RotatingKeyProvider
				var filePath string
				var before []byte
				if providerKind == "memory" {
					memory, err := NewMemoryKeyProvider(version, key)
					if err != nil {
						t.Fatal(err)
					}
					if scenario == "occupied-next" {
						memory.keys[2] = append([]byte(nil), key...)
					}
					provider = memory
				} else {
					filePath = filepath.Join(t.TempDir(), "synthetic-keys.json")
					file := &FileKeyProvider{path: filePath}
					document := keyFileDocument{CurrentVersion: version, Keys: map[string]string{strconv.Itoa(version): base64.RawStdEncoding.EncodeToString(key)}}
					if scenario == "occupied-next" {
						document.Keys["2"] = base64.RawStdEncoding.EncodeToString(key)
					}
					if err := file.write(document); err != nil {
						t.Fatal(err)
					}
					before, _ = os.ReadFile(filePath)
					provider = file
				}
				if _, err := provider.Rotate(context.Background()); ErrorCodeOf(err) != CodeKeyRotationFailed || err.Error() != "key_rotation_failed" {
					t.Fatalf("version reuse error = %v", err)
				}
				current, err := provider.Current(context.Background())
				if err != nil || current.Version != version || !bytes.Equal(current.Key, key) {
					t.Fatal("failed rotation replaced the active key")
				}
				if scenario == "occupied-next" {
					retained, err := provider.ForVersion(context.Background(), 2)
					if err != nil || !bytes.Equal(retained.Key, key) {
						t.Fatal("failed rotation replaced a retained key")
					}
				}
				if filePath != "" {
					after, err := os.ReadFile(filePath)
					if err != nil || !bytes.Equal(before, after) {
						t.Fatal("failed rotation changed key file bytes")
					}
				}
			})
		}
	}
}
