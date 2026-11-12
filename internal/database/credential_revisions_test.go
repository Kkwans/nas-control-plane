package database

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"
)

// 所有夹具仅使用 t.TempDir 中的 SQLite 和合成数据，不连接远端或读取真实凭据。
func revisionTestStore(t *testing.T) *SQLiteCredentialStore {
	t.Helper()
	return revisionOpenStore(t, filepath.Join(t.TempDir(), "revision.db"))
}

func revisionOpenStore(t *testing.T, path string) *SQLiteCredentialStore {
	t.Helper()
	store, err := OpenSQLiteCredentialStore(path)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return time.Unix(1730000000, 0).UTC() }
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func revisionFixture(sourceID string) StoredCredential {
	return StoredCredential{
		SourceID: sourceID, Driver: DriverPostgreSQL, Endpoint: "synthetic.invalid:5432/fixture",
		Username: "fixture-user", Database: "fixture", Ciphertext: []byte("synthetic-ciphertext"),
		KeyVersion: 1, AutomaticEnabled: true,
		CreatedAt: time.Unix(1730000000, 0).UTC(), UpdatedAt: time.Unix(1730000000, 0).UTC(),
	}
}

func revisionSnapshot(t *testing.T, store *SQLiteCredentialStore, sourceID string) CredentialSnapshot {
	t.Helper()
	snapshot, err := store.ReadCredentialSnapshot(context.Background(), sourceID)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func revisionInsert(t *testing.T, store *SQLiteCredentialStore, sourceID string) CredentialSnapshot {
	t.Helper()
	before := revisionSnapshot(t, store, sourceID)
	if err := store.CompareAndSwapCredential(context.Background(), before.Baseline, revisionFixture(sourceID)); err != nil {
		t.Fatal(err)
	}
	return revisionSnapshot(t, store, sourceID)
}

// 常规夹具也必须经过快照和 CAS；仅旧 schema 迁移夹具使用明确的原始 INSERT。
func revisionPut(t *testing.T, store *SQLiteCredentialStore, record StoredCredential) error {
	t.Helper()
	return store.CompareAndSwapCredential(context.Background(), revisionSnapshot(t, store, record.SourceID).Baseline, record)
}

func revisionLegacyPut(ctx context.Context, store *SQLiteCredentialStore, record StoredCredential) error {
	_, err := store.database.ExecContext(ctx, `INSERT INTO ncp_database_credentials (
		source_id, driver, endpoint, username, database_name, ciphertext, key_version,
		automatic_enabled, last_error_code, created_at_unix, updated_at_unix
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, record.SourceID, string(record.Driver), record.Endpoint,
		record.Username, record.Database, record.Ciphertext, record.KeyVersion, boolInt(record.AutomaticEnabled),
		string(record.LastErrorCode), record.CreatedAt.Unix(), record.UpdatedAt.Unix())
	return err
}

func revisionExec(t *testing.T, store *SQLiteCredentialStore, statement string, args ...any) {
	t.Helper()
	if _, err := store.database.ExecContext(context.Background(), statement, args...); err != nil {
		t.Fatal(err)
	}
}

func revisionAssertUnchanged(t *testing.T, store *SQLiteCredentialStore, before CredentialSnapshot) {
	t.Helper()
	if after := revisionSnapshot(t, store, before.Baseline.SourceID); !reflect.DeepEqual(after, before) {
		t.Fatalf("snapshot changed unexpectedly for %q", before.Baseline.SourceID)
	}
}

func revisionAssertConflict(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, errCredentialConflict) || err.Error() != string(CodeCredentialConflict) || ErrorCodeOf(err) != CodeCredentialConflict {
		t.Fatalf("expected stable credential conflict, got %v", err)
	}
}

func revisionAssertStoreFailure(t *testing.T, err error) {
	t.Helper()
	if ErrorCodeOf(err) != CodeCredentialStoreUnavailable || errors.Is(err, errCredentialConflict) || err.Error() != string(CodeCredentialStoreUnavailable) {
		t.Fatalf("expected fixed persistence failure, got %v", err)
	}
}

func revisionLegacyStore(t *testing.T) *SQLiteCredentialStore {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewSQLiteCredentialStore(db)
	if err != nil {
		t.Fatal(err)
	}
	revisionExec(t, store, `CREATE TABLE ncp_database_schema_migrations (version INTEGER PRIMARY KEY, applied_at_unix INTEGER NOT NULL)`)
	for _, migration := range credentialMigrations[:2] {
		if err := store.applyMigration(context.Background(), migration); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func TestCredentialRevisionMigrationPreservesLegacyV1AndRepeats(t *testing.T) {
	store := revisionLegacyStore(t)
	ctx := context.Background()
	keys, err := NewMemoryKeyProvider(1, bytes.Repeat([]byte{0x42}, deploymentKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	credentials := Credentials{Username: "fixture-user", Password: "synthetic-password", Token: "synthetic-token", Database: "fixture"}
	legacy := revisionFixture("legacy")
	legacy.Ciphertext, legacy.KeyVersion, err = EncryptCredentials(ctx, keys, credentialAAD(legacy.SourceID, legacy.Driver, legacy.Endpoint), credentials)
	if err != nil {
		t.Fatal(err)
	}
	if err := revisionLegacyPut(ctx, store, legacy); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := store.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		got := revisionSnapshot(t, store, "legacy")
		want := legacy
		want.Revision = 1
		if !reflect.DeepEqual(got.Record, want) || got.Baseline != (CredentialBaseline{SourceID: "legacy", Revision: 1, Exists: true}) {
			t.Fatal("migration changed legacy credential data or repeated the backfill")
		}
		decoded, err := DecryptCredentials(ctx, keys, credentialAAD(legacy.SourceID, legacy.Driver, legacy.Endpoint), got.Record.Ciphertext)
		if err != nil || decoded != credentials {
			t.Fatal("legacy v1 payload no longer authenticates")
		}
	}
	var migrations, triggers int
	if err := store.database.QueryRow(`SELECT COUNT(*) FROM ncp_database_schema_migrations`).Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	if err := store.database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND tbl_name = 'ncp_database_credentials'`).Scan(&triggers); err != nil {
		t.Fatal(err)
	}
	if migrations != 3 || triggers != 4 {
		t.Fatalf("migrations=%d triggers=%d", migrations, triggers)
	}
}

func TestCredentialRevisionMigrationFailureRollsBackAndRetries(t *testing.T) {
	store := revisionLegacyStore(t)
	legacy := revisionFixture("legacy")
	if err := revisionLegacyPut(context.Background(), store, legacy); err != nil {
		t.Fatal(err)
	}
	// 让第三次迁移在已建表和回填后失败，验证 DDL、回填和 ledger 一起回滚。
	revisionExec(t, store, `CREATE TRIGGER ncp_database_credentials_revision_update AFTER UPDATE ON ncp_database_credentials BEGIN SELECT 1; END`)
	if err := store.Migrate(context.Background()); ErrorCodeOf(err) != CodeMigrationFailed {
		t.Fatalf("migration error = %v", err)
	}
	var tables, versions int
	if err := store.database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'ncp_database_credential_revisions'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if err := store.database.QueryRow(`SELECT COUNT(*) FROM ncp_database_schema_migrations`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if tables != 0 || versions != 2 {
		t.Fatal("failed migration left partial schema or ledger")
	}
	revisionExec(t, store, `DROP TRIGGER ncp_database_credentials_revision_update`)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	legacy.Revision = 1
	if !reflect.DeepEqual(revisionSnapshot(t, store, "legacy").Record, legacy) {
		t.Fatal("failed migration changed legacy data")
	}
}

func TestCredentialRevisionTracksSecurityFieldsOnly(t *testing.T) {
	store := revisionTestStore(t)
	initial := revisionInsert(t, store, "source")
	for _, statement := range []string{
		`UPDATE ncp_database_credentials SET updated_at_unix = updated_at_unix + 1`,
		`UPDATE ncp_database_credentials SET created_at_unix = created_at_unix - 1`,
		`UPDATE ncp_database_credentials SET source_id = source_id, driver = driver, endpoint = endpoint, username = username,
		 database_name = database_name, ciphertext = ciphertext, key_version = key_version, automatic_enabled = automatic_enabled, last_error_code = last_error_code`,
	} {
		revisionExec(t, store, statement)
		if got := revisionSnapshot(t, store, "source").Baseline; got != initial.Baseline {
			t.Fatal("bookkeeping or no-op update invalidated baseline")
		}
	}
	for i, assignment := range []string{
		`driver = 'mysql'`, `endpoint = 'other.invalid:3306/fixture'`, `username = 'other-user'`,
		`database_name = 'other-database'`, `ciphertext = x'1234'`, `key_version = 2`,
		`automatic_enabled = 0`, `last_error_code = 'credential_corrupt'`,
	} {
		revisionExec(t, store, `UPDATE ncp_database_credentials SET `+assignment+` WHERE source_id = 'source'`)
		if got := revisionSnapshot(t, store, "source").Baseline.Revision; got != int64(i+2) {
			t.Fatalf("assignment %q revision=%d", assignment, got)
		}
	}
	before := revisionSnapshot(t, store, "source")
	if _, err := store.database.Exec(`UPDATE ncp_database_credentials SET source_id = 'renamed' WHERE source_id = 'source'`); err == nil {
		t.Fatal("source identity changed in place")
	}
	revisionAssertUnchanged(t, store, before)
	if revisionSnapshot(t, store, "renamed").Baseline.Revision != 0 {
		t.Fatal("failed rename created a tombstone")
	}
}

func TestCredentialRevisionMetadataOnlyUpdateKeepsCASUsable(t *testing.T) {
	store := revisionTestStore(t)
	before := revisionInsert(t, store, "source")
	revisionExec(t, store, `UPDATE ncp_database_credentials SET updated_at_unix = updated_at_unix + 10`)
	replacement := before.Record
	replacement.Ciphertext = []byte("replacement")
	replacement.CreatedAt = before.Record.CreatedAt.Add(time.Hour)
	if err := store.CompareAndSwapCredential(context.Background(), before.Baseline, replacement); err != nil {
		t.Fatal(err)
	}
	after := revisionSnapshot(t, store, "source")
	if after.Baseline.Revision != before.Baseline.Revision+1 || after.Record.CreatedAt != before.Record.CreatedAt {
		t.Fatal("CAS failed to preserve creation time or advance revision")
	}
	if err := store.CompareAndSwapCredential(context.Background(), after.Baseline, after.Record); err != nil {
		t.Fatal(err)
	}
	revisionAssertUnchanged(t, store, after)
}

func TestCredentialRevisionSnapshotAbsentAndOwnedBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshots.db")
	store := revisionOpenStore(t, path)
	missing := revisionSnapshot(t, store, "unknown")
	if missing.Baseline != (CredentialBaseline{SourceID: "unknown"}) || !reflect.DeepEqual(missing.Record, StoredCredential{}) {
		t.Fatal("fresh source snapshot is not revision zero with a zero record")
	}
	if _, err := store.GetCredential(context.Background(), "unknown"); !errors.Is(err, errCredentialNotFound) {
		t.Fatalf("get unknown = %v", err)
	}
	if _, err := store.GetCredential(context.Background(), " "); !errors.Is(err, errCredentialNotFound) {
		t.Fatalf("legacy get empty source = %v", err)
	}
	if _, err := store.ReadCredentialSnapshot(context.Background(), " "); err == nil {
		t.Fatal("snapshot accepted an empty source")
	}
	before := revisionInsert(t, store, "source")
	got := revisionSnapshot(t, store, "source")
	got.Record.Ciphertext[0] ^= 0xff
	record, err := store.GetCredential(context.Background(), "source")
	if err != nil || record.Revision != before.Baseline.Revision {
		t.Fatalf("get revision: %v", err)
	}
	record.Ciphertext[0] ^= 0xff
	listed, err := store.ListCredentials(context.Background())
	if err != nil || len(listed) != 1 || listed[0].Revision != before.Baseline.Revision {
		t.Fatalf("list revision: %v", err)
	}
	listed[0].Ciphertext[0] ^= 0xff
	revisionAssertUnchanged(t, store, before)
	if err := store.DeleteCredential(context.Background(), "source"); err != nil {
		t.Fatal(err)
	}
	tombstone := revisionSnapshot(t, store, "source")
	if tombstone.Baseline.Exists || tombstone.Baseline.Revision != 2 || !reflect.DeepEqual(tombstone.Record, StoredCredential{}) {
		t.Fatal("delete did not retain tombstone")
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	revisionAssertUnchanged(t, store, tombstone)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = revisionOpenStore(t, path)
	revisionAssertUnchanged(t, store, tombstone)
}

func TestCredentialRevisionSameSecondAndABACannotOverwrite(t *testing.T) {
	for _, scenario := range []string{"same-second", "state-aba", "delete-reinsert", "deleted"} {
		t.Run(scenario, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "shared.db")
			store := revisionOpenStore(t, path)
			other := revisionOpenStore(t, path)
			before := revisionInsert(t, store, "source")
			switch scenario {
			case "same-second":
				replacement := before.Record
				replacement.Ciphertext = []byte("concurrent-replacement")
				if err := revisionPut(t, other, replacement); err != nil {
					t.Fatal(err)
				}
			case "state-aba":
				if err := other.DisableAutomaticIfUnchanged(context.Background(), revisionSnapshot(t, other, "source").Baseline, CodeCredentialCorrupt); err != nil {
					t.Fatal(err)
				}
				if err := revisionPut(t, other, before.Record); err != nil {
					t.Fatal(err)
				}
			case "delete-reinsert", "deleted":
				if err := other.DeleteCredential(context.Background(), "source"); err != nil {
					t.Fatal(err)
				}
				if scenario == "delete-reinsert" {
					if err := revisionPut(t, other, before.Record); err != nil {
						t.Fatal(err)
					}
				}
			}
			current := revisionSnapshot(t, store, "source")
			if current.Baseline.Revision <= before.Baseline.Revision {
				t.Fatal("concurrent change did not advance revision")
			}
			if current.Baseline.Exists && current.Record.UpdatedAt != before.Record.UpdatedAt {
				t.Fatal("fixture did not preserve same-second timestamps")
			}
			revisionAssertConflict(t, store.CompareAndSwapCredential(context.Background(), before.Baseline, before.Record))
			revisionAssertConflict(t, store.DisableAutomaticIfUnchanged(context.Background(), before.Baseline, CodeCredentialCorrupt))
			revisionAssertConflict(t, store.ApplyRotation(context.Background(), []CredentialRotationUpdate{revisionRotation(before)}, nil))
			revisionAssertUnchanged(t, store, current)
		})
	}
}

