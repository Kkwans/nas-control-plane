package database

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

// 用 SQLite 执行真实元数据查询，模拟 information_schema 的关系及占位符。
// 不启动 MySQL/PostgreSQL，也不据此声称已验证其真实目录、权限或驱动。
func newKeyMetadataFixture(t *testing.T, driver Driver) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	generatedFields := "extra TEXT"
	if driver == DriverPostgreSQL {
		generatedFields = "is_identity TEXT, is_generated TEXT"
	}
	keyReferenceField := ""
	if driver == DriverMySQL {
		keyReferenceField = ", referenced_table_name TEXT"
	}
	for _, statement := range []string{
		`ATTACH DATABASE ':memory:' AS information_schema`,
		`CREATE TABLE information_schema.columns(table_schema TEXT, table_name TEXT, column_name TEXT, data_type TEXT, is_nullable TEXT, column_default TEXT, ordinal_position INTEGER, ` + generatedFields + `)`,
		// MySQL 的 table_constraints 没有 table_catalog；不能通过虚构该列掩盖不兼容查询。
		`CREATE TABLE information_schema.table_constraints(constraint_catalog TEXT, constraint_schema TEXT, constraint_name TEXT, table_schema TEXT, table_name TEXT, constraint_type TEXT)`,
		`CREATE TABLE information_schema.key_column_usage(constraint_catalog TEXT, constraint_schema TEXT, constraint_name TEXT, table_schema TEXT, table_name TEXT, column_name TEXT` + keyReferenceField + `)`,
	} {
		keyMetadataExec(t, db, statement)
	}
	return db
}

func keyMetadataExec(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.Exec(statement, args...); err != nil {
		t.Fatal(err)
	}
}

func keyMetadataColumn(t *testing.T, db *sql.DB, driver Driver, table, name string, position int) {
	t.Helper()
	generated := "''"
	if driver == DriverPostgreSQL {
		generated = "'NO', 'NEVER'"
	}
	keyMetadataExec(t, db, `INSERT INTO information_schema.columns VALUES('app', ?, ?, 'integer', 'NO', NULL, ?, `+generated+`)`, table, name, position)
}

func keyMetadataConstraint(t *testing.T, db *sql.DB, driver Driver, table, name, kind string, columns ...string) {
	t.Helper()
	keyMetadataExec(t, db, `INSERT INTO information_schema.table_constraints VALUES('database','app',?,'app',?,?)`, name, table, kind)
	var referenced any
	if kind == "FOREIGN KEY" {
		referenced = "parent"
	}
	for _, column := range columns {
		if driver == DriverMySQL {
			keyMetadataExec(t, db, `INSERT INTO information_schema.key_column_usage VALUES('database','app',?,'app',?,?,?)`, name, table, column, referenced)
		} else {
			keyMetadataExec(t, db, `INSERT INTO information_schema.key_column_usage VALUES('database','app',?,'app',?,?)`, name, table, column)
		}
	}
}

func TestKeyMetadataRepeatedNamesDoNotDuplicateColumns(t *testing.T) {
	for _, driver := range []Driver{DriverMySQL} {
		t.Run(string(driver), func(t *testing.T) {
			db := newKeyMetadataFixture(t, driver)
			for _, table := range []string{"first", "second", "third"} {
				keyMetadataColumn(t, db, driver, table, "id", 1)
				keyMetadataConstraint(t, db, driver, table, "PRIMARY", "PRIMARY KEY", "id")
			}
			keyMetadataColumn(t, db, driver, "first", "value", 2)
			columns, err := listColumns(context.Background(), db, driver, "app", "first")
			want := []Column{{Name: "id", DataType: "integer", PrimaryKey: true, Position: 1, WriteMode: "required"}, {Name: "value", DataType: "integer", Position: 2, WriteMode: "required"}}
			if err != nil || !reflect.DeepEqual(columns, want) {
				t.Fatalf("columns=%#v error=%v", columns, err)
			}
		})
	}
}

