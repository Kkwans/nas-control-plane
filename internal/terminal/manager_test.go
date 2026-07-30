package terminal

import (
	"context"
	"errors"
	"io"
	"testing"
)

func TestManagerRejectsUnsupportedTargetWithoutStartingSession(t *testing.T) {
	host := &fakeStarter{}
	manager := NewManager(host, &fakeStarter{}, 1)

	_, err := manager.Open(context.Background(), StartRequest{Target: Target("root")})

	if ErrorCode(err) != "TERMINAL_TARGET_REJECTED" {
		t.Fatalf("error code = %q", ErrorCode(err))
	}
	if host.starts != 0 {
		t.Fatalf("host starts = %d, want 0", host.starts)
	}
}

func TestManagerEnforcesSessionLimitAndReleasesClosedSession(t *testing.T) {
	hostSession := &fakeSession{}
	host := &fakeStarter{session: hostSession}
	manager := NewManager(host, &fakeStarter{}, 1)

	first, err := manager.Open(context.Background(), StartRequest{Target: TargetHost, Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("open first session: %v", err)
	}
	if first.ID == "" {
		t.Fatal("session id is required")
	}
	if _, err := manager.Open(context.Background(), StartRequest{Target: TargetHost, Rows: 24, Cols: 80}); ErrorCode(err) != "TERMINAL_SESSION_LIMIT_REACHED" {
		t.Fatalf("limit error code = %q", ErrorCode(err))
	}

	if err := manager.Close(first.ID); err != nil {
		t.Fatalf("close first session: %v", err)
	}
	if !hostSession.closed {
		t.Fatal("underlying session was not closed")
	}
	if _, err := manager.Open(context.Background(), StartRequest{Target: TargetHost, Rows: 24, Cols: 80}); err != nil {
		t.Fatalf("open after close: %v", err)
	}
}

func TestManagerWritesAndResizesTrackedSession(t *testing.T) {
	session := &fakeSession{}
	manager := NewManager(&fakeStarter{session: session}, &fakeStarter{}, 1)

	opened, err := manager.Open(context.Background(), StartRequest{Target: TargetHost, Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	if err := manager.Write(opened.ID, []byte("printf ready\\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := manager.Resize(opened.ID, 34, 120); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if got := string(session.written); got != "printf ready\\n" {
		t.Fatalf("written = %q", got)
	}
	if session.rows != 34 || session.cols != 120 {
		t.Fatalf("size = %dx%d", session.rows, session.cols)
	}
}

func TestManagerReturnsActualDimensionsAndSessionMetadata(t *testing.T) {
	session := &fakeSession{
		metadata: SessionMetadata{
			Shell:       "bash",
			Enhancement: "blesh",
			Reason:      "ready",
		},
	}
	starter := &fakeStarter{session: session}
	manager := NewManager(starter, &fakeStarter{}, 1)

	opened, err := manager.Open(context.Background(), StartRequest{Target: TargetHost, Rows: 42, Cols: 132})
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	if opened.Rows != 42 || opened.Cols != 132 {
		t.Fatalf("opened dimensions = %dx%d", opened.Rows, opened.Cols)
	}
	if opened.Shell != "bash" || opened.Enhancement != "blesh" || opened.Reason != "ready" {
		t.Fatalf("opened metadata = %#v", opened)
	}
	if starter.request.Rows != 42 || starter.request.Cols != 132 {
		t.Fatalf("starter request = %#v", starter.request)
	}
}

type fakeStarter struct {
	session Session
	starts  int
	err     error
	request StartRequest
}

func (f *fakeStarter) Start(_ context.Context, request StartRequest) (Session, error) {
	f.starts++
	f.request = request
	if f.err != nil {
		return nil, f.err
	}
	if f.session == nil {
		f.session = &fakeSession{}
	}
	return f.session, nil
}

type fakeSession struct {
	written  []byte
	rows     uint16
	cols     uint16
	closed   bool
	metadata SessionMetadata
}

func (f *fakeSession) Metadata() SessionMetadata {
	return f.metadata
}

func (f *fakeSession) Read(context.Context, []byte) (int, error) {
	return 0, io.EOF
}

func (f *fakeSession) Write(input []byte) (int, error) {
	if f.closed {
		return 0, errors.New("session closed")
	}
	f.written = append(f.written, input...)
	return len(input), nil
}

func (f *fakeSession) Resize(rows, cols uint16) error {
	f.rows = rows
	f.cols = cols
	return nil
}

func (f *fakeSession) Close() error {
	f.closed = true
	return nil
}