func TestCredentialRevisionAbsentCreateDeleteABA(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	store, other := revisionOpenStore(t, path), revisionOpenStore(t, path)
	before := revisionSnapshot(t, store, "source")
	revisionInsert(t, other, "source")
	if err := other.DeleteCredential(context.Background(), "source"); err != nil {
		t.Fatal(err)
	}
	current := revisionSnapshot(t, store, "source")
	revisionAssertConflict(t, store.CompareAndSwapCredential(context.Background(), before.Baseline, revisionFixture("source")))
	revisionAssertUnchanged(t, store, current)
	if err := store.CompareAndSwapCredential(context.Background(), current.Baseline, revisionFixture("source")); err != nil {
		t.Fatal(err)
	}
	if after := revisionSnapshot(t, store, "source"); !after.Baseline.Exists || after.Baseline.Revision != current.Baseline.Revision+1 {
		t.Fatal("fresh tombstone baseline could not reinsert")
	}
}

func TestCredentialRevisionConcurrentCASHasOneWinner(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "shared.db")
			stores := []*SQLiteCredentialStore{revisionOpenStore(t, path), revisionOpenStore(t, path)}
			before := revisionSnapshot(t, stores[0], "source")
			if existing {
				before = revisionInsert(t, stores[0], "source")
			}
			start := make(chan struct{})
			errorsByWriter := make([]error, len(stores))
			var workers sync.WaitGroup
			for i, store := range stores {
				workers.Go(func() {
					<-start
					record := revisionFixture("source")
					record.Ciphertext = []byte(fmt.Sprintf("writer-%d", i))
					errorsByWriter[i] = store.CompareAndSwapCredential(context.Background(), before.Baseline, record)
				})
			}
			close(start)
			workers.Wait()
			winners := 0
			for _, err := range errorsByWriter {
				if err == nil {
					winners++
				} else {
					revisionAssertConflict(t, err)
				}
			}
			if winners != 1 || revisionSnapshot(t, stores[0], "source").Baseline.Revision != before.Baseline.Revision+1 {
				t.Fatal("CAS did not select exactly one winner")
			}
		})
	}
}

func TestCredentialRevisionReadsConsistentWithConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	reader, writer := revisionOpenStore(t, path), revisionOpenStore(t, path)
	initial := revisionFixture("source")
	initial.Username = "1"
	if err := revisionPut(t, writer, initial); err != nil {
		t.Fatal(err)
	}
	writeDone := make(chan error, 1)
	go func() {
		for i := 2; i <= 100; i++ {
			if _, err := writer.database.Exec(`UPDATE ncp_database_credentials SET username = ? WHERE source_id = 'source'`, strconv.Itoa(i)); err != nil {
				writeDone <- err
				return
			}
		}
		writeDone <- nil
	}()
	check := func(record StoredCredential) {
		t.Helper()
		if record.Username != strconv.FormatInt(record.Revision, 10) {
			t.Fatalf("record and revision are from different snapshots: username=%s revision=%d", record.Username, record.Revision)
		}
	}
	for range 100 {
		snapshot := revisionSnapshot(t, reader, "source")
		if snapshot.Record.Revision != snapshot.Baseline.Revision || !snapshot.Baseline.Exists {
			t.Fatal("snapshot baseline disagrees with record")
		}
		check(snapshot.Record)
		record, err := reader.GetCredential(context.Background(), "source")
		if err != nil {
			t.Fatal(err)
		}
		check(record)
		records, err := reader.ListCredentials(context.Background())
		if err != nil || len(records) != 1 {
			t.Fatalf("list error: %v", err)
		}
		check(records[0])
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
}

