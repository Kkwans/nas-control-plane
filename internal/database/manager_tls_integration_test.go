package database

import (
	"context"
	"crypto/x509"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestManagerTLSPolicySupportAndStrictDispatch(t *testing.T) {
	clearDatabaseTLSPostgresTestEnvironment(t)
	if !NewManager().SupportsDatabaseTLSPolicy() {
		t.Fatal("Manager must explicitly support strict TLS dispatch")
	}
	for _, driverName := range []Driver{DriverMySQL, DriverPostgreSQL} {
		t.Run(string(driverName), func(t *testing.T) {
			h := newManagerTLSHarness(t, driverName, &TLSPolicy{Mode: TLSVerifyFull})
			ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), managerTLSContextKey{}, "request"), time.Minute)
			defer cancel()
			source, db, err := h.manager.connect(ctx, h.connection)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if source.ID != h.source.ID || h.strictOpens != 1 || h.legacyOpens != 0 {
				t.Fatalf("unexpected dispatch: source=%q strict=%d legacy=%d", source.ID, h.strictOpens, h.legacyOpens)
			}
			h.fake.mu.Lock()
			if len(h.fake.connectContexts) != 1 || h.fake.connectContexts[0] != ctx ||
				len(h.fake.pingContexts) != 1 || h.fake.pingContexts[0] != ctx {
				t.Error("Connect and Ping must receive the original request context")
			}
			h.fake.mu.Unlock()
			if stats := db.Stats(); stats.MaxOpenConnections != 2 || stats.Idle != 1 {
				t.Fatalf("pool changed: %+v", stats)
			}
			// 同时借出两条假连接，再归还，证明最大空闲数仍为 1。
			first, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			second, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			if err := second.Close(); err != nil {
				t.Fatal(err)
			}
			if stats := db.Stats(); stats.Idle != 1 || stats.MaxIdleClosed != 1 {
				t.Fatalf("idle limit changed: %+v", stats)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if h.fake.closedCount() != 2 {
				t.Fatal("pool must close both fake connections")
			}
		})
	}
}

func TestManagerTLSNilKeepsLegacyConnectionString(t *testing.T) {
	for _, driverName := range []Driver{DriverSQLite, DriverMySQL, DriverPostgreSQL} {
		t.Run(string(driverName), func(t *testing.T) {
			h := newManagerTLSHarness(t, driverName, nil)
			wantDriver, wantDSN, err := connectionString(h.source, h.connection.Credentials)
			if err != nil {
				t.Fatal(err)
			}
			_, db, err := h.manager.connect(context.Background(), h.connection)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if h.legacyOpens != 1 || h.strictOpens != 0 || h.legacyDriver != wantDriver || h.legacyDSN != wantDSN {
				t.Fatal("nil TLS must use exactly the existing DSN path")
			}
		})
	}
}

func TestManagerTLSFreezesPolicyBeforeDiscovery(t *testing.T) {
	for _, initiallyValid := range []bool{true, false} {
		name := "valid remains valid"
		mode, replacement := TLSVerifyFull, TLSMode("prefer")
		if !initiallyValid {
			name, mode, replacement = "invalid remains invalid", replacement, mode
		}
		t.Run(name, func(t *testing.T) {
			policy := &TLSPolicy{Mode: mode}
			h := newManagerTLSHarness(t, DriverMySQL, policy)
			h.manager.discoverFn = func(context.Context) (Discovery, error) {
				policy.Mode = replacement
				policy.ServerName = "changed.example"
				return Discovery{CollectedAt: time.Now(), Sources: []Source{h.source}}, nil
			}
			_, db, err := h.manager.connect(context.Background(), h.connection)
			if initiallyValid {
				if err != nil || db == nil || h.strictOpens != 1 {
					t.Fatalf("discovery changed frozen valid policy: %v", err)
				}
				defer db.Close()
			} else if ErrorCodeOf(err) != CodeTLSConfigurationInvalid || db != nil || h.strictOpens != 0 {
				t.Fatalf("discovery repaired a frozen invalid policy: %v", err)
			}
			if h.legacyOpens != 0 {
				t.Fatal("policy mutation must never change dispatch to legacy")
			}
		})
	}
}

