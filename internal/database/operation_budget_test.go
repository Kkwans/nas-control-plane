package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOperationBudgetConcurrentBoundAndCancellation(t *testing.T) {
	var b operationBudget
	releases := make([]func(), maxActiveDatabaseOperations)
	for i := range releases {
		var err error
		releases[i], err = b.acquire(context.Background(), "fake")
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.acquire(ctx, "fake"); ErrorCodeOf(err) != CodeTimeout {
		t.Fatalf("canceled full budget: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if release, err := b.acquire(context.Background(), "fake"); ErrorCodeOf(err) != CodeOperationBusy || release != nil {
				t.Errorf("full budget: %v", err)
			}
		}()
	}
	wg.Wait()
	for _, release := range releases {
		release()
		release()
	}
	if b.active != 0 {
		t.Fatal("release leaked")
	}
	if _, err := b.acquire(ctx, "fake"); ErrorCodeOf(err) != CodeTimeout || b.active != 0 {
		t.Fatalf("canceled idle budget: %v", err)
	}
}

func TestManagerOperationBudgetCoversEveryOwnerBeforeDiscovery(t *testing.T) {
	m, connection, connector := newOperationBudgetManager()
	var cleanup []func()
	for i := 0; i < maxActiveDatabaseOperations; i++ {
		_, pool, closePool, err := m.connect(context.Background(), connection)
		if err != nil {
			t.Fatal(err)
		}
		if pool.Stats().MaxOpenConnections != 2 {
			t.Fatal("per-pool limit changed")
		}
		cleanup = append(cleanup, closePool)
		t.Cleanup(closePool)
	}
	calls := []struct {
		name string
		run  func() error
	}{
		{"diagnostic", func() error { _, err := m.TestConnection(context.Background(), connection); return err }},
		{"catalog", func() error {
			_, err := m.Catalog(context.Background(), CatalogRequest{Connection: connection})
			return err
		}},
		{"query", func() error {
			_, err := m.Query(context.Background(), QueryRequest{Connection: connection, SQL: "SELECT 1"})
			return err
		}},
		{"options", func() error {
			_, err := m.QueryWithOptions(context.Background(), QueryRequest{Connection: connection, SQL: "SELECT 1"})
			return err
		}},
		{"rows", func() error { _, err := m.Rows(context.Background(), RowsRequest{Connection: connection}); return err }},
		{"insert", func() error {
			_, err := m.Insert(context.Background(), InsertRequest{Connection: connection})
			return err
		}},
		{"update", func() error {
			_, err := m.Update(context.Background(), UpdateRequest{InsertRequest: InsertRequest{Connection: connection}})
			return err
		}},
		{"delete", func() error {
			_, err := m.Delete(context.Background(), DeleteRequest{Connection: connection})
			return err
		}},
	}
	// 饱和后即使来源无效也先拒绝，不再发现或打开池。
	connection.SourceID = "missing"
	m.discoverFn = func(context.Context) (Discovery, error) {
		t.Error("admission ran discovery")
		return Discovery{}, errors.New("unexpected")
	}
	m.registry.expiresAt = time.Time{}
	for _, call := range calls {
		t.Run(call.name, func(t *testing.T) {
			if err := call.run(); ErrorCodeOf(err) != CodeOperationBusy {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if connector.opens.Load() != 4 || connector.queries.Load() != 0 {
		t.Fatal("rejection performed SQL")
	}
	other, otherConnection, _ := newOperationBudgetManager()
	if _, err := other.TestConnection(context.Background(), otherConnection); err != nil {
		t.Fatal(err)
	}
	for _, closePool := range cleanup {
		closePool()
	}
	if m.operations.active != 0 {
		t.Fatal("permits leaked")
	}
}

func TestManagerOperationBudgetHoldsThroughClose(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "ping failure"}[failure], func(t *testing.T) {
			m, connection, c := newOperationBudgetManager()
			if failure {
				c.pingErr = errors.New("fake ping failure")
			}
			c.closeEntered = make(chan struct{}, 4)
			c.closeContinue = make(chan struct{})
			var unblock sync.Once
			defer unblock.Do(func() { close(c.closeContinue) })
			done := make(chan struct{}, 4)
			for i := 0; i < 4; i++ {
				go func() {
					defer func() { done <- struct{}{} }()
					_, _, cleanup, err := m.connect(context.Background(), connection)
					if failure {
						if err == nil {
							t.Error("expected ping error")
						}
						return
					}
					if err != nil {
						t.Error(err)
						return
					}
					cleanup()
					cleanup()
				}()
			}
			for i := 0; i < 4; i++ {
				awaitBudgetSignal(t, c.closeEntered)
			}
			if _, _, cleanup, err := m.connect(context.Background(), connection); cleanup != nil || ErrorCodeOf(err) != CodeOperationBusy {
				t.Fatalf("released before close: %v", err)
			}
			unblock.Do(func() { close(c.closeContinue) })
			for i := 0; i < 4; i++ {
				awaitBudgetSignal(t, done)
			}
			if m.operations.active != 0 || c.closes.Load() != 4 {
				t.Fatal("close/release count wrong")
			}
		})
	}
}

func TestManagerOperationBudgetHoldsThroughRowsClose(t *testing.T) {
	m, connection, c := newOperationBudgetManager()
	c.rowsEntered = make(chan struct{}, 4)
	c.rowsContinue = make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(c.rowsContinue) })
	done := make(chan struct{}, 4)
	for i := 0; i < 4; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			_, err := m.QueryWithOptions(context.Background(), QueryRequest{Connection: connection, SQL: "SELECT 1", ResultMode: QueryResultModeRows})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	for i := 0; i < 4; i++ {
		awaitBudgetSignal(t, c.rowsEntered)
	}
	if _, err := m.TestConnection(context.Background(), connection); ErrorCodeOf(err) != CodeOperationBusy {
		t.Fatalf("released before rows close: %v", err)
	}
	unblock.Do(func() { close(c.rowsContinue) })
	for i := 0; i < 4; i++ {
		awaitBudgetSignal(t, done)
	}
	if c.queries.Load() != 4 || m.operations.active != 0 || c.closes.Load() != 4 {
		t.Fatal("query repeated or lease leaked")
	}
}

