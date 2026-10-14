package database

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/sqliteconn"
)

var errCredentialNotFound = errors.New("credential record not found")

// errCredentialConflict 仅供内部 CAS 使用；A1 接入前不增加对外错误码。
var errCredentialConflict = errors.New("credential snapshot conflict")

// StoredCredential contains persistence metadata and authenticated ciphertext
// only. Passwords and tokens are never columns in this record.
type StoredCredential struct {
	SourceID         string
	Revision         int64
	Driver           Driver
	Endpoint         string
	Username         string
	Database         string
	Ciphertext       []byte
	KeyVersion       int
	AutomaticEnabled bool
	LastErrorCode    ErrorCode
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// CredentialBaseline 是同一次查询取得的版本和存在状态。删除后版本仍保留，
// 因而不存在的记录也有可用于 CAS 的基线；只有从未出现的 source 版本为零。
type CredentialBaseline struct {
	SourceID string
	Revision int64
	Exists   bool
}

type CredentialSnapshot struct {
	Baseline CredentialBaseline
	Record   StoredCredential
}

type CredentialRotationUpdate struct {
	SourceID string
	// Expected 仅由 ApplyRotationIfUnchanged 使用；旧 ApplyRotation 不校验基线。
	Expected         CredentialBaseline
	Ciphertext       []byte
	KeyVersion       int
	AutomaticEnabled bool
	LastErrorCode    ErrorCode
}

type CredentialRotationDisable struct {
	SourceID string
	// Expected 仅由 ApplyRotationIfUnchanged 使用；旧 ApplyRotation 不校验基线。
	Expected CredentialBaseline
	Code     ErrorCode
}

// CredentialStore is the narrow persistence boundary used by CredentialVault.
// The shared controlstore package is intentionally not part of this interface.
type CredentialStore interface {
	Migrate(context.Context) error
	GetCredential(context.Context, string) (StoredCredential, error)
	PutCredential(context.Context, StoredCredential) error
	ListCredentials(context.Context) ([]StoredCredential, error)
	DisableAutomatic(context.Context, string, ErrorCode) error
	ApplyRotation(context.Context, []CredentialRotationUpdate, []CredentialRotationDisable) error
	DeleteCredential(context.Context, string) error
}

type SQLiteCredentialStore struct {
	database *sql.DB
	now      func() time.Time
	owned    bool
}

func NewSQLiteCredentialStore(database *sql.DB) (*SQLiteCredentialStore, error) {
	if database == nil {
		return nil, &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_store_open"}
	}
	return &SQLiteCredentialStore{database: database, now: time.Now}, nil
}

// OpenSQLiteCredentialStore opens the database path without taking ownership
// of any other controlstore connection. The adapter may therefore be wired to
// the same SQLite file while remaining independently migratable and testable.
func OpenSQLiteCredentialStore(path string) (*SQLiteCredentialStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_store_open"}
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Clean(path)), 0o750); err != nil {
		return nil, &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_store_open", Cause: err}
	}
	database, err := sqliteconn.Open(path, "PRAGMA busy_timeout = 5000")
	if err != nil {
		return nil, &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_store_open", Cause: err}
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	store, err := NewSQLiteCredentialStore(database)
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	store.owned = true
	if err := store.Migrate(context.Background()); err != nil {
		_ = database.Close()
		return nil, err
	}
	return store, nil
}

// OpenCredentialStore is the database-specific adapter name used by Server
// wiring; the SQLite suffix remains available for callers that want to make
// the persistence engine explicit.
func OpenCredentialStore(path string) (*SQLiteCredentialStore, error) {
	return OpenSQLiteCredentialStore(path)
}

func (s *SQLiteCredentialStore) Close() error {
	if s == nil || !s.owned || s.database == nil {
		return nil
	}
	return s.database.Close()
}

type credentialMigration struct {
	Version    int
	Statements []string
}

