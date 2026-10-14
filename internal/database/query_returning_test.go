package database

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"testing"
)

func TestSQLiteDMLReturning(t *testing.T) {
	tests := []struct {
		name, sql string
		want      bool
	}{
		{"insert", `INSERT INTO t(n) VALUES(1),(2) RETURNING n`, true},
		{"update", `UPDATE t SET n=n+1 WHERE id=1 RETURNING n`, true},
		{"delete", `DELETE FROM t WHERE id=1 RETURNING *`, true},
		{"default", `INSERT INTO t DEFAULT VALUES RETURNING n`, true},
		{"select", `INSERT INTO t SELECT 1 AS n RETURNING n`, true},
		{"upsert", `INSERT INTO t VALUES(1) ON CONFLICT(n) DO UPDATE SET n=excluded.n RETURNING n`, true},
		{"or ignore", `INSERT OR IGNORE INTO t VALUES(1) RETURNING n`, true},
		{"update from", `UPDATE t SET n=x.n FROM (SELECT 1 AS n) x RETURNING n`, true},
		{"nested expression", `UPDATE t SET n=(SELECT 1),s='RETURNING' RETURNING (n+1) AS n`, true},
		{"case comments", "/*lead*/ uPdAtE/*gap*/t SET s='it''s RETURNING;()' ReTuRnInG n; --tail", true},
		{"bracket", `UPDATE [t] SET [returning]='RETURNING' RETURNING [returning]`, true},
		{"backtick", "UPDATE `t``x` SET `returning`='RETURNING' RETURNING `returning`", true},
		{"double quote", `UPDATE "t""x" SET "returning"='RETURNING' RETURNING "returning"`, true},
		{"quoted unicode", `UPDATE "表" SET "列"='值' RETURNING "列"`, true},
		{"backslash", `UPDATE t SET s='\' RETURNING n`, true},
		{"blob", `UPDATE t SET s=x'52455455524e494e47' RETURNING n`, true},
		{"line lf", "UPDATE t SET n=1 --ignored\nRETURNING n", true},
		{"line crlf", "UPDATE t SET n=1 --ignored\r\nRETURNING n", true},
		{"non nested comment", `UPDATE t SET n=1 /*outer /*inner */ RETURNING n`, true},
		{"none", `UPDATE t SET n=1`, false},
		{"literal", `UPDATE t SET s='RETURNING;()'`, false},
		{"identifier", `UPDATE "returning" SET n=1`, false},
		{"attribute", `UPDATE t SET n=t."returning"`, false},
		{"alias", `INSERT INTO t SELECT 1 AS "returning"`, false},
		{"legacy quoted alias", `INSERT INTO t SELECT 1 AS 'returning'`, false},
		{"comment", `UPDATE t SET n=1 /* RETURNING n */`, false},
		{"line cr", "UPDATE t SET n=1 --ignored\rRETURNING n", false},
		{"nested token", `UPDATE t SET n=(SELECT 1 AS returning)`, false},
		{"whole token", `UPDATE t SET returning_value=1,returning1=2,returningx=3`, false},
		{"dollar identifier", `UPDATE t SET n=1 RETURNING n$returning`, false},
		{"nonascii identifier", `UPDATE t SET n=1 RETURNING n AS 数值`, false},
		{"hex number", `UPDATE t SET n=0x10 RETURNING n`, false},
		{"numeric underscore", `UPDATE t SET n=1_000 RETURNING n`, false},
		{"colon parameter", `UPDATE t SET n=:RETURNING`, false},
		{"at parameter", `UPDATE t SET n=@RETURNING RETURNING n`, false},
		{"dollar parameter", `UPDATE t SET n=$x::a(RETURNING) RETURNING n`, false},
		{"positional parameter", `UPDATE t SET n=?1 RETURNING n`, false},
		{"batch", `UPDATE t SET n=1 RETURNING n; SELECT 2`, false},
		{"double semicolon", `UPDATE t SET n=1 RETURNING n;;`, false},
		{"nested semicolon", `UPDATE t SET n=(1;2) RETURNING n`, false},
		{"trailing token", `UPDATE t SET n=1 RETURNING n; /*tail*/ x`, false},
		{"empty tail", `UPDATE t SET n=1 RETURNING /*tail*/;`, false},
		{"duplicate", `UPDATE t SET n=1 RETURNING n RETURNING n`, false},
		{"open quote", `UPDATE t SET s='RETURNING`, false},
		{"open identifier", `UPDATE t SET n=1 RETURNING "n`, false},
		{"open comment", `UPDATE t SET n=1 RETURNING n /*`, false},
		{"open parentheses", `UPDATE t SET n=1 RETURNING (n`, false},
		{"extra parentheses", `UPDATE t SET n=1 RETURNING n)`, false},
		{"unknown punctuation", `UPDATE t SET n={1} RETURNING n`, false},
		{"executable comment", `UPDATE t SET n=1 RETURNING n /*! hidden */`, false},
		{"nul", "UPDATE t SET n=1 RETURNING n\x00", false},
		{"nul literal", "UPDATE t SET n=1 RETURNING 'n\x00'", false},
		{"nul comment", "UPDATE t SET n=1 RETURNING n --\x00", false},
		{"replace", `REPLACE INTO t VALUES(1) RETURNING n`, false},
		{"with", `WITH x AS(SELECT 1) UPDATE t SET n=1 RETURNING n`, false},
		{"explain", `EXPLAIN UPDATE t SET n=1 RETURNING n`, false},
		{"empty", ``, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, driver := range []Driver{DriverSQLite, DriverPostgreSQL, DriverMySQL, Driver("unknown")} {
				want := driver == DriverSQLite && tt.want
				if got := sqliteDMLReturning(driver, tt.sql); got != want {
					t.Fatalf("driver=%s got=%v want=%v SQL=%q", driver, got, want, tt.sql)
				}
			}
		})
	}
	// 保持其他首词与方言的分类，不能把新 helper 当成 SQL 权限校验。
	for _, tt := range []struct {
		driver Driver
		sql    string
		want   bool
	}{
		{DriverSQLite, `WITH x AS(SELECT 1) UPDATE t SET n=1`, false},
		{DriverSQLite, `WITH x AS(SELECT 1) UPDATE t SET n=1 RETURNING n`, true},
		{DriverSQLite, `REPLACE INTO t VALUES(1) RETURNING n`, false},
		{DriverSQLite, `EXPLAIN UPDATE t SET n=1 RETURNING n`, true},
		{DriverPostgreSQL, `INSERT INTO t SELECT 1 AS returning`, false},
		{DriverPostgreSQL, `UPDATE t SET n=t.returning`, false},
		{DriverPostgreSQL, `UPDATE t SET n=1 RETURNING n`, false},
		{DriverMySQL, `UPDATE t SET n=1 RETURNING n`, false},
	} {
		if got := returnsRows(tt.driver, tt.sql); got != tt.want {
			t.Fatalf("old scope changed: driver=%s SQL=%q got=%v", tt.driver, tt.sql, got)
		}
	}
}

