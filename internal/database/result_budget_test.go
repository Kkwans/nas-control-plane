package database

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestResultBudgetExactBoundaryAndEscaping(t *testing.T) {
	for _, extra := range []int{-1, 0, 1} {
		value := strings.Repeat("x", MaxResultBytes-resultNodeCost+extra)
		err := ValidateResultBudget(value)
		if (err == nil) != (extra <= 0) || extra > 0 && ErrorCodeOf(err) != CodeResultTooLarge {
			t.Fatalf("boundary %+d error=%v", extra, err)
		}
	}
	for _, value := range []string{"", "plain", "中文🙂", "\x00\x01\n\r\t\b\f\\\"<>&", "\u2028\u2029", string([]byte{0xff, 0xfe, 'x'})} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		want := resultNodeCost + len(encoded) - 2
		for _, useBytes := range []bool{false, true} {
			budget := &resultBudget{remaining: want}
			if useBytes {
				err = budget.bytesText([]byte(value))
			} else {
				err = budget.text(value)
			}
			if err != nil || budget.remaining != 0 {
				t.Fatalf("bytes=%v value=%q remaining=%d error=%v", useBytes, value, budget.remaining, err)
			}
		}
	}
	for _, value := range []any{strings.Repeat("<", MaxResultBytes/6), strings.Repeat("\x00", MaxResultBytes/6), bytes.Repeat([]byte{0xff}, MaxResultBytes)} {
		if ErrorCodeOf(ValidateResultBudget(value)) != CodeResultTooLarge {
			t.Fatal("escaped/base64 expansion escaped budget")
		}
	}
}

type budgetTextMarshaler string

func (budgetTextMarshaler) MarshalText() ([]byte, error) { panic("must not invoke marshaler") }

type budgetJSONMarshaler struct{}

func (budgetJSONMarshaler) MarshalJSON() ([]byte, error) { panic("must not invoke marshaler") }

func TestResultBudgetMetadataContainersAndCustomEncoders(t *testing.T) {
	for _, result := range []any{
		QueryResult{Columns: []string{strings.Repeat("c", MaxResultBytes)}},
		RowsResult{Table: Table{Definition: strings.Repeat("d", MaxResultBytes)}},
		map[string]any{strings.Repeat("k", MaxResultBytes): nil},
		make([]any, MaxResultBytes/resultNodeCost),
	} {
		if ErrorCodeOf(ValidateResultBudget(result)) != CodeResultTooLarge {
			t.Fatalf("metadata/container result %T accepted", result)
		}
	}
	for _, result := range []any{budgetTextMarshaler("short"), map[budgetTextMarshaler]string{"key": "value"}, budgetJSONMarshaler{}, &budgetJSONMarshaler{}} {
		if err := ValidateResultBudget(result); err == nil {
			t.Fatalf("custom marshaler %T accepted", result)
		}
	}
	cyclic := map[string]any{}
	cyclic["cycle"] = cyclic
	if ErrorCodeOf(ValidateResultBudget(cyclic)) != CodeResultTooLarge {
		t.Fatal("unbounded nesting accepted")
	}
	if err := ValidateResultBudget(Discovery{CollectedAt: time.Now(), Sources: []Source{}}); err != nil {
		t.Fatal(err)
	}
}