var credentialMigrations = []credentialMigration{
	{
		Version: 1,
		Statements: []string{`
CREATE TABLE IF NOT EXISTS ncp_database_credentials (
		source_id TEXT PRIMARY KEY,
		driver TEXT NOT NULL,
		endpoint TEXT NOT NULL,
		username TEXT NOT NULL DEFAULT '',
		database_name TEXT NOT NULL DEFAULT '',
		ciphertext BLOB NOT NULL,
		key_version INTEGER NOT NULL,
		automatic_enabled INTEGER NOT NULL DEFAULT 1 CHECK (automatic_enabled IN (0, 1)),
		created_at_unix INTEGER NOT NULL,
		updated_at_unix INTEGER NOT NULL
)`,
		},
	},
	{
		Version: 2,
		Statements: []string{
			`ALTER TABLE ncp_database_credentials ADD COLUMN last_error_code TEXT NOT NULL DEFAULT ''`,
			`CREATE INDEX IF NOT EXISTS ncp_database_credentials_auto_idx
				ON ncp_database_credentials(automatic_enabled, updated_at_unix DESC)`,
		},
	},
	{
		Version: 3,
		Statements: []string{
			`CREATE TABLE ncp_database_credential_revisions (
				source_id TEXT PRIMARY KEY,
				revision INTEGER NOT NULL CHECK (typeof(revision) = 'integer' AND revision > 0)
			)`,
			`INSERT INTO ncp_database_credential_revisions (source_id, revision)
				SELECT source_id, 1 FROM ncp_database_credentials`,
			`CREATE TRIGGER ncp_database_credentials_revision_insert
				AFTER INSERT ON ncp_database_credentials BEGIN
				INSERT INTO ncp_database_credential_revisions (source_id, revision) VALUES (NEW.source_id, 1)
				ON CONFLICT(source_id) DO UPDATE SET revision = ncp_database_credential_revisions.revision + 1;
			END`,
			`CREATE TRIGGER ncp_database_credentials_revision_delete
				AFTER DELETE ON ncp_database_credentials BEGIN
				INSERT INTO ncp_database_credential_revisions (source_id, revision) VALUES (OLD.source_id, 1)
				ON CONFLICT(source_id) DO UPDATE SET revision = ncp_database_credential_revisions.revision + 1;
			END`,
			`CREATE TRIGGER ncp_database_credentials_revision_update
				AFTER UPDATE OF driver, endpoint, username, database_name, ciphertext, key_version,
					automatic_enabled, last_error_code ON ncp_database_credentials
				WHEN OLD.driver IS NOT NEW.driver OR OLD.endpoint IS NOT NEW.endpoint
					OR OLD.username IS NOT NEW.username OR OLD.database_name IS NOT NEW.database_name
					OR OLD.ciphertext IS NOT NEW.ciphertext OR OLD.key_version IS NOT NEW.key_version
					OR OLD.automatic_enabled IS NOT NEW.automatic_enabled OR OLD.last_error_code IS NOT NEW.last_error_code
				BEGIN
				INSERT INTO ncp_database_credential_revisions (source_id, revision) VALUES (NEW.source_id, 1)
				ON CONFLICT(source_id) DO UPDATE SET revision = ncp_database_credential_revisions.revision + 1;
			END`,
			`CREATE TRIGGER ncp_database_credentials_source_immutable
				BEFORE UPDATE OF source_id ON ncp_database_credentials WHEN OLD.source_id IS NOT NEW.source_id
				BEGIN SELECT RAISE(ABORT, 'credential source cannot change'); END`,
		},
	},
}

func (s *SQLiteCredentialStore) Migrate(ctx context.Context) error {
	if s == nil || s.database == nil {
		return &DatabaseError{Code: CodeMigrationFailed, Operation: "migration"}
	}
	if err := contextError(ctx); err != nil {
		return &DatabaseError{Code: CodeMigrationFailed, Operation: "migration", Cause: err}
	}
	if _, err := s.database.ExecContext(ctx, `PRAGMA busy_timeout = 5000`); err != nil {
		return &DatabaseError{Code: CodeMigrationFailed, Operation: "migration", Cause: err}
	}
	if _, err := s.database.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS ncp_database_schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at_unix INTEGER NOT NULL
)`); err != nil {
		return &DatabaseError{Code: CodeMigrationFailed, Operation: "migration", Cause: err}
	}
	applied, err := s.appliedMigrations(ctx)
	if err != nil {
		return &DatabaseError{Code: CodeMigrationFailed, Operation: "migration", Cause: err}
	}
	for _, migration := range credentialMigrations {
		if applied[migration.Version] {
			continue
		}
		if err := s.applyMigration(ctx, migration); err != nil {
			return &DatabaseError{Code: CodeMigrationFailed, Operation: "migration", Cause: err}
		}
	}
	return nil
}

func (s *SQLiteCredentialStore) appliedMigrations(ctx context.Context) (map[int]bool, error) {
	rows, err := s.database.QueryContext(ctx, `SELECT version FROM ncp_database_schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[int]bool)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		result[version] = true
	}
	return result, rows.Err()
}

