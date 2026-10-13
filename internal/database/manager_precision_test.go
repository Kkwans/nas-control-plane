package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestManagerMutationsPreserveExactStringParameters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "precision.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// TEXT 列只验证参数文本无损，不把 SQLite NUMERIC/REAL 当作任意精度小数。
	if _, err := db.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, exact_text TEXT, enabled BOOLEAN, optional TEXT);
		INSERT INTO items VALUES (9007199254740992, 'adjacent', 0, 'keep')`); err != nil {
		t.Fatal(err)
	}
	source := Source{ID: "precision-fixture", Driver: DriverSQLite, Path: path, Reachability: "host", Evidence: "host-path"}
	manager := NewManager()
	manager.discoverFn = func(context.Context) (Discovery, error) {
		return Discovery{CollectedAt: time.Now(), Sources: []Source{source}}, nil
	}
	connection := Connection{SourceID: source.ID}
	ctx := context.Background()
	checkMutation := func(result MutationResult, err error) {
		t.Helper()
		if err != nil || result.RowsAffected != 1 {
			t.Fatalf("mutation = %#v, error = %v", result, err)
		}
	}
	checkAdjacent := func() {
		t.Helper()
		var text, optional string
		var enabled bool
		if err := db.QueryRow(`SELECT exact_text, enabled, optional FROM items WHERE id = 9007199254740992`).Scan(&text, &enabled, &optional); err != nil {
			t.Fatal(err)
		}
		if text != "adjacent" || enabled || optional != "keep" {
			t.Fatalf("adjacent row was changed: %q, %v, %q", text, enabled, optional)
		}
	}
	checkRow := func(id int64, text string) {
		t.Helper()
		var actualID int64
		var actualText string
		var enabled bool
		var optional any
		if err := db.QueryRow(`SELECT id, exact_text, enabled, optional FROM items WHERE id = ?`, id).Scan(&actualID, &actualText, &enabled, &optional); err != nil {
			t.Fatal(err)
		}
		if actualID != id || actualText != text || !enabled || optional != nil {
			t.Fatalf("row = %d, %q, %v, %#v", actualID, actualText, enabled, optional)
		}
		checkAdjacent()
	}

	const decimal = "1.234567890123456789002500"
	checkMutation(manager.Insert(ctx, InsertRequest{
		Connection: connection, Table: "items",
		Values: map[string]any{"id": "9007199254740993", "exact_text": decimal, "enabled": true, "optional": nil},
	}))
	checkRow(9007199254740993, decimal)

	const exponent = "1.234500e+40"
	checkMutation(manager.Update(ctx, UpdateRequest{
		InsertRequest: InsertRequest{Connection: connection, Table: "items", Values: map[string]any{"exact_text": exponent}},
		Keys:          map[string]any{"id": "9007199254740993"},
	}))
	checkRow(9007199254740993, exponent)

	checkMutation(manager.Update(ctx, UpdateRequest{
		InsertRequest: InsertRequest{Connection: connection, Table: "items", Values: map[string]any{"id": "9007199254740995", "exact_text": decimal}},
		Keys:          map[string]any{"id": "9007199254740993"},
	}))
	checkRow(9007199254740995, decimal)
	var oldKeyCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM items WHERE id = 9007199254740993`).Scan(&oldKeyCount); err != nil || oldKeyCount != 0 {
		t.Fatalf("old key count = %d, error = %v", oldKeyCount, err)
	}

	page, err := manager.Rows(ctx, RowsRequest{Connection: connection, Table: "items"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 2 || page.Rows[0]["id"] != "9007199254740992" || page.Rows[1]["id"] != "9007199254740995" || page.Rows[1]["exact_text"] != decimal {
		t.Fatalf("rows did not round-trip exact values: %#v", page.Rows)
	}
	checkMutation(manager.Delete(ctx, DeleteRequest{Connection: connection, Table: "items", Keys: map[string]any{"id": "9007199254740995"}}))
	checkAdjacent()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("remaining rows = %d, error = %v", count, err)
	}

	// 普通小整数、bool 与 NULL 使用相同参数路径。
	checkMutation(manager.Insert(ctx, InsertRequest{
		Connection: connection, Table: "items",
		Values: map[string]any{"id": "42", "exact_text": "small", "enabled": true, "optional": nil},
	}))
	checkRow(42, "small")
	checkMutation(manager.Delete(ctx, DeleteRequest{Connection: connection, Table: "items", Keys: map[string]any{"id": "42"}}))
	checkAdjacent()
}
