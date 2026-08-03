package database

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ConnectionTester is implemented by the Root Agent boundary. The Server
// side coordinator supplies credentials, while the Agent remains the only
// component that opens a database connection.
type ConnectionTester interface {
	TestConnection(context.Context, Connection) (ConnectionDiagnostic, error)
}

type ConnectionCoordinator struct {
	vault  *CredentialVault
	tester ConnectionTester
	now    func() time.Time
}

func NewConnectionCoordinator(vault *CredentialVault, tester ConnectionTester) (*ConnectionCoordinator, error) {
	if vault == nil {
		return nil, &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "connection_coordinator_init"}
	}
	if tester == nil {
		return nil, &DatabaseError{Code: CodeAgentUnavailable, Operation: "connection_coordinator_init"}
	}
	return &ConnectionCoordinator{vault: vault, tester: tester, now: time.Now}, nil
}

// Connect uses saved credentials when manual is nil. A non-nil manual value
// is tested first and persisted only after the Root Agent reports success.
func (c *ConnectionCoordinator) Connect(ctx context.Context, source Source, manual *Credentials) (ConnectionDiagnostic, error) {
	if manual == nil {
		return c.AutoConnect(ctx, source)
	}
	return c.ManualConnect(ctx, source, *manual)
}

// ResolveConnection returns the credentials that a subsequent catalog or
// query operation should use. It is intentionally separate from Connect:
// callers must run Connect first for manual credentials so persistence only
// happens after a successful diagnostic. A nil manual value resolves the
// encrypted saved credential and never falls back to plaintext storage.
func (c *ConnectionCoordinator) ResolveConnection(ctx context.Context, source Source, manual *Credentials) (Connection, error) {
	if c == nil || c.vault == nil {
		return Connection{}, &DatabaseError{Code: CodeCredentialStoreUnavailable, Operation: "connection_resolve"}
	}
	if source.Driver == DriverSQLite {
		// SQLite is opened by path and has no username/password credential
		// exchange. Keep the resolver compatible with an enabled credential
		// vault without forcing local databases through the credential flow.
		return Connection{SourceID: source.ID}, nil
	}
	if manual != nil {
		if err := validateCredentialTarget(source, *manual); err != nil {
			return Connection{}, err
		}
		return Connection{SourceID: source.ID, Credentials: *manual}, nil
	}
	credentials, found, err := c.vault.Resolve(ctx, source)
	if err != nil {
		return Connection{}, err
	}
	if !found {
		return Connection{}, newDatabaseError(CodeCredentialsRequired, source, "connection_resolve", errors.New("saved credentials are unavailable"))
	}
	return Connection{SourceID: source.ID, Credentials: credentials}, nil
}

func (c *ConnectionCoordinator) AutoConnect(ctx context.Context, source Source) (ConnectionDiagnostic, error) {
	started := c.now()
	if source.Driver == DriverSQLite {
		return c.test(ctx, source, Credentials{}, "auto_connect", started)
	}
	credentials, found, err := c.vault.Resolve(ctx, source)
	if err != nil {
		return diagnosticForError(source, "auto_connect", started, err), err
	}
	if !found {
		err := newDatabaseError(CodeCredentialsRequired, source, "auto_connect", errors.New("saved credentials are unavailable"))
		return diagnosticForError(source, "auto_connect", started, err), err
	}
	return c.test(ctx, source, credentials, "auto_connect", started)
}

func (c *ConnectionCoordinator) ManualConnect(ctx context.Context, source Source, credentials Credentials) (ConnectionDiagnostic, error) {
	started := c.now()
	if err := validateCredentialTarget(source, credentials); err != nil {
		return diagnosticForError(source, "manual_connect", started, err), err
	}
	diagnostic, err := c.test(ctx, source, credentials, "manual_connect", started)
	if err != nil {
		// A failed manual attempt is intentionally not written to the vault.
		return diagnostic, err
	}
	if _, err := c.vault.Save(ctx, source, credentials); err != nil {
		return diagnosticForError(source, "manual_persist", started, err), err
	}
	return diagnostic, nil
}

func (c *ConnectionCoordinator) test(ctx context.Context, source Source, credentials Credentials, operation string, started time.Time) (ConnectionDiagnostic, error) {
	if err := contextError(ctx); err != nil {
		return diagnosticForError(source, operation, started, err), err
	}
	diagnostic, err := c.tester.TestConnection(ctx, Connection{SourceID: source.ID, Credentials: credentials})
	if err != nil {
		err = MapError(ctx, source, operation, err)
		return mergeDiagnostic(diagnostic, diagnosticForError(source, operation, started, err)), err
	}
	if diagnostic.Code != "" && diagnostic.Code != "connected" && diagnostic.Code != "ok" {
		code := CodeFromString(diagnostic.Code)
		if code == "" {
			code = CodeUnreachable
		}
		err = newDatabaseError(code, source, operation, errors.New("connection test did not succeed"))
		return mergeDiagnostic(diagnostic, diagnosticForError(source, operation, started, err)), err
	}
	if !diagnostic.Connected {
		diagnostic.Connected = true
	}
	if diagnostic.Code == "" {
		diagnostic.Code = "connected"
	}
	fillDiagnosticContext(&diagnostic, source, operation, started)
	return diagnostic, nil
}