func (s *SQLiteCredentialStore) applyMigration(ctx context.Context, migration credentialMigration) error {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	rollback := func(cause error) error {
		_ = transaction.Rollback()
		return cause
	}
	for _, statement := range migration.Statements {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return rollback(err)
		}
	}
	if _, err := transaction.ExecContext(ctx,
		`INSERT INTO ncp_database_schema_migrations (version, applied_at_unix) VALUES (?, ?)`,
		migration.Version, s.now().UTC().Unix()); err != nil {
		return rollback(err)
	}
	if err := transaction.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteCredentialStore) GetCredential(ctx context.Context, sourceID string) (StoredCredential, error) {
	snapshot, err := s.readCredentialSnapshot(ctx, sourceID, "credential_read")
	if err != nil {
		return StoredCredential{}, err
	}
	if !snapshot.Baseline.Exists {
		return StoredCredential{}, errCredentialNotFound
	}
	return snapshot.Record, nil
}

// ReadCredentialSnapshot 只用一条 SELECT 同时读取记录与保留版本，包括不存在的 source。
// 这些条件写入基础接口尚未接入 CredentialStore / vault；A1 必须切换全部生产写入。
func (s *SQLiteCredentialStore) ReadCredentialSnapshot(ctx context.Context, sourceID string) (CredentialSnapshot, error) {
	if strings.TrimSpace(sourceID) == "" {
		return CredentialSnapshot{}, credentialPersistenceError("credential_snapshot", errors.New("credential source is empty"))
	}
	return s.readCredentialSnapshot(ctx, sourceID, "credential_snapshot")
}

func (s *SQLiteCredentialStore) readCredentialSnapshot(ctx context.Context, sourceID, operation string) (CredentialSnapshot, error) {
	failure := func(cause error) (CredentialSnapshot, error) {
		return CredentialSnapshot{}, &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: operation, Cause: cause}
	}
	if s == nil || s.database == nil {
		return failure(nil)
	}
	sourceID = strings.TrimSpace(sourceID)
	var revision sql.NullInt64
	var id, driver, endpoint, username, databaseName, lastError sql.NullString
	var keyVersion, automatic sql.Null[int]
	var createdAt, updatedAt sql.NullInt64
	var ciphertext []byte
	err := s.database.QueryRowContext(ctx, `
		SELECT r.revision, c.source_id, c.driver, c.endpoint, c.username, c.database_name,
			c.ciphertext, c.key_version, c.automatic_enabled, c.last_error_code, c.created_at_unix, c.updated_at_unix
		FROM (SELECT ? AS source_id) AS requested
		LEFT JOIN ncp_database_credential_revisions AS r ON r.source_id = requested.source_id
		LEFT JOIN ncp_database_credentials AS c ON c.source_id = requested.source_id`, sourceID).Scan(
		&revision, &id, &driver, &endpoint, &username, &databaseName, &ciphertext,
		&keyVersion, &automatic, &lastError, &createdAt, &updatedAt)
	if err != nil {
		return failure(err)
	}
	if (revision.Valid && revision.Int64 <= 0) || (id.Valid && !revision.Valid) {
		return failure(errors.New("credential revision is invalid"))
	}
	snapshot := CredentialSnapshot{Baseline: CredentialBaseline{SourceID: sourceID, Revision: revision.Int64, Exists: id.Valid}}
	if !id.Valid {
		return snapshot, nil
	}
	if !driver.Valid || !endpoint.Valid || !username.Valid || !databaseName.Valid || ciphertext == nil ||
		!keyVersion.Valid || !automatic.Valid || !lastError.Valid || !createdAt.Valid || !updatedAt.Valid {
		return failure(errors.New("credential snapshot is incomplete"))
	}
	snapshot.Record = StoredCredential{
		SourceID: id.String, Revision: revision.Int64, Driver: Driver(driver.String), Endpoint: endpoint.String,
		Username: username.String, Database: databaseName.String, Ciphertext: append([]byte(nil), ciphertext...),
		KeyVersion: keyVersion.V, AutomaticEnabled: automatic.V != 0, LastErrorCode: ErrorCode(lastError.String),
		CreatedAt: time.Unix(createdAt.Int64, 0).UTC(), UpdatedAt: time.Unix(updatedAt.Int64, 0).UTC(),
	}
	return snapshot, nil
}

func (s *SQLiteCredentialStore) PutCredential(ctx context.Context, record StoredCredential) error {
	if s == nil || s.database == nil {
		return &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_write"}
	}
	if err := validateStoredCredential(record); err != nil {
		return err
	}
	now := s.now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = now
	}
	_, err := s.database.ExecContext(ctx, `
		INSERT INTO ncp_database_credentials (
			source_id, driver, endpoint, username, database_name, ciphertext, key_version,
			automatic_enabled, last_error_code, created_at_unix, updated_at_unix
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_id) DO UPDATE SET
			driver = excluded.driver,
			endpoint = excluded.endpoint,
			username = excluded.username,
			database_name = excluded.database_name,
			ciphertext = excluded.ciphertext,
			key_version = excluded.key_version,
			automatic_enabled = excluded.automatic_enabled,
			last_error_code = excluded.last_error_code,
			updated_at_unix = excluded.updated_at_unix
	`, record.SourceID, string(record.Driver), record.Endpoint, record.Username, record.Database,
		record.Ciphertext, record.KeyVersion, boolInt(record.AutomaticEnabled), string(record.LastErrorCode),
		record.CreatedAt.Unix(), record.UpdatedAt.Unix())
	if err != nil {
		return &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_write", Cause: err}
	}
	return nil
}