func revisionRotation(snapshot CredentialSnapshot) CredentialRotationUpdate {
	return CredentialRotationUpdate{SourceID: snapshot.Baseline.SourceID, Expected: snapshot.Baseline,
		Ciphertext: []byte("rotated-ciphertext"), KeyVersion: 2, AutomaticEnabled: snapshot.Record.AutomaticEnabled,
		LastErrorCode: snapshot.Record.LastErrorCode}
}

func TestCredentialRevisionConditionalDisable(t *testing.T) {
	store := revisionTestStore(t)
	before := revisionInsert(t, store, "source")
	if err := store.DisableAutomaticIfUnchanged(context.Background(), before.Baseline, ErrorCode("unknown")); err != nil {
		t.Fatal(err)
	}
	after := revisionSnapshot(t, store, "source")
	if after.Record.AutomaticEnabled || after.Record.LastErrorCode != CodeKeyRotationFailed ||
		!bytes.Equal(after.Record.Ciphertext, before.Record.Ciphertext) || after.Baseline.Revision != before.Baseline.Revision+1 {
		t.Fatal("conditional disable did not preserve ciphertext and advance policy revision")
	}
	revisionAssertConflict(t, store.DisableAutomaticIfUnchanged(context.Background(), before.Baseline, CodeCredentialCorrupt))
	if err := store.DisableAutomaticIfUnchanged(context.Background(), after.Baseline, CodeKeyRotationFailed); err != nil {
		t.Fatal(err)
	}
	revisionAssertUnchanged(t, store, after)
	revisionAssertConflict(t, store.DisableAutomaticIfUnchanged(context.Background(), revisionSnapshot(t, store, "missing").Baseline, CodeCredentialCorrupt))
}

