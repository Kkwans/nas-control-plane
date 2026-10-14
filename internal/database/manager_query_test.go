package database

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func newQueryFixture(t *testing.T) (*Manager, Connection, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "query.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE sample (id INTEGER PRIMARY KEY, n INTEGER, label TEXT)`); err != nil {
		t.Fatal(err)
	}
	source := Source{ID: "query-fixture", Driver: DriverSQLite, Path: path, Reachability: "host", Evidence: "host-path"}
	manager := NewManager()
	manager.discoverFn = func(context.Context) (Discovery, error) {
		return Discovery{CollectedAt: time.Now(), Sources: []Source{source}}, nil
	}
	return manager, Connection{SourceID: source.ID}, db
}

func runFixtureQuery(t *testing.T, manager *Manager, connection Connection, statement string, mode QueryResultMode) QueryResult {
	t.Helper()
	result, err := manager.QueryWithOptions(context.Background(), QueryRequest{Connection: connection, SQL: statement, ResultMode: mode})
	if err != nil {
		t.Fatalf("query error=%v SQL=%q", err, statement)
	}
	if result.Columns == nil || result.Rows == nil {
		t.Fatalf("null arrays: %#v", result)
	}
	return result
}

func assertFixtureScalar(t *testing.T, db *sql.DB, statement string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(statement).Scan(&got); err != nil || got != want {
		t.Fatalf("%s: got=%d want=%d error=%v", statement, got, want, err)
	}
}

func TestManagerQueryLeadingCommentsAndWritesExactlyOnce(t *testing.T) {
	manager, connection, db := newQueryFixture(t)
	for _, statement := range []string{"-- comment\nSELECT 42 AS answer", "/* comment */ SELECT/*adjacent*/42 AS answer", " \r\n/*a*/ --b\r\nSELECT 42 AS answer"} {
		result := runFixtureQuery(t, manager, connection, statement, "")
		if len(result.Columns) != 1 || result.Columns[0] != "answer" || len(result.Rows) != 1 || result.Rows[0][0] != int64(42) || result.RowsAffectedKnown {
			t.Fatalf("result=%#v", result)
		}
	}
	for _, statement := range []string{"-- comment\nINSERT INTO sample VALUES(1,0,'a')", "/* comment */ UPDATE sample SET n=n+1 WHERE id=1", "-- comment\nDELETE FROM sample WHERE id=1"} {
		result := runFixtureQuery(t, manager, connection, statement, QueryResultModeAuto)
		if !result.RowsAffectedKnown || result.RowsAffected != 1 || len(result.Rows) != 0 || len(result.Columns) != 0 {
			t.Fatalf("result=%#v", result)
		}
		if statement == "/* comment */ UPDATE sample SET n=n+1 WHERE id=1" {
			assertFixtureScalar(t, db, "SELECT n FROM sample WHERE id=1", 1)
		}
	}
	assertFixtureScalar(t, db, "SELECT COUNT(*) FROM sample", 0)
}

func TestManagerQueryRowsReturningWritesAndEmptyResults(t *testing.T) {
	manager, connection, db := newQueryFixture(t)
	for _, tt := range []struct {
		statement, label string
		n                int
	}{
		{`INSERT INTO sample VALUES(1,0,'inserted') RETURNING id,n,label`, "inserted", 0},
		{`UPDATE sample SET n=n+1,label='updated' WHERE id=1 RETURNING id,n,label`, "updated", 1},
		{`WITH x AS (SELECT 1) UPDATE sample SET n=n+1,label='cte' WHERE id=1 RETURNING id,n,label`, "cte", 2},
	} {
		result := runFixtureQuery(t, manager, connection, tt.statement, QueryResultModeRows)
		if len(result.Columns) != 3 || len(result.Rows) != 1 || result.Rows[0][0] != int64(1) || result.Rows[0][1] != int64(tt.n) || result.Rows[0][2] != tt.label || result.RowsAffectedKnown || result.RowsAffected != 0 {
			t.Fatalf("result=%#v", result)
		}
		assertFixtureScalar(t, db, "SELECT n FROM sample WHERE id=1", tt.n)
		assertFixtureScalar(t, db, "SELECT COUNT(*) FROM sample", 1)
	}
	empty := runFixtureQuery(t, manager, connection, `UPDATE sample SET n=n+1 WHERE id=2 RETURNING id,n`, QueryResultModeRows)
	if len(empty.Columns) != 2 || len(empty.Rows) != 0 || empty.RowsAffectedKnown {
		t.Fatalf("empty result=%#v", empty)
	}
	assertFixtureScalar(t, db, "SELECT n FROM sample WHERE id=1", 2)
	deleted := runFixtureQuery(t, manager, connection, `DELETE FROM sample WHERE id=1 RETURNING id,n`, QueryResultModeRows)
	if len(deleted.Rows) != 1 || deleted.Rows[0][1] != int64(2) || deleted.RowsAffectedKnown {
		t.Fatalf("delete result=%#v", deleted)
	}
	assertFixtureScalar(t, db, "SELECT COUNT(*) FROM sample", 0)
}

