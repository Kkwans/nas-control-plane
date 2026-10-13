package agentsocket

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/Kkwans/nas-control-plane/internal/terminal"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

// 全部使用内存 Session 与 RPC fake，不启动 shell、PTY、child 或 listener。
func TestTerminalHostCleanupClassification(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		code  string
		local bool
	}{
		{"host_exit", terminal.ErrHostExitUnconfirmed, "TERMINAL_HOST_EXIT_UNCONFIRMED", false},
		{"host_local", terminal.ErrHostCleanupUnconfirmed, "TERMINAL_CLOSE_FAILED", true},
		{"host_both", errors.Join(terminal.ErrHostExitUnconfirmed, terminal.ErrHostCleanupUnconfirmed), "TERMINAL_HOST_EXIT_UNCONFIRMED", true},
		{"container_exit", terminal.ErrContainerExitUnconfirmed, "TERMINAL_CONTAINER_EXIT_UNCONFIRMED", false},
		{"container_local", terminal.ErrContainerCleanupUnconfirmed, "TERMINAL_CLOSE_FAILED", true},
		{"container_both", errors.Join(terminal.ErrContainerExitUnconfirmed, terminal.ErrContainerCleanupUnconfirmed), "TERMINAL_CONTAINER_EXIT_UNCONFIRMED", true},
	}
	for _, tc := range cases {
		for _, path := range []string{"explicit", "cancel", "invalid_message"} {
			t.Run(tc.name+"/"+path, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					h := newCleanupTerminalHarness(t, ctx)
					defer func() { close(h.transportStop); synctest.Wait() }()
					var logs bytes.Buffer
					closes := 0
					session := &revocationTerminalSession{
						read: func(ctx context.Context, _ []byte) (int, error) { <-ctx.Done(); return 0, ctx.Err() },
						close: func() error {
							closes++
							return errors.Join(tc.err, errors.New("secret-cookie-and-command"))
						},
					}
					service := newTerminalPOCService(terminal.NewManager(&fakeTerminalStarter{session: session}, nil, 1))
					service.logger = slog.New(slog.NewTextHandler(&logs, nil))
					done := make(chan error, 1)
					go func() { done <- service.Open(h.stream) }()
					synctest.Wait()
					wantStatus := codes.Internal
					switch path {
					case "explicit":
						h.push(t, terminal.Message{Type: terminal.MessageClose})
					case "cancel":
						wantStatus = codes.OK
						cancel()
					case "invalid_message":
						wantStatus = codes.InvalidArgument
						h.push(t, terminal.Message{Type: terminal.MessageStart, Target: terminal.TargetHost, Rows: 24, Cols: 80})
					}
					err := <-done
					if grpcstatus.Code(err) != wantStatus {
						t.Fatalf("status = %v, want %v", err, wantStatus)
					}
					if closes != 1 {
						t.Fatalf("close calls = %d, want 1", closes)
					}
					logged := logs.String()
					local := "local_cleanup_unconfirmed=false"
					if tc.local {
						local = "local_cleanup_unconfirmed=true"
					}
					if strings.Count(logged, "terminal cleanup incomplete") != 1 || !strings.Contains(logged, "code="+tc.code) || !strings.Contains(logged, local) || !strings.Contains(logged, "session_id=term-p0-1") || !strings.Contains(logged, "target=host") {
						t.Fatalf("incorrect cleanup classification: %s", logged)
					}
					if strings.HasPrefix(tc.name, "host_") && strings.Contains(logged, "TERMINAL_CONTAINER_") {
						t.Fatalf("host cleanup mislabeled as container: %s", logged)
					}
					if strings.Contains(logged, "secret") || (err != nil && strings.Contains(err.Error(), "secret")) {
						t.Fatal("underlying cleanup text leaked")
					}
				})
			})
		}
	}
}