func TestSQLiteDMLReturningScanLimits(t *testing.T) {
	const base = `UPDATE t SET n=1 RETURNING n`
	for _, size := range []int{(1 << 20) - 1, 1 << 20, (1 << 20) + 1} {
		statement := base + strings.Repeat(" ", size-len(base))
		if got := sqliteDMLReturning(DriverSQLite, statement); got != (size <= 1<<20) {
			t.Fatalf("size=%d got=%v", size, got)
		}
	}
	for _, depth := range []int{255, 256, 257} {
		for _, statement := range []string{
			`UPDATE t SET n=` + strings.Repeat("(", depth) + `1` + strings.Repeat(")", depth) + ` RETURNING n`,
			`UPDATE t SET n=1 RETURNING ` + strings.Repeat("(", depth) + `n` + strings.Repeat(")", depth),
		} {
			if got := sqliteDMLReturning(DriverSQLite, statement); got != (depth <= 256) {
				t.Fatalf("depth=%d got=%v", depth, got)
			}
		}
	}
}

// 期望值独立于分类器，涵盖成功、失败与显式覆盖。原文与总调用数始终不变。
func TestQuerySQLiteReturningModesNeverReplay(t *testing.T) {
	for _, tt := range []struct {
		sql        string
		sqliteRows bool
	}{
		{" /*head*/ INSERT INTO t VALUES(1) RETURNING n; --tail", true},
		{`UPDATE t SET n=n+1 RETURNING n`, true},
		{`DELETE FROM t RETURNING n`, true},
		{`UPDATE t SET n=1 RETURNING n AS 数值`, false},
		{`UPDATE t SET n=:RETURNING`, false},
		{`UPDATE t SET s='RETURNING'`, false},
		{`UPDATE t SET n=1 RETURNING n; SELECT 2`, false},
		{`REPLACE INTO t VALUES(1) RETURNING n`, false},
		{`UPDATE t SET n=1 RETURNING n` + strings.Repeat(" ", 1<<20), false},
	} {
		for _, driver := range []Driver{DriverSQLite, DriverPostgreSQL, DriverMySQL} {
			for _, mode := range []QueryResultMode{"", QueryResultModeAuto, QueryResultModeRows, QueryResultModeExec} {
				for _, failure := range []error{nil, errors.New("runner failed"), context.Canceled, context.DeadlineExceeded} {
					rows := &fakeQueryRows{count: 1}
					runner := &fakeQueryRunner{rows: rows, result: fakeQueryResult{count: 1}, err: failure}
					result, err := executeQuery(context.Background(), runner, driver, QueryRequest{SQL: tt.sql, ResultMode: mode})
					wantRows := mode == QueryResultModeRows || (mode != QueryResultModeExec && driver == DriverSQLite && tt.sqliteRows)
					if !errors.Is(err, failure) || runner.queryCalls+runner.execCalls != 1 || (runner.queryCalls == 1) != wantRows || runner.statement != tt.sql {
						t.Fatalf("driver=%s mode=%q query=%d exec=%d error=%v want=%v", driver, mode, runner.queryCalls, runner.execCalls, err, failure)
					}
					if failure == nil && (result.RowsAffectedKnown == wantRows || rows.closes != map[bool]int{true: 1, false: 0}[wantRows]) {
						t.Fatalf("result=%#v closes=%d rows=%v", result, rows.closes, wantRows)
					}
				}
			}
		}
	}
}

