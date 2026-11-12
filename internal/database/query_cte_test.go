package database

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"testing"
)

// 这些期望由支持范围独立给出，不能由分类器反推执行路径。
func TestCTEMainDMLWithoutReturning(t *testing.T) {
	tests := []struct {
		name, statement string
		want            bool
	}{
		{"update", `WITH x AS (SELECT 1) UPDATE t SET n=n+1`, true},
		{"insert", `WITH x AS (SELECT 1) INSERT INTO t SELECT * FROM x`, true},
		{"delete", `WITH x AS (SELECT 1) DELETE FROM t WHERE id=1`, true},
		{"case and comments", " /* leading */ wItH/*x*/c/*x*/AS/*x*/(SELECT 1)\nUpDaTe t SET n=1; -- tail", true},
		{"recursive names and multiple", `WITH RECURSIVE x(a,b) AS (VALUES(1,2)), y AS (SELECT * FROM x) UPDATE t SET n=(SELECT a FROM y)`, true},
		{"materialized", `WITH x AS MATERIALIZED (SELECT 1) UPDATE t SET n=1`, true},
		{"not materialized", `WITH x AS NOT MATERIALIZED (SELECT 1) UPDATE t SET n=1`, true},
		{"quoted names", `WITH "x""y"("a""b") AS (SELECT 1) DELETE FROM "t"`, true},
		{"quoted returning", `WITH x AS (SELECT 'RETURNING') UPDATE "returning" SET n=1, s='it''s RETURNING', "RETURNING"=2`, true},
		{"comment returning", "WITH x AS (SELECT 1) UPDATE t SET n=1 /* RETURNING */ -- RETURNING\n", true},
		{"body returning", `WITH x AS (UPDATE t SET n=1 RETURNING id) DELETE FROM t WHERE id IN (SELECT id FROM x)`, true},
		{"nested returning", `WITH x AS (SELECT 1) INSERT INTO t SELECT (SELECT 1 AS returning)`, true},
		{"numeric operators", `WITH x AS (SELECT .5,1.2,1e3,1E-2) UPDATE t SET n=(n+1)*2/3%4 WHERE n>=1 AND n<>2 AND n!=3`, true},
		{"tail comments", "WITH x AS (SELECT 1) UPDATE t SET n=1; /* x */ -- end", true},
		{"returning", `WITH x AS (SELECT 1) UPDATE t SET n=n+1 RETURNING n`, false},
		{"returning insert", `WITH x AS (SELECT 1) INSERT INTO t VALUES(1) RETURNING id`, false},
		{"returning delete", `WITH x AS (SELECT 1) DELETE FROM t RETURNING id`, false},
		{"returning alias", `WITH x AS (SELECT 1) INSERT INTO t SELECT 1 AS returning`, false},
		{"returning attribute", `WITH x AS (SELECT 1) INSERT INTO t SELECT x.returning FROM x`, false},
		{"select", `WITH x AS (SELECT 1) SELECT * FROM x`, false},
		{"merge", `WITH x AS (SELECT 1) MERGE INTO t USING x ON true WHEN MATCHED THEN DELETE`, false},
		{"search", `WITH RECURSIVE x AS (SELECT 1) SEARCH DEPTH FIRST BY id SET ord UPDATE t SET n=1`, false},
		{"cycle", `WITH RECURSIVE x AS (SELECT 1) CYCLE id SET cycle USING path DELETE FROM t`, false},
		{"direct dml", `UPDATE t SET n=1`, false},
		{"empty", ``, false},
		{"just with", `WITH`, false},
		{"no name", `WITH AS (SELECT 1) UPDATE t SET n=1`, false},
		{"no as", `WITH x (SELECT 1) UPDATE t SET n=1`, false},
		{"empty columns", `WITH x() AS (SELECT 1) UPDATE t SET n=1`, false},
		{"trailing column comma", `WITH x(a,) AS (SELECT 1) UPDATE t SET n=1`, false},
		{"missing column comma", `WITH x(a b) AS (SELECT 1) UPDATE t SET n=1`, false},
		{"empty body", `WITH x AS () UPDATE t SET n=1`, false},
		{"comment body", `WITH x AS (/* empty */) UPDATE t SET n=1`, false},
		{"bad materialization", `WITH x AS NOT (SELECT 1) UPDATE t SET n=1`, false},
		{"duplicate materialization", `WITH x AS MATERIALIZED MATERIALIZED (SELECT 1) UPDATE t SET n=1`, false},
		{"no main", `WITH x AS (SELECT 1)`, false},
		{"main keyword only", `WITH x AS (SELECT 1) UPDATE`, false},
		{"trailing cte comma", `WITH x AS (SELECT 1), UPDATE t SET n=1`, false},
		{"unclosed body", `WITH x AS (SELECT 1 UPDATE t SET n=1`, false},
		{"unclosed expression", `WITH x AS (SELECT 1) UPDATE t SET n=(1`, false},
		{"extra close", `WITH x AS (SELECT 1) UPDATE t SET n=1)`, false},
		{"unclosed literal", `WITH x AS (SELECT 1) UPDATE t SET s='x`, false},
		{"unclosed identifier", `WITH x AS (SELECT 1) UPDATE "t SET n=1`, false},
		{"unclosed comment", `WITH x AS (SELECT 1) UPDATE t SET n=1 /*`, false},
		{"body semicolon", `WITH x AS (SELECT 1;) UPDATE t SET n=1`, false},
		{"expression semicolon", `WITH x AS (SELECT 1) UPDATE t SET n=(1;)`, false},
		{"double semicolon", `WITH x AS (SELECT 1) UPDATE t SET n=1;;`, false},
		{"second statement", `WITH x AS (SELECT 1) UPDATE t SET n=1; SELECT 1`, false},
		{"tail token", `WITH x AS (SELECT 1) UPDATE t SET n=1; /* tail */ x`, false},
		{"executable comment", `WITH x AS (SELECT 1) UPDATE t SET n=1 /*! RETURNING n */`, false},
		{"maria comment", `WITH x AS (SELECT 1) UPDATE t SET n=1 /*M! RETURNING n */`, false},
		{"maria lowercase comment", `WITH x AS (SELECT 1) UPDATE t SET n=1 /*m! RETURNING n */`, false},
		{"unknown punctuation", `WITH x AS (SELECT 1) UPDATE t SET n={1}`, false},
		{"nonascii bare name", `WITH 名 AS (SELECT 1) UPDATE t SET n=1`, false},
		{"nonascii bare main", `WITH x AS (SELECT 1) UPDATE t SET 名=1`, false},
		{"nul", "WITH x AS (SELECT 1) UPDATE t SET n=1\x00", false},
		{"nul literal", "WITH x AS (SELECT 1) UPDATE t SET s='x\x00y'", false},
		{"nul identifier", "WITH x AS (SELECT 1) UPDATE \"x\x00y\" SET n=1", false},
		{"nul block comment", "WITH x AS (SELECT 1) UPDATE t SET n=1 /* x\x00y */", false},
		{"nul line comment", "WITH x AS (SELECT 1) UPDATE t SET n=1 -- x\x00y", false},
		{"returning whole token", `WITH x AS (SELECT 1) UPDATE t SET returning_value=1, returning1=2`, true},
		{"quoted unicode", `WITH "名" AS (SELECT '中文') UPDATE "表" SET "列"=1`, true},
		{"single quoted cte name", `WITH 'x' AS (SELECT 1) UPDATE t SET n=1`, false},
		{"dollar", `WITH x AS (SELECT $$RETURNING$$) UPDATE t SET n=1`, false},
		{"numeric junk", `WITH x AS (SELECT 1) UPDATE t SET n=1returning`, false},
	}
	for _, driver := range []Driver{DriverSQLite, DriverPostgreSQL} {
		for _, tt := range tests {
			t.Run(string(driver)+"/"+tt.name, func(t *testing.T) {
				if got := cteMainDMLWithoutReturning(driver, tt.statement); got != tt.want {
					t.Fatalf("classification=%v, want=%v SQL=%q", got, tt.want, tt.statement)
				}
			})
		}
	}
}

func TestCTEMainDMLDialectBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name, statement string
		sqlite, pg      bool
	}{
		{"backticks", "WITH `x``y`(`a`) AS (SELECT 1) UPDATE `t` SET n=1", true, false},
		{"brackets", `WITH [x](a) AS (SELECT 1) UPDATE [t] SET [returning]=1`, true, false},
		{"bracket first close", `WITH x AS (SELECT 1) UPDATE [t]] SET n=1`, false, false},
		{"unclosed backtick", "WITH x AS (SELECT 1) UPDATE `t SET n=1", false, false},
		{"unclosed bracket", `WITH x AS (SELECT 1) UPDATE [t SET n=1`, false, false},
		{"nested comment", `WITH x AS (SELECT 1) UPDATE t SET n=1 /* outer /* inner */ RETURNING n */`, false, true},
		{"nested unclosed comment", `WITH x AS (SELECT 1) UPDATE t SET n=1 /* outer /* inner */`, true, false},
		{"line cr", "WITH x AS (SELECT 1) UPDATE t SET n=1 --comment\rRETURNING n", true, false},
		{"line crlf", "WITH x AS (SELECT 1) UPDATE t SET n=1 --comment\r\nRETURNING n", false, false},
		{"leading line cr", "-- comment\rWITH x AS (SELECT 1) UPDATE t SET n=1", false, true},
		{"single backslash", `WITH x AS (SELECT 1) UPDATE t SET s='back\slash'`, true, false},
		{"double quoted backslash", `WITH x AS (SELECT 1) UPDATE "back\slash" SET n=1`, true, true},
		{"pg E prefix", `WITH x AS (SELECT E'plain') UPDATE t SET n=1`, true, false},
		{"pg e prefix", `WITH x AS (SELECT e'plain') UPDATE t SET n=1`, true, false},
		{"pg B prefix", `WITH x AS (SELECT B'01') UPDATE t SET n=1`, true, false},
		{"pg X prefix", `WITH x AS (SELECT X'01') UPDATE t SET n=1`, true, false},
		{"pg N prefix", `WITH x AS (SELECT N'plain') UPDATE t SET n=1`, true, false},
		{"pg unicode string", `WITH x AS (SELECT U&'plain') UPDATE t SET n=1`, true, false},
		{"pg unicode identifier", `WITH U&"x" AS (SELECT 1) UPDATE t SET n=1`, false, false},
		{"pg cast", `WITH x AS (SELECT 1::int) UPDATE t SET n=1`, false, true},
		{"positional parameter", `WITH x AS (SELECT 1) UPDATE t SET n=?1`, false, false},
		{"named colon parameter", `WITH x AS (SELECT 1) UPDATE t SET n=:value`, false, false},
		{"named at parameter", `WITH x AS (SELECT 1) UPDATE t SET n=@value`, false, false},
		{"named dollar parameter", `WITH x AS (SELECT 1) UPDATE t SET n=$value`, false, false},
		{"pg positional parameter", `WITH x AS (SELECT 1) UPDATE t SET n=$1`, false, false},
		{"pg tagged dollar string", `WITH x AS (SELECT $tag$RETURNING$tag$) UPDATE t SET n=1`, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, d := range []struct {
				driver Driver
				want   bool
			}{{DriverSQLite, tt.sqlite}, {DriverPostgreSQL, tt.pg}} {
				if got := cteMainDMLWithoutReturning(d.driver, tt.statement); got != d.want {
					t.Fatalf("driver=%s got=%v want=%v SQL=%q", d.driver, got, d.want, tt.statement)
				}
			}
		})
	}
	for _, driver := range []Driver{DriverMySQL, Driver("unknown")} {
		if cteMainDMLWithoutReturning(driver, `WITH x AS (SELECT 1) UPDATE t SET n=1`) {
			t.Fatalf("unsupported driver %s classified", driver)
		}
	}
}