func diagnosticForError(source Source, operation string, started time.Time, err error) ConnectionDiagnostic {
	code := ErrorCodeOf(err)
	if databaseErr, ok := err.(*DatabaseError); ok && databaseErr != nil {
		if source.ID == "" {
			source.ID = databaseErr.SourceID
		}
		if source.Driver == "" {
			source.Driver = databaseErr.Driver
		}
		if source.Host == "" && databaseErr.Endpoint != "" {
			source.Location = databaseErr.Endpoint
		}
	}
	if code == "" {
		err = MapError(context.Background(), source, operation, err)
		code = ErrorCodeOf(err)
	}
	diagnostic := ConnectionDiagnostic{Connected: false, Code: string(code)}
	fillDiagnosticContext(&diagnostic, source, operation, started)
	return diagnostic
}

func mergeDiagnostic(primary, fallback ConnectionDiagnostic) ConnectionDiagnostic {
	primary.Connected = false
	primary.Code = fallback.Code
	if primary.Driver == "" {
		primary.Driver = fallback.Driver
	}
	if primary.Endpoint == "" {
		primary.Endpoint = fallback.Endpoint
	}
	if primary.Database == "" {
		primary.Database = fallback.Database
	}
	if primary.Operation == "" {
		primary.Operation = fallback.Operation
	}
	if primary.DurationMs == 0 {
		primary.DurationMs = fallback.DurationMs
	}
	return primary
}

func fillDiagnosticContext(diagnostic *ConnectionDiagnostic, source Source, operation string, started time.Time) {
	if diagnostic.Driver == "" {
		diagnostic.Driver = source.Driver
	}
	if diagnostic.Endpoint == "" {
		diagnostic.Endpoint = sourceEndpoint(source, diagnostic.Database)
	}
	if diagnostic.Database == "" {
		diagnostic.Database = effectiveDatabase(source, diagnostic.Database)
	}
	if diagnostic.Operation == "" {
		diagnostic.Operation = operation
	}
	if diagnostic.DurationMs == 0 {
		diagnostic.DurationMs = time.Since(started).Milliseconds()
	}
}

func sourceEndpoint(source Source, databaseName string) string {
	if source.Driver == DriverSQLite {
		return strings.TrimSpace(source.Path)
	}
	databaseName = effectiveDatabase(source, databaseName)
	host := strings.TrimSpace(source.Host)
	port := source.Port
	if host == "" {
		host, port, databaseName = endpointParts(source.Location, port, databaseName)
	}
	if host == "" {
		return sanitizeEndpoint(source.Location)
	}
	address := host
	if port > 0 {
		address = net.JoinHostPort(host, strconv.Itoa(port))
	}
	databaseName = strings.Trim(strings.TrimSpace(databaseName), "/")
	if databaseName != "" {
		return address + "/" + databaseName
	}
	return address
}

func endpointParts(location string, fallbackPort int, fallbackDatabase string) (string, int, string) {
	location = sanitizeEndpoint(location)
	if location == "" {
		return "", fallbackPort, fallbackDatabase
	}
	parsed, err := url.Parse("tcp://" + strings.TrimPrefix(location, "//"))
	if err != nil {
		return "", fallbackPort, fallbackDatabase
	}
	host := parsed.Hostname()
	port := fallbackPort
	if parsed.Port() != "" {
		if parsedPort, parseErr := strconv.Atoi(parsed.Port()); parseErr == nil {
			port = parsedPort
		}
	}
	databaseName := strings.Trim(parsed.Path, "/")
	if databaseName == "" {
		databaseName = fallbackDatabase
	}
	return host, port, databaseName
}

func sanitizeEndpoint(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err == nil && parsed.Host != "" {
		address := parsed.Hostname()
		if parsed.Port() != "" {
			address = net.JoinHostPort(address, parsed.Port())
		}
		return address + strings.TrimSuffix(parsed.EscapedPath(), "/")
	}
	if at := strings.LastIndex(value, "@"); at >= 0 {
		value = value[at+1:]
	}
	if query := strings.IndexAny(value, "?#"); query >= 0 {
		value = value[:query]
	}
	return strings.TrimSuffix(value, "/")
}
