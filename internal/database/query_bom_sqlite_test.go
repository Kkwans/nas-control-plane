package database

import (
	"context"
	"testing"
)

// 使用项目实际驱动的隔离临时库；auto 必须保留 SQLite 已返回的列与行。
func TestSQLiteBOMAutoPreservesRows(t *testing.T) {
	for _, statement := range []string{
		"\ufeffSELECT 1",
		"\ufeffINSERT INTO sample(id,n) VALUES(1,1) RETURNING n",
	} {
		t.Run(statement, func(t *testing.T) {
			_, _, db := newQueryFixture(t)
			runner := &returningSQLiteRunner{db: db}
			result, err := executeQuery(context.Background(), runner, DriverSQLite, QueryRequest{SQL: statement})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Columns) != 1 || len(result.Rows) != 1 || result.Rows[0][0] != int64(1) || runner.queries != 1 || runner.execs != 0 || runner.statement != statement {
				t.Fatalf("result=%#v queries=%d execs=%d preserved=%v", result, runner.queries, runner.execs, runner.statement == statement)
			}
		})
	}
}

func TestSQLiteBOMModesWriteOnce(t *testing.T) {
	for _, mode := range []QueryResultMode{"", QueryResultModeAuto, QueryResultModeRows, QueryResultModeExec} {
		for _, tt := range []struct {
			sql  string
			rows bool
		}{
			{"\ufeffUPDATE sample SET n=n+1 RETURNING n", true},
			{"\ufeffWITH \ufeff x AS(SELECT 1) UPDATE sample SET n=n+1", false},
			{"WITH x AS(SELECT 1) UPDATE sample SET n=n+1 \ufeff RETURNING n", true},
			{"UPDATE \ufeff sample SET n=n+1,label='\ufeffRETURNING' RETURNING n", true},
		} {
			t.Run(string(mode)+tt.sql, func(t *testing.T) {
				_, _, db := newQueryFixture(t)
				if _, err := db.Exec(`INSERT INTO sample VALUES(1,0,'x')`); err != nil {
					t.Fatal(err)
				}
				wantRows := mode == QueryResultModeRows || (mode != QueryResultModeExec && tt.rows)
				result := returningSQLiteQuery(t, db, tt.sql, mode, wantRows)
				if wantRows && tt.rows && (len(result.Rows) != 1 || result.Rows[0][0] != int64(1)) {
					t.Fatalf("result=%#v", result)
				}
				assertFixtureScalar(t, db, "SELECT n FROM sample", 1)
			})
		}
	}
}

func TestSQLiteBOMIdentifierBytesRemainIntact(t *testing.T) {
	_, _, db := newQueryFixture(t)
	if _, err := db.Exec("ALTER TABLE sample ADD COLUMN \"n\ufeffRETURNING\" INTEGER"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sample(id,n) VALUES(1,0)`); err != nil {
		t.Fatal(err)
	}
	// SQLite 把裸标识符内 BOM 当作名称的一部分；分类器必须保守回退，不能拆成 RETURNING。
	statement := "UPDATE sample SET n\ufeffRETURNING=7"
	returningSQLiteQuery(t, db, statement, QueryResultModeAuto, false)
	assertFixtureScalar(t, db, "SELECT \"n\ufeffRETURNING\" FROM sample", 7)
	statement = "\ufeffUPDATE sample SET label='\ufeffRETURNING' RETURNING \"n\ufeffRETURNING\",label"
	result := returningSQLiteQuery(t, db, statement, QueryResultModeAuto, true)
	if len(result.Rows) != 1 || result.Rows[0][0] != int64(7) || result.Rows[0][1] != "\ufeffRETURNING" {
		t.Fatalf("result=%#v", result)
	}
}