// CompareAndSwapCredential 是尚未激活的条件写入基础。A1 接入前仍保留旧 PutCredential，
// 所以本阶段不能宣称 vault 已具有并发替换保护；旧二进制的无条件写入也不受 CAS 约束。
func (s *SQLiteCredentialStore) CompareAndSwapCredential(ctx context.Context, expected CredentialBaseline, record StoredCredential) error {
	const operation = "credential_compare_and_swap"
	if s == nil || s.database == nil {
		return credentialPersistenceError(operation, nil)
	}
	if err := validateCredentialBaseline(expected, record.SourceID); err != nil {
		return credentialPersistenceError(operation, err)
	}
	if err := validateStoredCredential(record); err != nil {
		return err
	}
	now := s.now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = now
	}
	var result sql.Result
	var err error
	if expected.Exists {
		result, err = s.database.ExecContext(ctx, `
			UPDATE ncp_database_credentials SET driver = ?, endpoint = ?, username = ?, database_name = ?,
				ciphertext = ?, key_version = ?, automatic_enabled = ?, last_error_code = ?, updated_at_unix = ?
			WHERE source_id = ? AND EXISTS (
				SELECT 1 FROM ncp_database_credential_revisions AS r
				WHERE r.source_id = ncp_database_credentials.source_id AND r.revision = ?)`,
			string(record.Driver), record.Endpoint, record.Username, record.Database, record.Ciphertext,
			record.KeyVersion, boolInt(record.AutomaticEnabled), string(record.LastErrorCode), record.UpdatedAt.Unix(),
			record.SourceID, expected.Revision)
	} else {
		result, err = s.database.ExecContext(ctx, `
			INSERT INTO ncp_database_credentials (
				source_id, driver, endpoint, username, database_name, ciphertext, key_version,
				automatic_enabled, last_error_code, created_at_unix, updated_at_unix
			) SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
			WHERE NOT EXISTS (SELECT 1 FROM ncp_database_credentials WHERE source_id = ?)
				AND COALESCE((SELECT revision FROM ncp_database_credential_revisions WHERE source_id = ?), 0) = ?
			ON CONFLICT(source_id) DO NOTHING`,
			record.SourceID, string(record.Driver), record.Endpoint, record.Username, record.Database, record.Ciphertext,
			record.KeyVersion, boolInt(record.AutomaticEnabled), string(record.LastErrorCode),
			record.CreatedAt.Unix(), record.UpdatedAt.Unix(), record.SourceID, record.SourceID, expected.Revision)
	}
	return credentialCASResult(result, err, operation)
}

// DisableAutomaticIfUnchanged 不追读或重试新基线，防止失败处理禁用刚修复的记录。
func (s *SQLiteCredentialStore) DisableAutomaticIfUnchanged(ctx context.Context, expected CredentialBaseline, code ErrorCode) error {
	const operation = "credential_disable_if_unchanged"
	if s == nil || s.database == nil {
		return credentialPersistenceError(operation, nil)
	}
	if err := validateCredentialBaseline(expected, expected.SourceID); err != nil {
		return credentialPersistenceError(operation, err)
	}
	if !expected.Exists {
		return errCredentialConflict
	}
	return disableCredentialIfUnchanged(ctx, s.database, expected, code, s.now().UTC().Unix(), operation)
}

type credentialExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func disableCredentialIfUnchanged(ctx context.Context, executor credentialExecer, expected CredentialBaseline, code ErrorCode, now int64, operation string) error {
	if CodeFromString(string(code)) == "" {
		code = CodeKeyRotationFailed
	}
	result, err := executor.ExecContext(ctx, `
		UPDATE ncp_database_credentials SET automatic_enabled = 0, last_error_code = ?, updated_at_unix = ?
		WHERE source_id = ? AND EXISTS (
			SELECT 1 FROM ncp_database_credential_revisions AS r
			WHERE r.source_id = ncp_database_credentials.source_id AND r.revision = ?)`,
		string(code), now, expected.SourceID, expected.Revision)
	return credentialCASResult(result, err, operation)
}

