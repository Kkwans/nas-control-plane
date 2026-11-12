package agentsocket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Kkwans/nas-control-plane/internal/terminal"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

// 全部启动与收尾在内存中执行，不创建 PTY、监听器或容器。
type startReservationDiagnosticSession struct {
	closes   atomic.Int32
	closeErr error
}

func (s *startReservationDiagnosticSession) Read(context.Context, []byte) (int, error) {
	return 0, io.EOF
}
func (s *startReservationDiagnosticSession) Write(data []byte) (int, error) { return len(data), nil }
func (s *startReservationDiagnosticSession) Resize(uint16, uint16) error    { return nil }
func (s *startReservationDiagnosticSession) Close() error                   { s.closes.Add(1); return s.closeErr }
func (s *startReservationDiagnosticSession) Metadata() terminal.SessionMetadata {
	panic("rejected start must not inspect metadata")
}

func TestTerminalStartReservationCanceledCleanupDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		code  string
		local bool
	}{
		{"normal", nil, "", false},
		{"generic", errors.New("secret-close-path"), "TERMINAL_CLOSE_FAILED", false},
		{"host_exit", terminal.ErrHostExitUnconfirmed, "TERMINAL_HOST_EXIT_UNCONFIRMED", false},
		{"host_local", terminal.ErrHostCleanupUnconfirmed, "TERMINAL_CLOSE_FAILED", true},
		{"container_exit", terminal.ErrContainerExitUnconfirmed, "TERMINAL_CONTAINER_EXIT_UNCONFIRMED", false},
		{"container_local", terminal.ErrContainerCleanupUnconfirmed, "TERMINAL_CLOSE_FAILED", true},
		{"combined", errors.Join(terminal.ErrHostExitUnconfirmed, terminal.ErrContainerExitUnconfirmed, terminal.ErrHostCleanupUnconfirmed, terminal.ErrContainerCleanupUnconfirmed, errors.New("secret-close-path")), "TERMINAL_CONTAINER_EXIT_UNCONFIRMED", true},
	} {
		for _, target := range []terminal.Target{terminal.TargetHost, terminal.TargetContainer} {
			t.Run(tc.name+"/"+string(target), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				session := &startReservationDiagnosticSession{closeErr: tc.err}
				starter := revocationTerminalStarter(func(context.Context, terminal.StartRequest) (terminal.Session, error) {
					cancel()
					return session, nil
				})
				logs, err := runStartReservationDiagnostic(t, ctx, target, starter)
				if err != nil || session.closes.Load() != 1 {
					t.Fatalf("Open=%v closes=%d", err, session.closes.Load())
				}
				assertStartReservationDiagnostic(t, logs, target, tc.code, tc.local)
			})
		}
	}
}