func TestManagerOperationBudgetFailureAndPanicRelease(t *testing.T) {
	for _, kind := range []string{"discover", "missing", "configuration", "open", "nil pool", "ping", "panic"} {
		t.Run(kind, func(t *testing.T) {
			m, connection, c := newOperationBudgetManager()
			switch kind {
			case "discover":
				m.discoverFn = func(context.Context) (Discovery, error) { return Discovery{}, errors.New("fake failure") }
			case "missing":
				connection.SourceID = "missing"
			case "configuration":
				connection.Credentials.TLS = &TLSPolicy{Mode: "invalid"}
			case "open":
				m.sqlOpen = func(string, string) (*sql.DB, error) { return nil, errors.New("fake open failure") }
			case "nil pool":
				m.sqlOpen = func(string, string) (*sql.DB, error) { return nil, nil }
			case "ping":
				c.pingErr = errors.New("fake ping failure")
			case "panic":
				m.discoverFn = func(context.Context) (Discovery, error) { panic("synthetic panic") }
			}
			func() {
				defer func() {
					if r := recover(); r != nil && kind != "panic" {
						panic(r)
					}
				}()
				_, _, cleanup, err := m.connect(context.Background(), connection)
				if cleanup != nil {
					cleanup()
				}
				if kind != "panic" && err == nil {
					t.Error("expected failure")
				}
			}()
			if m.operations.active != 0 {
				t.Fatal("failure leaked admission")
			}
		})
	}
}

func awaitBudgetSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("fake operation did not reach barrier")
	}
}
func newOperationBudgetManager() (*Manager, Connection, *operationBudgetConnector) {
	m := NewManager()
	c := &operationBudgetConnector{}
	m.discoverFn = func(context.Context) (Discovery, error) {
		return Discovery{CollectedAt: time.Now(), Sources: []Source{{ID: "fake", Driver: DriverSQLite, Path: "/never-opened", Evidence: "test"}}}, nil
	}
	m.sqlOpen = func(string, string) (*sql.DB, error) { return sql.OpenDB(c), nil }
	return m, Connection{SourceID: "fake"}, c
}

type operationBudgetConnector struct {
	opens, closes, queries                                 atomic.Int32
	pingFn                                                 func(context.Context) error
	pingErr                                                error
	closeEntered, closeContinue, rowsEntered, rowsContinue chan struct{}
}

func (c *operationBudgetConnector) Driver() driver.Driver { return managerTLSFakeDriver{} }
func (c *operationBudgetConnector) Connect(context.Context) (driver.Conn, error) {
	c.opens.Add(1)
	return &operationBudgetConn{c}, nil
}

type operationBudgetConn struct{ c *operationBudgetConnector }

func (c *operationBudgetConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unused prepare")
}
func (c *operationBudgetConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unused transaction")
}
func (c *operationBudgetConn) Ping(ctx context.Context) error {
	if c.c.pingFn != nil {
		return c.c.pingFn(ctx)
	}
	return c.c.pingErr
}
func (c *operationBudgetConn) Close() error {
	if c.c.closeEntered != nil {
		c.c.closeEntered <- struct{}{}
		<-c.c.closeContinue
	}
	c.c.closes.Add(1)
	return nil
}
func (c *operationBudgetConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	c.c.queries.Add(1)
	return &operationBudgetRows{c: c.c}, nil
}