// ApplyRotationIfUnchanged 将全部更新和禁用放入同一个短事务；任何冲突或存储失败
// 均回滚整个批次。它不会调用旧 rotation 的失败处理，也不会禁用并发替换后的记录。
func (s *SQLiteCredentialStore) ApplyRotationIfUnchanged(ctx context.Context, updates []CredentialRotationUpdate, disables []CredentialRotationDisable) error {
	const operation = "credential_rotate_if_unchanged"
	if s == nil || s.database == nil {
		return credentialPersistenceError(operation, nil)
	}
	seen := make(map[string]bool, len(updates)+len(disables))
	validate := func(sourceID string, expected CredentialBaseline) error {
		if err := validateCredentialBaseline(expected, sourceID); err != nil {
			return credentialPersistenceError(operation, err)
		}
		if !expected.Exists {
			return errCredentialConflict
		}
		if seen[sourceID] {
			return credentialPersistenceError(operation, errors.New("duplicate credential rotation source"))
		}
		seen[sourceID] = true
		return nil
	}
	for _, update := range updates {
		if err := validate(update.SourceID, update.Expected); err != nil {
			return err
		}
		if update.KeyVersion <= 0 || len(update.Ciphertext) == 0 ||
			(update.LastErrorCode != "" && CodeFromString(string(update.LastErrorCode)) == "") {
			return credentialPersistenceError(operation, errors.New("invalid credential rotation update"))
		}
	}
	for _, disable := range disables {
		if err := validate(disable.SourceID, disable.Expected); err != nil {
			return err
		}
	}
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return credentialPersistenceError(operation, err)
	}
	defer transaction.Rollback()
	now := s.now().UTC().Unix()
	for _, update := range updates {
		result, err := transaction.ExecContext(ctx, `
			UPDATE ncp_database_credentials SET ciphertext = ?, key_version = ?, automatic_enabled = ?,
				last_error_code = ?, updated_at_unix = ?
			WHERE source_id = ? AND EXISTS (
				SELECT 1 FROM ncp_database_credential_revisions AS r
				WHERE r.source_id = ncp_database_credentials.source_id AND r.revision = ?)`,
			update.Ciphertext, update.KeyVersion, boolInt(update.AutomaticEnabled), string(update.LastErrorCode),
			now, update.SourceID, update.Expected.Revision)
		if err := credentialCASResult(result, err, operation); err != nil {
			return err
		}
	}
	for _, disable := range disables {
		if err := disableCredentialIfUnchanged(ctx, transaction, disable.Expected, disable.Code, now, operation); err != nil {
			return err
		}
	}
	if err := transaction.Commit(); err != nil {
		return credentialPersistenceError(operation, err)
	}
	return nil
}

func validateCredentialBaseline(expected CredentialBaseline, sourceID string) error {
	if strings.TrimSpace(expected.SourceID) == "" || expected.SourceID != sourceID ||
		expected.Revision < 0 || (expected.Exists && expected.Revision == 0) {
		return errors.New("credential baseline is invalid")
	}
	return nil
}

func credentialPersistenceError(operation string, cause error) error {
	return &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: operation, Cause: cause}
}

func credentialCASResult(result sql.Result, err error, operation string) error {
	if err != nil {
		return credentialPersistenceError(operation, err)
	}
	if result == nil {
		return credentialPersistenceError(operation, errors.New("credential write result is missing"))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return credentialPersistenceError(operation, err)
	}
	if affected == 0 {
		return errCredentialConflict
	}
	if affected != 1 {
		return credentialPersistenceError(operation, errors.New("credential write row count is invalid"))
	}
	return nil
}

func (s *SQLiteCredentialStore) ListCredentials(ctx context.Context) ([]StoredCredential, error) {
	if s == nil || s.database == nil {
		return nil, &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_list"}
	}
	rows, err := s.database.QueryContext(ctx, `
		SELECT c.source_id, c.driver, c.endpoint, c.username, c.database_name, c.ciphertext, c.key_version,
			c.automatic_enabled, c.last_error_code, c.created_at_unix, c.updated_at_unix, COALESCE(r.revision, 0)
		FROM ncp_database_credentials AS c
		LEFT JOIN ncp_database_credential_revisions AS r ON r.source_id = c.source_id
		ORDER BY c.updated_at_unix DESC, c.source_id`)
	if err != nil {
		return nil, &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_list", Cause: err}
	}
	defer rows.Close()
	result := make([]StoredCredential, 0)
	for rows.Next() {
		var record StoredCredential
		var driver, lastError string
		var automatic int
		var createdAt, updatedAt int64
		if err := rows.Scan(&record.SourceID, &driver, &record.Endpoint, &record.Username, &record.Database,
			&record.Ciphertext, &record.KeyVersion, &automatic, &lastError, &createdAt, &updatedAt, &record.Revision); err != nil {
			return nil, &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_list", Cause: err}
		}
		if record.Revision <= 0 {
			return nil, &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_list", Cause: errors.New("credential revision is invalid")}
		}
		record.Ciphertext = append([]byte(nil), record.Ciphertext...)
		record.Driver = Driver(driver)
		record.AutomaticEnabled = automatic != 0
		record.LastErrorCode = ErrorCode(lastError)
		record.CreatedAt = time.Unix(createdAt, 0).UTC()
		record.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_list", Cause: err}
	}
	return result, nil
}

