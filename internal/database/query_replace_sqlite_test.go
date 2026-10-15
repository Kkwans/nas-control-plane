package database

import "testing"

func TestSQLiteReplaceReturningAutoWritesOnce(t *testing.T) {
	for _, prefix := range []string{"/*leading*/ ", "WITH x AS (SELECT 1) "} {
		for _, mode := range []QueryResultMode{"", QueryResultModeAuto, QueryResultModeRows, QueryResultModeExec} {
			t.Run(prefix+string(mode), func(t *testing.T) {
				_, _, db := newQueryFixture(t)
				if _, err := db.Exec(`INSERT INTO sample VALUES(1,0,'old'); CREATE TABLE audit(n INTEGER); CREATE TRIGGER count_insert AFTER INSERT ON sample BEGIN INSERT INTO audit VALUES(new.n); END`); err != nil {
					t.Fatal(err)
				}
				statement := prefix + "RePlAcE INTO [sample](id,n,label) VALUES(1,(SELECT n+1 FROM sample WHERE id=1),'it''s RETURNING;()') RETURNING id,n; --tail"
				result := returningSQLiteQuery(t, db, statement, mode, mode != QueryResultModeExec)
				if mode != QueryResultModeExec {
					if len(result.Columns) != 2 || len(result.Rows) != 1 || result.Rows[0][0] != int64(1) || result.Rows[0][1] != int64(1) || result.RowsAffectedKnown {
						t.Fatalf("result=%#v", result)
					}
				} else if len(result.Rows) != 0 || !result.RowsAffectedKnown || result.RowsAffected != 1 {
					t.Fatalf("result=%#v", result)
				}
				assertFixtureScalar(t, db, `SELECT n FROM sample WHERE id=1`, 1)
				assertFixtureScalar(t, db, `SELECT count(*) FROM audit`, 1)
			})
		}
	}
}

func TestSQLiteCTEReplaceWithoutReturningWritesOnce(t *testing.T) {
	for _, mode := range []QueryResultMode{"", QueryResultModeAuto, QueryResultModeRows, QueryResultModeExec} {
		t.Run(string(mode), func(t *testing.T) {
			_, _, db := newQueryFixture(t)
			if _, err := db.Exec(`INSERT INTO sample VALUES(1,0,'old'); CREATE TABLE audit(n INTEGER); CREATE TRIGGER count_insert AFTER INSERT ON sample BEGIN INSERT INTO audit VALUES(new.n); END`); err != nil {
				t.Fatal(err)
			}
			statement := "/*leading*/ WITH x(n) AS (SELECT n+1 FROM sample WHERE id=1) RePlAcE INTO sample(id,n,label) SELECT 1,n,'RETURNING' FROM x; --tail"
			result := returningSQLiteQuery(t, db, statement, mode, mode == QueryResultModeRows)
			if len(result.Columns) != 0 || len(result.Rows) != 0 || result.RowsAffectedKnown != (mode != QueryResultModeRows) {
				t.Fatalf("result=%#v", result)
			}
			if mode != QueryResultModeRows && result.RowsAffected != 1 {
				t.Fatalf("affected=%d", result.RowsAffected)
			}
			assertFixtureScalar(t, db, `SELECT n FROM sample WHERE id=1`, 1)
			assertFixtureScalar(t, db, `SELECT count(*) FROM audit`, 1)
		})
	}
}