func TestManagerQueryCTEModesAndOrdinaryAutoWrites(t *testing.T) {
	manager, connection, db := newQueryFixture(t)
	if _, err := db.Exec(`INSERT INTO sample VALUES(1,0,'x')`); err != nil {
		t.Fatal(err)
	}
	selected := runFixtureQuery(t, manager, connection, `WITH x AS (SELECT 42 AS n) SELECT n FROM x`, QueryResultModeAuto)
	if len(selected.Rows) != 1 || selected.Rows[0][0] != int64(42) || selected.RowsAffectedKnown {
		t.Fatalf("select=%#v", selected)
	}
	for _, tt := range []struct {
		suffix string
		known  bool
		count  int64
		mode   QueryResultMode
		n      int
	}{
		{"WHERE id=1", true, 1, QueryResultModeExec, 1},
		{"WHERE id=2", true, 0, QueryResultModeExec, 1},
		{"WHERE id=1", true, 1, QueryResultModeAuto, 2},
		{"WHERE id=1", false, 0, QueryResultModeRows, 3},
	} {
		result := runFixtureQuery(t, manager, connection, `WITH x AS (SELECT 1) UPDATE sample SET n=n+1 `+tt.suffix, tt.mode)
		if result.RowsAffectedKnown != tt.known || result.RowsAffected != tt.count {
			t.Fatalf("mode=%s result=%#v", tt.mode, result)
		}
		assertFixtureScalar(t, db, "SELECT n FROM sample WHERE id=1", tt.n)
	}
}

func TestManagerQuery500RowDisplayLimitIsNotWriteLimit(t *testing.T) {
	manager, connection, db := newQueryFixture(t)
	for _, count := range []int{499, 500, 501} {
		statement := fmt.Sprintf(`WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n<%d) SELECT n FROM seq`, count)
		result := runFixtureQuery(t, manager, connection, statement, QueryResultModeAuto)
		if len(result.Rows) != min(count, 500) || result.Truncated != (count > 500) || result.RowsAffectedKnown {
			t.Fatalf("count=%d result rows=%d truncated=%v known=%v", count, len(result.Rows), result.Truncated, result.RowsAffectedKnown)
		}
	}
	if _, err := db.Exec(`WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n<501) INSERT INTO sample SELECT n,0,'x' FROM seq`); err != nil {
		t.Fatal(err)
	}
	result := runFixtureQuery(t, manager, connection, `UPDATE sample SET n=n+1 RETURNING id,n`, QueryResultModeRows)
	if len(result.Rows) != 500 || !result.Truncated || result.RowsAffectedKnown || result.RowsAffected != 0 {
		t.Fatalf("write result=%#v", result)
	}
	assertFixtureScalar(t, db, "SELECT COUNT(*) FROM sample WHERE n=1", 501)
	assertFixtureScalar(t, db, "SELECT SUM(n) FROM sample", 501)
}

func TestManagerQueryAutoSQLiteReturningAndUnknownFallback(t *testing.T) {
	manager, connection, db := newQueryFixture(t)
	result := runFixtureQuery(t, manager, connection, `INSERT INTO sample VALUES(1,0,'RETURNING') RETURNING id`, QueryResultModeAuto)
	if len(result.Columns) != 1 || len(result.Rows) != 1 || result.Rows[0][0] != int64(1) || result.RowsAffectedKnown {
		t.Fatalf("auto result=%#v", result)
	}
	assertFixtureScalar(t, db, "SELECT COUNT(*) FROM sample", 1)
	result = runFixtureQuery(t, manager, connection, `UPDATE /* RETURNING */ sample SET n=n+1, label='RETURNING'`, QueryResultModeAuto)
	if !result.RowsAffectedKnown || result.RowsAffected != 1 {
		t.Fatalf("auto comment result=%#v", result)
	}
	assertFixtureScalar(t, db, "SELECT n FROM sample WHERE id=1", 1)
	// 未引用非 ASCII 标识符超出分类范围，SQL 仍原样执行一次并保留旧 exec。
	result = runFixtureQuery(t, manager, connection, `UPDATE sample SET n=n+1 RETURNING n AS 数值`, QueryResultModeAuto)
	if len(result.Columns) != 0 || len(result.Rows) != 0 || !result.RowsAffectedKnown || result.RowsAffected != 1 {
		t.Fatalf("unknown auto result=%#v", result)
	}
	assertFixtureScalar(t, db, "SELECT n FROM sample WHERE id=1", 2)
}