func (s *SQLiteCredentialStore) DisableAutomatic(ctx context.Context, sourceID string, code ErrorCode) error {
	if s == nil || s.database == nil {
		return &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_disable"}
	}
	if CodeFromString(string(code)) == "" {
		code = CodeKeyRotationFailed
	}
	result, err := s.database.ExecContext(ctx, `
		UPDATE ncp_database_credentials SET automatic_enabled = 0, last_error_code = ?, updated_at_unix = ?
		WHERE source_id = ?`, string(code), s.now().UTC().Unix(), strings.TrimSpace(sourceID))
	if err != nil {
		return &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_disable", Cause: err}
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return errCredentialNotFound
	}
	return nil
}

func (s *SQLiteCredentialStore) ApplyRotation(ctx context.Context, updates []CredentialRotationUpdate, disables []CredentialRotationDisable) error {
	if s == nil || s.database == nil {
		return &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_rotate"}
	}
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_rotate", Cause: err}
	}
	rollback := func(cause error) error {
		_ = transaction.Rollback()
		return &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_rotate", Cause: cause}
	}
	now := s.now().UTC().Unix()
	for _, update := range updates {
		if update.SourceID == "" || update.KeyVersion <= 0 || len(update.Ciphertext) == 0 {
			return rollback(errors.New("invalid credential rotation update"))
		}
		result, err := transaction.ExecContext(ctx, `
			UPDATE ncp_database_credentials SET ciphertext = ?, key_version = ?, automatic_enabled = ?,
				last_error_code = ?, updated_at_unix = ? WHERE source_id = ?`,
			update.Ciphertext, update.KeyVersion, boolInt(update.AutomaticEnabled), string(update.LastErrorCode), now, update.SourceID)
		if err != nil {
			return rollback(err)
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return rollback(errCredentialNotFound)
		}
	}
	for _, disable := range disables {
		code := disable.Code
		if CodeFromString(string(code)) == "" {
			code = CodeKeyRotationFailed
		}
		if _, err := transaction.ExecContext(ctx, `
			UPDATE ncp_database_credentials SET automatic_enabled = 0, last_error_code = ?, updated_at_unix = ?
			WHERE source_id = ?`, string(code), now, disable.SourceID); err != nil {
			return rollback(err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_rotate", Cause: err}
	}
	return nil
}

func (s *SQLiteCredentialStore) DeleteCredential(ctx context.Context, sourceID string) error {
	if s == nil || s.database == nil {
		return &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_delete"}
	}
	_, err := s.database.ExecContext(ctx, `DELETE FROM ncp_database_credentials WHERE source_id = ?`, strings.TrimSpace(sourceID))
	if err != nil {
		return &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_delete", Cause: err}
	}
	return nil
}

func validateStoredCredential(record StoredCredential) error {
	if strings.TrimSpace(record.SourceID) == "" || record.Driver == "" || strings.TrimSpace(record.Endpoint) == "" ||
		record.KeyVersion <= 0 || len(record.Ciphertext) == 0 {
		return &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_validate", Cause: errors.New("credential record is incomplete")}
	}
	if record.LastErrorCode != "" && CodeFromString(string(record.LastErrorCode)) == "" {
		return &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_validate", Cause: errors.New("credential error code is invalid")}
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

type CredentialVault struct {
	store CredentialStore
	keys  KeyProvider
	now   func() time.Time
}

func NewCredentialVault(store CredentialStore, keys KeyProvider) (*CredentialVault, error) {
	if store == nil {
		return nil, &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "vault_init"}
	}
	if keys == nil {
		return nil, &DatabaseError{Code: CodeKeyUnavailable, Operation: "vault_init"}
	}
	return &CredentialVault{store: store, keys: keys, now: time.Now}, nil
}

func (v *CredentialVault) Migrate(ctx context.Context) error {
	if v == nil || v.store == nil {
		return &DatabaseError{Code: CodeMigrationFailed, Operation: "migration"}
	}
	return v.store.Migrate(ctx)
}

// Save is deliberately a low-level encrypted write. ConnectionCoordinator is
// the public workflow that calls it only after a successful manual test.
func (v *CredentialVault) Save(ctx context.Context, source Source, credentials Credentials) (SavedCredential, error) {
	if v == nil || v.store == nil || v.keys == nil {
		return SavedCredential{}, &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_save"}
	}
	if err := validateCredentialTarget(source, credentials); err != nil {
		return SavedCredential{}, err
	}
	databaseName := effectiveDatabase(source, credentials.Database)
	endpoint := sourceEndpoint(source, databaseName)
	ciphertext, keyVersion, err := EncryptCredentials(ctx, v.keys, credentialAAD(source.ID, source.Driver, endpoint), credentials)
	if err != nil {
		return SavedCredential{}, err
	}
	now := v.now().UTC()
	record := StoredCredential{
		SourceID: source.ID, Driver: source.Driver, Endpoint: endpoint,
		Username: credentials.Username, Database: databaseName, Ciphertext: ciphertext,
		KeyVersion: keyVersion, AutomaticEnabled: true, UpdatedAt: now, CreatedAt: now,
	}
	if err := v.store.PutCredential(ctx, record); err != nil {
		return SavedCredential{}, err
	}
	return toSavedCredential(record), nil
}

func (v *CredentialVault) Resolve(ctx context.Context, source Source) (Credentials, bool, error) {
	if v == nil || v.store == nil || v.keys == nil {
		return Credentials{}, false, &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "credential_resolve"}
	}
	if source.Driver == DriverSQLite {
		return Credentials{}, true, nil
	}
	record, err := v.store.GetCredential(ctx, source.ID)
	if errors.Is(err, errCredentialNotFound) {
		return Credentials{}, false, nil
	}
	if err != nil {
		return Credentials{}, false, err
	}
	if !record.AutomaticEnabled || record.Driver != source.Driver || record.Endpoint != sourceEndpoint(source, record.Database) {
		_ = v.store.DisableAutomatic(ctx, source.ID, CodeCredentialsRequired)
		return Credentials{}, false, newDatabaseError(CodeCredentialsRequired, source, "credential_resolve", errors.New("automatic credentials are disabled"))
	}
	credentials, err := DecryptCredentials(ctx, v.keys, credentialAAD(record.SourceID, record.Driver, record.Endpoint), record.Ciphertext)
	if err != nil {
		_ = v.store.DisableAutomatic(ctx, source.ID, ErrorCodeOf(err))
		return Credentials{}, false, err
	}
	return credentials, true, nil
}

func (v *CredentialVault) Saved(ctx context.Context, sourceID string) (SavedCredential, error) {
	record, err := v.store.GetCredential(ctx, sourceID)
	if errors.Is(err, errCredentialNotFound) {
		return SavedCredential{}, newDatabaseError(CodeDatabaseNotFound, Source{ID: sourceID}, "credential_read", err)
	}
	if err != nil {
		return SavedCredential{}, err
	}
	return toSavedCredential(record), nil
}

func (v *CredentialVault) List(ctx context.Context) ([]SavedCredential, error) {
	records, err := v.store.ListCredentials(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]SavedCredential, 0, len(records))
	for _, record := range records {
		result = append(result, toSavedCredential(record))
	}
	return result, nil
}

