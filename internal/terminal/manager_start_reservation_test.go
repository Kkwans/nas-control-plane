package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type reservationStarter func(context.Context, StartRequest) (Session, error)

func (f reservationStarter) Start(ctx context.Context, r StartRequest) (Session, error) {
	return f(ctx, r)
}

type reservationSession struct {
	metadata      func() SessionMetadata
	close         func() error
	closes        atomic.Int32
	metadataCalls atomic.Int32
}

func (s *reservationSession) Read(context.Context, []byte) (int, error) { return 0, io.EOF }
func (s *reservationSession) Write(p []byte) (int, error)               { return len(p), nil }
func (s *reservationSession) Resize(uint16, uint16) error               { return nil }
func (s *reservationSession) Metadata() SessionMetadata {
	s.metadataCalls.Add(1)
	if s.metadata != nil {
		return s.metadata()
	}
	return SessionMetadata{}
}
func (s *reservationSession) Close() error {
	s.closes.Add(1)
	if s.close != nil {
		return s.close()
	}
	return nil
}

type reservationBarrier struct {
	entered                chan struct{}
	release                chan struct{}
	enterOnce, releaseOnce sync.Once
}

func (b *reservationBarrier) block()   { b.enterOnce.Do(func() { close(b.entered) }); <-b.release }
func (b *reservationBarrier) unblock() { b.releaseOnce.Do(func() { close(b.release) }) }

type reservationHarness struct {
	t        *testing.T
	wg       sync.WaitGroup
	barriers []*reservationBarrier
}

func newReservationHarness(t *testing.T) *reservationHarness {
	h := &reservationHarness{t: t}
	t.Cleanup(func() {
		for _, b := range h.barriers {
			b.unblock()
		}
		done := make(chan struct{})
		go func() { h.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("fake workers did not finish")
		}
	})
	return h
}
func (h *reservationHarness) barrier() *reservationBarrier {
	b := &reservationBarrier{entered: make(chan struct{}), release: make(chan struct{})}
	h.barriers = append(h.barriers, b)
	return b
}
func (h *reservationHarness) wait(c <-chan struct{}) {
	h.t.Helper()
	select {
	case <-c:
	case <-time.After(3 * time.Second):
		h.t.Fatal("fake barrier timed out")
	}
}

type reservationResult struct {
	info SessionInfo
	err  error
}

func (h *reservationHarness) run(f func() reservationResult) <-chan reservationResult {
	c := make(chan reservationResult, 1)
	h.wg.Add(1)
	go func() { defer h.wg.Done(); c <- f() }()
	return c
}
func (h *reservationHarness) result(c <-chan reservationResult) reservationResult {
	h.t.Helper()
	select {
	case r := <-c:
		return r
	case <-time.After(3 * time.Second):
		h.t.Fatal("manager operation blocked")
		return reservationResult{}
	}
}
func reservationRequest() StartRequest { return StartRequest{Target: TargetHost, Rows: 24, Cols: 80} }
func (h *reservationHarness) open(m *Manager, ctx context.Context) <-chan reservationResult {
	return h.run(func() reservationResult { i, e := m.Open(ctx, reservationRequest()); return reservationResult{i, e} })
}
func reservationCounts(t *testing.T, m *Manager, o, l, c int) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.openingCount != o || len(m.sessions) != l || m.closingCount != c {
		t.Fatalf("counts = %d/%d/%d, want %d/%d/%d", m.openingCount, len(m.sessions), m.closingCount, o, l, c)
	}
}
func reservationLimit(t *testing.T, h *reservationHarness, m *Manager) {
	t.Helper()
	r := h.result(h.open(m, context.Background()))
	if ErrorCode(r.err) != "TERMINAL_SESSION_LIMIT_REACHED" {
		t.Fatalf("limit result = %#v", r)
	}
}

func TestTerminalStartReservationBlockedStartAllowsLiveOperations(t *testing.T) {
	h := newReservationHarness(t)
	b := h.barrier()
	var starts atomic.Int32
	m := NewManager(reservationStarter(func(context.Context, StartRequest) (Session, error) {
		if starts.Add(1) == 2 {
			b.block()
		}
		return &reservationSession{}, nil
	}), nil, 2)
	a := h.result(h.open(m, context.Background()))
	if a.err != nil {
		t.Fatal(a.err)
	}
	pending := h.open(m, context.Background())
	h.wait(b.entered)
	r := h.result(h.run(func() reservationResult {
		_, err := m.Read(context.Background(), a.info.ID, make([]byte, 1))
		if !errors.Is(err, io.EOF) {
			return reservationResult{err: fmt.Errorf("read: %v", err)}
		}
		if err = m.Write(a.info.ID, []byte("x")); err != nil {
			return reservationResult{err: err}
		}
		if err = m.Resize(a.info.ID, 30, 90); err != nil {
			return reservationResult{err: err}
		}
		return reservationResult{err: m.Close(a.info.ID)}
	}))
	if r.err != nil {
		t.Fatal(r.err)
	}
	reservationCounts(t, m, 1, 0, 0)
	b.unblock()
	r = h.result(pending)
	if r.err != nil {
		t.Fatal(r.err)
	}
	_ = m.Close(r.info.ID)
}