func TestBudgetCellPreservesExactValuesAndOwnsBytes(t *testing.T) {
	for _, tt := range []struct {
		value        any
		databaseType string
		want         any
	}{
		{int64(9007199254740993), "BIGINT", "9007199254740993"},
		{uint64(^uint64(0)), "BIGINT", "18446744073709551615"},
		{[]byte("1.234567890123456789002500"), "DECIMAL", "1.234567890123456789002500"},
		{[]byte("中文\n<&"), "TEXT", "中文\n<&"},
		{[]byte{0xff, 0x00, 0xfe}, "BLOB", "base64:" + base64.StdEncoding.EncodeToString([]byte{0xff, 0x00, 0xfe})},
		{nil, "", nil}, {true, "", true}, {int64(42), "INTEGER", int64(42)},
		{float64(1.25), "REAL", float64(1.25)},
		{time.Date(2026, 10, 13, 1, 2, 3, 0, time.UTC), "TIMESTAMPTZ", "2026-10-13T01:02:03Z"},
	} {
		budget := &resultBudget{remaining: MaxResultBytes}
		cell := budgetCell{budget: budget, databaseType: tt.databaseType}
		if err := cell.Scan(tt.value); err != nil || !reflect.DeepEqual(cell.value, tt.want) {
			t.Fatalf("cell=%#v want=%#v err=%v", cell.value, tt.want, err)
		}
		wantBudget, err := newResultBudget(tt.want)
		if err != nil || budget.remaining != wantBudget.remaining {
			t.Fatalf("normalized charge differs: %d != %d err=%v", budget.remaining, wantBudget.remaining, err)
		}
		if raw, ok := tt.value.([]byte); ok && len(raw) > 0 {
			raw[0] = 'z'
			if !reflect.DeepEqual(cell.value, tt.want) {
				t.Fatal("retained value aliases driver buffer")
			}
		}
	}
}

func TestBudgetCellRejectsBeforeLargeByteCopyOrBase64(t *testing.T) {
	for _, bytesValue := range [][]byte{bytes.Repeat([]byte{'x'}, 5<<20), bytes.Repeat([]byte{0xff}, 5<<20), bytes.Repeat([]byte{'\x00'}, MaxResultBytes/4)} {
		cell := budgetCell{budget: &resultBudget{remaining: MaxResultBytes}}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		err := cell.Scan(bytesValue)
		runtime.ReadMemStats(&after)
		if ErrorCodeOf(err) != CodeResultTooLarge || cell.value != nil {
			t.Fatalf("oversize cell retained: error=%v", err)
		}
		if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 64<<10 {
			t.Fatalf("rejected cell allocated %d bytes before guard", allocated)
		}
	}
}

type budgetRows struct {
	columns                []string
	values                 [][]any
	next, scans, closes    int
	closeErr, iterationErr error
	onClose                func()
}

func (r *budgetRows) Columns() ([]string, error)              { return r.columns, nil }
func (r *budgetRows) ColumnTypes() ([]*sql.ColumnType, error) { return nil, nil }
func (r *budgetRows) Next() bool {
	if r.next == len(r.values) {
		return false
	}
	r.next++
	return true
}
func (r *budgetRows) Scan(destinations ...any) error {
	r.scans++
	for index, value := range r.values[r.next-1] {
		if err := destinations[index].(sql.Scanner).Scan(value); err != nil {
			return err
		}
	}
	return nil
}
func (r *budgetRows) Close() error {
	r.closes++
	if r.onClose != nil {
		r.onClose()
	}
	return r.closeErr
}
func (r *budgetRows) Err() error { return r.iterationErr }

func TestQueryByteBudgetKeepsOnlyCompletePrefixAndReason(t *testing.T) {
	large := strings.Repeat("x", MaxResultBytes/3)
	rows := &budgetRows{columns: []string{"id", "value"}, values: [][]any{{int64(9007199254740993), large}, {int64(9007199254740995), large}, {int64(9007199254740997), large}, {int64(4), "unread"}}}
	runner := &fakeQueryRunner{rows: rows}
	result, err := executeQuery(context.Background(), runner, DriverSQLite, QueryRequest{SQL: "UPDATE sample SET n=n+1 RETURNING id,value", ResultMode: QueryResultModeRows})
	if err != nil || len(result.Rows) != 2 || !result.Truncated || result.TruncationReason != "byte_limit" || result.RowsAffectedKnown || result.RowsAffected != 0 {
		t.Fatalf("rows=%d result flags=%+v error=%v", len(result.Rows), struct {
			Truncated bool
			Reason    string
		}{result.Truncated, result.TruncationReason}, err)
	}
	if rows.scans != 3 || rows.next != 3 || rows.closes != 1 || runner.queryCalls != 1 || runner.execCalls != 0 {
		t.Fatalf("rows=%#v runner calls=%d/%d", rows, runner.queryCalls, runner.execCalls)
	}
	if result.Rows[0][0] != "9007199254740993" || result.Rows[1][0] != "9007199254740995" || result.Rows[1][1] != large {
		t.Fatal("cell or exact key was cut")
	}
	if err := ValidateResultBudget(result); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if len(encoded) > MaxResultBytes {
		t.Fatal("JSON exceeded limit")
	}
}

