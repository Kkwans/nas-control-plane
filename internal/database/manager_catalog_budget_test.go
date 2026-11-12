package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

type catalogBudgetConnector struct {
	base  catalogConnector
	query func(string) (driver.Rows, bool)
}

func (c *catalogBudgetConnector) Driver() driver.Driver { return managerTLSFakeDriver{} }
func (c *catalogBudgetConnector) Connect(context.Context) (driver.Conn, error) {
	return &catalogBudgetConn{catalogConn{&c.base}, c}, nil
}

type catalogBudgetConn struct {
	catalogConn
	owner *catalogBudgetConnector
}

func (c *catalogBudgetConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if rows, ok := c.owner.query(query); ok {
		return rows, nil
	}
	return c.catalogConn.QueryContext(ctx, query, args)
}
func newBudgetCatalogManager(c *catalogBudgetConnector) (*Manager, Connection) {
	m, connection := newCatalogManager(&c.base)
	m.sqlOpen = func(string, string) (*sql.DB, error) { return sql.OpenDB(c), nil }
	return m, connection
}

type lazyCatalogRows struct {
	names               []string
	count, read, closes int
	values              func(int) []driver.Value
	closeErr            error
}

func (r *lazyCatalogRows) Columns() []string { return r.names }
func (r *lazyCatalogRows) Close() error      { r.closes++; return r.closeErr }
func (r *lazyCatalogRows) Next(out []driver.Value) error {
	if r.read >= r.count {
		return io.EOF
	}
	copy(out, r.values(r.read))
	r.read++
	return nil
}
func tableBudgetRows(count int, definition string) *lazyCatalogRows {
	return &lazyCatalogRows{names: []string{"schema", "name", "type", "definition"}, count: count, values: func(i int) []driver.Value {
		return []driver.Value{"app", fmt.Sprintf("table%d", i), "VIEW", definition}
	}}
}
func columnBudgetRows(count int, defaultValue driver.Value) *lazyCatalogRows {
	return &lazyCatalogRows{names: []string{"name", "type", "nullable", "default", "position", "pk", "identity", "generated"}, count: count, values: func(i int) []driver.Value {
		return []driver.Value{fmt.Sprintf("column%d", i), "text", "YES", defaultValue, int64(i + 1), int64(0), "", "NEVER"}
	}}
}

