package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// 仅临时 SQLite，统计请求实际调用，不在错误、预算截断或关闭后换路。
type returningSQLiteRunner struct {
	db             *sql.DB
	queries, execs int
	statement      string
}

func (r *returningSQLiteRunner) exec(ctx context.Context, statement string) (sql.Result, error) {
	r.execs++
	r.statement = statement
	return r.db.ExecContext(ctx, statement)
}
func (r *returningSQLiteRunner) query(ctx context.Context, statement string) (queryRows, error) {
	r.queries++
	r.statement = statement
	return r.db.QueryContext(ctx, statement)
}
func returningSQLiteQuery(t *testing.T, db *sql.DB, statement string, mode QueryResultMode, wantRows bool) QueryResult {
	t.Helper()
	runner := &returningSQLiteRunner{db: db}
	result, err := executeQuery(context.Background(), runner, DriverSQLite, QueryRequest{SQL: statement, ResultMode: mode})
	if err != nil {
		t.Fatal(err)
	}
	if runner.queries+runner.execs != 1 || (runner.queries == 1) != wantRows || runner.statement != statement {
		t.Fatalf("runner=%#v", runner)
	}
	return result
}

func TestSQLiteReturningAutoWritesOnce(t *testing.T) {
	for _, mode := range []QueryResultMode{"", QueryResultModeAuto, QueryResultModeRows, QueryResultModeExec} {
		t.Run(string(mode), func(t *testing.T) {
			_, _, db := newQueryFixture(t)
			for _, tt := range []struct {
				sql              string
				count, sum, rows int
			}{
				{`INSERT INTO sample(id,n,label) VALUES(1,0,'RETURNING'),(2,0,'x') RETURNING id,n`, 2, 0, 2},
				{`/*RETURNING*/ UPDATE "sample" SET n=n+1,label='it''s RETURNING;()' WHERE id=1 RETURNING id,n; --tail`, 2, 1, 1},
				{`UPDATE sample SET n=n+1 WHERE id=9 RETURNING id,n`, 2, 1, 0},
				{`DELETE FROM sample WHERE id=9 RETURNING id,n`, 2, 1, 0},
				{`DELETE FROM sample WHERE id=1 RETURNING id,n`, 1, 0, 1},
			} {
				wantRows := mode != QueryResultModeExec
				result := returningSQLiteQuery(t, db, tt.sql, mode, wantRows)
				if wantRows {
					if len(result.Columns) != 2 || len(result.Rows) != tt.rows || result.RowsAffectedKnown || result.RowsAffected != 0 {
						t.Fatalf("SQL=%s result=%#v", tt.sql, result)
					}
				} else {
					if len(result.Columns) != 0 || len(result.Rows) != 0 || !result.RowsAffectedKnown || result.RowsAffected != int64(tt.rows) {
						t.Fatalf("exec result=%#v", result)
					}
				}
				assertFixtureScalar(t, db, `SELECT count(*) FROM sample`, tt.count)
				assertFixtureScalar(t, db, `SELECT coalesce(sum(n),0) FROM sample`, tt.sum)
			}
		})
	}
}

// SQLite 自身返回列为独立 oracle；每例一张新临时表，只执行一条被测 SQL。
func TestSQLiteReturningColumnsOracle(t *testing.T) {
	for _, tt := range []struct {
		sql     string
		columns int
	}{
		{`INSERT INTO sample(id,n) VALUES(2,1),(3,2) RETURNING id,n`, 2},
		{`INSERT INTO sample DEFAULT VALUES RETURNING id`, 1},
		{`REPLACE INTO sample(id,n) VALUES(1,2) RETURNING id,n`, 2},
		{`REPLACE INTO sample(id,n) VALUES(1,2)`, 0},
		{`INSERT INTO sample(id,n) SELECT 2,n+1 FROM sample WHERE id=1 RETURNING id,n`, 2},
		{`INSERT INTO sample(id,n) VALUES(1,10) ON CONFLICT(id) DO UPDATE SET n=excluded.n RETURNING id,n`, 2},
		{`INSERT OR IGNORE INTO sample(id,n) VALUES(1,10) RETURNING id,n`, 2},
		{`UPDATE sample SET n=n+1 WHERE id=1 RETURNING n`, 1},
		{`UPDATE sample SET n=(SELECT 2),label='RETURNING' RETURNING id`, 1},
		{`UPDATE sample SET n=2 FROM (SELECT 1 AS k) x WHERE id=x.k RETURNING n`, 1},
		{`DELETE FROM sample WHERE id=1 RETURNING *`, 3},
		{`UPDATE sample SET n=1 WHERE id=9 RETURNING n`, 1},
		{`/*lead*/ UPDATE sample SET label='it''s RETURNING;()' ReTuRnInG n; --tail`, 1},
		{`UPDATE sample SET label='\' RETURNING n`, 1},
		{`UPDATE sample SET label=x'52455455524e494e47' RETURNING n`, 1},
		{`UPDATE [sample] SET [label]='RETURNING' RETURNING [label] AS [returning]`, 1},
		{"UPDATE `sample` SET `label`='RETURNING' RETURNING `label` AS `returning`", 1},
		{`UPDATE sample SET label='RETURNING'`, 0},
		{`UPDATE sample SET label=sample."label"`, 0},
		{`INSERT INTO sample(id) SELECT 2 AS "returning"`, 0},
		{`INSERT INTO sample(id) SELECT 2 AS 'returning'`, 0},
		{`INSERT INTO sample(id) SELECT (SELECT 2 AS "returning")`, 0},
		{`UPDATE sample SET n=1 /* RETURNING */`, 0},
		{"UPDATE sample SET n=1 -- ignored RETURNING\rRETURNING n", 0},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			_, _, db := newQueryFixture(t)
			if _, err := db.Exec(`INSERT INTO sample VALUES(1,0,'x')`); err != nil {
				t.Fatal(err)
			}
			rows, err := db.Query(tt.sql)
			if err != nil {
				t.Fatal(err)
			}
			columns, err := rows.Columns()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			for rows.Next() {
			}
			if err := finishResultRows(context.Background(), rows, nil); err != nil {
				t.Fatal(err)
			}
			if len(columns) != tt.columns || sqliteDMLReturning(DriverSQLite, tt.sql) != (tt.columns > 0) {
				t.Fatalf("columns=%v want=%d classifier=%v", columns, tt.columns, sqliteDMLReturning(DriverSQLite, tt.sql))
			}
		})
	}
}