func TestCredentialRevisionRotationZeroOneAndMultiple(t *testing.T) {
	for _, count := range []int{0, 1, 3} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			store := revisionTestStore(t)
			var snapshots []CredentialSnapshot
			var updates []CredentialRotationUpdate
			var disables []CredentialRotationDisable
			for i := 0; i < count; i++ {
				snapshot := revisionInsert(t, store, fmt.Sprintf("source-%d", i))
				snapshots = append(snapshots, snapshot)
				if i == 2 {
					disables = append(disables, CredentialRotationDisable{SourceID: snapshot.Baseline.SourceID, Expected: snapshot.Baseline, Code: CodeCredentialCorrupt})
				} else {
					updates = append(updates, revisionRotation(snapshot))
				}
			}
			if err := store.ApplyRotation(context.Background(), updates, disables); err != nil {
				t.Fatal(err)
			}
			for i, before := range snapshots {
				after := revisionSnapshot(t, store, before.Baseline.SourceID)
				if after.Baseline.Revision != before.Baseline.Revision+1 || after.Record.CreatedAt != before.Record.CreatedAt {
					t.Fatal("rotation revision or creation timestamp incorrect")
				}
				if i == 2 {
					if after.Record.AutomaticEnabled || after.Record.LastErrorCode != CodeCredentialCorrupt || !bytes.Equal(after.Record.Ciphertext, before.Record.Ciphertext) {
						t.Fatal("rotation disable changed ciphertext or failed policy update")
					}
				} else if after.Record.KeyVersion != 2 || !bytes.Equal(after.Record.Ciphertext, updates[i].Ciphertext) {
					t.Fatal("rotation update was not committed")
				}
			}
		})
	}
}

