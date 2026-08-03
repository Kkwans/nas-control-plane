package database

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestErrorMappingKeepsSafeConnectionContext(t *testing.T) {
	source := Source{
		ID: "source-1", Driver: DriverPostgreSQL, Host: "db.internal", Port: 5432,
		DefaultDatabase: "control",
	}
	cases := []struct {
		name string
		text string
		want ErrorCode
	}{
		{name: "credentials", text: "postgresql username is required", want: CodeCredentialsRequired},
		{name: "auth", text: "password authentication failed for user admin", want: CodeAuthFailed},
		{name: "unreachable", text: "dial tcp 10.0.0.3:5432: connection refused", want: CodeUnreachable},
		{name: "not found", text: "database \"missing\" does not exist", want: CodeDatabaseNotFound},
		{name: "permission", text: "permission denied for relation users", want: CodePermissionDenied},
		{name: "sql", text: "syntax error at or near SELECT", want: CodeSQLInvalid},
		{name: "constraint", text: "duplicate key value violates unique constraint", want: CodeConstraintFailed},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := MapError(context.Background(), source, "query", errors.New(testCase.text))
			if got := ErrorCodeOf(err); got != testCase.want {
				t.Fatalf("code = %q, want %q", got, testCase.want)
			}
			if err.Error() == "" || strings.Contains(err.Error(), "admin") || strings.Contains(err.Error(), "10.0.0.3") {
				t.Fatalf("error leaked driver context: %q", err.Error())
			}
			var databaseErr *DatabaseError
			if !errors.As(err, &databaseErr) {
				t.Fatal("error is not a DatabaseError")
			}
			if databaseErr.Driver != source.Driver || databaseErr.Endpoint != "db.internal:5432/control" || databaseErr.Operation != "query" {
				t.Fatalf("safe context = %#v", databaseErr)
			}
		})
	}

	deadline, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-deadline.Done()
	if got := ErrorCodeOf(MapError(deadline, source, "connect", errors.New("driver timeout"))); got != CodeTimeout {
		t.Fatalf("timeout code = %q", got)
	}
}

