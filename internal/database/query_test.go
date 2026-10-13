package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"
)

func TestLeadingQueryKeyword(t *testing.T) {
	tests := []struct {
		name   string
		driver Driver
		sql    string
		want   string
		rows   bool
	}{
		{"whitespace", DriverSQLite, " \t\r\nselect 1", "SELECT", true},
		{"comments", DriverSQLite, "-- one\r\n/* two */\t--three\nSELECT/* four */1", "SELECT", true},
		{"paren", DriverPostgreSQL, "VALUES(1)", "VALUES", true},
		{"whole token", DriverSQLite, "SELECTED 1", "SELECTED", false},
		{"identifier", DriverSQLite, "SELECT_foo", "SELECT_FOO", false},
		{"digit", DriverSQLite, "SELECT1", "SELECT1", false},
		{"non ascii token", DriverSQLite, "SELECTé", "SELECTÉ", false},
		{"nested pg", DriverPostgreSQL, "/* outer /* inner */ outer */ SELECT 1", "SELECT", true},
		{"non nested sqlite", DriverSQLite, "/* outer /* inner */ outer */ SELECT 1", "OUTER", false},
		{"non nested mysql", DriverMySQL, "/* outer /* inner */ outer */ SELECT 1", "OUTER", false},
		{"hash mysql", DriverMySQL, "# one\nSELECT 1", "SELECT", true},
		{"hash pg", DriverPostgreSQL, "# one\nSELECT 1", "", false},
		{"hash sqlite", DriverSQLite, "# one\nSELECT 1", "", false},
		{"mysql dash space", DriverMySQL, "-- comment\r\nSHOW TABLES", "SHOW", true},
		{"mysql dash control", DriverMySQL, "--\tcomment\nSELECT 1", "SELECT", true},
		{"mysql dash not comment", DriverMySQL, "--x\nSELECT 1", "", false},
		{"sqlite dash no space", DriverSQLite, "--x\nSELECT 1", "SELECT", true},
		{"pg dash no space", DriverPostgreSQL, "--x\nSELECT 1", "SELECT", true},
		{"mysql executable", DriverMySQL, "/*!50000 SELECT 1 */ SELECT 2", "", false},
		{"maria executable", DriverMySQL, "/*M!100100 SELECT 1 */ SELECT 2", "", false},
		{"maria lowercase conservative", DriverMySQL, "/*m!100100 SELECT 1 */ SELECT 2", "", false},
		{"mysql executable after ordinary", DriverMySQL, "/* harmless */ /*! SELECT 1 */ SELECT 2", "", false},
		{"mysql hint", DriverMySQL, "/*+ hint */ SELECT 1", "SELECT", true},
		{"sqlite normal bang comment", DriverSQLite, "/*! comment */ SELECT 1", "SELECT", true},
		{"unclosed", DriverSQLite, "/* SELECT 1", "", false},
		{"unclosed nested", DriverPostgreSQL, "/* /* closed */ SELECT 1", "", false},
		{"only comment", DriverSQLite, " /* x */ -- end", "", false},
		{"eof line", DriverPostgreSQL, "--SELECT 1", "", false},
		{"sqlite bare cr stays comment", DriverSQLite, "-- comment\rINSERT ignored\nSELECT 1", "SELECT", true},
		{"empty", DriverSQLite, "", "", false},
		{"spaces", DriverSQLite, " \n ", "", false},
		{"dml returning stays exec", DriverSQLite, "INSERT INTO t VALUES(1) RETURNING id", "INSERT", false},
		{"write cte stays rows", DriverSQLite, "WITH x AS (SELECT 1) UPDATE t SET n=n+1", "WITH", true},
		{"string returning", DriverSQLite, "UPDATE t SET s='RETURNING'", "UPDATE", false},
		{"quoted returning", DriverSQLite, `UPDATE "RETURNING" SET n=1`, "UPDATE", false},
		{"comment returning", DriverSQLite, "UPDATE /* RETURNING */ t SET n=1", "UPDATE", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := leadingQueryKeyword(tt.driver, tt.sql); got != tt.want {
				t.Fatalf("keyword = %q, want %q", got, tt.want)
			}
			if got := returnsRows(tt.driver, tt.sql); got != tt.rows {
				t.Fatalf("returns rows = %v", got)
			}
		})
	}
	for _, keyword := range []string{"SELECT", "WITH", "PRAGMA", "EXPLAIN", "SHOW", "DESCRIBE", "DESC", "VALUES"} {
		if !returnsRows(DriverSQLite, keyword+"/*x*/(1)") {
			t.Errorf("lost existing row keyword %s", keyword)
		}
	}
}