func TestCTEMainDMLScanLimits(t *testing.T) {
	const base = `WITH x AS (SELECT 1) UPDATE t SET n=1`
	for _, driver := range []Driver{DriverSQLite, DriverPostgreSQL} {
		for _, size := range []int{(1 << 20) - 1, 1 << 20, (1 << 20) + 1} {
			statement := base + strings.Repeat(" ", size-len(base))
			if got := cteMainDMLWithoutReturning(driver, statement); got != (size <= 1<<20) {
				t.Fatalf("driver=%s size=%d got=%v", driver, size, got)
			}
		}
		for _, depth := range []int{255, 256, 257} {
			body := "WITH x AS " + strings.Repeat("(", depth) + "SELECT 1" + strings.Repeat(")", depth) + " UPDATE t SET n=1"
			main := "WITH x AS (SELECT 1) UPDATE t SET n=" + strings.Repeat("(", depth) + "1" + strings.Repeat(")", depth)
			for _, statement := range []string{body, main} {
				if got := cteMainDMLWithoutReturning(driver, statement); got != (depth <= 256) {
					t.Fatalf("driver=%s depth=%d got=%v", driver, depth, got)
				}
			}
			comment := base + strings.Repeat("/*", depth) + "x" + strings.Repeat("*/", depth)
			if got := cteMainDMLWithoutReturning(DriverPostgreSQL, comment); got != (depth <= 256) {
				t.Fatalf("comment depth=%d got=%v", depth, got)
			}
		}
	}
	// 旧前导扫描不受新限额影响，WITH 只会回退原来的 rows。
	large := "/*" + strings.Repeat("x", 1<<20) + "*/ WITH x AS (SELECT 1) UPDATE t SET n=1"
	if leadingQueryKeyword(DriverSQLite, large) != "WITH" || !returnsRows(DriverSQLite, large) {
		t.Fatal("large SQL changed legacy leading classification")
	}
}

