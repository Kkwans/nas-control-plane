package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCatalogEnrichmentCancellation(t *testing.T) {
	for _, engine := range []Driver{DriverSQLite, DriverMySQL, DriverPostgreSQL} {
		for _, stage := range []int{1, 2} {
			for _, tableCount := range []int{1, 2} {
				for _, failure := range []string{"wrapped cancel", "wrapped deadline", "cancel context", "cancel successful scan"} {
					t.Run(fmt.Sprintf("%s/stage%d/tables%d/%s", engine, stage, tableCount, failure), func(t *testing.T) {
						ctx, cancel := context.WithCancel(context.Background())
						defer cancel()
						c := &catalogConnector{engine: engine, tableCount: tableCount, failStage: stage}
						cause := context.Canceled
						switch failure {
						case "wrapped cancel":
							c.failure = fmt.Errorf("fixture: %w", context.Canceled)
						case "wrapped deadline":
							cause = context.DeadlineExceeded
							c.failure = fmt.Errorf("fixture: %w", cause)
						case "cancel successful scan":
							c.cancelOnClose = cancel
						case "cancel context":
							c.onFailure = cancel
							c.failure = errors.New("driver interrupted")
						}
						m, connection := newCatalogManager(c)
						result, err := m.Catalog(ctx, CatalogRequest{Connection: connection})
						if ErrorCodeOf(err) != CodeTimeout || !errors.Is(err, cause) || result.Tables != nil || result.Source.ID != "" {
							t.Errorf("catalog = %+v, error = %v; want empty timeout with %v", result, err, cause)
						}
						if c.enrichments.Load() != int32(stage) || c.columnQueries.Load() != 1 {
							t.Errorf("continued after cancellation: enrichment=%d columns=%d", c.enrichments.Load(), c.columnQueries.Load())
						}
						if c.closes.Load() != 1 || m.operations.active != 0 {
							t.Errorf("cleanup: closes=%d active=%d", c.closes.Load(), m.operations.active)
						}
					})
				}
			}
		}
	}
}
func TestCatalogOptionalEnrichmentFailuresRemainOptional(t *testing.T) {
	for _, engine := range []Driver{DriverSQLite, DriverMySQL, DriverPostgreSQL} {
		for _, stage := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/stage%d", engine, stage), func(t *testing.T) {
				c := &catalogConnector{engine: engine, tableCount: 2, failStage: stage, failure: errors.New("optional metadata unavailable")}
				m, connection := newCatalogManager(c)
				result, err := m.Catalog(context.Background(), CatalogRequest{Connection: connection})
				if err != nil || len(result.Tables) != 2 {
					t.Fatalf("catalog=%+v error=%v", result, err)
				}
				if c.enrichments.Load() != 4 || c.columnQueries.Load() != 2 || c.closes.Load() != 1 || m.operations.active != 0 {
					t.Fatal("optional failure stopped work or leaked resources")
				}
			})
		}
	}
}
func newCatalogManager(c *catalogConnector) (*Manager, Connection) {
	m := NewManager()
	m.discoverFn = func(context.Context) (Discovery, error) {
		return Discovery{CollectedAt: time.Now(), Sources: []Source{{ID: "catalog", Driver: c.engine, Path: "/never-opened", Host: "fixture.invalid", Port: 1234}}}, nil
	}
	m.sqlOpen = func(string, string) (*sql.DB, error) { return sql.OpenDB(c), nil }
	return m, Connection{SourceID: "catalog", Credentials: Credentials{Username: "fixture", Database: "fixture"}}
}

type catalogConnector struct {
	catalogClose                       func()
	cancelOnClose                      func()
	engine                             Driver
	tableCount, failStage              int
	failure                            error
	onFailure                          func()
	closes, enrichments, columnQueries atomic.Int32
}

func (c *catalogConnector) Driver() driver.Driver                        { return managerTLSFakeDriver{} }
func (c *catalogConnector) Connect(context.Context) (driver.Conn, error) { return &catalogConn{c}, nil }

type catalogConn struct{ c *catalogConnector }

func (c *catalogConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *catalogConn) Begin() (driver.Tx, error)  { return nil, errors.New("unexpected transaction") }
func (c *catalogConn) Ping(context.Context) error { return nil }
func (c *catalogConn) Close() error               { c.c.closes.Add(1); return nil }
func (c *catalogConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(query, "sqlite_master") || strings.HasPrefix(query, "SELECT table_schema, table_name") {
		rows := make([][]driver.Value, c.c.tableCount)
		for i := range rows {
			rows[i] = []driver.Value{"fixture", fmt.Sprintf("table%d", i), "VIEW", ""}
		}
		result := catalogRowsOf(4, rows...)
		result.onClose = c.c.catalogClose
		return result, nil
	}
	if strings.HasPrefix(query, "PRAGMA table_info") || strings.Contains(query, "FROM information_schema.columns c") {
		c.c.columnQueries.Add(1)
		if c.c.engine == DriverSQLite {
			return catalogRowsOf(6, []driver.Value{int64(0), "value", "TEXT", int64(0), nil, int64(0)}), nil
		}
		return catalogRowsOf(8, []driver.Value{"value", "text", "YES", nil, int64(1), int64(0), "", "NEVER"}), nil
	}
	stage := c.c.enrichments.Add(1)
	if int(stage) == c.c.failStage && c.c.cancelOnClose == nil {
		if c.c.onFailure != nil {
			c.c.onFailure()
		}
		return nil, c.c.failure
	}
	finish := func(rows *catalogRows) (driver.Rows, error) {
		if int(stage) == c.c.failStage {
			rows.onClose = c.c.cancelOnClose
		}
		return rows, nil
	}
	switch c.c.engine {
	case DriverSQLite:
		return finish(catalogRowsOf(1, []driver.Value{int64(1)}))
	case DriverMySQL:
		if strings.HasPrefix(query, "SHOW CREATE") {
			return finish(catalogRowsOf(2, []driver.Value{"table", "definition"}))
		}
		return finish(catalogRowsOf(3, []driver.Value{int64(1), int64(1), ""}))
	default:
		if strings.Contains(query, "pg_get_viewdef") {
			return finish(catalogRowsOf(1, []driver.Value{"definition"}))
		}
		return finish(catalogRowsOf(2, []driver.Value{int64(1), int64(1)}))
	}
}

type catalogRows struct {
	onClose func()
	names   []string
	values  [][]driver.Value
}

func catalogRowsOf(count int, values ...[]driver.Value) *catalogRows {
	names := make([]string, count)
	for i := range names {
		names[i] = fmt.Sprintf("column%d", i)
	}
	return &catalogRows{names: names, values: values}
}
func (r *catalogRows) Columns() []string { return r.names }
func (r *catalogRows) Close() error {
	if r.onClose != nil {
		r.onClose()
	}
	return nil
}
func (r *catalogRows) Next(out []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(out, r.values[0])
	r.values = r.values[1:]
	return nil
}

func TestCatalogEmptyCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &catalogConnector{engine: DriverSQLite, catalogClose: cancel}
	m, connection := newCatalogManager(c)
	result, err := m.Catalog(ctx, CatalogRequest{Connection: connection})
	if ErrorCodeOf(err) != CodeTimeout || !errors.Is(err, context.Canceled) || result.Tables != nil || result.Source.ID != "" {
		t.Fatalf("catalog=%+v error=%v", result, err)
	}
	if c.enrichments.Load() != 0 || c.columnQueries.Load() != 0 || c.closes.Load() != 1 || m.operations.active != 0 {
		t.Fatal("empty cancelled catalog performed work or leaked resources")
	}
}