func TestTerminalStartReservationOpeningConsumesLimit(t *testing.T) {
	h := newReservationHarness(t)
	start := h.barrier()
	cleanup := h.barrier()
	s := &reservationSession{close: func() error { cleanup.block(); return nil }}
	var calls atomic.Int32
	m := NewManager(reservationStarter(func(context.Context, StartRequest) (Session, error) { calls.Add(1); start.block(); return s, nil }), nil, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pending := h.open(m, ctx)
	h.wait(start.entered)
	reservationLimit(t, h, m)
	cancel()
	reservationLimit(t, h, m)
	reservationCounts(t, m, 1, 0, 0)
	start.unblock()
	h.wait(cleanup.entered)
	reservationCounts(t, m, 0, 0, 1)
	reservationLimit(t, h, m)
	cleanup.unblock()
	r := h.result(pending)
	if ErrorCode(r.err) != "TERMINAL_SESSION_CANCELED" || !errors.Is(r.err, context.Canceled) || r.info != (SessionInfo{}) || s.closes.Load() != 1 || s.metadataCalls.Load() != 0 || calls.Load() != 1 {
		t.Fatalf("late success: %#v closes=%d metadata=%d calls=%d", r, s.closes.Load(), s.metadataCalls.Load(), calls.Load())
	}
	reservationCounts(t, m, 0, 0, 0)
}

func TestTerminalStartReservationParallelAdmissionNeverExceedsLimit(t *testing.T) {
	h := newReservationHarness(t)
	b := h.barrier()
	entered := make(chan struct{}, 12)
	m := NewManager(reservationStarter(func(context.Context, StartRequest) (Session, error) {
		entered <- struct{}{}
		b.block()
		return &reservationSession{}, nil
	}), nil, 3)
	results := make(chan reservationResult, 12)
	for range 12 {
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			i, e := m.Open(context.Background(), reservationRequest())
			results <- reservationResult{i, e}
		}()
	}
	for range 3 {
		h.wait(entered)
	}
	for range 9 {
		r := h.result(results)
		if ErrorCode(r.err) != "TERMINAL_SESSION_LIMIT_REACHED" {
			t.Fatalf("rejection=%#v", r)
		}
	}
	reservationCounts(t, m, 3, 0, 0)
	b.unblock()
	ids := map[string]bool{}
	for range 3 {
		r := h.result(results)
		if r.err != nil || ids[r.info.ID] || r.info.ID == "" {
			t.Fatalf("publication=%#v", r)
		}
		ids[r.info.ID] = true
	}
	reservationCounts(t, m, 0, 3, 0)
	for id := range ids {
		if err := m.Close(id); err != nil {
			t.Fatal(err)
		}
	}
	reservationCounts(t, m, 0, 0, 0)
}

func TestTerminalStartReservationFailureReleasesExactlyOnce(t *testing.T) {
	cause := errors.New("start cause")
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{{"plain", cause, ""}, {"coded", coded("TERMINAL_HOST_START_FAILED", cause), "TERMINAL_HOST_START_FAILED"}, {"nil", nil, "TERMINAL_SESSION_START_FAILED"}} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			m := NewManager(reservationStarter(func(context.Context, StartRequest) (Session, error) {
				if calls.Add(1) == 1 {
					return nil, tc.err
				}
				return &reservationSession{}, nil
			}), nil, 1)
			i, e := m.Open(context.Background(), reservationRequest())
			if i != (SessionInfo{}) || ErrorCode(e) != tc.code || e == nil {
				t.Fatalf("failure=%#v %v", i, e)
			}
			if tc.err != nil && e != tc.err {
				t.Fatal("primary identity changed")
			}
			reservationCounts(t, m, 0, 0, 0)
			i, e = m.Open(context.Background(), reservationRequest())
			if e != nil || i.ID != "term-p0-1" {
				t.Fatalf("retry=%#v %v", i, e)
			}
			_ = m.Close(i.ID)
			reservationCounts(t, m, 0, 0, 0)
		})
	}
}

// 第一次 Err 后阻塞，使取消精确发生在进入 admission 锁之前。
type reservationAdmissionContext struct {
	context.Context
	first   chan struct{}
	proceed chan struct{}
	calls   atomic.Int32
}