func TestManagerTLSInvalidConfigurationNeverOpens(t *testing.T) {
	clearDatabaseTLSPostgresTestEnvironment(t)
	tests := []struct {
		name   string
		change func(*managerTLSHarness)
	}{
		{name: "empty policy", change: func(h *managerTLSHarness) { h.connection.Credentials.TLS = &TLSPolicy{} }},
		{name: "unknown mode", change: func(h *managerTLSHarness) { h.connection.Credentials.TLS.Mode = "prefer" }},
		{name: "bad CA", change: func(h *managerTLSHarness) { h.connection.Credentials.TLS.CAPEM = "fake-ca-sentinel" }},
		{name: "bad identity", change: func(h *managerTLSHarness) { h.connection.Credentials.TLS.ServerName = "*.example" }},
		{name: "sqlite", change: func(h *managerTLSHarness) { h.source.Driver = DriverSQLite }},
		{name: "unknown driver", change: func(h *managerTLSHarness) { h.source.Driver = "unknown" }},
		{name: "unreachable", change: func(h *managerTLSHarness) { h.source.Reachability = "unreachable" }},
		{name: "internal", change: func(h *managerTLSHarness) { h.source.Reachability = "container-internal" }},
		{name: "no evidence", change: func(h *managerTLSHarness) { h.source.Evidence = "none" }},
		{name: "missing user", change: func(h *managerTLSHarness) { h.connection.Credentials.Username = "" }},
		{name: "missing database", change: func(h *managerTLSHarness) { h.connection.Credentials.Database, h.source.DefaultDatabase = "", "" }},
		{name: "bad port", change: func(h *managerTLSHarness) { h.source.Port = 65536 }},
		{name: "missing host", change: func(h *managerTLSHarness) { h.source.Host = "" }},
		{name: "socket host", change: func(h *managerTLSHarness) { h.source.Host = "/fake/socket" }},
	}
	for _, driverName := range []Driver{DriverMySQL, DriverPostgreSQL} {
		for _, tt := range tests {
			t.Run(string(driverName)+"/"+tt.name, func(t *testing.T) {
				h := newManagerTLSHarness(t, driverName, &TLSPolicy{Mode: TLSVerifyFull})
				tt.change(h)
				_, db, err := h.manager.connect(context.Background(), h.connection)
				if db != nil || ErrorCodeOf(err) != CodeTLSConfigurationInvalid || h.strictOpens != 0 || h.legacyOpens != 0 {
					t.Fatalf("invalid policy reached opener: err=%v strict=%d legacy=%d", err, h.strictOpens, h.legacyOpens)
				}
				if errors.Unwrap(err) != nil {
					t.Fatal("configuration error must not retain raw driver or credential data")
				}
			})
		}
	}
}

func TestManagerTLSFailureClosesWithoutFallback(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code ErrorCode
	}{
		{name: "verification", err: x509.UnknownAuthorityError{}, code: CodeTLSVerificationFailed},
		{name: "cancelled", err: context.Canceled, code: CodeTimeout},
		{name: "deadline", err: context.DeadlineExceeded, code: CodeTimeout},
		{name: "authentication", err: errors.New("password authentication failed"), code: CodeAuthFailed},
	}
	for _, strict := range []bool{false, true} {
		for _, tt := range tests {
			name := "legacy/" + tt.name
			var policy *TLSPolicy
			if strict {
				name, policy = "strict/"+tt.name, &TLSPolicy{Mode: TLSVerifyFull}
			}
			t.Run(name, func(t *testing.T) {
				h := newManagerTLSHarness(t, DriverMySQL, policy)
				h.fake.pingErr = tt.err
				_, db, err := h.manager.connect(context.Background(), h.connection)
				if db != nil || ErrorCodeOf(err) != tt.code {
					t.Fatalf("unexpected mapped failure: %v", err)
				}
				if h.fake.closedCount() != 1 || h.db.Stats().OpenConnections != 0 {
					t.Fatal("failed ping must close the database and its fake connection")
				}
				if strict && (h.strictOpens != 1 || h.legacyOpens != 0) || !strict && (h.legacyOpens != 1 || h.strictOpens != 0) {
					t.Fatal("failed ping must not retry through another opener")
				}
			})
		}
	}
}

