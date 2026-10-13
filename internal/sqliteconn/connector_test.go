package sqliteconn

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
)

type fakeDriver struct {
	conn   driver.Conn
	err    error
	opened []string
	onOpen func()
}

func (d *fakeDriver) Open(dsn string) (driver.Conn, error) {
	d.opened = append(d.opened, dsn)
	if d.onOpen != nil {
		d.onOpen()
	}
	return d.conn, d.err
}

type plainConn struct{ closed int }

func (c *plainConn) Prepare(string) (driver.Stmt, error) { panic("unexpected Prepare") }
func (c *plainConn) Begin() (driver.Tx, error)           { panic("unexpected Begin") }
func (c *plainConn) Close() error                        { c.closed++; return nil }

type fakeConn struct {
	plainConn
	queries []string
	err     error
	onExec  func()
}

func (c *fakeConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	c.queries = append(c.queries, q)
	if c.onExec != nil {
		c.onExec()
	}
	return driver.RowsAffected(0), c.err
}
func (c *fakeConn) ResetSession(context.Context) error { return nil }
func (c *fakeConn) IsValid() bool                      { return true }

func TestConnectorPreservesDSNDriverAndRawConnection(t *testing.T) {
	for _, dsn := range []string{"relative.db", " leading and trailing ", "a?b#c%d\\e", "file:a%20b?mode=rwc", ":memory:", "file::memory:"} {
		t.Run(dsn, func(t *testing.T) {
			raw := &fakeConn{}
			base := &fakeDriver{conn: raw}
			c := &connector{base: base, dsn: dsn, pragmas: []string{"PRAGMA foreign_keys = ON", "PRAGMA busy_timeout = 5000"}}
			got, err := c.Connect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got != raw || c.Driver() != base {
				t.Fatal("driver or raw connection identity changed")
			}
			if !reflect.DeepEqual(base.opened, []string{dsn}) {
				t.Fatalf("DSN changed: %#v", base.opened)
			}
			if !reflect.DeepEqual(raw.queries, c.pragmas) || raw.closed != 0 {
				t.Fatal("initialization or ownership changed")
			}
			if _, ok := got.(driver.SessionResetter); !ok {
				t.Fatal("lost ResetSession")
			}
			if _, ok := got.(driver.Validator); !ok {
				t.Fatal("lost IsValid")
			}
			_ = got.Close()
			if raw.closed != 1 {
				t.Fatal("close not delegated")
			}
		})
	}
}

func TestConnectorFailureClosesOnlyOpenedConnections(t *testing.T) {
	sentinel := errors.New("fixture failure")
	for _, phase := range []string{"before_open", "open_error", "after_open", "missing_execer", "exec_error", "after_exec"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			raw := &fakeConn{}
			base := &fakeDriver{conn: raw}
			want := error(context.Canceled)
			closed := 1
			switch phase {
			case "before_open":
				cancel()
				closed = 0
			case "open_error":
				base.err = sentinel
				want = sentinel
				closed = 0
			case "after_open":
				base.onOpen = cancel
			case "missing_execer":
				base.conn = &raw.plainConn
				want = nil
			case "exec_error":
				raw.err = sentinel
				want = sentinel
			case "after_exec":
				raw.onExec = cancel
			}
			c := &connector{base: base, dsn: "do not expose", pragmas: []string{"PRAGMA foreign_keys = ON"}}
			conn, err := c.Connect(ctx)
			if err == nil || conn != nil {
				t.Fatal("failed connection escaped")
			}
			if want != nil && !errors.Is(err, want) {
				t.Fatalf("error changed: %v", err)
			}
			if raw.closed != closed {
				t.Fatalf("closed %d, want %d", raw.closed, closed)
			}
			if phase == "before_open" && len(base.opened) != 0 {
				t.Fatal("opened after cancellation")
			}
		})
	}
}

func pragma(t *testing.T, db *sql.DB, name string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("PRAGMA " + name).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s=%d, want %d", name, got, want)
	}
}

func TestOpenRegisteredDriverIsolationAndRecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "space # percent%.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	settings := []string{"PRAGMA foreign_keys = ON", "PRAGMA busy_timeout = 5000"}
	db, err := Open(path, settings...)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	settings[0] = "invalid caller mutation"
	if db.Driver() != raw.Driver() {
		t.Fatal("registered driver replaced")
	}
	db.SetMaxOpenConns(1)
	for _, stage := range []string{"initial", "recycle", "interruption"} {
		t.Run(stage, func(t *testing.T) {
			switch stage {
			case "recycle":
				db.SetMaxIdleConns(0)
				db.SetMaxIdleConns(1)
			case "interruption":
				interrupt(t, db)
			}
			pragma(t, db, "foreign_keys", 1)
			pragma(t, db, "busy_timeout", 5000)
			pragma(t, raw, "foreign_keys", 0)
			pragma(t, raw, "busy_timeout", 0)
		})
	}
}
func TestOpenMemorySurvivesInterruption(t *testing.T) {
	for _, dsn := range []string{":memory:", "file::memory:?cache=shared"} {
		t.Run(dsn, func(t *testing.T) {
			for range 2 {
				func() {
					db, err := Open(dsn, "PRAGMA foreign_keys = ON")
					if err != nil {
						t.Fatal(err)
					}
					defer db.Close()
					db.SetMaxOpenConns(1)
					if _, err := db.Exec("CREATE TABLE fixture (value INTEGER); INSERT INTO fixture VALUES (42)"); err != nil {
						t.Fatal(err)
					}
					interrupt(t, db)
					pragma(t, db, "foreign_keys", 1)
					var got int
					if err := db.QueryRow("SELECT value FROM fixture").Scan(&got); err != nil || got != 42 {
						t.Fatalf("memory lost: %d %v", got, err)
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
	sqlite.MustRegisterScalarFunction("ncp_test_sqliteconn_interrupt", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
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
  SELECT ncp_test_sqliteconn_interrupt(?)
  UNION ALL SELECT x+1 FROM n
 ) SELECT sum(x) FROM n`, id).Scan(&sum)
	if !marker.reached.Load() {
		t.Fatalf("SQLite execution marker was not reached: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected SQL-triggered cancellation: %v", err)
	}
}
