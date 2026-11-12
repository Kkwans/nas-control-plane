package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/auth"
	"github.com/Kkwans/nas-control-plane/internal/controlstore"

	"modernc.org/sqlite"
)

func TestOwnedSQLiteConnectionSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned.db")
	store, err := OpenSQLiteCredentialStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.database
	check := func(t *testing.T) {
		t.Helper()
		for name, want := range map[string]int{"foreign_keys": 0, "busy_timeout": 5000} {
			var got int
			if err := db.QueryRow("PRAGMA " + name).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("%s=%d want %d", name, got, want)
			}
		}
	}
	identity := func() any {
		conn, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		var raw any
		if err := conn.Raw(func(c any) error { raw = c; return nil }); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	previous := identity()
	for _, stage := range []string{"initial", "recycle", "interruption"} {
		t.Run(stage, func(t *testing.T) {
			switch stage {
			case "recycle":
				db.SetMaxIdleConns(0)
				db.SetMaxIdleConns(1)
			case "interruption":
				interrupt(t, db)
			}
			check(t)
			current := identity()
			if stage != "initial" && current == previous {
				t.Fatal("physical connection was not replaced")
			}
			previous = current

		})
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE connection_fixture (value INTEGER); INSERT INTO connection_fixture VALUES (42)"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSQLiteCredentialStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var got int
	if err := reopened.database.QueryRow("SELECT value FROM connection_fixture").Scan(&got); err != nil || got != 42 {
		t.Fatalf("existing data lost: %d %v", got, err)
	}
}

func TestCredentialExternalConnectionOwnership(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "external.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	store, err := NewSQLiteCredentialStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var busy int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil || busy != 5000 {
		t.Fatalf("migration behavior changed: %d %v", busy, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("adapter closed external database: %v", err)
	}
	db.SetMaxIdleConns(0)
	db.SetMaxIdleConns(1)
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil || busy != 0 {
		t.Fatalf("external pool reconfigured: %d %v", busy, err)
	}
}

func TestOwnedSQLiteStoresShareExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	a, err := auth.Open(path, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	c, err := controlstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	d, err := OpenSQLiteCredentialStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, table := range []string{"users", "sessions", "user_preferences", "ncp_database_credentials"} {
		var count int
		if err := d.database.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("%s missing: %v", table, err)
		}
	}
}

func TestOwnedSQLiteMemoryConnections(t *testing.T) {
	for _, path := range []string{":memory:", "file::memory:?cache=shared"} {
		t.Run(path, func(t *testing.T) {
			for range 2 {
				func() {
					store, err := OpenSQLiteCredentialStore(path)
					if err != nil {
						t.Fatal(err)
					}
					defer store.Close()
					if _, err := store.database.Exec("CREATE TABLE connection_fixture (value INTEGER); INSERT INTO connection_fixture VALUES (42)"); err != nil {
						t.Fatal(err)
					}
					interrupt(t, store.database)
					var value int
					if err := store.database.QueryRow("SELECT value FROM connection_fixture").Scan(&value); err != nil || value != 42 {
						t.Fatalf("memory data lost: %d %v", value, err)
					}
				}()
			}
		})
	}
}

// 标记函数只存在于测试二进制；取消必须发生在 SQLite 已进入执行阶段之后。
type interruptMarker struct {
	cancel  context.CancelFunc
	reached atomic.Bool
}

var interruptMarkers sync.Map
var interruptSequence atomic.Int64

func init() {
	sqlite.MustRegisterScalarFunction("ncp_test_database_interrupt", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		marker, ok := interruptMarkers.Load(args[0])
		if !ok {
			return nil, errors.New("missing test interruption marker")
		}
		m := marker.(*interruptMarker)
		m.reached.Store(true)
		m.cancel()
		return int64(1), nil
	})
}

func interrupt(t *testing.T, db *sql.DB) {
	t.Helper()
	// 此超时只限制故障等待；真正的取消由 SQL 标记函数同步触发。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id := interruptSequence.Add(1)
	marker := &interruptMarker{cancel: cancel}
	interruptMarkers.Store(id, marker)
	defer interruptMarkers.Delete(id)
	var sum int64
	err := db.QueryRowContext(ctx, `WITH RECURSIVE n(x) AS (
  SELECT ncp_test_database_interrupt(?)
  UNION ALL SELECT x+1 FROM n
 ) SELECT sum(x) FROM n`, id).Scan(&sum)
	if !marker.reached.Load() {
		t.Fatalf("SQLite execution marker was not reached: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected SQL-triggered cancellation: %v", err)
	}
}
