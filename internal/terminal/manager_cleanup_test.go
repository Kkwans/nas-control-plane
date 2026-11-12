package terminal

import (
	"context"
	"errors"
	"testing"
)

type cleanupManagerSession struct {
	fakeSession
	entered  chan struct{}
	release  chan struct{}
	closeErr error
}

func (s *cleanupManagerSession) Close() error { close(s.entered); <-s.release; return s.closeErr }

func TestTerminalCleanupManagerReservesClosingSlot(t *testing.T) {
	for _, failure := range []bool{false, true} {
		name := "success"
		if failure {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			s := &cleanupManagerSession{entered: make(chan struct{}), release: make(chan struct{})}
			if failure {
				s.closeErr = ErrContainerExitUnconfirmed
			}
			m := NewManager(&fakeStarter{session: s}, nil, 1)
			request := StartRequest{Target: TargetHost, Rows: 24, Cols: 80}
			first, err := m.Open(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- m.Close(first.ID) }()
			<-s.entered
			if _, err := m.Open(context.Background(), request); ErrorCode(err) != "TERMINAL_SESSION_LIMIT_REACHED" {
				t.Fatalf("slot reused before cleanup: %v", err)
			}
			if err := m.Write(first.ID, []byte("input")); ErrorCode(err) != "TERMINAL_SESSION_NOT_FOUND" {
				t.Fatalf("closing session still accessible: %v", err)
			}
			close(s.release)
			err = <-done
			if failure && (!errors.Is(err, ErrContainerExitUnconfirmed) || ErrorCode(err) != "TERMINAL_CLOSE_FAILED") {
				t.Fatalf("classification lost: %v", err)
			}
			if !failure && err != nil {
				t.Fatal(err)
			}
			if _, err := m.Open(context.Background(), request); err != nil {
				t.Fatalf("reservation leaked: %v", err)
			}
		})
	}
}