func (v *CredentialVault) DisableAutomatic(ctx context.Context, sourceID string, code ErrorCode) error {
	if code == "" {
		code = CodeKeyRotationFailed
	}
	return v.store.DisableAutomatic(ctx, sourceID, code)
}

func (v *CredentialVault) Delete(ctx context.Context, sourceID string) error {
	return v.store.DeleteCredential(ctx, sourceID)
}

// Rotate creates a new deployment key first. If key creation or any
// re-encryption fails, the old ciphertext is retained and all automatic
// connections remain disabled; there is no plaintext fallback.
func (v *CredentialVault) Rotate(ctx context.Context) error {
	rotator, ok := v.keys.(RotatingKeyProvider)
	if !ok {
		return &DatabaseError{Code: CodeKeyRotationFailed, Operation: "credential_rotate", Cause: errors.New("key provider cannot rotate")}
	}
	if _, err := rotator.Rotate(ctx); err != nil {
		return v.disableAllAfterRotationFailure(ctx, err)
	}
	return v.reencryptCurrent(ctx)
}

func (v *CredentialVault) reencryptCurrent(ctx context.Context) error {
	records, err := v.store.ListCredentials(ctx)
	if err != nil {
		return err
	}
	current, err := v.keys.Current(ctx)
	if err != nil {
		return v.disableAllAfterRotationFailure(ctx, err)
	}
	updates := make([]CredentialRotationUpdate, 0, len(records))
	for _, record := range records {
		credentials, decryptErr := DecryptCredentials(ctx, v.keys, credentialAAD(record.SourceID, record.Driver, record.Endpoint), record.Ciphertext)
		if decryptErr != nil {
			return v.disableAllAfterRotationFailure(ctx, decryptErr)
		}
		ciphertext, keyVersion, encryptErr := encryptWithKey(ctx, current, credentialAAD(record.SourceID, record.Driver, record.Endpoint), credentials)
		if encryptErr != nil {
			return v.disableAllAfterRotationFailure(ctx, encryptErr)
		}
		updates = append(updates, CredentialRotationUpdate{
			SourceID: record.SourceID, Ciphertext: ciphertext, KeyVersion: keyVersion,
			AutomaticEnabled: record.AutomaticEnabled, LastErrorCode: record.LastErrorCode,
		})
	}
	if err := v.store.ApplyRotation(ctx, updates, nil); err != nil {
		return v.disableAllAfterRotationFailure(ctx, err)
	}
	return nil
}

