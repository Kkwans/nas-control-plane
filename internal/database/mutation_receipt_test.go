package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"math"
	"reflect"
	"testing"
)

// 仅使用进程内驱动；执行与读取回执分别计数，不创建连接或监听器。
type mutationReceiptFixture struct {
	result    driver.Result
	execErr   error
	execPanic any
	execCalls int
	statement string
	args      []driver.NamedValue
}
type mutationReceiptConnector struct{ fixture *mutationReceiptFixture }
type mutationReceiptDriver struct{ fixture *mutationReceiptFixture }
type mutationReceiptConn struct{ fixture *mutationReceiptFixture }

func (c mutationReceiptConnector) Connect(context.Context) (driver.Conn, error) {
	return &mutationReceiptConn{c.fixture}, nil
}
func (c mutationReceiptConnector) Driver() driver.Driver { return mutationReceiptDriver{c.fixture} }
func (d mutationReceiptDriver) Open(string) (driver.Conn, error) {
	return &mutationReceiptConn{d.fixture}, nil
}
func (c *mutationReceiptConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *mutationReceiptConn) Close() error { return nil }
func (c *mutationReceiptConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected transaction")
}
func (c *mutationReceiptConn) ExecContext(_ context.Context, statement string, args []driver.NamedValue) (driver.Result, error) {
	c.fixture.execCalls++
	if c.fixture.execPanic != nil {
		panic(c.fixture.execPanic)
	}
	c.fixture.statement, c.fixture.args = statement, append([]driver.NamedValue(nil), args...)
	return c.fixture.result, c.fixture.execErr
}

type mutationReceiptResult struct {
	count      int64
	err        error
	panicValue any
	calls      int
}

func (r *mutationReceiptResult) LastInsertId() (int64, error) { panic("unexpected LastInsertId") }
func (r *mutationReceiptResult) RowsAffected() (int64, error) {
	r.calls++
	if r.panicValue != nil {
		panic(r.panicValue)
	}
	return r.count, r.err
}

func TestMutationReceiptValidationNeverReplays(t *testing.T) {
	for _, tc := range []struct {
		name       string
		count      int64
		err        error
		panicValue any
		nilResult  bool
		typedNil   bool
		valid      bool
	}{
		{name: "zero", valid: true}, {name: "one", count: 1, valid: true}, {name: "safe maximum", count: maxSafeRowsAffected, valid: true},
		{name: "negative", count: -1}, {name: "minimum", count: math.MinInt64}, {name: "unsafe", count: maxSafeRowsAffected + 1}, {name: "maximum", count: math.MaxInt64},
		{name: "error with zero", err: errors.New("password=private; SQL=private")}, {name: "error with valid count", count: 1, err: errors.New("private")},
		{name: "panic", panicValue: "password=private"}, {name: "nil driver result", nilResult: true}, {name: "typed nil driver result", typedNil: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt := &mutationReceiptResult{count: tc.count, err: tc.err, panicValue: tc.panicValue}
			fixture := &mutationReceiptFixture{result: receipt}
			if tc.nilResult {
				fixture.result = nil
			}
			if tc.typedNil {
				fixture.result = (*mutationReceiptResult)(nil)
			}
			db := sql.OpenDB(mutationReceiptConnector{fixture})
			defer db.Close()
			// 捕获基线 panic，使全部坏回执的回归证据都能输出。
			var got MutationResult
			var err error
			var panicked any
			func() {
				defer func() { panicked = recover() }()
				got, err = execMutation(context.Background(), db, "UPDATE fixture SET value=?", []any{"exact-value"})
			}()
			if fixture.execCalls != 1 || fixture.statement != "UPDATE fixture SET value=?" || !reflect.DeepEqual(fixture.args, []driver.NamedValue{{Ordinal: 1, Value: "exact-value"}}) {
				t.Fatalf("execution changed: %#v", fixture)
			}
			if !tc.nilResult && !tc.typedNil && receipt.calls != 1 {
				t.Fatalf("receipt calls=%d", receipt.calls)
			}
			if panicked != nil {
				t.Fatalf("receipt panic escaped: %T", panicked)
			}
			if tc.valid {
				if err != nil || got.RowsAffected != tc.count {
					t.Fatalf("valid receipt=%#v err=%v", got, err)
				}
				return
			}
			if ErrorCodeOf(err) != ErrorCode("mutation_receipt_invalid") || got != (MutationResult{}) {
				t.Fatalf("invalid receipt=%#v err=%v", got, err)
			}
			if errors.Unwrap(err) != nil {
				t.Fatal("raw receipt cause retained")
			}
			mapped := MapError(context.Background(), Source{}, "update", err)
			if mapped != err {
				t.Fatal("receipt error reclassified")
			}
		})
	}
}

func TestMutationReceiptExecErrorDoesNotReadReceipt(t *testing.T) {
	failure := errors.New("execution failed")
	receipt := &mutationReceiptResult{count: 1}
	fixture := &mutationReceiptFixture{result: receipt, execErr: failure}
	db := sql.OpenDB(mutationReceiptConnector{fixture})
	defer db.Close()
	got, err := execMutation(context.Background(), db, "DELETE FROM fixture", nil)
	if err != failure || got != (MutationResult{}) || fixture.execCalls != 1 || receipt.calls != 0 {
		t.Fatalf("result=%#v err=%v calls=%d/%d", got, err, fixture.execCalls, receipt.calls)
	}
}

func TestMutationReceiptDoesNotRecoverExecutionPanic(t *testing.T) {
	receipt := &mutationReceiptResult{count: 1}
	fixture := &mutationReceiptFixture{result: receipt, execPanic: "execution panic"}
	db := sql.OpenDB(mutationReceiptConnector{fixture})
	defer db.Close()
	defer func() {
		if got := recover(); got != "execution panic" {
			t.Fatalf("execution panic changed: %v", got)
		}
		if fixture.execCalls != 1 || receipt.calls != 0 {
			t.Fatalf("calls=%d/%d", fixture.execCalls, receipt.calls)
		}
	}()
	_, _ = execMutation(context.Background(), db, "DELETE FROM fixture", nil)
}

func TestMutationReceiptNilResult(t *testing.T) {
	got, err := mutationReceipt(nil)
	if got != (MutationResult{}) || ErrorCodeOf(err) != CodeMutationReceiptInvalid || errors.Unwrap(err) != nil {
		t.Fatalf("receipt=%#v error=%v", got, err)
	}
}
