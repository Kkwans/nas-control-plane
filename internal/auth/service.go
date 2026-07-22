package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

const (
	RootRole               = "root"
	defaultSessionLifetime = 24 * time.Hour
	sessionTokenBytes      = 32
)

type Options struct {
	SessionLifetime time.Duration
	Clock           func() time.Time
	Random          io.Reader
}

type Service struct {
	database        *sql.DB
	sessionLifetime time.Duration
	now             func() time.Time
	random          io.Reader
}

type Principal struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

type Session struct {
	Token     string    `json:"-"`
	ExpiresAt time.Time `json:"expiresAt"`
	Principal Principal `json:"principal"`
}

func Open(databasePath string, options Options) (*Service, error) {
	if strings.TrimSpace(databasePath) == "" {
		return nil, coded("AUTH_DATABASE_PATH_INVALID", errors.New("database path is required"))
	}
	if !isSQLiteMemoryPath(databasePath) {
		if err := os.MkdirAll(filepath.Dir(databasePath), 0o750); err != nil {
			return nil, coded("AUTH_DATABASE_DIRECTORY_FAILED", err)
		}
	}

	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, coded("AUTH_DATABASE_OPEN_FAILED", err)
	}
	database.SetMaxOpenConns(1)
	service := &Service{
		database:        database,
		sessionLifetime: normalizedSessionLifetime(options.SessionLifetime),
		now:             normalizedClock(options.Clock),
		random:          normalizedRandom(options.Random),
	}
	if err := service.migrate(context.Background()); err != nil {
		_ = database.Close()
		return nil, err
	}
	return service, nil
}

func (s *Service) Close() error {
	if s == nil || s.database == nil {
		return nil
	}
	return s.database.Close()
}

func (s *Service) Initialized(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	var count int
	if err := s.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return false, coded("AUTH_DATABASE_QUERY_FAILED", err)
	}
	return count > 0, nil
}

func (s *Service) Bootstrap(ctx context.Context, username, password string) (Principal, error) {
	if err := ctx.Err(); err != nil {
		return Principal{}, err
	}
	username, err := normalizeUsername(username)
	if err != nil {
		return Principal{}, err
	}
	if err := validatePassword(password); err != nil {
		return Principal{}, err
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return Principal{}, coded("AUTH_PASSWORD_HASH_FAILED", err)
	}

	createdAt := s.now().UTC().Unix()
	result, err := s.database.ExecContext(ctx, `
		INSERT INTO users (username, password_hash, role, created_at_unix)
		SELECT ?, ?, ?, ?
		WHERE NOT EXISTS (SELECT 1 FROM users)
	`, username, passwordHash, RootRole, createdAt)
	if err != nil {
		return Principal{}, coded("AUTH_DATABASE_WRITE_FAILED", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return Principal{}, coded("AUTH_DATABASE_WRITE_FAILED", err)
	}
	if rows != 1 {
		return Principal{}, coded("AUTH_ALREADY_INITIALIZED", errors.New("root account already exists"))
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Principal{}, coded("AUTH_DATABASE_WRITE_FAILED", err)
	}
	return Principal{ID: id, Username: username, Role: RootRole}, nil
}

func (s *Service) Login(ctx context.Context, username, password string) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	username, err := normalizeUsername(username)
	if err != nil {
		return Session{}, invalidCredentials()
	}
	var principal Principal
	var passwordHash []byte
	err = s.database.QueryRowContext(ctx, `
		SELECT id, username, role, password_hash
		FROM users
		WHERE username = ? COLLATE NOCASE
	`, username).Scan(&principal.ID, &principal.Username, &principal.Role, &passwordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, invalidCredentials()
	}
	if err != nil {
		return Session{}, coded("AUTH_DATABASE_QUERY_FAILED", err)
	}
	if bcrypt.CompareHashAndPassword(passwordHash, []byte(password)) != nil {
		return Session{}, invalidCredentials()
	}
	return s.createSession(ctx, principal)
}

