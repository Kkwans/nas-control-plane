package database

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteIdentityMetadata(t *testing.T) {
	tests := []struct {
		name         string
		definition   string
		suffix       string
		generated    bool
		primaryCount int
		omittedID    any
		insertFails  bool
	}{
		{name: "ordinary", definition: `id INTEGER PRIMARY KEY, value TEXT`, generated: true, primaryCount: 1, omittedID: int64(1)},
		{name: "autoincrement", definition: `id INTEGER PRIMARY KEY AUTOINCREMENT, value TEXT`, generated: true, primaryCount: 1, omittedID: int64(1)},
		{name: "column_asc", definition: `id INTEGER PRIMARY KEY ASC, value TEXT`, generated: true, primaryCount: 1, omittedID: int64(1)},
		{name: "table_asc", definition: `id INTEGER, value TEXT, PRIMARY KEY(id ASC)`, generated: true, primaryCount: 1, omittedID: int64(1)},
		{name: "table_desc", definition: `id INTEGER, value TEXT, PRIMARY KEY(id DESC)`, generated: true, primaryCount: 1, omittedID: int64(1)},
		{name: "mixed_case", definition: `id integer PRIMARY KEY, value TEXT`, generated: true, primaryCount: 1, omittedID: int64(1)},
		{name: "unique_index", definition: `id INTEGER PRIMARY KEY UNIQUE, value TEXT UNIQUE`, generated: true, primaryCount: 1, omittedID: int64(1)},
		{name: `quoted"table's`, definition: `id INTEGER PRIMARY KEY, value TEXT`, generated: true, primaryCount: 1, omittedID: int64(1)},
		{name: "without_rowid", definition: `id INTEGER PRIMARY KEY, value TEXT`, suffix: " WITHOUT ROWID", primaryCount: 1, insertFails: true},
		{name: "without_rowid_desc", definition: `id INTEGER, value TEXT, PRIMARY KEY(id DESC)`, suffix: " WITHOUT ROWID", primaryCount: 1, insertFails: true},
		{name: "column_desc", definition: `id INTEGER PRIMARY KEY DESC, value TEXT`, primaryCount: 1},
		{name: "column_desc_not_null", definition: `id INTEGER PRIMARY KEY DESC NOT NULL, value TEXT`, primaryCount: 1, insertFails: true},
		{name: "int_primary", definition: `id INT PRIMARY KEY, value TEXT`, primaryCount: 1},
		{name: "text_primary", definition: `id TEXT PRIMARY KEY, value TEXT`, primaryCount: 1},
		{name: "composite", definition: `id INTEGER, value TEXT, PRIMARY KEY(id,value)`, primaryCount: 2},
		{name: "no_primary", definition: `id INTEGER, value TEXT`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newSQLiteIdentityFixture(t)
			table := quote(DriverSQLite, tt.name)
			if _, err := db.Exec(`CREATE TABLE ` + table + ` (` + tt.definition + `)` + tt.suffix); err != nil {
				t.Fatal(err)
			}
			if tt.name == "unique_index" {
				if _, err := db.Exec(`CREATE INDEX ordinary_index ON ` + table + `(value)`); err != nil {
					t.Fatal(err)
				}
			}
			// 限制一个连接并设置超时，防止元数据查询持有未释放的结果集。
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			columns, err := listColumns(ctx, db, DriverSQLite, "", tt.name)
			if err != nil {
				t.Fatal(err)
			}
			if len(columns) != 2 {
				t.Fatalf("columns=%#v", columns)
			}
			wantMode := "required"
			if tt.generated {
				wantMode = "server-generated"
			}
			if columns[0].Name != "id" || columns[0].WriteMode != wantMode || columns[0].Position != 1 || columns[1].WriteMode != "required" {
				t.Fatalf("columns=%#v want id write mode %q", columns, wantMode)
			}
			primaryCount := 0
			for _, column := range columns {
				if column.PrimaryKey {
					primaryCount++
				}
			}
			if primaryCount != tt.primaryCount {
				t.Fatalf("primary count=%d want=%d", primaryCount, tt.primaryCount)
			}
			// 用真实 INSERT 验证声明的生成能力，不仅比较推导出来的标志。
			_, err = db.ExecContext(ctx, `INSERT INTO `+table+` (value) VALUES ('test')`)
			if tt.insertFails {
				if err == nil {
					t.Fatal("omitting id unexpectedly succeeded")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var id any
			if err := db.QueryRowContext(ctx, `SELECT id FROM `+table).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if id != tt.omittedID {
				t.Fatalf("omitted id=%#v want=%#v", id, tt.omittedID)
			}
		})
	}
}

func TestSQLiteIdentityPreservesDefaultAndMissingTable(t *testing.T) {
	db := newSQLiteIdentityFixture(t)
	if _, err := db.Exec(`CREATE TABLE defaults (id INTEGER PRIMARY KEY DESC DEFAULT 7, value TEXT DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	columns, err := listColumns(context.Background(), db, DriverSQLite, "", "defaults")
	if err != nil || len(columns) != 2 {
		t.Fatalf("columns=%#v error=%v", columns, err)
	}
	for _, column := range columns {
		if column.WriteMode != "optional-default" {
			t.Fatalf("column=%#v", column)
		}
	}
	columns, err = listColumns(context.Background(), db, DriverSQLite, "", "missing")
	if err != nil || columns == nil || len(columns) != 0 {
		t.Fatalf("missing columns=%#v error=%v", columns, err)
	}
}

func TestSQLiteIdentityCancellation(t *testing.T) {
	db := newSQLiteIdentityFixture(t)
	if _, err := db.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	columns, err := listColumns(ctx, db, DriverSQLite, "", "items")
	if !errors.Is(err, context.Canceled) || columns != nil {
		t.Fatalf("columns=%#v error=%v", columns, err)
	}
}

func newSQLiteIdentityFixture(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "identity.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	return db
}

func TestSQLiteIdentityMetadataPragmaNameCollision(t *testing.T) {
	db := newSQLiteIdentityFixture(t)
	if _, err := db.Exec(`CREATE TABLE pragma_index_list (id INTEGER PRIMARY KEY); CREATE TABLE items (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, table := range []string{"items", "pragma_index_list"} {
		columns, err := listColumns(ctx, db, DriverSQLite, "", table)
		if err != nil || len(columns) != 1 || columns[0].WriteMode != "server-generated" {
			t.Fatalf("table=%s columns=%#v error=%v", table, columns, err)
		}
	}
}