func TestCredentialRevisionRotationConflictRollsBackWholeBatch(t *testing.T) {
	for _, conflictAt := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(conflictAt), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "shared.db")
			store, other := revisionOpenStore(t, path), revisionOpenStore(t, path)
			snapshots := []CredentialSnapshot{revisionInsert(t, store, "a"), revisionInsert(t, store, "b"), revisionInsert(t, store, "c")}
			updates := []CredentialRotationUpdate{revisionRotation(snapshots[0]), revisionRotation(snapshots[1])}
			disables := []CredentialRotationDisable{{SourceID: "c", Expected: snapshots[2].Baseline, Code: CodeCredentialCorrupt}}
			replacement := snapshots[conflictAt].Record
			replacement.Ciphertext = []byte("concurrent-success")
			if err := revisionPut(t, other, replacement); err != nil {
				t.Fatal(err)
			}
			snapshots[conflictAt] = revisionSnapshot(t, store, replacement.SourceID)
			revisionAssertConflict(t, store.ApplyRotation(context.Background(), updates, disables))
			for _, before := range snapshots {
				revisionAssertUnchanged(t, store, before)
			}
		})
	}
}

func TestCredentialRevisionRotationPersistenceFailureRollsBack(t *testing.T) {
	for _, failure := range []string{"statement", "commit"} {
		t.Run(failure, func(t *testing.T) {
			store := revisionTestStore(t)
			a, b := revisionInsert(t, store, "a"), revisionInsert(t, store, "b")
			if failure == "statement" {
				revisionExec(t, store, `CREATE TRIGGER fixture_rotation_failure BEFORE UPDATE ON ncp_database_credentials WHEN OLD.source_id = 'b' BEGIN SELECT RAISE(ABORT, 'synthetic failure'); END`)
			} else {
				revisionExec(t, store, `PRAGMA foreign_keys = ON`)
				revisionExec(t, store, `CREATE TABLE fixture_parent (id INTEGER PRIMARY KEY)`)
				revisionExec(t, store, `CREATE TABLE fixture_child (id INTEGER REFERENCES fixture_parent(id) DEFERRABLE INITIALLY DEFERRED)`)
				revisionExec(t, store, `CREATE TRIGGER fixture_rotation_failure AFTER UPDATE ON ncp_database_credentials WHEN OLD.source_id = 'b' BEGIN INSERT INTO fixture_child VALUES (1); END`)
			}
			err := store.ApplyRotation(context.Background(), []CredentialRotationUpdate{revisionRotation(a), revisionRotation(b)}, nil)
			revisionAssertStoreFailure(t, err)
			revisionAssertUnchanged(t, store, a)
			revisionAssertUnchanged(t, store, b)
		})
	}
}