func TestQueryOversizedFirstCellColumnsAndManySmallCells(t *testing.T) {
	rows := &budgetRows{columns: []string{"blob"}, values: [][]any{{bytes.Repeat([]byte{'a'}, 5<<20)}}}
	result, err := readQueryRows(context.Background(), rows)
	if err != nil || !result.Truncated || result.TruncationReason != "byte_limit" || len(result.Rows) != 0 || len(result.Columns) != 1 || rows.closes != 1 {
		t.Fatalf("empty prefix=%+v error=%v", result, err)
	}
	rows = &budgetRows{columns: []string{strings.Repeat("k", MaxResultBytes)}, values: [][]any{{1}}}
	_, err = readQueryRows(context.Background(), rows)
	if ErrorCodeOf(err) != CodeResultTooLarge || rows.scans != 0 || rows.closes != 1 {
		t.Fatalf("column guard: scans=%d error=%v", rows.scans, err)
	}
	rows = &budgetRows{columns: make([]string, 100), values: make([][]any, 500)}
	for index := range rows.values {
		rows.values[index] = make([]any, len(rows.columns))
	}
	result, err = readQueryRows(context.Background(), rows)
	if err != nil || !result.Truncated || result.TruncationReason != "byte_limit" || len(result.Rows) == 0 || len(result.Rows) >= 500 {
		t.Fatalf("small cells rows=%d error=%v", len(result.Rows), err)
	}
	if err := ValidateResultBudget(result); err != nil {
		t.Fatal(err)
	}
}

func TestQueryByteTruncationStillChecksCloseIterationAndCancellation(t *testing.T) {
	failure := errors.New("late driver failure")
	for _, mode := range []string{"close", "iteration", "cancel"} {
		ctx, cancel := context.WithCancel(context.Background())
		rows := &budgetRows{columns: []string{"value"}, values: [][]any{{strings.Repeat("x", MaxResultBytes)}}}
		want := failure
		switch mode {
		case "close":
			rows.closeErr = failure
		case "iteration":
			rows.iterationErr = failure
		case "cancel":
			rows.onClose = cancel
			want = context.Canceled
		}
		result, err := readQueryRows(ctx, rows)
		cancel()
		if !errors.Is(err, want) || rows.closes != 1 || result.Rows != nil {
			t.Fatalf("mode=%s error=%v", mode, err)
		}
	}
}

func testPagedResult(t *testing.T, rows *budgetRows, limit int) (RowsResult, error) {
	t.Helper()
	result := RowsResult{Table: Table{Name: "sample"}, Rows: []Row{}, Limit: limit, Ordering: Ordering{Stable: true, Columns: []string{"id"}}}
	budget, err := newResultBudget(result)
	if err != nil {
		t.Fatal(err)
	}
	return readPagedRows(context.Background(), rows, result, budget)
}

func TestPagedRowsBudgetFailsWholePageAndSkipsProbeScan(t *testing.T) {
	large := strings.Repeat("x", MaxResultBytes)
	rows := &budgetRows{columns: []string{"id", "value"}, values: [][]any{{1, "kept"}, {2, large}}}
	result, err := testPagedResult(t, rows, 1)
	if err != nil || len(result.Rows) != 1 || !result.HasMore || rows.scans != 1 || rows.next != 2 || rows.closes != 1 {
		t.Fatalf("probe result=%+v scans=%d error=%v", result, rows.scans, err)
	}
	rows = &budgetRows{columns: []string{"id", "value"}, values: [][]any{{1, "kept"}, {2, large}}}
	result, err = testPagedResult(t, rows, 2)
	if ErrorCodeOf(err) != CodeResultTooLarge || result.Rows != nil || rows.scans != 2 || rows.closes != 1 {
		t.Fatalf("partial page escaped: result=%+v error=%v", result, err)
	}
	failure := errors.New("probe close failed")
	rows = &budgetRows{columns: []string{"id"}, values: [][]any{{1}, {2}}, closeErr: failure}
	result, err = testPagedResult(t, rows, 1)
	if !errors.Is(err, failure) || result.Rows != nil || rows.scans != 1 || rows.closes != 1 {
		t.Fatalf("probe close=%v", err)
	}
}