func TestManagerTLSCancellationReachesPingAndCloses(t *testing.T) {
	h := newManagerTLSHarness(t, DriverMySQL, &TLSPolicy{Mode: TLSVerifyFull})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered := make(chan struct{})
	h.fake.pingFn = func(pingContext context.Context) error {
		close(entered)
		<-pingContext.Done()
		return pingContext.Err()
	}
	finished := make(chan error, 1)
	go func() {
		_, err := h.manager.TestConnection(ctx, h.connection)
		finished <- err
	}()
	select {
	case <-entered:
		cancel()
	case <-ctx.Done():
		t.Fatal("fake Ping did not start")
	}
	err := <-finished
	if ErrorCodeOf(err) != CodeTimeout || !errors.Is(err, context.Canceled) {
		t.Fatalf("request cancellation was not preserved: %v", err)
	}
	if h.fake.closedCount() != 1 || h.strictOpens != 1 || h.legacyOpens != 0 {
		t.Fatal("cancelled strict request must close without fallback")
	}
}

func TestManagerTLSAllPublicOperationsDispatchAndClose(t *testing.T) {
	operations := []struct {
		name string
		run  func(*Manager, Connection) error
	}{
		{name: "test", run: func(m *Manager, c Connection) error { _, err := m.TestConnection(context.Background(), c); return err }},
		{name: "catalog", run: func(m *Manager, c Connection) error {
			_, err := m.Catalog(context.Background(), CatalogRequest{Connection: c})
			return err
		}},
		{name: "query", run: func(m *Manager, c Connection) error {
			_, err := m.Query(context.Background(), QueryRequest{Connection: c, SQL: "SELECT 1"})
			return err
		}},
		{name: "query options", run: func(m *Manager, c Connection) error {
			_, err := m.QueryWithOptions(context.Background(), QueryRequest{Connection: c, SQL: "SELECT 1", ResultMode: QueryResultModeRows})
			return err
		}},
		{name: "rows", run: func(m *Manager, c Connection) error {
			_, err := m.Rows(context.Background(), RowsRequest{Connection: c, Table: "example"})
			return err
		}},
		{name: "insert", run: func(m *Manager, c Connection) error {
			_, err := m.Insert(context.Background(), InsertRequest{Connection: c, Table: "example"})
			return err
		}},
		{name: "update", run: func(m *Manager, c Connection) error {
			_, err := m.Update(context.Background(), UpdateRequest{InsertRequest: InsertRequest{Connection: c, Table: "example"}})
			return err
		}},
		{name: "delete", run: func(m *Manager, c Connection) error {
			_, err := m.Delete(context.Background(), DeleteRequest{Connection: c, Table: "example"})
			return err
		}},
	}
	for _, strict := range []bool{false, true} {
		for _, operation := range operations {
			name := "legacy/" + operation.name
			var policy *TLSPolicy
			if strict {
				name, policy = "strict/"+operation.name, &TLSPolicy{Mode: TLSVerifyFull}
			}
			t.Run(name, func(t *testing.T) {
				h := newManagerTLSHarness(t, DriverMySQL, policy)
				err := operation.run(h.manager, h.connection)
				if (operation.name == "test") != (err == nil) {
					t.Fatalf("unexpected operation outcome: %v", err)
				}
				if h.fake.closedCount() != 1 || h.db == nil || h.db.Stats().OpenConnections != 0 {
					t.Fatal("public operation did not close its fake database")
				}
				if strict && (h.strictOpens != 1 || h.legacyOpens != 0) || !strict && (h.legacyOpens != 1 || h.strictOpens != 0) {
					t.Fatal("public operation bypassed the centralized dispatch")
				}
			})
		}
	}
}

func TestManagerTLSLegacyOpenFailureDoesNotTryStrict(t *testing.T) {
	h := newManagerTLSHarness(t, DriverMySQL, nil)
	h.manager.sqlOpen = func(string, string) (*sql.DB, error) {
		h.legacyOpens++
		return nil, errors.New("fake connection refused")
	}
	_, db, err := h.manager.connect(context.Background(), h.connection)
	if db != nil || err == nil || h.legacyOpens != 1 || h.strictOpens != 0 {
		t.Fatal("legacy opener failure must propagate without another path")
	}
}