func TestQuerySQLiteReturningFinalizationNeverReplays(t *testing.T) {
	failure := errors.New("rows failed")
	for _, kind := range []string{"columns", "scan", "iteration", "close", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			rows := &fakeQueryRows{count: 1}
			want := failure
			switch kind {
			case "columns":
				rows.columnsErr = failure
			case "scan":
				rows.scanErr = failure
			case "iteration":
				rows.iterationErr = failure
			case "close":
				rows.closeErr = failure
			case "cancel":
				rows.onClose = cancel
				want = context.Canceled
			}
			runner := &fakeQueryRunner{rows: rows}
			const statement = `UPDATE t SET n=n+1 RETURNING n`
			_, err := executeQuery(ctx, runner, DriverSQLite, QueryRequest{SQL: statement})
			if !errors.Is(err, want) || runner.queryCalls != 1 || runner.execCalls != 0 || runner.statement != statement || rows.closes != 1 {
				t.Fatalf("error=%v runner=%#v closes=%d", err, runner, rows.closes)
			}
		})
	}
}

func TestSQLiteDMLReturningRandomInputs(t *testing.T) {
	random := rand.New(rand.NewSource(20261014))
	for i := 0; i < 2000; i++ {
		data := make([]byte, random.Intn(2048))
		_, _ = random.Read(data)
		if sqliteDMLReturning(DriverSQLite, `UPDATE t SET n=1 RETURNING {`+string(data)) {
			t.Fatal("unknown punctuation accepted")
		}
		_ = sqliteDMLReturning(DriverSQLite, string(data))
		_ = sqliteDMLReturning(DriverSQLite, `UPDATE t SET n=1 RETURNING `+string(data))
	}
}

func FuzzSQLiteDMLReturning(f *testing.F) {
	for _, seed := range []string{`UPDATE t SET n=n+1 RETURNING n`, `INSERT INTO t SELECT 1 AS "returning"`, `UPDATE t SET n=1 RETURNING n; SELECT 2`, `DELETE FROM t RETURNING n --tail`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, statement string) {
		got := sqliteDMLReturning(DriverSQLite, statement)
		if got {
			leading := leadingQueryKeyword(DriverSQLite, statement)
			if len(statement) > 1<<20 || (leading != "INSERT" && leading != "UPDATE" && leading != "DELETE") {
				t.Fatal("classification escaped bounds")
			}
			if sqliteDMLReturning(DriverSQLite, statement+"\n; SELECT 1") {
				t.Fatal("batch accepted")
			}
		}
	})
}
