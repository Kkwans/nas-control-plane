package terminal

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunPOCVerifiesFixedTranscriptAndTerminatesSession(t *testing.T) {
	session := newTranscriptSession()
	manager := NewManager(&fakeStarter{session: session}, &fakeStarter{}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result, err := RunPOC(ctx, manager, TargetHost)

	if err != nil {
		t.Fatalf("run terminal POC: %v", err)
	}
	if !result.PTY || !result.Resize || !result.CtrlC || !result.SessionTerminated {
		t.Fatalf("result = %#v", result)
	}
	if session.rows != 34 || session.cols != 120 || !session.closed {
		t.Fatalf("session state rows=%d cols=%d closed=%t", session.rows, session.cols, session.closed)
	}
}

type transcriptSession struct {
	mu      sync.Mutex
	outputs chan []byte
	closedC chan struct{}
	rows    uint16
	cols    uint16
	closed  bool
	once    sync.Once
}

func newTranscriptSession() *transcriptSession {
	return &transcriptSession{outputs: make(chan []byte, 4), closedC: make(chan struct{})}
}

func (s *transcriptSession) Read(ctx context.Context, output []byte) (int, error) {
	select {
	case data := <-s.outputs:
		return copy(output, data), nil
	case <-s.closedC:
		return 0, io.EOF
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (s *transcriptSession) Write(input []byte) (int, error) {
	text := string(input)
	switch {
	case text == "printf 'NCP_P0_TERMINAL_READY\\n'\n":
		s.outputs <- []byte("NCP_P0_TERMINAL_READY\n")
	case text == "stty size\n":
		s.mu.Lock()
		s.outputs <- []byte("34 120\n")
		s.mu.Unlock()
	case len(input) == 1 && input[0] == 3:
	case strings.Contains(text, "NCP_P0_CTRL_C_OK"):
		s.outputs <- []byte("NCP_P0_CTRL_C_OK\n")
	}
	return len(input), nil
}

func (s *transcriptSession) Resize(rows, cols uint16) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = rows
	s.cols = cols
	return nil
}

func (s *transcriptSession) Close() error {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.closedC)
	})
	return nil
}