func TestCatalogBudgetLazyCollectionsStop(t *testing.T) {
	for _, stage := range []string{"tables", "columns"} {
		t.Run(stage, func(t *testing.T) {
			c := &catalogBudgetConnector{base: catalogConnector{engine: DriverPostgreSQL, tableCount: 2}}
			tables, columns := tableBudgetRows(1_000_000_000, ""), columnBudgetRows(1_000_000_000, nil)
			columnCalls := 0
			c.query = func(q string) (driver.Rows, bool) {
				if stage == "tables" && strings.HasPrefix(q, "SELECT table_schema, table_name") {
					return tables, true
				}
				if strings.Contains(q, "FROM information_schema.columns c") {
					columnCalls++
					if stage == "columns" {
						return columns, true
					}
				}
				return nil, false
			}
			m, conn := newBudgetCatalogManager(c)
			result, err := m.Catalog(context.Background(), CatalogRequest{Connection: conn})
			if ErrorCodeOf(err) != CodeResultTooLarge || result.Source.ID != "" || result.Tables != nil {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if c.base.closes.Load() != 1 || m.operations.active != 0 || c.base.enrichments.Load() != 0 {
				t.Fatal("continued work or leaked operation")
			}
			rows := tables
			wantCalls := 0
			if stage == "columns" {
				rows = columns
				wantCalls = 1
			}
			if rows.read < 2 || rows.read > MaxResultBytes/resultNodeCost || rows.closes != 1 || columnCalls != wantCalls {
				t.Fatalf("read=%d close=%d column calls=%d", rows.read, rows.closes, columnCalls)
			}
		})
	}
}

func TestCatalogBudgetMetadataAndDefaultOverflow(t *testing.T) {
	for _, stage := range []string{"definition", "name", "datatype", "default text", "default bytes", "enrichment"} {
		t.Run(stage, func(t *testing.T) {
			huge := strings.Repeat("x", MaxResultBytes+1)
			c := &catalogBudgetConnector{base: catalogConnector{engine: DriverPostgreSQL, tableCount: 2}}
			columns := 0
			c.query = func(q string) (driver.Rows, bool) {
				if stage == "definition" && strings.HasPrefix(q, "SELECT table_schema, table_name") {
					return tableBudgetRows(2, huge), true
				}
				if strings.Contains(q, "FROM information_schema.columns c") {
					columns++
					r := columnBudgetRows(1, nil)
					switch stage {
					case "name", "datatype":
						old := r.values
						r.values = func(i int) []driver.Value {
							v := old(i)
							index := 0
							if stage == "datatype" {
								index = 1
							}
							v[index] = huge
							return v
						}
					case "default text":
						r = columnBudgetRows(1, huge)
					case "default bytes":
						r = columnBudgetRows(1, []byte(huge))
					}
					return r, true
				}
				if stage == "enrichment" && strings.Contains(q, "pg_get_viewdef") {
					return catalogRowsOf(1, []driver.Value{huge}), true
				}
				return nil, false
			}
			m, conn := newBudgetCatalogManager(c)
			result, err := m.Catalog(context.Background(), CatalogRequest{Connection: conn})
			if ErrorCodeOf(err) != CodeResultTooLarge || result.Tables != nil || m.operations.active != 0 || c.base.closes.Load() != 1 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if columns > 1 {
				t.Fatal("queried later table")
			}
		})
	}
}

func TestCatalogBudgetAccountingMatchesIndependentOracle(t *testing.T) {
	for _, def := range []any{nil, "", false, int64(0), "<\\\"\u2028é"} {
		t.Run(fmt.Sprintf("%#v", def), func(t *testing.T) {
			source := Source{ID: "fixture", Driver: DriverPostgreSQL}
			table := Table{Schema: "app", Name: "t", Type: "VIEW", Definition: "existing"}
			budget, err := newResultBudget(Catalog{Source: source, Tables: []Table{}})
			if err != nil {
				t.Fatal(err)
			}
			if err = chargeCatalogValue(budget, table); err != nil {
				t.Fatal(err)
			}
			cell := catalogDefaultCell{budget: budget}
			if err = cell.Scan(def); err != nil {
				t.Fatal(err)
			}
			column := Column{Name: "n", DataType: "text", Default: cell.value, Position: 1, WriteMode: "required"}
			if err = retainCatalogColumn(context.Background(), budget, &column, true, false); err != nil {
				t.Fatal(err)
			}
			table.Columns = []Column{column}
			result := Catalog{Source: source, Tables: []Table{table}}
			oracle, err := newResultBudget(result)
			if err != nil {
				t.Fatal(err)
			}
			if budget.remaining != oracle.remaining {
				t.Fatalf("remaining=%d oracle=%d", budget.remaining, oracle.remaining)
			}
			if err = ValidateResultBudget(result); err != nil {
				t.Fatal(err)
			}
			exact := resultBudget{remaining: MaxResultBytes - oracle.remaining}
			if err = chargeCatalogValue(&exact, result); err != nil || exact.remaining != 0 {
				t.Fatalf("exact=%d err=%v", exact.remaining, err)
			}
			short := resultBudget{remaining: MaxResultBytes - oracle.remaining - 1}
			if ErrorCodeOf(chargeCatalogValue(&short, result)) != CodeResultTooLarge {
				t.Fatal("one byte overflow accepted")
			}
		})
	}
}

func TestCatalogBudgetCloseFailure(t *testing.T) {
	sentinel := errors.New("catalog close fixture")
	c := &catalogBudgetConnector{base: catalogConnector{engine: DriverPostgreSQL, tableCount: 2}}
	r := tableBudgetRows(1, "")
	r.closeErr = sentinel
	c.query = func(q string) (driver.Rows, bool) {
		if strings.HasPrefix(q, "SELECT table_schema, table_name") {
			return r, true
		}
		return nil, false
	}
	m, conn := newBudgetCatalogManager(c)
	result, err := m.Catalog(context.Background(), CatalogRequest{Connection: conn})
	if !errors.Is(err, sentinel) || result.Tables != nil || c.base.columnQueries.Load() != 0 || m.operations.active != 0 || c.base.closes.Load() != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCatalogBudgetTemporarySQLite(t *testing.T) {
	m, conn, db := newQueryFixture(t)
	if _, err := db.Exec(`CREATE TABLE catalog_budget (id INTEGER PRIMARY KEY, text_value TEXT DEFAULT '', numeric_value INTEGER DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	result, err := m.Catalog(context.Background(), CatalogRequest{Connection: conn})
	if err != nil {
		t.Fatal(err)
	}
	if err = ValidateResultBudget(result); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, table := range result.Tables {
		if table.Name == "catalog_budget" {
			found = true
			if len(table.Columns) != 3 || table.Columns[0].WriteMode != "server-generated" || table.Columns[1].Default != "''" {
				t.Fatalf("table=%+v", table)
			}
		}
	}
	if !found {
		t.Fatal("missing table")
	}
}

func TestCatalogBudgetAggregateAcrossTables(t *testing.T) {
	c := &catalogBudgetConnector{base: catalogConnector{engine: DriverPostgreSQL, tableCount: 3}}
	calls := 0
	c.query = func(q string) (driver.Rows, bool) {
		if strings.Contains(q, "FROM information_schema.columns c") {
			calls++
			return columnBudgetRows(1, strings.Repeat("a", MaxResultBytes/2+1)), true
		}
		return nil, false
	}
	m, conn := newBudgetCatalogManager(c)
	result, err := m.Catalog(context.Background(), CatalogRequest{Connection: conn})
	if ErrorCodeOf(err) != CodeResultTooLarge || result.Tables != nil || calls != 2 || c.base.enrichments.Load() != 2 || m.operations.active != 0 {
		t.Fatalf("result=%+v err=%v calls=%d enrich=%d", result, err, calls, c.base.enrichments.Load())
	}
}

func TestCatalogBudgetEnrichmentScalarDelta(t *testing.T) {
	for _, engine := range []Driver{DriverSQLite, DriverMySQL, DriverPostgreSQL} {
		t.Run(string(engine), func(t *testing.T) {
			c := &catalogBudgetConnector{base: catalogConnector{engine: engine}}
			c.query = func(string) (driver.Rows, bool) { return nil, false }
			db := sql.OpenDB(c)
			defer db.Close()
			table := Table{Schema: "app", Name: "t", Type: "VIEW", Definition: "existing-definition", Columns: []Column{{Name: "n", DataType: "text", WriteMode: "required"}}}
			result := Catalog{Source: Source{ID: "fixture", Driver: engine}, Tables: []Table{table}}
			budget, err := newResultBudget(result)
			if err != nil {
				t.Fatal(err)
			}
			if err = enrichCatalogTable(context.Background(), db, engine, &table, budget); err != nil {
				t.Fatal(err)
			}
			result.Tables[0] = table
			oracle, err := newResultBudget(result)
			if err != nil {
				t.Fatal(err)
			}
			if oracle.remaining != budget.remaining {
				t.Fatalf("remaining=%d oracle=%d", budget.remaining, oracle.remaining)
			}
			if engine == DriverSQLite && table.Definition != "existing-definition" {
				t.Fatal("SQLite definition changed")
			}
		})
	}
}

func TestCatalogBudgetLegacyColumnWrapperUnchanged(t *testing.T) {
	huge := strings.Repeat("a", MaxResultBytes+1)
	c := &catalogBudgetConnector{base: catalogConnector{engine: DriverPostgreSQL}}
	c.query = func(q string) (driver.Rows, bool) {
		if strings.Contains(q, "FROM information_schema.columns c") {
			return columnBudgetRows(1, huge), true
		}
		return nil, false
	}
	db := sql.OpenDB(c)
	defer db.Close()
	table, err := loadTable(context.Background(), db, DriverPostgreSQL, "app", "t")
	if err != nil || len(table.Columns) != 1 || table.Columns[0].Default != huge {
		t.Fatalf("legacy metadata changed: err=%v", err)
	}
}

func TestCatalogBudgetEnrichmentStopsBeforeNextQuery(t *testing.T) {
	c := &catalogBudgetConnector{base: catalogConnector{engine: DriverMySQL, tableCount: 2}}
	showCalls := 0
	c.query = func(q string) (driver.Rows, bool) {
		if strings.Contains(q, "SELECT table_rows, data_length") {
			return catalogRowsOf(3, []driver.Value{int64(1), int64(1), strings.Repeat("a", MaxResultBytes+1)}), true
		}
		if strings.HasPrefix(q, "SHOW CREATE") {
			showCalls++
		}
		return nil, false
	}
	m, conn := newBudgetCatalogManager(c)
	result, err := m.Catalog(context.Background(), CatalogRequest{Connection: conn})
	if ErrorCodeOf(err) != CodeResultTooLarge || result.Tables != nil || showCalls != 0 || c.base.columnQueries.Load() != 1 || m.operations.active != 0 {
		t.Fatalf("result=%+v err=%v show=%d", result, err, showCalls)
	}
}

func TestCatalogBudgetSQLiteColumnCloseCancellationStopsIndex(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &catalogBudgetConnector{base: catalogConnector{engine: DriverSQLite, tableCount: 1}}
	indexCalls := 0
	c.query = func(q string) (driver.Rows, bool) {
		if strings.HasPrefix(q, "PRAGMA table_info") {
			r := catalogRowsOf(6, []driver.Value{int64(0), "id", "INTEGER", int64(0), nil, int64(1)})
			r.onClose = cancel
			return r, true
		}
		if strings.HasPrefix(q, "PRAGMA index_list") {
			indexCalls++
		}
		return nil, false
	}
	m, conn := newBudgetCatalogManager(c)
	result, err := m.Catalog(ctx, CatalogRequest{Connection: conn})
	if ErrorCodeOf(err) != CodeTimeout || !errors.Is(err, context.Canceled) || result.Tables != nil || indexCalls != 0 || c.base.enrichments.Load() != 0 || m.operations.active != 0 {
		t.Fatalf("result=%+v err=%v index=%d", result, err, indexCalls)
	}
}