type fakeQueryRunner struct {
	execCalls, queryCalls int
	statement             string
	result                sql.Result
	rows                  queryRows
	err                   error
}

func (r *fakeQueryRunner) exec(_ context.Context, statement string) (sql.Result, error) {
	r.execCalls++
	r.statement = statement
	return r.result, r.err
}
func (r *fakeQueryRunner) query(_ context.Context, statement string) (queryRows, error) {
	r.queryCalls++
	r.statement = statement
	return r.rows, r.err
}

type fakeQueryResult struct {
	count int64
	err   error
}

func (r fakeQueryResult) LastInsertId() (int64, error) { panic("not used") }
func (r fakeQueryResult) RowsAffected() (int64, error) { return r.count, r.err }

type fakeQueryRows struct {
	count, next, scans, closes int
	columnsErr, scanErr        error
	iterationErr, closeErr     error
	events                     []string
	onClose                    func()
}

func (r *fakeQueryRows) Columns() ([]string, error)              { return []string{"n"}, r.columnsErr }
func (r *fakeQueryRows) ColumnTypes() ([]*sql.ColumnType, error) { return nil, nil }
func (r *fakeQueryRows) Next() bool {
	if r.next >= r.count {
		return false
	}
	r.next++
	return true
}
func (r *fakeQueryRows) Scan(values ...any) error {
	r.scans++
	if r.scanErr != nil {
		return r.scanErr
	}
	return values[0].(sql.Scanner).Scan(int64(r.next))
}
func (r *fakeQueryRows) Close() error {
	r.closes++
	r.events = append(r.events, "close")
	if r.onClose != nil {
		r.onClose()
	}
	return r.closeErr
}
func (r *fakeQueryRows) Err() error {
	r.events = append(r.events, "err")
	return r.iterationErr
}

func TestQueryModeChoosesOnlyOnePathAndPreservesSQL(t *testing.T) {
	for _, statement := range []string{" \n/* x */ SELECT 1;  ", "/*! SELECT 1 */", "WITH x AS (SELECT 1) UPDATE t SET n=n+1", "INSERT INTO t VALUES(1) RETURNING id"} {
		for _, mode := range []QueryResultMode{"", QueryResultModeAuto, QueryResultModeRows, QueryResultModeExec} {
			runner := &fakeQueryRunner{rows: &fakeQueryRows{}, result: fakeQueryResult{count: 1}}
			_, err := executeQuery(context.Background(), runner, DriverMySQL, QueryRequest{SQL: statement, ResultMode: mode})
			if err != nil {
				t.Fatal(err)
			}
			wantRows := mode == QueryResultModeRows || mode != QueryResultModeExec && returnsRows(DriverMySQL, statement)
			if runner.queryCalls+runner.execCalls != 1 || (runner.queryCalls == 1) != wantRows || runner.statement != statement {
				t.Fatalf("mode %q: query=%d exec=%d SQL=%q", mode, runner.queryCalls, runner.execCalls, runner.statement)
			}
		}
	}
}

func TestQueryExecutionErrorsNeverReplay(t *testing.T) {
	for _, mode := range []QueryResultMode{QueryResultModeRows, QueryResultModeExec} {
		for _, err := range []error{errors.New("syntax error"), errors.New("constraint failed"), context.DeadlineExceeded, context.Canceled} {
			runner := &fakeQueryRunner{err: err}
			_, got := executeQuery(context.Background(), runner, DriverSQLite, QueryRequest{SQL: "UPDATE t SET n=n+1", ResultMode: mode})
			if !errors.Is(got, err) || runner.queryCalls+runner.execCalls != 1 {
				t.Fatalf("error=%v calls=%#v", got, runner)
			}
		}
	}
}