type operationBudgetRows struct{ c *operationBudgetConnector }

func (r *operationBudgetRows) Columns() []string         { return []string{"value"} }
func (r *operationBudgetRows) Next([]driver.Value) error { return io.EOF }
func (r *operationBudgetRows) Close() error {
	if r.c.rowsEntered != nil {
		r.c.rowsEntered <- struct{}{}
		<-r.c.rowsContinue
	}
	return nil
}

func TestManagerOperationBudgetCancellationDuringPingReusesCapacity(t *testing.T) {
	m, connection, c := newOperationBudgetManager()
	entered := make(chan struct{}, 4)
	c.pingFn = func(ctx context.Context) error { entered <- struct{}{}; <-ctx.Done(); return ctx.Err() }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{}, 4)
	for i := 0; i < 4; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			_, err := m.TestConnection(ctx, connection)
			if ErrorCodeOf(err) != CodeTimeout {
				t.Errorf("cancel code: %v", err)
			}
		}()
	}
	for i := 0; i < 4; i++ {
		awaitBudgetSignal(t, entered)
	}
	if _, err := m.TestConnection(context.Background(), connection); ErrorCodeOf(err) != CodeOperationBusy {
		t.Fatalf("active canceled work not bounded: %v", err)
	}
	cancel()
	for i := 0; i < 4; i++ {
		awaitBudgetSignal(t, done)
	}
	c.pingFn = nil
	if _, err := m.TestConnection(context.Background(), connection); err != nil {
		t.Fatal(err)
	}
	if c.closes.Load() != 5 || m.operations.active != 0 {
		t.Fatal("canceled operation leaked pool or permit")
	}
}

func TestManagerOperationBudgetMixedLegacyAndTLSOwners(t *testing.T) {
	m, legacy, c := newOperationBudgetManager()
	m.discoverFn = func(context.Context) (Discovery, error) {
		return Discovery{CollectedAt: time.Now(), Sources: []Source{{ID: "fake", Driver: DriverMySQL, Host: "db.example", Port: 3306, Evidence: "test", Reachability: "published"}}}, nil
	}
	legacy.Credentials = Credentials{Username: "synthetic", Database: "fixture"}
	strict := legacy
	strict.Credentials.TLS = &TLSPolicy{Mode: TLSVerifyFull}
	var strictOpens atomic.Int32
	m.sqlOpenDB = func(driver.Connector) *sql.DB { strictOpens.Add(1); return sql.OpenDB(c) }
	c.rowsEntered = make(chan struct{}, 4)
	c.rowsContinue = make(chan struct{})
	c.closeEntered = make(chan struct{}, 4)
	c.closeContinue = make(chan struct{})
	var unblockRows, unblockClose sync.Once
	defer unblockRows.Do(func() { close(c.rowsContinue) })
	defer unblockClose.Do(func() { close(c.closeContinue) })
	done := make(chan struct{}, 4)
	calls := []func(){
		func() {
			_, err := m.TestConnection(context.Background(), strict)
			if err != nil {
				t.Error(err)
			}
		},
		func() {
			_, err := m.QueryWithOptions(context.Background(), QueryRequest{Connection: strict, SQL: "SELECT 1", ResultMode: QueryResultModeRows})
			if err != nil {
				t.Error(err)
			}
		},
		func() {
			_, err := m.Catalog(context.Background(), CatalogRequest{Connection: legacy})
			if err != nil {
				t.Error(err)
			}
		},
		func() {
			_, err := m.Rows(context.Background(), RowsRequest{Connection: legacy, Table: "missing"})
			if ErrorCodeOf(err) != CodeDatabaseNotFound {
				t.Errorf("expected missing metadata: %v", err)
			}
		},
	}
	for _, call := range calls {
		go func() { defer func() { done <- struct{}{} }(); call() }()
	}
	awaitBudgetSignal(t, c.closeEntered)
	for i := 0; i < 3; i++ {
		awaitBudgetSignal(t, c.rowsEntered)
	}
	for _, connection := range []Connection{legacy, strict} {
		if _, err := m.TestConnection(context.Background(), connection); ErrorCodeOf(err) != CodeOperationBusy {
			t.Errorf("mixed budget bypass: %v", err)
		}
	}
	if c.opens.Load() != 4 || strictOpens.Load() != 2 {
		t.Fatal("unexpected open dispatch")
	}
	unblockRows.Do(func() { close(c.rowsContinue) })
	unblockClose.Do(func() { close(c.closeContinue) })
	for i := 0; i < 4; i++ {
		awaitBudgetSignal(t, done)
	}
	if m.operations.active != 0 || c.closes.Load() != 4 {
		t.Fatal("mixed operations leaked")
	}
}
