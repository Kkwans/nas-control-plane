package journal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestQueryOutputCollectorBudget(t *testing.T) {
	for _, tc := range []struct {
		name         string
		chunk, count int
		overflow     bool
	}{
		{"exact", maximumQueryOutputSize, 1, false},
		{"plus_one", maximumQueryOutputSize + 1, 1, true},
		{"huge_chunk", maximumQueryOutputSize * 8, 1, true},
		{"many_exact_chunks", 1024, maximumQueryOutputSize / 1024, false},
		{"many_chunks_overflow", 1024, maximumQueryOutputSize/1024 + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cancels := 0
			collector := newQueryOutputCollector(func() { cancels++ })
			chunk := bytes.Repeat([]byte("x"), tc.chunk)
			for i := 0; i < tc.count; i++ {
				before := len(collector.data)
				n, err := collector.Write(chunk)
				if len(chunk) > maximumQueryOutputSize-before {
					if n != 0 || !errors.Is(err, errQueryOutputBudget) || len(collector.data) != before {
						t.Fatalf("overflow write = %d, %v, retained %d", n, err, len(collector.data))
					}
				} else if n != len(chunk) || err != nil {
					t.Fatalf("write = %d, %v", n, err)
				}
				if len(collector.data) > maximumQueryOutputSize || cap(collector.data) != maximumQueryOutputSize {
					t.Fatal("collector allocation exceeded fixed budget")
				}
			}
			if collector.overflow != tc.overflow {
				t.Fatalf("overflow = %v", collector.overflow)
			}
			if tc.overflow {
				before := len(collector.data)
				for i := 0; i < 10; i++ {
					if n, err := collector.Write([]byte("retry")); n != 0 || !errors.Is(err, errQueryOutputBudget) {
						t.Fatalf("retry = %d, %v", n, err)
					}
				}
				if len(collector.data) != before || cancels != 1 {
					t.Fatalf("retained %d, cancellations %d", len(collector.data), cancels)
				}
			} else if cancels != 0 || !bytes.Equal(collector.data, bytes.Repeat([]byte("x"), maximumQueryOutputSize)) {
				t.Fatal("exact-budget data or cancellation changed")
			}
		})
	}
}

func TestQueryOutputOverflowWinsOverExecutorResult(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"nil", nil}, {"exit_one", journalExitError(1)}, {"other_exit", journalExitError(2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := context.Background()
			finished := false
			var child context.Context
			output, err := queryOutput(caller, []string{"--lines=2"}, func(ctx context.Context, w io.Writer, args ...string) error {
				child = ctx
				if len(args) != 1 || args[0] != "--lines=2" {
					t.Fatalf("arguments = %v", args)
				}
				_, _ = w.Write([]byte(entryJSON("first", "2026-07-19T10:00:00Z", "token=secret") + "\n"))
				_, _ = w.Write(make([]byte, maximumQueryOutputSize))
				if !errors.Is(ctx.Err(), context.Canceled) || caller.Err() != nil {
					t.Fatal("overflow must cancel only the command context")
				}
				finished = true
				return tc.err
			})
			if !finished || child.Err() == nil || output != nil || !errors.Is(err, errQueryOutputBudget) {
				t.Fatalf("result = %d bytes, %v, finished %v", len(output), err, finished)
			}
		})
	}
}

func TestQueryOutputCallerCancellationPrecedence(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		output, err := queryOutput(ctx, nil, func(commandCtx context.Context, w io.Writer, _ ...string) error {
			if overflow {
				_, _ = w.Write(make([]byte, maximumQueryOutputSize+1))
			}
			cancel()
			if commandCtx.Err() == nil {
				t.Fatal("caller cancellation did not reach command")
			}
			return journalExitError(1)
		})
		if output != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled result = %d, %v", len(output), err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := queryOutput(ctx, nil, func(context.Context, io.Writer, ...string) error { t.Fatal("executed pre-canceled query"); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestQueryOutputOrdinaryExitHandling(t *testing.T) {
	for _, exit := range []error{nil, journalExitError(1), journalExitError(2)} {
		var child context.Context
		output, err := queryOutput(context.Background(), nil, func(ctx context.Context, w io.Writer, _ ...string) error {
			child = ctx
			_, _ = w.Write([]byte("record"))
			return exit
		})
		if string(output) != "record" || child.Err() == nil {
			t.Fatal("output or context cleanup changed")
		}
		if exit == nil || isNoEntriesExit(exit) {
			if err != nil {
				t.Fatal(err)
			}
		} else if !errors.Is(err, exit) {
			t.Fatal(err)
		}
	}
	output, err := queryOutput(context.Background(), nil, func(context.Context, io.Writer, ...string) error { return journalExitError(1) })
	if len(output) != 0 || err != nil {
		t.Fatalf("empty exit one = %q, %v", output, err)
	}
}

type budgetRunner func(context.Context, ...string) ([]byte, error)

func (run budgetRunner) Output(ctx context.Context, args ...string) ([]byte, error) {
	return run(ctx, args...)
}