func (v *CredentialVault) disableAllAfterRotationFailure(ctx context.Context, cause error) error {
	records, listErr := v.store.ListCredentials(ctx)
	if listErr == nil {
		disables := make([]CredentialRotationDisable, 0, len(records))
		for _, record := range records {
			disables = append(disables, CredentialRotationDisable{SourceID: record.SourceID, Code: CodeKeyRotationFailed})
		}
		if len(disables) > 0 {
			_ = v.store.ApplyRotation(ctx, nil, disables)
		}
	}
	return &DatabaseError{Code: CodeKeyRotationFailed, Operation: "credential_rotate", Cause: cause}
}

func encryptWithKey(ctx context.Context, material KeyMaterial, aad string, credentials Credentials) ([]byte, int, error) {
	if err := contextError(ctx); err != nil {
		return nil, 0, err
	}
	if err := validateKeyMaterial(material); err != nil {
		return nil, 0, &DatabaseError{Code: CodeKeyRotationFailed, Operation: "credential_rotate", Cause: err}
	}
	ciphertext, nonce, err := sealCredential(material.Key, []byte(aad), credentials)
	if err != nil {
		return nil, 0, err
	}
	envelope := encryptedCredentialEnvelope{
		Version: credentialCiphertextVersion, Algorithm: credentialCiphertextAlgorithm,
		KeyVersion: material.Version,
		Nonce:      base64Raw(nonce), Ciphertext: base64Raw(ciphertext),
	}
	encoded, err := jsonMarshal(envelope)
	if err != nil {
		return nil, 0, &DatabaseError{Code: CodeKeyRotationFailed, Operation: "credential_rotate", Cause: err}
	}
	return encoded, material.Version, nil
}

func base64Raw(value []byte) string {
	return base64.RawStdEncoding.EncodeToString(value)
}

func jsonMarshal(value any) ([]byte, error) {
	return json.Marshal(value)
}

func validateCredentialTarget(source Source, credentials Credentials) error {
	if strings.TrimSpace(source.ID) == "" {
		return newDatabaseError(CodeDatabaseNotFound, source, "credential_save", errors.New("source is required"))
	}
	if source.Driver == DriverSQLite {
		if strings.TrimSpace(source.Path) == "" {
			return newDatabaseError(CodeDatabaseNotFound, source, "credential_save", errors.New("sqlite path is required"))
		}
		return nil
	}
	if strings.TrimSpace(credentials.Username) == "" {
		return newDatabaseError(CodeCredentialsRequired, source, "credential_save", errors.New("username is required"))
	}
	if (source.Driver == DriverMySQL || source.Driver == DriverPostgreSQL) && effectiveDatabase(source, credentials.Database) == "" {
		return newDatabaseError(CodeCredentialsRequired, source, "credential_save", errors.New("database is required"))
	}
	if source.Driver != DriverMySQL && source.Driver != DriverPostgreSQL {
		return newDatabaseError(CodeSQLInvalid, source, "credential_save", errors.New("unsupported driver"))
	}
	return nil
}

func effectiveDatabase(source Source, requested string) string {
	if strings.TrimSpace(requested) != "" {
		return strings.TrimSpace(requested)
	}
	return strings.TrimSpace(source.DefaultDatabase)
}

func toSavedCredential(record StoredCredential) SavedCredential {
	return SavedCredential{
		SourceID: record.SourceID, Driver: record.Driver, Endpoint: record.Endpoint,
		Username: record.Username, Database: record.Database, KeyVersion: record.KeyVersion,
		AutomaticEnabled: record.AutomaticEnabled, LastErrorCode: string(record.LastErrorCode),
		UpdatedAt: record.UpdatedAt,
	}
}