type managerTLSContextKey struct{}

type managerTLSHarness struct {
	manager      *Manager
	source       Source
	connection   Connection
	fake         *managerTLSFakeConnector
	db           *sql.DB
	legacyOpens  int
	strictOpens  int
	legacyDriver string
	legacyDSN    string
}

func newManagerTLSHarness(t *testing.T, driverName Driver, policy *TLSPolicy) *managerTLSHarness {
	t.Helper()
	h := &managerTLSHarness{
		manager: NewManager(),
		source: Source{ID: "synthetic", Driver: driverName, Host: "db.example", Port: 5432,
			DefaultDatabase: "default_db", Path: "/fake/never-opened.sqlite", Reachability: "published", Evidence: "test"},
		connection: Connection{SourceID: "synthetic", Credentials: Credentials{
			Username: "test_user", Password: "fake-password-sentinel", Database: "test_db", TLS: policy}},
		fake: &managerTLSFakeConnector{},
	}
	h.manager.discoverFn = func(context.Context) (Discovery, error) {
		return Discovery{CollectedAt: time.Now(), Sources: []Source{h.source}}, nil
	}
	h.manager.sqlOpen = func(driverName, dsn string) (*sql.DB, error) {
		h.legacyOpens++
		h.legacyDriver, h.legacyDSN = driverName, dsn
		h.db = sql.OpenDB(h.fake)
		return h.db, nil
	}
	h.manager.sqlOpenDB = func(connector driver.Connector) *sql.DB {
		h.strictOpens++
		// 必须先经过真实严格工厂；只截断 sql.OpenDB，绝不调用真实 Connect。
		switch h.source.Driver {
		case DriverMySQL:
			if _, ok := connector.Driver().(*mysql.MySQLDriver); !ok {
				t.Fatalf("strict MySQL did not construct its native connector: %T", connector)
			}
		case DriverPostgreSQL:
			if _, ok := connector.Driver().(*stdlib.Driver); !ok {
				t.Fatalf("strict PostgreSQL did not construct its native connector: %T", connector)
			}
		default:
			t.Fatalf("unexpected strict connector for %s", h.source.Driver)
		}
		h.db = sql.OpenDB(h.fake)
		return h.db
	}
	t.Cleanup(func() {
		if h.db != nil {
			h.db.Close()
		}
	})
	return h
}

type managerTLSFakeConnector struct {
	mu              sync.Mutex
	connectContexts []context.Context
	pingContexts    []context.Context
	pingErr         error
	pingFn          func(context.Context) error
	closed          int
}

func (c *managerTLSFakeConnector) Connect(ctx context.Context) (driver.Conn, error) {
	c.mu.Lock()
	c.connectContexts = append(c.connectContexts, ctx)
	c.mu.Unlock()
	return &managerTLSFakeConn{connector: c}, nil
}

func (c *managerTLSFakeConnector) Driver() driver.Driver { return managerTLSFakeDriver{} }

func (c *managerTLSFakeConnector) closedCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

type managerTLSFakeDriver struct{}

func (managerTLSFakeDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("fake connector must not use Driver.Open")
}

type managerTLSFakeConn struct{ connector *managerTLSFakeConnector }

func (c *managerTLSFakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("fake connection does not prepare statements")
}

func (c *managerTLSFakeConn) Begin() (driver.Tx, error) {
	return nil, errors.New("fake connection does not begin transactions")
}

func (c *managerTLSFakeConn) Close() error {
	c.connector.mu.Lock()
	defer c.connector.mu.Unlock()
	c.connector.closed++
	return nil
}

func (c *managerTLSFakeConn) Ping(ctx context.Context) error {
	c.connector.mu.Lock()
	c.connector.pingContexts = append(c.connector.pingContexts, ctx)
	c.connector.mu.Unlock()
	if c.connector.pingFn != nil {
		return c.connector.pingFn(ctx)
	}
	return c.connector.pingErr
}

func (*managerTLSFakeConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return nil, errors.New("fake query rejection")
}

func (*managerTLSFakeConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return nil, errors.New("fake exec rejection")
}