func TestQueryBudgetRejectsWholePageBeforeParsing(t *testing.T) {
	valid := entryJSON("cursor-1", "2026-07-19T10:00:00Z", "token=secret") + "\n"
	for _, viaCollector := range []bool{false, true} {
		reader := NewReader(budgetRunner(func(ctx context.Context, args ...string) ([]byte, error) {
			data := []byte(valid + strings.Repeat("!", maximumQueryOutputSize))
			if !viaCollector {
				return data, nil
			}
			return queryOutput(ctx, args, func(_ context.Context, w io.Writer, _ ...string) error {
				_, _ = w.Write(data)
				return journalExitError(1)
			})
		}))
		page, err := reader.Query(context.Background(), Query{Limit: 1})
		if ErrorCode(err) != "JOURNAL_QUERY_FAILED" || err.Error() != "JOURNAL_QUERY_FAILED" || !errors.Is(err, errQueryOutputBudget) {
			t.Fatalf("unsanitized or wrong error: %v", err)
		}
		if page.Entries != nil || page.NextCursor != "" {
			t.Fatalf("partial page: %#v", page)
		}
	}
}

func TestQueryExactBudgetPaginationAndRedaction(t *testing.T) {
	line := entryJSON("cursor-1", "2026-07-19T10:00:00Z", "token=secret") + "\n"
	data := []byte(line + entryJSON("cursor-2", "2026-07-19T10:00:01Z", "second") + "\n")
	data = append(data, bytes.Repeat([]byte("\n"), maximumQueryOutputSize-len(data))...)
	reader := NewReader(budgetRunner(func(ctx context.Context, args ...string) ([]byte, error) {
		return queryOutput(ctx, args, func(_ context.Context, w io.Writer, _ ...string) error { _, err := w.Write(data); return err })
	}))
	page, err := reader.Query(context.Background(), Query{Limit: 1})
	if err != nil || len(page.Entries) != 1 || page.NextCursor != "cursor-1" {
		t.Fatalf("page = %#v, error %v", page, err)
	}
	if strings.Contains(page.Entries[0].Message, "secret") || !strings.Contains(page.Entries[0].Message, "[REDACTED]") {
		t.Fatal("redaction changed")
	}
}

func TestQueryAlternateRunnerCancellationPrecedesBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := NewReader(budgetRunner(func(context.Context, ...string) ([]byte, error) {
		cancel()
		return make([]byte, maximumQueryOutputSize+1), nil
	}))
	page, err := reader.Query(ctx, Query{})
	if ErrorCode(err) != "JOURNAL_QUERY_CANCELED" || !errors.Is(err, context.Canceled) || page.Entries != nil || page.NextCursor != "" {
		t.Fatalf("page = %#v, error %v", page, err)
	}
}

func TestQueryOutputWaitsForExecutorAndIsolatesCalls(t *testing.T) {
	entered := make(chan error, 1)
	release := make(chan struct{})
	releaseExecutor := sync.OnceFunc(func() { close(release) })
	defer releaseExecutor()
	done := make(chan error, 1)
	go func() {
		_, err := queryOutput(context.Background(), nil, func(ctx context.Context, w io.Writer, _ ...string) error {
			_, _ = w.Write(make([]byte, maximumQueryOutputSize+1))
			entered <- ctx.Err()
			<-release
			return nil
		})
		done <- err
	}()
	select {
	case cancellation := <-entered:
		if !errors.Is(cancellation, context.Canceled) {
			t.Fatalf("overflow command cancellation = %v", cancellation)
		}
	case prematureErr := <-done:
		t.Fatalf("query returned before executor entered: %v", prematureErr)
	case <-time.After(5 * time.Second):
		t.Fatal("executor did not enter before guard timeout")
	}
	// 第一个执行器尚未结束，独立调用仍可成功且不会继承其取消或溢出状态。
	output, err := queryOutput(context.Background(), nil, func(ctx context.Context, w io.Writer, _ ...string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, err := w.Write([]byte("independent"))
		return err
	})
	select {
	case prematureErr := <-done:
		t.Fatalf("query returned before executor completed: %v", prematureErr)
	default:
	}
	releaseExecutor()
	select {
	case overflowErr := <-done:
		if !errors.Is(overflowErr, errQueryOutputBudget) {
			t.Fatal(overflowErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("query did not return after executor release")
	}
	if err != nil || string(output) != "independent" {
		t.Fatalf("independent query = %q, %v", output, err)
	}
}

func TestQueryOutputExpiredDeadlineSkipsExecution(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancel()
	output, err := queryOutput(ctx, nil, func(context.Context, io.Writer, ...string) error { t.Fatal("executed after deadline"); return nil })
	if output != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("result = %q, %v", output, err)
	}
	page, err := NewReader(budgetRunner(func(context.Context, ...string) ([]byte, error) {
		t.Fatal("called runner after deadline")
		return nil, nil
	})).Query(ctx, Query{})
	if ErrorCode(err) != "JOURNAL_QUERY_CANCELED" || !errors.Is(err, context.DeadlineExceeded) || page.Entries != nil || page.NextCursor != "" {
		t.Fatalf("page = %#v, %v", page, err)
	}
}

func TestQueryInBudgetMalformedResponseRemainsInvalid(t *testing.T) {
	page, err := NewReader(budgetRunner(func(context.Context, ...string) ([]byte, error) { return []byte("{invalid token=secret"), nil })).Query(context.Background(), Query{})
	if ErrorCode(err) != "JOURNAL_RESPONSE_INVALID" || err.Error() != "JOURNAL_RESPONSE_INVALID" || page.Entries != nil || page.NextCursor != "" {
		t.Fatalf("page = %#v, %v", page, err)
	}
}
