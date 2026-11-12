package database

import (
	"context"
	"errors"
	"testing"
)

func TestQueryBOMLexicalBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name, sql string
		rows      bool
	}{
		{"select", "\ufeffSELECT 1", true},
		{"comments", " /*head*/\ufeff --line\n\ufeffSELECT 1", true},
		{"repeated", "\ufeff\ufeffSELECT 1", true},
		{"insert", "\ufeffINSERT INTO t VALUES(1) RETURNING n", true},
		{"inner boundary", "UPDATE \ufeff t SET n=1 \ufeff RETURNING n", true},
		{"cte exec", "\ufeffWITH \ufeff x AS (SELECT 1) UPDATE t SET n=1", false},
		{"cte rows", "\ufeffWITH x AS (SELECT 1) UPDATE t SET n=1 RETURNING n", true},
		{"literal", "UPDATE t SET s='\ufeffRETURNING'", false},
		{"quoted identifier", "UPDATE t SET \"n\ufeffRETURNING\"=1", false},
		{"quoted return", "UPDATE t SET s='\ufeff' RETURNING \"n\ufeff\"", true},
		{"bare identifier", "UPDATE t SET n\ufeffRETURNING=1", false},
		{"bare result identifier", "UPDATE t SET n=1 RETURNING n\ufeffx", false},
		{"bare cte identifier", "WITH x AS(SELECT 1) UPDATE t SET n\ufeffx=1", true},
		{"keyword suffix", "SELECT\ufeff 1", false},
		{"truncated one", "\xefSELECT 1", false},
		{"truncated two", "\xef\xbbSELECT 1", false},
		{"truncated inner", "UPDATE t SET n=1 \xef\xbb RETURNING n", false},
		{"other unicode", "\u2003SELECT 1", false},
		{"batch fallback", "\ufeffUPDATE t SET n=1 RETURNING n; SELECT 2", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := returnsRows(DriverSQLite, tt.sql); got != tt.rows {
				t.Fatalf("rows=%v want=%v SQL=%q", got, tt.rows, tt.sql)
			}
		})
	}
	for _, driver := range []Driver{DriverMySQL, DriverPostgreSQL, Driver("unknown")} {
		for _, statement := range []string{"\ufeffSELECT 1", "/*x*/\ufeffSELECT 1", "\ufeffWITH x AS(SELECT 1) SELECT * FROM x", "UPDATE t SET n=1 \ufeff RETURNING n"} {
			if returnsRows(driver, statement) {
				t.Fatalf("other dialect changed: %s %q", driver, statement)
			}
		}
	}
	// BOM 不能使合法 Unicode 标识符被拆成普通关键字。
	for _, statement := range []string{"SELECT\ufeff", "SELECT\ufeffX"} {
		if got := leadingQueryKeyword(DriverSQLite, statement); got != statement {
			t.Fatalf("keyword=%q SQL=%q", got, statement)
		}
	}
}

func TestQueryBOMModesNeverReplay(t *testing.T) {
	for _, tt := range []struct {
		sql  string
		rows bool
	}{
		{"\ufeffSELECT 1", true},
		{"\ufeffUPDATE t SET n=n+1 RETURNING n", true},
		{"\ufeffWITH x AS(SELECT 1) UPDATE t SET n=n+1", false},
		{"WITH x AS(SELECT 1) UPDATE t SET n\ufeffx=1", true},
	} {
		for _, mode := range []QueryResultMode{"", QueryResultModeAuto, QueryResultModeRows, QueryResultModeExec} {
			for _, failure := range []error{nil, errors.New("runner failure"), context.Canceled, context.DeadlineExceeded} {
				runner := &fakeQueryRunner{rows: &fakeQueryRows{count: 1}, result: fakeQueryResult{count: 1}, err: failure}
				result, err := executeQuery(context.Background(), runner, DriverSQLite, QueryRequest{SQL: tt.sql, ResultMode: mode})
				wantRows := mode == QueryResultModeRows || (mode != QueryResultModeExec && tt.rows)
				if !errors.Is(err, failure) || runner.execCalls+runner.queryCalls != 1 || (runner.queryCalls == 1) != wantRows || runner.statement != tt.sql {
					t.Fatalf("mode=%q SQL=%q result=%#v runner=%#v error=%v", mode, tt.sql, result, runner, err)
				}
				if failure == nil && result.RowsAffectedKnown == wantRows {
					t.Fatalf("count=%#v", result)
				}
			}
		}
	}
}