func TestCredentialEncryptionUsesVersionedCiphertextAndIndependentKeyFile(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "deployment", "database-key.json")
	provider, err := CreateFileKeyProvider(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %o, want 600", info.Mode().Perm())
	}
	credentials := Credentials{Username: "admin", Password: "super-secret", Token: "token-secret", Database: "control"}
	ciphertext, version, err := EncryptCredentials(context.Background(), provider, "source-aad", credentials)
	if err != nil {
		t.Fatal(err)
	}
	if version != 1 || bytes.Contains(ciphertext, []byte(credentials.Password)) || bytes.Contains(ciphertext, []byte(credentials.Token)) {
		t.Fatalf("ciphertext contains plaintext secret: %q", ciphertext)
	}
	var envelope encryptedCredentialEnvelope
	if err := json.Unmarshal(ciphertext, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Version != credentialCiphertextVersion || envelope.Algorithm != credentialCiphertextAlgorithm || envelope.KeyVersion != version {
		t.Fatalf("envelope = %#v", envelope)
	}
	reloaded, err := NewFileKeyProvider(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecryptCredentials(context.Background(), reloaded, "source-aad", ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != credentials {
		t.Fatalf("decoded credentials = %#v", decoded)
	}
	wrongKey, err := NewMemoryKeyProvider(1, bytes.Repeat([]byte{0x7f}, deploymentKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecryptCredentials(context.Background(), wrongKey, "source-aad", ciphertext); ErrorCodeOf(err) != CodeCredentialCorrupt {
		t.Fatalf("wrong key error code = %q", ErrorCodeOf(err))
	}
}

func TestManualConnectionPersistsOnlyAfterSuccessAndAutoUsesSavedCredentials(t *testing.T) {
	store, err := OpenSQLiteCredentialStore(filepath.Join(t.TempDir(), "credentials.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	provider, err := NewMemoryKeyProvider(1, bytes.Repeat([]byte{0x11}, deploymentKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	vault, err := NewCredentialVault(store, provider)
	if err != nil {
		t.Fatal(err)
	}
	tester := &capturingTester{diagnostic: ConnectionDiagnostic{Connected: true, Code: "connected"}}
	coordinator, err := NewConnectionCoordinator(vault, tester)
	if err != nil {
		t.Fatal(err)
	}
	source := Source{ID: "mysql-1", Driver: DriverMySQL, Host: "127.0.0.1", Port: 3306, DefaultDatabase: "control"}
	manual := Credentials{Username: "root", Password: "secret", Token: "token", Database: "control"}
	if _, err := coordinator.ManualConnect(context.Background(), source, manual); err != nil {
		t.Fatal(err)
	}
	if tester.last.Credentials != manual {
		t.Fatalf("manual credentials sent to tester = %#v", tester.last.Credentials)
	}
	record, err := store.GetCredential(context.Background(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(record.Ciphertext, []byte(manual.Password)) || bytes.Contains(record.Ciphertext, []byte(manual.Token)) {
		t.Fatalf("stored ciphertext contains plaintext secret")
	}
	saved, err := vault.Saved(context.Background(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(saved)
	if bytes.Contains(encoded, []byte(manual.Password)) || bytes.Contains(encoded, []byte(manual.Token)) {
		t.Fatalf("saved metadata contains secret: %s", encoded)
	}
	if _, err := coordinator.AutoConnect(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	if tester.last.Credentials != manual {
		t.Fatalf("auto credentials = %#v, want saved credentials", tester.last.Credentials)
	}

	tester.err = newDatabaseError(CodeAuthFailed, source, "test_connection", errors.New("password=not-to-be-returned"))
	failedSource := source
	failedSource.ID = "mysql-failed"
	if _, err := coordinator.ManualConnect(context.Background(), failedSource, manual); ErrorCodeOf(err) != CodeAuthFailed {
		t.Fatalf("manual failure code = %q", ErrorCodeOf(err))
	}
	if _, err := store.GetCredential(context.Background(), failedSource.ID); !errors.Is(err, errCredentialNotFound) {
		t.Fatalf("failed manual attempt persisted credentials: %v", err)
	}
}

func TestRotationFailureRetainsCiphertextAndDisablesAutomaticConnection(t *testing.T) {
	store, err := OpenSQLiteCredentialStore(filepath.Join(t.TempDir(), "rotation.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	provider := &failingRotationProvider{keys: map[int][]byte{1: bytes.Repeat([]byte{0x22}, deploymentKeyBytes)}, current: 1}
	vault, err := NewCredentialVault(store, provider)
	if err != nil {
		t.Fatal(err)
	}
	source := Source{ID: "postgres-1", Driver: DriverPostgreSQL, Host: "db.internal", Port: 5432, DefaultDatabase: "control"}
	if _, err := vault.Save(context.Background(), source, Credentials{Username: "admin", Password: "secret", Database: "control"}); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetCredential(context.Background(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	rotationErr := vault.Rotate(context.Background())
	if ErrorCodeOf(rotationErr) != CodeKeyRotationFailed {
		t.Fatalf("rotation error code = %q", ErrorCodeOf(rotationErr))
	}
	after, err := store.GetCredential(context.Background(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.Ciphertext, after.Ciphertext) || after.AutomaticEnabled || after.LastErrorCode != CodeKeyRotationFailed {
		t.Fatalf("rotation failure state = %#v", after)
	}
}

func TestCredentialMigrationFailureDoesNotRecordFailedVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migration.sqlite")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	_, err = database.Exec(`CREATE TABLE ncp_database_credentials (
		source_id TEXT PRIMARY KEY, driver TEXT NOT NULL, endpoint TEXT NOT NULL,
		username TEXT NOT NULL DEFAULT '', database_name TEXT NOT NULL DEFAULT '',
		ciphertext BLOB NOT NULL, key_version INTEGER NOT NULL,
		automatic_enabled INTEGER NOT NULL DEFAULT 1, created_at_unix INTEGER NOT NULL,
		updated_at_unix INTEGER NOT NULL, last_error_code TEXT NOT NULL DEFAULT ''
	)`)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewSQLiteCredentialStore(database)
	if err != nil {
		t.Fatal(err)
	}
	migrationErr := store.Migrate(context.Background())
	if ErrorCodeOf(migrationErr) != CodeMigrationFailed {
		t.Fatalf("migration error code = %q", ErrorCodeOf(migrationErr))
	}
	var applied int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ncp_database_schema_migrations WHERE version = 2`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 0 {
		t.Fatal("failed migration version was recorded")
	}
}

type capturingTester struct {
	diagnostic ConnectionDiagnostic
	err        error
	last       Connection
}

func (t *capturingTester) TestConnection(_ context.Context, request Connection) (ConnectionDiagnostic, error) {
	t.last = request
	return t.diagnostic, t.err
}

type failingRotationProvider struct {
	keys    map[int][]byte
	current int
}

func (p *failingRotationProvider) Current(context.Context) (KeyMaterial, error) {
	return KeyMaterial{Version: p.current, Key: append([]byte(nil), p.keys[p.current]...)}, nil
}

func (p *failingRotationProvider) ForVersion(_ context.Context, version int) (KeyMaterial, error) {
	key, ok := p.keys[version]
	if !ok || version != p.current {
		return KeyMaterial{}, &DatabaseError{Code: CodeKeyUnavailable, Operation: "key_lookup"}
	}
	return KeyMaterial{Version: version, Key: append([]byte(nil), key...)}, nil
}

func (p *failingRotationProvider) Rotate(context.Context) (KeyMaterial, error) {
	p.current = 2
	p.keys[p.current] = bytes.Repeat([]byte{0x33}, deploymentKeyBytes)
	return KeyMaterial{Version: p.current, Key: append([]byte(nil), p.keys[p.current]...)}, nil
}