func (c *reservationAdmissionContext) Err() error {
	if c.calls.Add(1) == 1 {
		close(c.first)
		<-c.proceed
		return nil
	}
	return c.Context.Err()
}
func TestTerminalStartReservationCanceledBeforeAdmissionDoesNotStart(t *testing.T) {
	h := newReservationHarness(t)
	var calls atomic.Int32
	m := NewManager(reservationStarter(func(context.Context, StartRequest) (Session, error) { calls.Add(1); return &reservationSession{}, nil }), nil, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := h.result(h.open(m, ctx))
	if ErrorCode(r.err) != "TERMINAL_SESSION_CANCELED" {
		t.Fatal(r.err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	b := h.barrier()
	c := &reservationAdmissionContext{Context: ctx2, first: b.entered, proceed: b.release}
	var pending <-chan reservationResult
	func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		pending = h.open(m, c)
		h.wait(c.first)
		cancel2()
		b.unblock()
	}()
	r = h.result(pending)
	if ErrorCode(r.err) != "TERMINAL_SESSION_CANCELED" || calls.Load() != 0 {
		t.Fatalf("admission=%#v calls=%d", r, calls.Load())
	}
	reservationCounts(t, m, 0, 0, 0)
}

func TestTerminalStartReservationLateSuccessCleanupFailurePreserved(t *testing.T) {
	for _, cleanup := range []error{ErrHostExitUnconfirmed, ErrHostCleanupUnconfirmed, ErrContainerExitUnconfirmed, ErrContainerCleanupUnconfirmed, errors.Join(ErrHostExitUnconfirmed, ErrContainerCleanupUnconfirmed)} {
		t.Run(cleanup.Error(), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := &reservationSession{close: func() error { return cleanup }}
			m := NewManager(reservationStarter(func(context.Context, StartRequest) (Session, error) { cancel(); return s, nil }), nil, 1)
			i, e := m.Open(ctx, reservationRequest())
			if i != (SessionInfo{}) || ErrorCode(e) != "TERMINAL_SESSION_CANCELED" || !errors.Is(e, context.Canceled) || !errors.Is(e, cleanup) || !errors.Is(e, ErrSessionCleanupFailed) || s.closes.Load() != 1 {
				t.Fatalf("cleanup=%#v %v", i, e)
			}
			reservationCounts(t, m, 0, 0, 0)
		})
	}
}

func TestTerminalStartReservationSessionAndErrorHasOwner(t *testing.T) {
	cause := errors.New("start cause")
	primary := coded("TERMINAL_HOST_START_FAILED", cause)
	for _, cleanup := range []error{nil, errors.New("close cause")} {
		s := &reservationSession{close: func() error { return cleanup }}
		m := NewManager(reservationStarter(func(context.Context, StartRequest) (Session, error) { return s, primary }), nil, 1)
		i, e := m.Open(context.Background(), reservationRequest())
		if i != (SessionInfo{}) || ErrorCode(e) != "TERMINAL_HOST_START_FAILED" || !errors.Is(e, cause) || s.closes.Load() != 1 || s.metadataCalls.Load() != 0 {
			t.Fatalf("owner=%#v %v", i, e)
		}
		if cleanup == nil && e != primary {
			t.Fatal("primary identity changed")
		}
		if cleanup != nil && (!errors.Is(e, cleanup) || !errors.Is(e, ErrSessionCleanupFailed)) {
			t.Fatal("cleanup cause missing")
		}
		reservationCounts(t, m, 0, 0, 0)
	}
}

func TestTerminalStartReservationUncodedSessionAndErrorPreservesCode(t *testing.T) {
	primary := errors.New("raw starter failure")
	cleanup := coded("TERMINAL_SESSION_CANCELED", errors.New("coded cleanup failure"))
	session := &reservationSession{close: func() error { return cleanup }}
	manager := NewManager(reservationStarter(func(context.Context, StartRequest) (Session, error) { return session, primary }), nil, 1)
	info, err := manager.Open(context.Background(), reservationRequest())
	if info != (SessionInfo{}) || ErrorCode(err) != "" || !errors.Is(err, primary) || !errors.Is(err, cleanup) || !errors.Is(err, ErrSessionCleanupFailed) {
		t.Fatalf("uncoded failure = %#v %v", info, err)
	}
	reservationCounts(t, manager, 0, 0, 0)
}

func TestTerminalStartReservationMetadataDoesNotHoldManagerLock(t *testing.T) {
	h := newReservationHarness(t)
	b := h.barrier()
	var calls atomic.Int32
	s := &reservationSession{metadata: func() SessionMetadata { b.block(); return SessionMetadata{Shell: "bash"} }}
	m := NewManager(reservationStarter(func(context.Context, StartRequest) (Session, error) {
		if calls.Add(1) == 2 {
			return s, nil
		}
		return &reservationSession{}, nil
	}), nil, 2)
	a := h.result(h.open(m, context.Background()))
	if a.err != nil {
		t.Fatal(a.err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pending := h.open(m, ctx)
	h.wait(b.entered)
	reservationLimit(t, h, m)
	r := h.result(h.run(func() reservationResult { return reservationResult{err: m.Close(a.info.ID)} }))
	if r.err != nil {
		t.Fatal(r.err)
	}
	cancel()
	b.unblock()
	r = h.result(pending)
	if ErrorCode(r.err) != "TERMINAL_SESSION_CANCELED" || r.info != (SessionInfo{}) || s.closes.Load() != 1 {
		t.Fatalf("metadata cancellation=%#v", r)
	}
	reservationCounts(t, m, 0, 0, 0)
}

func TestTerminalStartReservationOpeningAndClosingShareCapacity(t *testing.T) {
	for _, openingFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(openingFirst), func(t *testing.T) {
			h := newReservationHarness(t)
			start := h.barrier()
			cleanup := h.barrier()
			var calls atomic.Int32
			s := &reservationSession{close: func() error { cleanup.block(); return nil }}
			m := NewManager(reservationStarter(func(context.Context, StartRequest) (Session, error) {
				switch calls.Add(1) {
				case 1:
					return s, nil
				case 2:
					start.block()
				}
				return &reservationSession{}, nil
			}), nil, 2)
			a := h.result(h.open(m, context.Background()))
			closing := h.run(func() reservationResult { return reservationResult{err: m.Close(a.info.ID)} })
			h.wait(cleanup.entered)
			opening := h.open(m, context.Background())
			h.wait(start.entered)
			reservationCounts(t, m, 1, 0, 1)
			reservationLimit(t, h, m)
			duplicate := h.result(h.run(func() reservationResult { return reservationResult{err: m.Close(a.info.ID)} }))
			if ErrorCode(duplicate.err) != "TERMINAL_SESSION_NOT_FOUND" || s.closes.Load() != 1 {
				t.Fatalf("duplicate=%#v", duplicate)
			}
			var opened reservationResult
			if openingFirst {
				start.unblock()
				opened = h.result(opening)
				reservationCounts(t, m, 0, 1, 1)
				reservationLimit(t, h, m)
				cleanup.unblock()
				if r := h.result(closing); r.err != nil {
					t.Fatal(r.err)
				}
			} else {
				cleanup.unblock()
				if r := h.result(closing); r.err != nil {
					t.Fatal(r.err)
				}
				reservationCounts(t, m, 1, 0, 0)
			}
			extra := h.result(h.open(m, context.Background()))
			if extra.err != nil {
				t.Fatal(extra.err)
			}
			reservationLimit(t, h, m)
			if !openingFirst {
				start.unblock()
				opened = h.result(opening)
			}
			if opened.err != nil {
				t.Fatal(opened.err)
			}
			_ = m.Close(opened.info.ID)
			_ = m.Close(extra.info.ID)
			reservationCounts(t, m, 0, 0, 0)
		})
	}
}

func TestTerminalStartReservationMetadataAndPublicationCompatible(t *testing.T) {
	metadata := SessionMetadata{Shell: "bash", Enhancement: "native", Reason: "ready"}
	s := &reservationSession{metadata: func() SessionMetadata { return metadata }}
	var host, container atomic.Int32
	m := NewManager(reservationStarter(func(context.Context, StartRequest) (Session, error) { host.Add(1); return s, nil }), reservationStarter(func(_ context.Context, r StartRequest) (Session, error) {
		container.Add(1)
		if r.ContainerID != "example" {
			return nil, errors.New("routing")
		}
		return &reservationSession{}, nil
	}), 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, e := m.Open(ctx, reservationRequest())
	if e != nil || a.ID != "term-p0-1" || a.Shell != "bash" || a.Enhancement != "native" || a.Reason != "ready" || a.Capabilities != sessionCapabilitiesFor("bash", "native") || a.Rows != 24 || a.Cols != 80 {
		t.Fatalf("host=%#v %v", a, e)
	}
	cancel()
	if s.closes.Load() != 0 {
		t.Fatal("manager closed published ownership")
	}
	b, e := m.Open(context.Background(), StartRequest{Target: TargetContainer, ContainerID: "example", Rows: 30, Cols: 90})
	if e != nil || b.ID != "term-p0-2" || b.Target != TargetContainer || b.Shell != "shell" || b.Enhancement != "native" || b.Rows != 30 || b.Cols != 90 || host.Load() != 1 || container.Load() != 1 {
		t.Fatalf("container=%#v %v", b, e)
	}
	_ = m.Close(a.ID)
	_ = m.Close(b.ID)
	if s.closes.Load() != 1 {
		t.Fatal("published session cleanup not unique")
	}
	reservationCounts(t, m, 0, 0, 0)
}
