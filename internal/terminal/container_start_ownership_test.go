package terminal

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

type startFailureGateway struct {
	cleanupGateway
	err error
}

func (g *startFailureGateway) Open(context.Context, string, uint16, uint16) (ContainerAttachment, error) {
	if g.openFn != nil {
		g.openFn()
	}
	return g.attachment, g.err
}

func TestContainerStartOwnershipGatewayFailurePreservesCauses(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		for _, partial := range []bool{false, true} {
			ctx, cancel := context.WithCancel(context.Background())
			gatewayErr := errors.New("gateway failure")
			closeErr := errors.New("connection close failure")
			g := &startFailureGateway{err: errors.Join(gatewayErr, ErrContainerExitUnconfirmed)}
			c := &cleanupConn{closeFn: func() error { return closeErr }}
			if partial {
				g.attachment.Conn = c
			}
			if canceled {
				g.openFn = cancel
			}
			s, err := newContainerStarter(g).Start(ctx, StartRequest{})
			cancel()
			code := "TERMINAL_CONTAINER_OPEN_FAILED"
			if canceled {
				code = "TERMINAL_SESSION_CANCELED"
			}
			if s != nil || ErrorCode(err) != code || !errors.Is(err, gatewayErr) || !errors.Is(err, ErrContainerExitUnconfirmed) || errors.Is(err, context.Canceled) != canceled || errors.Is(err, closeErr) != partial {
				t.Fatalf("canceled=%v partial=%v session=%v err=%v", canceled, partial, s, err)
			}
			if partial && c.closes.Load() != 1 {
				t.Fatalf("partial connection closes=%d", c.closes.Load())
			}
			if g.waits.Load() != 0 {
				t.Fatal("failed attachment acquired a session cleanup owner")
			}
		}
	}
}

func TestContainerStartOwnershipLateCancelKeepsClosingReservation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		closeErr := errors.New("connection close failure")
		exitErr := errors.New("exit state unavailable")
		c := &cleanupConn{closeFn: func() error { return closeErr }}
		entered, release := make(chan struct{}), make(chan struct{})
		g := &cleanupGateway{
			attachment: ContainerAttachment{ExecID: "exec", Reader: c, Conn: c},
			openFn:     cancel,
			waitFn: func(cleanupCtx context.Context) error {
				if cleanupCtx.Err() != nil {
					t.Error("cleanup inherited canceled startup context")
				}
				if _, ok := cleanupCtx.Deadline(); !ok {
					t.Error("cleanup has no deadline")
				}
				close(entered)
				<-release
				return exitErr
			},
		}
		m := NewManager(nil, newContainerStarter(g), 1)
		request := StartRequest{Target: TargetContainer, ContainerID: "container", Rows: 24, Cols: 80}
		done := make(chan error, 1)
		go func() {
			info, err := m.Open(ctx, request)
			if info != (SessionInfo{}) {
				t.Error("canceled session was published")
			}
			done <- err
		}()
		<-entered
		reservationCounts(t, m, 0, 0, 1)
		if _, err := m.Open(context.Background(), request); ErrorCode(err) != "TERMINAL_SESSION_LIMIT_REACHED" {
			t.Errorf("closing reservation reused: %v", err)
		}
		select {
		case <-done:
			t.Error("Open returned before its cleanup owner finished")
		default:
		}
		close(release)
		err := <-done
		for _, cause := range []error{context.Canceled, closeErr, exitErr, ErrContainerExitUnconfirmed, ErrSessionCleanupFailed} {
			if !errors.Is(err, cause) {
				t.Errorf("missing cause %v in %v", cause, err)
			}
		}
		if ErrorCode(err) != "TERMINAL_SESSION_CANCELED" {
			t.Fatal(err)
		}
		assertCleanupClosed(t, c, g)
		if c.writes.Load() != 0 {
			t.Fatal("cancellation sent control input")
		}
		reservationCounts(t, m, 0, 0, 0)
		// 结束本地清理后恢复容量；远端未确认仍保留在首次错误中。
		next := &cleanupConn{closeFn: func() error { return nil }, writeFn: func(p []byte) (int, error) { return len(p), nil }, deadlineFn: func(_ time.Time) error { return nil }}
		g.attachment = ContainerAttachment{ExecID: "next", Reader: next, Conn: next}
		g.openFn, g.waitFn = nil, nil
		info, err := m.Open(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.Close(info.ID); err != nil {
			t.Fatal(err)
		}
	})
}