func TestQueryExecAffectedCountIsTrustworthy(t *testing.T) {
	for _, tt := range []struct {
		name  string
		count int64
		err   error
		known bool
	}{
		{"zero", 0, nil, true}, {"one", 1, nil, true}, {"many", 12, nil, true},
		{"safe maximum", maxSafeRowsAffected, nil, true}, {"unsafe", maxSafeRowsAffected + 1, nil, false},
		{"unsupported", 9, errors.New("unsupported"), false}, {"negative", -1, nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeQueryRunner{result: fakeQueryResult{count: tt.count, err: tt.err}}
			result, err := executeQuery(context.Background(), runner, DriverSQLite, QueryRequest{SQL: "UPDATE t SET n=n+1", ResultMode: QueryResultModeExec})
			if err != nil || result.RowsAffectedKnown != tt.known || result.RowsAffected != map[bool]int64{true: tt.count, false: 0}[tt.known] {
				t.Fatalf("result=%#v error=%v", result, err)
			}
			if result.Columns == nil || result.Rows == nil || result.Truncated || runner.execCalls != 1 || runner.queryCalls != 0 {
				t.Fatalf("bad result/calls: %#v %#v", result, runner)
			}
		})
	}
}

func TestQueryRowsBoundariesAndFinalization(t *testing.T) {
	for _, count := range []int{0, 499, 500, 501, 900} {
		rows := &fakeQueryRows{count: count}
		runner := &fakeQueryRunner{rows: rows}
		result, err := executeQuery(context.Background(), runner, DriverSQLite, QueryRequest{SQL: "SELECT 1"})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Rows) != min(count, 500) || result.Truncated != (count > 500) || rows.scans != min(count, 500) || rows.next != min(count, 501) || rows.closes != 1 {
			t.Fatalf("count=%d result=%#v rows=%#v", count, result, rows)
		}
		if result.RowsAffectedKnown || result.RowsAffected != 0 || result.Rows == nil {
			t.Fatalf("invented count: %#v", result)
		}
		if !reflect.DeepEqual(rows.events, []string{"close", "err"}) {
			t.Fatalf("finalization order=%v", rows.events)
		}
	}
}

func TestQueryRowsFailuresCloseAndPreservePrimaryError(t *testing.T) {
	primary, cleanup := errors.New("original failure"), errors.New("close failure")
	for _, tt := range []struct {
		name string
		rows fakeQueryRows
		want error
	}{
		{"columns", fakeQueryRows{columnsErr: primary, closeErr: cleanup}, primary},
		{"scan", fakeQueryRows{count: 1, scanErr: primary, closeErr: cleanup}, primary},
		{"next", fakeQueryRows{iterationErr: primary, closeErr: cleanup}, primary},
		{"close eof", fakeQueryRows{closeErr: cleanup}, cleanup},
		{"close truncated", fakeQueryRows{count: 501, closeErr: cleanup}, cleanup},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeQueryRunner{rows: &tt.rows}
			result, err := executeQuery(context.Background(), runner, DriverSQLite, QueryRequest{SQL: "SELECT 1"})
			if !errors.Is(err, tt.want) || tt.rows.closes != 1 || result.Rows != nil || runner.queryCalls != 1 || runner.execCalls != 0 {
				t.Fatalf("result=%#v error=%v rows=%#v runner=%#v", result, err, tt.rows, runner)
			}
			if !reflect.DeepEqual(tt.rows.events, []string{"close", "err"}) {
				t.Fatalf("finalization=%v", tt.rows.events)
			}
		})
	}
}