func TestCredentialRevisionIntegerRangeAndOverflowRollback(t *testing.T) {
	store := revisionTestStore(t)
	revisionInsert(t, store, "source")
	for _, revision := range []int64{1<<53 + 17, math.MaxInt64 - 1} {
		revisionExec(t, store, `UPDATE ncp_database_credential_revisions SET revision = ? WHERE source_id = 'source'`, revision)
		before := revisionSnapshot(t, store, "source")
		if before.Baseline.Revision != revision {
			t.Fatal("integer revision lost precision")
		}
		replacement := before.Record
		replacement.Ciphertext = []byte(fmt.Sprintf("changed-%d", revision))
		if err := store.CompareAndSwapCredential(context.Background(), before.Baseline, replacement); err != nil {
			t.Fatal(err)
		}
		if revisionSnapshot(t, store, "source").Baseline.Revision != revision+1 {
			t.Fatal("integer increment lost precision")
		}
	}
	before := revisionSnapshot(t, store, "source")
	replacement := before.Record
	replacement.Ciphertext = []byte("must-not-persist")
	revisionAssertStoreFailure(t, store.CompareAndSwapCredential(context.Background(), before.Baseline, replacement))
	revisionAssertUnchanged(t, store, before)
	revisionAssertStoreFailure(t, store.DisableAutomaticIfUnchanged(context.Background(), before.Baseline, CodeCredentialCorrupt))
	revisionAssertUnchanged(t, store, before)
	revisionAssertStoreFailure(t, store.DeleteCredential(context.Background(), "source"))
	revisionAssertUnchanged(t, store, before)
	first := revisionInsert(t, store, "first")
	revisionAssertStoreFailure(t, store.ApplyRotation(context.Background(), []CredentialRotationUpdate{revisionRotation(first), revisionRotation(before)}, nil))
	revisionAssertUnchanged(t, store, first)
	revisionAssertUnchanged(t, store, before)
	// 无变化写入不会递增，最大整数版本仍可完成真正的 no-op。
	if err := store.CompareAndSwapCredential(context.Background(), before.Baseline, before.Record); err != nil {
		t.Fatal(err)
	}
	revisionAssertUnchanged(t, store, before)
	revisionExec(t, store, `INSERT INTO ncp_database_credential_revisions (source_id, revision) VALUES ('tombstone', ?)`, int64(math.MaxInt64))
	tombstone := revisionSnapshot(t, store, "tombstone")
	revisionAssertStoreFailure(t, store.CompareAndSwapCredential(context.Background(), tombstone.Baseline, revisionFixture("tombstone")))
	revisionAssertUnchanged(t, store, tombstone)
}

func TestCredentialRevisionRejectsInvalidAndUnknownBaselines(t *testing.T) {
	store := revisionTestStore(t)
	before := revisionInsert(t, store, "source")
	for _, expected := range []CredentialBaseline{
		{}, {SourceID: "other", Revision: 1, Exists: true}, {SourceID: "source", Revision: -1}, {SourceID: "source", Exists: true},
	} {
		revisionAssertStoreFailure(t, store.CompareAndSwapCredential(context.Background(), expected, before.Record))
	}
	for _, expected := range []CredentialBaseline{
		{SourceID: "source", Revision: 100, Exists: true}, {SourceID: "source", Revision: 1}, {SourceID: "source"},
	} {
		revisionAssertConflict(t, store.CompareAndSwapCredential(context.Background(), expected, before.Record))
	}
	missing := CredentialBaseline{SourceID: "missing", Revision: 1, Exists: true}
	revisionAssertConflict(t, store.CompareAndSwapCredential(context.Background(), missing, revisionFixture("missing")))
	revisionAssertConflict(t, store.DisableAutomaticIfUnchanged(context.Background(), missing, CodeCredentialCorrupt))
	update := revisionRotation(before)
	update.Expected.SourceID = "other"
	revisionAssertStoreFailure(t, store.ApplyRotation(context.Background(), []CredentialRotationUpdate{update}, nil))
	update = revisionRotation(before)
	revisionAssertStoreFailure(t, store.ApplyRotation(context.Background(), []CredentialRotationUpdate{update, update}, nil))
	update.KeyVersion = 0
	revisionAssertStoreFailure(t, store.ApplyRotation(context.Background(), []CredentialRotationUpdate{update}, nil))
	update = revisionRotation(before)
	update.Expected = missing
	update.SourceID = missing.SourceID
	revisionAssertConflict(t, store.ApplyRotation(context.Background(), []CredentialRotationUpdate{revisionRotation(before), update}, nil))
	revisionAssertUnchanged(t, store, before)
}

