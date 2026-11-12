package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Kkwans/nas-control-plane/internal/terminal"
	"github.com/coder/websocket"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

type nilTerminalStatus struct{}

func (nilTerminalStatus) Error() string                  { return "TERMINAL_SESSION_LIMIT_REACHED secret" }
func (nilTerminalStatus) GRPCStatus() *grpcstatus.Status { return nil }

func TestTerminalErrorCodeExactStatusContract(t *testing.T) {
	codesToCheck := []string{
		"TERMINAL_UNAVAILABLE", "TERMINAL_INITIALIZATION_FAILED", "TERMINAL_INITIALIZATION_TIMEOUT",
		"TERMINAL_TARGET_UNAVAILABLE", "TERMINAL_SESSION_FAILED", "TERMINAL_INPUT_INVALID",
		"TERMINAL_OUTPUT_CLOSED", "TERMINAL_OUTPUT_FAILED", "TERMINAL_STREAM_FAILED",
		"TERMINAL_SESSION_LIMIT_REACHED", "TERMINAL_START_INVALID", "TERMINAL_MESSAGE_INVALID",
		"TERMINAL_REQUEST_INVALID", "TERMINAL_STREAM_RECEIVE_FAILED", "TERMINAL_STREAM_SEND_FAILED",
		"TERMINAL_POC_UNAVAILABLE", "TERMINAL_POC_INITIALIZATION_FAILED", "TERMINAL_POC_TIMEOUT",
		"TERMINAL_POC_OUTPUT_CLOSED", "TERMINAL_POC_OUTPUT_FAILED",
	}
	for _, code := range codesToCheck {
		for depth := 0; depth <= 3; depth++ {
			t.Run(fmt.Sprintf("%s/wrap%d", code, depth), func(t *testing.T) {
				err := grpcstatus.Error(codes.ResourceExhausted, code)
				for i := 0; i < depth; i++ {
					err = fmt.Errorf("TERMINAL_INITIALIZATION_FAILED secret-layer: %w", err)
				}
				if got := terminalErrorCode(err, "TERMINAL_SESSION_FAILED"); got != terminal.NormalizeErrorCode(code) {
					t.Fatalf("code=%q", got)
				}
			})
		}
	}
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"nil", nil}, {"nil_status", nilTerminalStatus{}},
		{"plain_code", errors.New("TERMINAL_SESSION_LIMIT_REACHED")},
		{"plain_embedded", errors.New("secret TERMINAL_UNAVAILABLE")},
		{"unknown_status", grpcstatus.Error(codes.Internal, "TERMINAL_SECRET")},
		{"secret_suffix", grpcstatus.Error(codes.Internal, "TERMINAL_SESSION_LIMIT_REACHED secret-token")},
		{"secret_prefix", grpcstatus.Error(codes.Internal, "secret-token TERMINAL_UNAVAILABLE")},
		{"legacy_suffix", grpcstatus.Error(codes.Internal, "TERMINAL_POC_UNAVAILABLE secret-token")},
		{"wrapped_unknown", fmt.Errorf("TERMINAL_UNAVAILABLE: %w", grpcstatus.Error(codes.Internal, "secret-token"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := terminalErrorCode(tc.err, "TERMINAL_POC_INITIALIZATION_FAILED"); got != "TERMINAL_INITIALIZATION_FAILED" {
				t.Fatalf("code=%q", got)
			}
			if got := terminalErrorCode(tc.err, "TERMINAL_UNKNOWN secret"); got != "TERMINAL_SESSION_FAILED" {
				t.Fatalf("unsafe fallback=%q", got)
			}
		})
	}
}

func TestTerminalProxyBusyErrorContract(t *testing.T) {
	for _, stage := range []string{"open", "send", "recv", "write_failure"} {
		t.Run(stage, func(t *testing.T) {
			c, s := newProxyTestConnection(), newProxyTestStream()
			busy := fmt.Errorf("AGENT_TERMINAL_RECEIVE_FAILED secret: %w", fmt.Errorf("outer secret: %w", grpcstatus.Error(codes.ResourceExhausted, "TERMINAL_SESSION_LIMIT_REACHED")))
			var frames []terminalControl
			c.write = func(_ context.Context, typ websocket.MessageType, payload []byte) error {
				if typ != websocket.MessageText || strings.Contains(string(payload), "secret") {
					t.Errorf("unsafe frame %q", payload)
				}
				var control terminalControl
				if err := json.Unmarshal(payload, &control); err != nil {
					t.Error(err)
				}
				frames = append(frames, control)
				if stage == "write_failure" {
					return errors.New("write failed")
				}
				return nil
			}
			s.recv = func(int32) (terminal.Message, error) { return terminal.Message{}, busy }
			if stage == "send" {
				s.send = func(terminal.Message) error { return busy }
			}
			runTerminalProxy(context.Background(), c, proxyTestClient{open: func(context.Context, string) (TerminalStream, error) {
				if stage == "open" {
					return nil, busy
				}
				return s, nil
			}}, "fake", terminal.Message{Type: terminal.MessageStart})
			if len(frames) != 1 || frames[0].Type != "error" || frames[0].Code != "TERMINAL_SESSION_LIMIT_REACHED" || frames[0].Message != "终端会话数量已达上限，请等待已有会话关闭后重试。" {
				t.Fatalf("frames=%+v", frames)
			}
			if stage == "open" {
				if c.closeCalls.Load() != 1 || s.closeCalls.Load() != 0 {
					t.Fatal("invalid open failure cleanup")
				}
			} else {
				assertProxyClosed(t, c, s)
			}
		})
	}
}