func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	if err := ctx.Err(); err != nil {
		return Principal{}, err
	}
	if strings.TrimSpace(token) == "" {
		return Principal{}, unauthorized()
	}
	tokenHash := hashToken(token)
	var principal Principal
	err := s.database.QueryRowContext(ctx, `
		SELECT users.id, users.username, users.role
		FROM sessions
		INNER JOIN users ON users.id = sessions.user_id
		WHERE sessions.token_hash = ? AND sessions.expires_at_unix > ?
	`, tokenHash, s.now().UTC().Unix()).Scan(&principal.ID, &principal.Username, &principal.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, unauthorized()
	}
	if err != nil {
		return Principal{}, coded("AUTH_DATABASE_QUERY_FAILED", err)
	}
	return principal, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(token) == "" {
		return nil
	}
	if _, err := s.database.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hashToken(token)); err != nil {
		return coded("AUTH_DATABASE_WRITE_FAILED", err)
	}
	return nil
}

func (s *Service) createSession(ctx context.Context, principal Principal) (Session, error) {
	now := s.now().UTC()
	if _, err := s.database.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at_unix <= ?`, now.Unix()); err != nil {
		return Session{}, coded("AUTH_DATABASE_WRITE_FAILED", err)
	}
	token, err := newToken(s.random)
	if err != nil {
		return Session{}, coded("AUTH_SESSION_TOKEN_FAILED", err)
	}
	expiresAt := now.Add(s.sessionLifetime)
	if _, err := s.database.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at_unix, created_at_unix)
		VALUES (?, ?, ?, ?)
	`, hashToken(token), principal.ID, expiresAt.Unix(), now.Unix()); err != nil {
		return Session{}, coded("AUTH_DATABASE_WRITE_FAILED", err)
	}
	return Session{Token: token, ExpiresAt: expiresAt, Principal: principal}, nil
}

func (s *Service) migrate(ctx context.Context) error {
	statements := []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY,
			username TEXT NOT NULL UNIQUE COLLATE NOCASE,
			password_hash BLOB NOT NULL,
			role TEXT NOT NULL,
			created_at_unix INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token_hash BLOB PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			expires_at_unix INTEGER NOT NULL,
			created_at_unix INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS sessions_expires_at_idx ON sessions (expires_at_unix)`,
	}
	for _, statement := range statements {
		if _, err := s.database.ExecContext(ctx, statement); err != nil {
			return coded("AUTH_DATABASE_MIGRATION_FAILED", err)
		}
	}
	return nil
}

func normalizedSessionLifetime(value time.Duration) time.Duration {
	if value <= 0 {
		return defaultSessionLifetime
	}
	return value
}

func normalizedClock(clock func() time.Time) func() time.Time {
	if clock == nil {
		return time.Now
	}
	return clock
}

func normalizedRandom(reader io.Reader) io.Reader {
	if reader == nil {
		return rand.Reader
	}
	return reader
}

func isSQLiteMemoryPath(databasePath string) bool {
	return databasePath == ":memory:" || strings.HasPrefix(databasePath, "file::memory:")
}

func normalizeUsername(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > 64 {
		return "", coded("AUTH_INPUT_INVALID", errors.New("username is invalid"))
	}
	for _, character := range value {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return "", coded("AUTH_INPUT_INVALID", errors.New("username is invalid"))
		}
	}
	return value, nil
}

func validatePassword(value string) error {
	length := utf8.RuneCountInString(value)
	if length < 8 || length > 256 {
		return coded("AUTH_INPUT_INVALID", errors.New("password is invalid"))
	}
	return nil
}

func newToken(reader io.Reader) (string, error) {
	buffer := make([]byte, sessionTokenBytes)
	if _, err := io.ReadFull(reader, buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func invalidCredentials() error {
	return coded("AUTH_INVALID_CREDENTIALS", errors.New("credentials are invalid"))
}

func unauthorized() error {
	return coded("AUTH_UNAUTHORIZED", errors.New("session is not authorized"))
}

type codedError struct {
	code string
	err  error
}

func coded(code string, err error) error {
	return &codedError{code: code, err: err}
}

func (e *codedError) Error() string {
	return e.code
}

func (e *codedError) Unwrap() error {
	return e.err
}

func ErrorCode(err error) string {
	var coded *codedError
	if errors.As(err, &coded) {
		return coded.code
	}
	return ""
}