func TestSQLiteReturningBareKeywordIsNotIdentifier(t *testing.T) {
	_, _, db := newQueryFixture(t)
	if _, err := db.Exec(`ALTER TABLE sample ADD COLUMN "returning" TEXT`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{`INSERT INTO sample(id) SELECT 2 AS returning`, `UPDATE sample SET label=sample.returning`, `UPDATE sample SET returning='x'`, `INSERT INTO returning VALUES(1)`} {
		prepared, err := db.Prepare(statement)
		if err == nil {
			prepared.Close()
			t.Fatalf("unexpected bare RETURNING identifier: %s", statement)
		}
		if !strings.Contains(err.Error(), `near "returning": syntax error`) {
			t.Fatalf("unexpected SQLite parse outcome: %v", err)
		}
	}
}

// 这些 SQLite 合法语句超出扫描范围；auto 回退仍会执行，rows 可显式取结果。
func TestSQLiteReturningUnknownLexicalFormsRemainExecutable(t *testing.T) {
	for _, tt := range []struct {
		sql  string
		want int
	}{
		{`UPDATE sample SET n=0x10 RETURNING n`, 16},
		{`UPDATE sample SET n=1_000 RETURNING n`, 1000},
		{`UPDATE sample SET n=n+1 RETURNING n /*unfinished`, 1},
		{`UPDATE sample SET n=n+1 RETURNING n AS 数值`, 1},
	} {
		for _, mode := range []QueryResultMode{"", QueryResultModeAuto, QueryResultModeRows, QueryResultModeExec} {
			t.Run(tt.sql+"/"+string(mode), func(t *testing.T) {
				_, _, db := newQueryFixture(t)
				if _, err := db.Exec(`INSERT INTO sample VALUES(1,0,'x')`); err != nil {
					t.Fatal(err)
				}
				wantRows := mode == QueryResultModeRows
				result := returningSQLiteQuery(t, db, tt.sql, mode, wantRows)
				if wantRows {
					if len(result.Columns) != 1 || len(result.Rows) != 1 || result.Rows[0][0] != int64(tt.want) || result.RowsAffectedKnown {
						t.Fatalf("result=%#v", result)
					}
				} else if len(result.Columns) != 0 || len(result.Rows) != 0 || !result.RowsAffectedKnown || result.RowsAffected != 1 {
					t.Fatalf("result=%#v", result)
				}
				assertFixtureScalar(t, db, `SELECT n FROM sample`, tt.want)
			})
		}
	}
}

func TestSQLiteReturningAutoDisplayLimitsDoNotLimitWrites(t *testing.T) {
	for _, count := range []int{499, 500, 501} {
		t.Run(fmt.Sprintf("rows=%d", count), func(t *testing.T) {
			_, _, db := newQueryFixture(t)
			seed := fmt.Sprintf(`WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n<%d) INSERT INTO sample SELECT n,0,'x' FROM seq`, count)
			if _, err := db.Exec(seed); err != nil {
				t.Fatal(err)
			}
			result := returningSQLiteQuery(t, db, `UPDATE sample SET n=n+1 RETURNING id,n`, QueryResultModeAuto, true)
			reason := ""
			if count > 500 {
				reason = "row_limit"
			}
			if len(result.Rows) != min(count, 500) || result.Truncated != (count > 500) || result.TruncationReason != reason || result.RowsAffectedKnown {
				t.Fatalf("result rows=%d truncated=%v reason=%s known=%v", len(result.Rows), result.Truncated, result.TruncationReason, result.RowsAffectedKnown)
			}
			assertFixtureScalar(t, db, `SELECT count(*) FROM sample WHERE n=1`, count)
			assertFixtureScalar(t, db, `SELECT sum(n) FROM sample`, count)
		})
	}
	t.Run("first row exceeds byte budget", func(t *testing.T) {
		_, _, db := newQueryFixture(t)
		if _, err := db.Exec(`INSERT INTO sample VALUES(1,0,'x')`); err != nil {
			t.Fatal(err)
		}
		result := returningSQLiteQuery(t, db, `UPDATE sample SET n=n+1,label=zeroblob(2097152) RETURNING id,n,label`, QueryResultModeAuto, true)
		if len(result.Columns) != 3 || len(result.Rows) != 0 || !result.Truncated || result.TruncationReason != "byte_limit" || result.RowsAffectedKnown {
			t.Fatalf("result=%#v", result)
		}
		assertFixtureScalar(t, db, `SELECT n FROM sample`, 1)
		assertFixtureScalar(t, db, `SELECT length(label) FROM sample`, 2097152)
	})
}