func TestCTEMainDMLRandomInputs(t *testing.T) {
	random := rand.New(rand.NewSource(20261014))
	const prefix = `WITH x AS (SELECT 1) UPDATE t SET n=`
	for i := 0; i < 2000; i++ {
		input := make([]byte, random.Intn(2048))
		_, _ = random.Read(input)
		for _, driver := range []Driver{DriverSQLite, DriverPostgreSQL} {
			// 任意尾部前的未知标点必须使整个分类保守回退。
			if cteMainDMLWithoutReturning(driver, prefix+"{"+string(input)) {
				t.Fatalf("unknown input accepted: %q", input)
			}
			_ = cteMainDMLWithoutReturning(driver, string(input))
			_ = cteMainDMLWithoutReturning(driver, prefix+string(input))
		}
	}
}

func FuzzCTEMainDMLWithoutReturning(f *testing.F) {
	for _, seed := range []string{`WITH x AS (SELECT 1) UPDATE t SET n=1`, `WITH x AS (SELECT 'x') DELETE FROM t RETURNING n`, `WITH x AS (SELECT 1) UPDATE t SET n=1; SELECT 2`, `WITH x AS (/* nested /* comment */) UPDATE t SET n=1`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, statement string) {
		for _, driver := range []Driver{DriverSQLite, DriverPostgreSQL} {
			got := cteMainDMLWithoutReturning(driver, statement)
			if got && (len(statement) > 1<<20 || leadingQueryKeyword(driver, statement) != "WITH") {
				t.Fatal("classification escaped driver/size/leading bounds")
			}
			if got && cteMainDMLWithoutReturning(driver, statement+"; SELECT 1") {
				// EOF 行注释可吞掉追加的文字，用换行明确结束注释再检查。
				if cteMainDMLWithoutReturning(driver, statement+"\n; SELECT 1") {
					t.Fatal("multiple statements accepted")
				}
			}
		}
	})
}

func TestQueryCTEModesErrorsAndUnknownCountsNeverReplay(t *testing.T) {
	for _, tt := range []struct {
		name, statement string
		driver          Driver
		autoRows        bool
	}{
		{"sqlite supported", " /* head */ WITH x AS (SELECT 1) UPDATE t SET n=n+1; -- end", DriverSQLite, false},
		{"pg supported", "-- head\rWITH x AS (SELECT 1) DELETE FROM t", DriverPostgreSQL, false},
		{"with returning", `WITH x AS (SELECT 1) UPDATE t SET n=n+1 RETURNING n`, DriverSQLite, true},
		{"pg alias", `WITH x AS (SELECT 1) INSERT INTO t SELECT 1 AS returning`, DriverPostgreSQL, true},
		{"pg unknown", `WITH x AS (SELECT E'plain') UPDATE t SET n=n+1`, DriverPostgreSQL, true},
		{"mysql unchanged", `WITH x AS (SELECT 1) UPDATE t SET n=n+1`, DriverMySQL, true},
		{"multiple statements", `WITH x AS (SELECT 1) UPDATE t SET n=n+1; SELECT 1`, DriverSQLite, true},
		{"sqlite direct returning", `UPDATE t SET n=n+1 RETURNING n`, DriverSQLite, true},
		{"pg direct returning unchanged", `UPDATE t SET n=n+1 RETURNING n`, DriverPostgreSQL, false},
	} {
		for _, mode := range []QueryResultMode{"", QueryResultModeAuto, QueryResultModeRows, QueryResultModeExec} {
			for _, outcome := range []struct {
				name string
				err  error
			}{{"success", nil}, {"failure", errors.New("syntax failure")}, {"deadline", context.DeadlineExceeded}, {"canceled", context.Canceled}} {
				t.Run(tt.name+"/"+string(mode)+"/"+outcome.name, func(t *testing.T) {
					runner := &fakeQueryRunner{rows: &fakeQueryRows{}, result: fakeQueryResult{count: 2}, err: outcome.err}
					result, err := executeQuery(context.Background(), runner, tt.driver, QueryRequest{SQL: tt.statement, ResultMode: mode})
					wantRows := mode == QueryResultModeRows || (mode != QueryResultModeExec && tt.autoRows)
					if !errors.Is(err, outcome.err) || runner.queryCalls+runner.execCalls != 1 || (runner.queryCalls == 1) != wantRows || runner.statement != tt.statement {
						t.Fatalf("result=%#v err=%v runner=%#v wantRows=%v", result, err, runner, wantRows)
					}
					if err == nil && (result.RowsAffectedKnown == wantRows || (!wantRows && result.RowsAffected != 2)) {
						t.Fatalf("unexpected affected count: %#v", result)
					}
				})
			}
		}
	}
	for _, result := range []fakeQueryResult{{count: -1}, {count: 1, err: errors.New("unavailable")}, {count: maxSafeRowsAffected + 1}} {
		for _, driver := range []Driver{DriverSQLite, DriverPostgreSQL} {
			runner := &fakeQueryRunner{result: result}
			got, err := executeQuery(context.Background(), runner, driver, QueryRequest{SQL: `WITH x AS (SELECT 1) UPDATE t SET n=n+1`})
			if err != nil || got.RowsAffectedKnown || got.RowsAffected != 0 || runner.execCalls != 1 || runner.queryCalls != 0 {
				t.Fatalf("unknown count replayed: result=%#v err=%v runner=%#v", got, err, runner)
			}
		}
	}
}