func TestKeyMetadataOtherTableKeyCannotAuthorizeMutation(t *testing.T) {
	for _, driver := range []Driver{DriverMySQL, DriverPostgreSQL} {
		t.Run(string(driver), func(t *testing.T) {
			db := newKeyMetadataFixture(t, driver)
			keyMetadataColumn(t, db, driver, "child", "owner_id", 1)
			constraintName := "shared_name"
			if driver == DriverMySQL {
				constraintName = "PRIMARY"
			}
			keyMetadataConstraint(t, db, driver, "parent", constraintName, "PRIMARY KEY", "id")
			keyMetadataConstraint(t, db, driver, "child", constraintName, "FOREIGN KEY", "owner_id")
			columns, err := listColumns(context.Background(), db, driver, "app", "child")
			if err != nil || len(columns) != 1 || columns[0].PrimaryKey {
				t.Fatalf("columns=%#v error=%v", columns, err)
			}
			keyMetadataExec(t, db, `CREATE TABLE child(owner_id INTEGER, label TEXT)`)
			keyMetadataExec(t, db, `INSERT INTO child VALUES(7,'first'),(7,'second')`)
			// 原缺陷会生成 owner_id = 7 的单行编辑谓词并删除两行。
			where, args, err := primaryKeyWhere(driver, Table{Columns: columns}, map[string]any{"owner_id": int64(7)}, 0)
			if err == nil {
				result, execErr := execMutation(context.Background(), db, "DELETE FROM child WHERE "+where, args)
				t.Fatalf("无主键表获得破坏性谓词 %q; affected=%d error=%v", where, result.RowsAffected, execErr)
			}
			if where != "" || len(args) != 0 {
				t.Fatalf("rejected predicate=%q args=%v", where, args)
			}
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM child`).Scan(&count); err != nil || count != 2 {
				t.Fatalf("count=%d error=%v", count, err)
			}
		})
	}
}

func TestKeyMetadataMySQLSameTableForeignKeyNameCollision(t *testing.T) {
	driver := DriverMySQL
	db := newKeyMetadataFixture(t, driver)
	keyMetadataColumn(t, db, driver, "child", "id", 1)
	keyMetadataColumn(t, db, driver, "child", "owner_id", 2)
	keyMetadataConstraint(t, db, driver, "child", "PRIMARY", "PRIMARY KEY", "id")
	keyMetadataConstraint(t, db, driver, "child", "PRIMARY", "FOREIGN KEY", "owner_id")
	columns, err := listColumns(context.Background(), db, DriverMySQL, "app", "child")
	if err != nil || len(columns) != 2 || !columns[0].PrimaryKey || columns[1].PrimaryKey {
		t.Fatalf("columns=%#v error=%v", columns, err)
	}
}

func TestKeyMetadataCompositeKeysKeepDialectPredicate(t *testing.T) {
	for _, driver := range []Driver{DriverMySQL, DriverPostgreSQL} {
		t.Run(string(driver), func(t *testing.T) {
			db := newKeyMetadataFixture(t, driver)
			keyMetadataColumn(t, db, driver, "target", `tenant"id`, 1)
			keyMetadataColumn(t, db, driver, "target", "item`id", 2)
			keyMetadataColumn(t, db, driver, "target", "value", 3)
			constraintName := "compound"
			if driver == DriverMySQL {
				constraintName = "PRIMARY"
			}
			keyMetadataConstraint(t, db, driver, "target", constraintName, "PRIMARY KEY", `tenant"id`, "item`id")
			keyMetadataConstraint(t, db, driver, "other", constraintName, "FOREIGN KEY", "value")
			columns, err := listColumns(context.Background(), db, driver, "app", "target")
			if err != nil || len(columns) != 3 || !columns[0].PrimaryKey || !columns[1].PrimaryKey || columns[2].PrimaryKey {
				t.Fatalf("columns=%#v error=%v", columns, err)
			}
			where, args, err := primaryKeyWhere(driver, Table{Columns: columns}, map[string]any{`tenant"id`: int64(3), "item`id": int64(9)}, 2)
			want := "`tenant\"id` = ? AND `item``id` = ?"
			if driver == DriverPostgreSQL {
				want = `"tenant""id" = $3 AND "item` + "`" + `id" = $4`
			}
			if err != nil || where != want || !reflect.DeepEqual(args, []any{int64(3), int64(9)}) {
				t.Fatalf("predicate=%q args=%v error=%v", where, args, err)
			}
		})
	}
}

func TestKeyMetadataUniqueAndOtherSchemaAreNotPrimaryKeys(t *testing.T) {
	for _, driver := range []Driver{DriverMySQL, DriverPostgreSQL} {
		t.Run(string(driver), func(t *testing.T) {
			db := newKeyMetadataFixture(t, driver)
			keyMetadataColumn(t, db, driver, "target", "id", 1)
			keyMetadataConstraint(t, db, driver, "target", "unique_id", "UNIQUE", "id")
			keyMetadataConstraint(t, db, driver, "target", "PRIMARY", "PRIMARY KEY", "id")
			keyMetadataExec(t, db, `UPDATE information_schema.table_constraints SET table_schema='other', constraint_schema='other' WHERE constraint_type='PRIMARY KEY'`)
			keyMetadataExec(t, db, `UPDATE information_schema.key_column_usage SET table_schema='other', constraint_schema='other' WHERE constraint_name='PRIMARY'`)
			columns, err := listColumns(context.Background(), db, driver, "app", "target")
			if err != nil || len(columns) != 1 || columns[0].PrimaryKey {
				t.Fatalf("columns=%#v error=%v", columns, err)
			}
		})
	}
}