type budgetEmbeddedFields struct{ Payload any }
type budgetEmbeddedResult struct{ budgetEmbeddedFields }

func TestResultBudgetUnexportedAnonymousFieldsCannotBypassPreflight(t *testing.T) {
	for _, payload := range []any{budgetJSONMarshaler{}, budgetTextMarshaler("short"), strings.Repeat("x", MaxResultBytes)} {
		value := budgetEmbeddedResult{budgetEmbeddedFields{Payload: payload}}
		if err := ValidateResultBudget(value); err == nil {
			t.Fatalf("anonymous embedded payload %T escaped preflight", payload)
		}
	}
	if err := ValidateResultBudget(budgetEmbeddedResult{budgetEmbeddedFields{Payload: "small"}}); err != nil {
		t.Fatal(err)
	}
}

type budgetByteJSON uint8

func (*budgetByteJSON) MarshalJSON() ([]byte, error) { panic("must not invoke byte encoder") }

type budgetByteText uint8

func (*budgetByteText) MarshalText() ([]byte, error) { panic("must not invoke byte encoder") }

type budgetNamedBytes []byte
type budgetNamedString string

func TestResultBudgetByteElementsCannotBypassEncoderPreflight(t *testing.T) {
	for _, value := range []any{[]budgetByteJSON{1}, []budgetByteText{1}} {
		if err := ValidateResultBudget(value); err == nil {
			t.Fatalf("byte element encoder %T escaped", value)
		}
	}
}

func TestBudgetCellRejectsUnknownDriverValuesWithoutRetainingAliases(t *testing.T) {
	backing := bytes.Repeat([]byte{'x'}, 8<<20)
	largeText := strings.Repeat("x", 8<<20)
	for _, value := range []any{budgetNamedBytes(backing[:1]), budgetNamedString(largeText[:1]), []string{largeText[:1]}, &largeText, map[string]any{"tiny": backing[:1]}, budgetByteJSON(1)} {
		cell := budgetCell{budget: &resultBudget{remaining: MaxResultBytes}}
		if err := cell.Scan(value); err == nil || cell.value != nil {
			t.Fatalf("unknown driver value %T retained", value)
		}
	}
}

type budgetCustomZero struct{}

func (budgetCustomZero) IsZero() bool { panic("must not invoke IsZero") }

func TestResultBudgetRejectsUnboundedJSONFieldOptions(t *testing.T) {
	for _, value := range []any{
		struct {
			Text string `json:"text,string"`
		}{Text: strings.Repeat("\\", 300000)},
		struct {
			Value budgetCustomZero `json:"value,omitzero"`
		}{},
		struct {
			Value string `json:"\\"`
		}{Value: "small"},
	} {
		if err := ValidateResultBudget(value); err == nil {
			t.Fatalf("unsupported field tag escaped: %T", value)
		}
	}
}

func TestResultBudgetOnlyBareDashTagSkipsField(t *testing.T) {
	for _, value := range []any{
		struct {
			Value any `json:"-,omitempty"`
		}{Value: budgetJSONMarshaler{}},
		struct {
			Value string `json:"-,"`
		}{Value: strings.Repeat("x", MaxResultBytes)},
	} {
		if err := ValidateResultBudget(value); err == nil {
			t.Fatalf("literal dash field escaped: %T", value)
		}
	}
	if err := ValidateResultBudget(struct {
		Value any `json:"-"`
	}{Value: budgetJSONMarshaler{}}); err != nil {
		t.Fatal(err)
	}
}