func TestQueryDurationIncludesCloseAndCancellationCannotSucceed(t *testing.T) {
	rows := &fakeQueryRows{onClose: func() { time.Sleep(12 * time.Millisecond) }}
	result, err := executeQuery(context.Background(), &fakeQueryRunner{rows: rows}, DriverSQLite, QueryRequest{SQL: "SELECT 1"})
	if err != nil || result.DurationMs < 10 {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rows = &fakeQueryRows{onClose: cancel}
	_, err = executeQuery(ctx, &fakeQueryRunner{rows: rows}, DriverSQLite, QueryRequest{SQL: "SELECT 1"})
	if !errors.Is(err, context.Canceled) || rows.closes != 1 {
		t.Fatalf("cancel error=%v rows=%#v", err, rows)
	}
}

func TestQueryInvalidModeMakesNoCalls(t *testing.T) {
	for _, mode := range []QueryResultMode{"invalid", "ROWS", " rows", "rows ", "Auto"} {
		runner := &fakeQueryRunner{}
		_, err := executeQuery(context.Background(), runner, DriverSQLite, QueryRequest{SQL: "SELECT 1", ResultMode: mode})
		if ErrorCodeOf(err) != CodeSQLInvalid || runner.queryCalls+runner.execCalls != 0 {
			t.Fatalf("mode=%q error=%v calls=%#v", mode, err, runner)
		}
	}
}

// database/sql 会在 EOF 时自动 Close；此夹具同时验证自动关闭和截断显式关闭。
type queryDriverFixture struct {
	count, next, closes, queryCalls, execCalls int
	closeErr, queryErr                         error
	statement                                  string
}

type queryFixtureConnector struct{ fixture *queryDriverFixture }
type queryFixtureDriver struct{ fixture *queryDriverFixture }
type queryFixtureConn struct{ fixture *queryDriverFixture }

func (c queryFixtureConnector) Connect(context.Context) (driver.Conn, error) {
	return &queryFixtureConn{c.fixture}, nil
}
func (c queryFixtureConnector) Driver() driver.Driver { return queryFixtureDriver{c.fixture} }
func (d queryFixtureDriver) Open(string) (driver.Conn, error) {
	return &queryFixtureConn{d.fixture}, nil
}
func (c *queryFixtureConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *queryFixtureConn) Close() error { return nil }
func (c *queryFixtureConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected transaction")
}
func (c *queryFixtureConn) QueryContext(_ context.Context, statement string, _ []driver.NamedValue) (driver.Rows, error) {
	c.fixture.queryCalls++
	c.fixture.statement = statement
	if c.fixture.queryErr != nil {
		return nil, c.fixture.queryErr
	}
	return c.fixture, nil
}
func (c *queryFixtureConn) ExecContext(_ context.Context, statement string, _ []driver.NamedValue) (driver.Result, error) {
	c.fixture.execCalls++
	c.fixture.statement = statement
	return driver.RowsAffected(1), nil
}
func (r *queryDriverFixture) Columns() []string { return []string{"n"} }
func (r *queryDriverFixture) Close() error      { r.closes++; return r.closeErr }
func (r *queryDriverFixture) Next(values []driver.Value) error {
	if r.next >= r.count {
		return io.EOF
	}
	r.next++
	values[0] = int64(r.next)
	return nil
}

func TestQueryRealSQLRowsPropagatesCloseErrorsWithoutFallback(t *testing.T) {
	for _, count := range []int{0, 1, 501} {
		failure := errors.New("driver close failed")
		fixture := &queryDriverFixture{count: count, closeErr: failure}
		db := sql.OpenDB(queryFixtureConnector{fixture})
		result, err := executeQuery(context.Background(), databaseQueryRunner{db}, DriverSQLite, QueryRequest{SQL: "SELECT 1"})
		db.Close()
		if !errors.Is(err, failure) || fixture.closes != 1 || fixture.queryCalls != 1 || fixture.execCalls != 0 || result.Rows != nil {
			t.Fatalf("count=%d result=%#v error=%v fixture=%#v", count, result, err, fixture)
		}
	}
	failure := errors.New("driver query failed")
	fixture := &queryDriverFixture{queryErr: failure}
	db := sql.OpenDB(queryFixtureConnector{fixture})
	defer db.Close()
	_, err := executeQuery(context.Background(), databaseQueryRunner{db}, DriverSQLite, QueryRequest{SQL: "SELECT 1"})
	if !errors.Is(err, failure) || fixture.queryCalls != 1 || fixture.execCalls != 0 {
		t.Fatalf("error=%v fixture=%#v", err, fixture)
	}
}