func TestTerminalStartReservationStarterFailureDiagnostics(t *testing.T) {
	// 从公开 API 获取真实 coded error，确保主错误映射不会被次级清理错误覆盖。
	_, primary := terminal.NewManager(nil, nil, 1).Open(context.Background(), terminal.StartRequest{Target: terminal.TargetHost, Rows: 24, Cols: 80})
	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()
	_, canceled := terminal.NewManager(nil, nil, 1).Open(canceledContext, terminal.StartRequest{Target: terminal.TargetHost, Rows: 24, Cols: 80})
	for _, tc := range []struct {
		name        string
		owned       bool
		startErr    error
		closeErr    error
		code        string
		local       bool
		grpcCode    codes.Code
		grpcMessage string
	}{
		{"ordinary", false, errors.New("secret-start-path"), nil, "", false, codes.Internal, "TERMINAL_SESSION_FAILED"},
		{"raw_primary_canceled_cleanup", true, errors.New("secret-start-path"), canceled, "TERMINAL_CLOSE_FAILED", false, codes.Internal, "TERMINAL_SESSION_FAILED"},
		{"raw_primary_target_cleanup", true, errors.New("secret-start-path"), primary, "TERMINAL_CLOSE_FAILED", false, codes.Internal, "TERMINAL_SESSION_FAILED"},
		{"coded", false, primary, nil, "", false, codes.FailedPrecondition, "TERMINAL_TARGET_UNAVAILABLE"},
		{"session_error", true, primary, errors.New("secret-close-path"), "TERMINAL_CLOSE_FAILED", false, codes.FailedPrecondition, "TERMINAL_TARGET_UNAVAILABLE"},
		{"session_error_clean", true, primary, nil, "", false, codes.FailedPrecondition, "TERMINAL_TARGET_UNAVAILABLE"},
		{"starter_host_exit", false, errors.Join(primary, terminal.ErrHostExitUnconfirmed, errors.New("secret-start-path")), nil, "TERMINAL_HOST_EXIT_UNCONFIRMED", false, codes.FailedPrecondition, "TERMINAL_TARGET_UNAVAILABLE"},
		{"starter_host_local", false, errors.Join(primary, terminal.ErrHostCleanupUnconfirmed), nil, "TERMINAL_CLOSE_FAILED", true, codes.FailedPrecondition, "TERMINAL_TARGET_UNAVAILABLE"},
		{"starter_container_exit", false, errors.Join(primary, terminal.ErrContainerExitUnconfirmed), nil, "TERMINAL_CONTAINER_EXIT_UNCONFIRMED", false, codes.FailedPrecondition, "TERMINAL_TARGET_UNAVAILABLE"},
		{"starter_container_local", false, errors.Join(primary, terminal.ErrContainerCleanupUnconfirmed), nil, "TERMINAL_CLOSE_FAILED", true, codes.FailedPrecondition, "TERMINAL_TARGET_UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := &startReservationDiagnosticSession{closeErr: tc.closeErr}
			starter := revocationTerminalStarter(func(context.Context, terminal.StartRequest) (terminal.Session, error) {
				if tc.owned {
					return session, tc.startErr
				}
				return nil, tc.startErr
			})
			logs, err := runStartReservationDiagnostic(t, context.Background(), terminal.TargetHost, starter)
			if grpcstatus.Code(err) != tc.grpcCode || grpcstatus.Convert(err).Message() != tc.grpcMessage {
				t.Fatalf("Open=%v, want %s %s", err, tc.grpcCode, tc.grpcMessage)
			}
			var wantCloses int32
			if tc.owned {
				wantCloses = 1
			}
			if session.closes.Load() != wantCloses {
				t.Fatalf("closes=%d, want %d", session.closes.Load(), wantCloses)
			}
			assertStartReservationDiagnostic(t, logs, terminal.TargetHost, tc.code, tc.local)
		})
	}
}

func TestTerminalStartReservationSafeDiagnosticTarget(t *testing.T) {
	var logs bytes.Buffer
	service := &terminalPOCService{logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	service.logCleanupFailure(errors.New("secret-close-path"), terminal.Target("secret-target"), "start", "")
	assertStartReservationDiagnostic(t, logs.String(), terminal.Target("unknown"), "TERMINAL_CLOSE_FAILED", false)
}

func runStartReservationDiagnostic(t *testing.T, ctx context.Context, target terminal.Target, starter terminal.Starter) (string, error) {
	t.Helper()
	var logs bytes.Buffer
	var sends, receives atomic.Int32
	service := newTerminalPOCService(terminal.NewManager(starter, starter, 1))
	service.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	frame := mustTerminalStruct(t, terminal.Message{Type: terminal.MessageStart, Target: target, ContainerID: "secret-container", Rows: 24, Cols: 80})
	stream := &revocationTerminalServerStream{ctx: ctx,
		recv: func() (*structpb.Struct, error) { receives.Add(1); return frame, nil },
		send: func(*structpb.Struct) error { sends.Add(1); return nil },
	}
	err := service.Open(stream)
	if sends.Load() != 0 || receives.Load() != 1 {
		t.Fatalf("sends=%d receives=%d", sends.Load(), receives.Load())
	}
	return logs.String(), err
}

func assertStartReservationDiagnostic(t *testing.T, logs string, target terminal.Target, code string, local bool) {
	t.Helper()
	if code == "" {
		if logs != "" {
			t.Fatalf("unexpected warning: %s", logs)
		}
		return
	}
	if strings.Count(logs, "\n") != 1 || strings.Contains(logs, "secret") {
		t.Fatalf("unsafe or duplicate diagnostic: %s", logs)
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(logs), &record); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"level": "WARN", "msg": "terminal cleanup incomplete", "code": code, "target": string(target), "phase": "start", "local_cleanup_unconfirmed": local}
	for key, value := range want {
		if record[key] != value {
			t.Errorf("%s=%v, want %v", key, record[key], value)
		}
	}
	for key := range record {
		if key != "time" {
			if _, ok := want[key]; !ok {
				t.Errorf("unexpected diagnostic field %s", key)
			}
		}
	}
}
