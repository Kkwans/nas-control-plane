package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// 仅供测试使用：HTTP 101 不代表 Agent 已启动会话；只有精确的忙码可重试。
type terminalReadinessOps[C any] struct {
	dial  func(context.Context) (C, error)
	read  func(context.Context, C) (websocket.MessageType, []byte, error)
	close func(C) error
	pause func(context.Context) error
}

func waitForTerminalStarted[C any](ctx context.Context, ops terminalReadinessOps[C]) (C, error) {
	var zero C
	for {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		connection, err := ops.dial(ctx)
		if err != nil {
			return zero, fmt.Errorf("dial terminal: %w", err)
		}
		typ, data, err := ops.read(ctx, connection)
		if err == nil {
			err = ctx.Err()
		}
		if err == nil && typ != websocket.MessageText {
			err = errors.New("terminal first frame is not text")
		}
		var control terminalControl
		if err == nil {
			err = json.Unmarshal(data, &control)
		}
		if err == nil && control.Type == "started" && control.SessionID != "" && control.Code == "" {
			return connection, nil
		}
		busy := err == nil && control.Type == "error" && control.Code == "TERMINAL_SESSION_LIMIT_REACHED"
		if err == nil && !busy {
			err = errors.New("terminal first frame is not a valid started control or session limit error")
			if code, known := knownTerminalErrorCode(control.Code); control.Type == "error" && known && code == control.Code {
				err = fmt.Errorf("terminal start failed: %s", code)
			}
		}
		closeErr := ops.close(connection)
		if err != nil || closeErr != nil {
			return zero, errors.Join(err, closeErr)
		}
		if err := ops.pause(ctx); err != nil {
			return zero, err
		}
	}
}

func pauseTerminalReadiness(ctx context.Context) error {
	timer := time.NewTimer(25 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

func TestTerminalReadinessBusyThenStarted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dials, reads, closes, pauses := 0, 0, 0, 0
	got, err := waitForTerminalStarted(ctx, terminalReadinessOps[int]{
		dial: func(got context.Context) (int, error) {
			if got != ctx {
				t.Fatal("replaced original context")
			}
			dials++
			return dials, nil
		},
		read: func(got context.Context, id int) (websocket.MessageType, []byte, error) {
			if got != ctx {
				t.Fatal("replaced original context")
			}
			reads++
			if id <= 2 {
				return websocket.MessageText, []byte(`{"type":"error","code":"TERMINAL_SESSION_LIMIT_REACHED"}`), nil
			}
			return websocket.MessageText, []byte(`{"type":"started","sessionId":"ready"}`), nil
		},
		close: func(id int) error {
			if id > 2 {
				t.Fatal("closed successful connection")
			}
			closes++
			return nil
		},
		pause: func(got context.Context) error {
			if got != ctx || closes != dials {
				t.Fatal("pause before close or wrong context")
			}
			pauses++
			return nil
		},
	})
	if err != nil || got != 3 || dials != 3 || reads != 3 || closes != 2 || pauses != 2 {
		t.Fatalf("result=%d err=%v counters=%d/%d/%d/%d", got, err, dials, reads, closes, pauses)
	}
}

func TestTerminalReadinessRejectsWithoutRetry(t *testing.T) {
	for _, tc := range []struct {
		name, payload                            string
		binary, dialError, readError, closeError bool
	}{
		{name: "dial", dialError: true}, {name: "read", readError: true},
		{name: "binary", payload: `{"type":"started","sessionId":"ready"}`, binary: true},
		{name: "invalid_json", payload: `{`}, {name: "null", payload: `null`},
		{name: "empty_session", payload: `{"type":"started"}`},
		{name: "wrong_type", payload: `{"type":"output","sessionId":"ready"}`},
		{name: "started_error", payload: `{"type":"started","sessionId":"ready","code":"TERMINAL_SESSION_LIMIT_REACHED"}`},
		{name: "wrong_code", payload: `{"type":"error","code":"TERMINAL_INITIALIZATION_FAILED"}`},
		{name: "legacy_code", payload: `{"type":"error","code":"TERMINAL_POC_UNAVAILABLE"}`},
		{name: "busy_suffix", payload: `{"type":"error","code":"TERMINAL_SESSION_LIMIT_REACHED secret"}`},
		{name: "close_failure", payload: `{"type":"error","code":"TERMINAL_SESSION_LIMIT_REACHED"}`, closeError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dials, closes := 0, 0
			_, err := waitForTerminalStarted(context.Background(), terminalReadinessOps[int]{
				dial: func(context.Context) (int, error) {
					dials++
					if tc.dialError {
						return 0, errors.New("dial failed")
					}
					return 1, nil
				},
				read: func(context.Context, int) (websocket.MessageType, []byte, error) {
					if tc.readError {
						return 0, nil, errors.New("read failed")
					}
					typ := websocket.MessageText
					if tc.binary {
						typ = websocket.MessageBinary
					}
					return typ, []byte(tc.payload), nil
				},
				close: func(int) error {
					closes++
					if tc.closeError {
						return errors.New("close failed")
					}
					return nil
				},
				pause: func(context.Context) error { t.Fatal("retried invalid readiness"); return nil },
			})
			if tc.name == "wrong_code" && (err == nil || !strings.Contains(err.Error(), "TERMINAL_INITIALIZATION_FAILED")) {
				t.Fatalf("missing safe diagnostic: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe diagnostic: %v", err)
			}
			expectedCloses := 1
			if tc.dialError {
				expectedCloses = 0
			}
			if err == nil || dials != 1 || closes != expectedCloses {
				t.Fatalf("err=%v dials=%d closes=%d", err, dials, closes)
			}
		})
	}
}

func TestTerminalReadinessCancellation(t *testing.T) {
	for _, phase := range []string{"before_dial", "read", "pause"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if phase == "before_dial" {
				cancel()
			}
			dials, closes := 0, 0
			_, err := waitForTerminalStarted(ctx, terminalReadinessOps[int]{
				dial: func(context.Context) (int, error) { dials++; return 1, nil },
				read: func(context.Context, int) (websocket.MessageType, []byte, error) {
					if phase == "read" {
						cancel()
						return websocket.MessageText, []byte(`{"type":"started","sessionId":"late"}`), nil
					}
					return websocket.MessageText, []byte(`{"type":"error","code":"TERMINAL_SESSION_LIMIT_REACHED"}`), nil
				},
				close: func(int) error { closes++; return nil },
				pause: func(ctx context.Context) error { cancel(); return pauseTerminalReadiness(ctx) },
			})
			expected := 1
			if phase == "before_dial" {
				expected = 0
			}
			if !errors.Is(err, context.Canceled) || dials != expected || closes != expected {
				t.Fatalf("err=%v dials=%d closes=%d", err, dials, closes)
			}
		})
	}
}

func TestTerminalReadinessExpiredDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := waitForTerminalStarted(ctx, terminalReadinessOps[int]{dial: func(context.Context) (int, error) { t.Fatal("dial after deadline"); return 0, nil }})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	if err := pauseTerminalReadiness(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pause err=%v", err)
	}
}