func TestManagerQueryPartialBatchFailureDoesNotReplayOrImplyRollback(t *testing.T) {
	for _, mode := range []QueryResultMode{QueryResultModeExec, QueryResultModeRows} {
		t.Run(string(mode), func(t *testing.T) {
			manager, connection, db := newQueryFixture(t)
			_, err := manager.Query(context.Background(), QueryRequest{Connection: connection, SQL: `INSERT INTO sample VALUES(1,0,'first'); NOT_VALID_SQL`, ResultMode: mode})
			if ErrorCodeOf(err) != CodeSQLInvalid {
				t.Fatalf("error=%v", err)
			}
			assertFixtureScalar(t, db, "SELECT COUNT(*) FROM sample", 1)
		})
	}
}

func TestManagerQueryValidatesBeforeDiscovery(t *testing.T) {
	manager := NewManager()
	discoveries := 0
	manager.discoverFn = func(context.Context) (Discovery, error) { discoveries++; return Discovery{}, nil }
	for _, request := range []QueryRequest{{SQL: "SELECT 1", ResultMode: "ROWS"}, {SQL: " \n\t ", ResultMode: QueryResultModeAuto}} {
		_, err := manager.Query(context.Background(), request)
		if ErrorCodeOf(err) != CodeSQLInvalid || discoveries != 0 {
			t.Fatalf("error=%v discoveries=%d", err, discoveries)
		}
	}
}

func TestManagerQueryCTEAutoDMLCountsAndSingleExecution(t *testing.T) {
	for _, mode := range []QueryResultMode{"", QueryResultModeAuto} {
		t.Run(string(mode), func(t *testing.T) {
			manager, connection, db := newQueryFixture(t)
			for _, tt := range []struct {
				name, statement string
				count, rows, n  int
			}{
				{"insert nonzero", `WITH x(id,n,label) AS (VALUES(1,0,'RETURNING')) INSERT INTO sample SELECT * FROM x`, 1, 1, 0},
				{"insert zero", `WITH x AS (SELECT 2,0,'unused' WHERE 0) INSERT INTO sample SELECT * FROM x`, 0, 1, 0},
				{"update nonzero", `WITH RECURSIVE x(n) AS MATERIALIZED (VALUES(1)), y AS NOT MATERIALIZED (SELECT n FROM x) UPDATE sample SET n=n+(SELECT n FROM y),label='RETURNING' WHERE id=1`, 1, 1, 1},
				{"update zero", `WITH x AS (SELECT 1) UPDATE sample SET n=n+1 WHERE id=2`, 0, 1, 1},
				{"delete zero", `WITH x AS (SELECT 1) DELETE FROM sample WHERE id=2`, 0, 1, 1},
				{"delete nonzero", `WITH x AS (SELECT 1) DELETE FROM sample WHERE id=1`, 1, 0, 0},
			} {
				t.Run(tt.name, func(t *testing.T) {
					result := runFixtureQuery(t, manager, connection, tt.statement, mode)
					if !result.RowsAffectedKnown || result.RowsAffected != int64(tt.count) || len(result.Columns) != 0 || len(result.Rows) != 0 {
						t.Fatalf("result=%#v", result)
					}
					assertFixtureScalar(t, db, "SELECT COUNT(*) FROM sample", tt.rows)
					assertFixtureScalar(t, db, "SELECT COALESCE(SUM(n),0) FROM sample", tt.n)
				})
			}
		})
	}
}

func TestManagerQueryCTEAutoReturningKeepsRowsAndSingleExecution(t *testing.T) {
	for _, mode := range []QueryResultMode{"", QueryResultModeAuto} {
		t.Run(string(mode), func(t *testing.T) {
			manager, connection, db := newQueryFixture(t)
			for _, tt := range []struct {
				statement string
				rows, n   int
			}{
				{`WITH x AS (SELECT 1) INSERT INTO sample VALUES(1,0,'inserted') RETURNING id,n`, 1, 0},
				{`WITH x AS (SELECT 1) UPDATE sample SET n=n+1 WHERE id=1 RETURNING id,n`, 1, 1},
				{`WITH x AS (SELECT 1) DELETE FROM sample WHERE id=2 RETURNING id,n`, 0, 1},
			} {
				result := runFixtureQuery(t, manager, connection, tt.statement, mode)
				if result.RowsAffectedKnown || result.RowsAffected != 0 || len(result.Columns) != 2 || len(result.Rows) != tt.rows || (tt.rows > 0 && result.Rows[0][1] != int64(tt.n)) {
					t.Fatalf("result=%#v", result)
				}
				assertFixtureScalar(t, db, "SELECT n FROM sample WHERE id=1", tt.n)
				assertFixtureScalar(t, db, "SELECT COUNT(*) FROM sample", 1)
			}
			result := runFixtureQuery(t, manager, connection, `WITH x AS (SELECT 1) DELETE FROM sample WHERE id=1 RETURNING id,n`, mode)
			if len(result.Rows) != 1 || result.Rows[0][1] != int64(1) || result.RowsAffectedKnown {
				t.Fatalf("delete=%#v", result)
			}
			assertFixtureScalar(t, db, "SELECT COUNT(*) FROM sample", 0)
		})
	}
}