func TestQueryCTERowsFinalizationNeverReplays(t *testing.T) {
	const statement = `WITH x AS (SELECT 1) UPDATE t SET n=n+1 RETURNING n`
	for _, driver := range []Driver{DriverSQLite, DriverPostgreSQL} {
		for _, failure := range []string{"close", "iteration", "cancel", "byte limit", "byte limit close"} {
			t.Run(string(driver)+"/"+failure, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				wanted := errors.New("result finalization failed")
				rows := &budgetRows{columns: []string{"n"}, values: [][]any{{int64(1)}}}
				switch failure {
				case "close":
					rows.closeErr = wanted
				case "iteration":
					rows.iterationErr = wanted
				case "cancel":
					rows.onClose = cancel
					wanted = context.Canceled
				case "byte limit", "byte limit close":
					rows.values[0][0] = strings.Repeat("x", 1<<20)
					if failure == "byte limit close" {
						rows.closeErr = wanted
					} else {
						wanted = nil
					}
				}
				runner := &fakeQueryRunner{rows: rows}
				result, err := executeQuery(ctx, runner, driver, QueryRequest{SQL: statement})
				if !errors.Is(err, wanted) || runner.execCalls != 0 || runner.queryCalls != 1 || runner.statement != statement || rows.closes != 1 {
					t.Fatalf("result=%#v error=%v wanted=%v runner=%#v closes=%d", result, err, wanted, runner, rows.closes)
				}
				if wanted == nil && (!result.Truncated || result.TruncationReason != "byte_limit" || result.RowsAffectedKnown || len(result.Rows) != 0) {
					t.Fatalf("byte truncation=%#v", result)
				}
			})
		}
	}
}

func TestCTEReplaceDialectBoundary(t *testing.T) {
	for _, driver := range []Driver{DriverSQLite, DriverPostgreSQL, DriverMySQL} {
		for _, tt := range []struct {
			statement        string
			withoutReturning bool
		}{
			{`WITH x AS (SELECT 1) REPLACE INTO t VALUES(1)`, true},
			{`WITH x AS (SELECT 1) REPLACE INTO t VALUES('RETURNING') /*RETURNING*/`, true},
			{`WITH x AS (SELECT 1) REPLACE INTO t VALUES(1) RETURNING id`, false},
			{`WITH x AS (SELECT 1) REPLACE INTO t VALUES(1); SELECT 2`, false},
			{`WITH x AS (SELECT 1) REPLACE INTO t VALUES(1) {unknown}`, false},
		} {
			want := driver == DriverSQLite && tt.withoutReturning
			if got := cteMainDMLWithoutReturning(driver, tt.statement); got != want {
				t.Fatalf("driver=%s got=%v want=%v SQL=%q", driver, got, want, tt.statement)
			}
			if got := returnsRows(driver, tt.statement); got != !want {
				t.Fatalf("driver=%s rows=%v want=%v SQL=%q", driver, got, !want, tt.statement)
			}
		}
	}
}