func TestCredentialRevisionReadAndWriteErrorsAreNotConflicts(t *testing.T) {
	for _, failure := range []string{"nil", "closed", "cancelled", "missing-table"} {
		t.Run(failure, func(t *testing.T) {
			store := revisionTestStore(t)
			before := revisionInsert(t, store, "source")
			ctx := context.Background()
			switch failure {
			case "nil":
				store = nil
			case "closed":
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "missing-table":
				revisionExec(t, store, `DROP TABLE ncp_database_credential_revisions`)
			}
			_, err := store.ReadCredentialSnapshot(ctx, "source")
			revisionAssertStoreFailure(t, err)
			revisionAssertStoreFailure(t, store.CompareAndSwapCredential(ctx, before.Baseline, before.Record))
			revisionAssertStoreFailure(t, store.DisableAutomaticIfUnchanged(ctx, before.Baseline, CodeCredentialCorrupt))
			revisionAssertStoreFailure(t, store.ApplyRotation(ctx, []CredentialRotationUpdate{revisionRotation(before)}, nil))
			if failure == "cancelled" {
				revisionAssertUnchanged(t, store, before)
			}
		})
	}
}

func TestCredentialRevisionMissingMetadataFailsClosed(t *testing.T) {
	store := revisionTestStore(t)
	revisionInsert(t, store, "source")
	revisionExec(t, store, `DELETE FROM ncp_database_credential_revisions WHERE source_id = 'source'`)
	_, err := store.ReadCredentialSnapshot(context.Background(), "source")
	revisionAssertStoreFailure(t, err)
	_, err = store.GetCredential(context.Background(), "source")
	revisionAssertStoreFailure(t, err)
	_, err = store.ListCredentials(context.Background())
	revisionAssertStoreFailure(t, err)
}

// SQLite 的 RowsAffected 正常可用；用最小 Result 验证异常驱动结果不会被吞掉。
type revisionResult struct {
	affected int64
	err      error
}

func (r revisionResult) LastInsertId() (int64, error) { return 0, nil }
func (r revisionResult) RowsAffected() (int64, error) { return r.affected, r.err }

func TestCredentialRevisionCASResultChecksExactRowsAffected(t *testing.T) {
	if err := credentialCASResult(revisionResult{affected: 1}, nil, "fixture"); err != nil {
		t.Fatal(err)
	}
	revisionAssertConflict(t, credentialCASResult(revisionResult{}, nil, "fixture"))
	for _, result := range []sql.Result{nil, revisionResult{affected: 2}, revisionResult{affected: -1}, revisionResult{affected: 1, err: errors.New("synthetic rows failure")}} {
		revisionAssertStoreFailure(t, credentialCASResult(result, nil, "fixture"))
	}
	revisionAssertStoreFailure(t, credentialCASResult(revisionResult{affected: 1}, errors.New("synthetic write failure"), "fixture"))
}

func TestCredentialRevisionLegacySQLWritesStillInvalidateBaseline(t *testing.T) {
	store := revisionTestStore(t)
	before := revisionInsert(t, store, "source")
	// 明确模拟旧二进制的无条件 UPSERT，不能恢复为生产方法。
	revisionExec(t, store, `INSERT INTO ncp_database_credentials (
		source_id, driver, endpoint, username, database_name, ciphertext, key_version,
		automatic_enabled, last_error_code, created_at_unix, updated_at_unix
	) VALUES ('source', 'postgresql', 'synthetic.invalid:5432/fixture', 'legacy-user', 'fixture', X'1234', 1, 1, '', 1730000000, 1730000000)
	ON CONFLICT(source_id) DO UPDATE SET username = excluded.username, ciphertext = excluded.ciphertext`)
	after := revisionSnapshot(t, store, "source")
	if after.Baseline.Revision != before.Baseline.Revision+1 {
		t.Fatal("legacy UPSERT did not advance revision")
	}
	revisionAssertConflict(t, store.CompareAndSwapCredential(context.Background(), before.Baseline, before.Record))
	revisionAssertUnchanged(t, store, after)
}
