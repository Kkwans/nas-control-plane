package terminal

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// 每次 Start 独占一份对象；同容器目标也不复用 Session。
type capacitySession struct {
	mu                            sync.Mutex
	marker                        string
	reads                         int
	writes                        []string
	sizes                         [][2]uint16
	closes                        int
	metadata                      SessionMetadata
	metadataBarrier, closeBarrier *reservationBarrier
	closeError                    error
}

func (s *capacitySession) Read(ctx context.Context, p []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	return copy(p, s.marker), nil
}
func (s *capacitySession) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = append(s.writes, string(p))
	return len(p), nil
}
func (s *capacitySession) Resize(r, c uint16) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sizes = append(s.sizes, [2]uint16{r, c})
	return nil
}
func (s *capacitySession) Metadata() SessionMetadata {
	if s.metadataBarrier != nil {
		s.metadataBarrier.block()
	}
	return s.metadata
}
func (s *capacitySession) Close() error {
	s.mu.Lock()
	s.closes++
	s.mu.Unlock()
	if s.closeBarrier != nil {
		s.closeBarrier.block()
	}
	return s.closeError
}
func capacityRequest(i int) StartRequest {
	r := StartRequest{Target: TargetHost, Rows: uint16(24 + i), Cols: 80}
	if i%2 == 1 {
		r.Target = TargetContainer
		r.ContainerID = "same-container"
	}
	return r
}
func capacityExercise(t *testing.T, m *Manager, id string, s *capacitySession, n int) {
	t.Helper()
	buf := make([]byte, 100)
	got, err := m.Read(context.Background(), id, buf)
	if err != nil || string(buf[:got]) != s.marker {
		t.Fatalf("output cross-wired: %q %v", buf[:got], err)
	}
	text := fmt.Sprintf("input-%s-%d", s.marker, n)
	if err := m.Write(id, []byte(text)); err != nil {
		t.Fatal(err)
	}
	if err := m.Resize(id, uint16(30+n), uint16(90+n)); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.writes) != n || s.writes[n-1] != text || s.reads != n || len(s.sizes) != n || s.sizes[n-1] != ([2]uint16{uint16(30 + n), uint16(90 + n)}) {
		t.Fatalf("session %s lost operation isolation", s.marker)
	}
}

func TestTerminalCapacityFourMixedOwners(t *testing.T) {
	h := newReservationHarness(t)
	release := h.barrier()
	entered := make(chan struct{}, 4)
	var calls atomic.Int32
	sessions := make([]*capacitySession, 4)
	for i := range sessions {
		sessions[i] = &capacitySession{marker: fmt.Sprintf("output-%d", i), metadata: SessionMetadata{Shell: fmt.Sprintf("shell-%d", i), Enhancement: "native", Capabilities: SessionCapabilities{Resize: true, Readline: i&1 != 0, ANSIColors: i&2 != 0}}}
	}
	starter := func(target Target) Starter {
		return reservationStarter(func(_ context.Context, r StartRequest) (Session, error) {
			calls.Add(1)
			if r.Target != target {
				return nil, errors.New("wrong starter")
			}
			i := int(r.Rows) - 24
			if i < 0 || i >= 4 {
				return nil, errors.New("unexpected fifth starter")
			}
			entered <- struct{}{}
			release.block()
			return sessions[i], nil
		})
	}
	m := NewManager(starter(TargetHost), starter(TargetContainer), 4)
	pending := make([]<-chan reservationResult, 4)
	for i := range pending {
		pending[i] = h.run(func() reservationResult {
			v, e := m.Open(context.Background(), capacityRequest(i))
			return reservationResult{v, e}
		})
	}
	for range 4 {
		h.wait(entered)
	}
	reservationCounts(t, m, 4, 0, 0)
	reservationLimit(t, h, m)
	if calls.Load() != 4 {
		t.Fatal("fifth request reached starter")
	}
	release.unblock()
	ids := map[string]bool{}
	infos := make([]SessionInfo, 4)
	for i, c := range pending {
		r := h.result(c)
		if r.err != nil || r.info.ID == "" || ids[r.info.ID] || r.info.Target != capacityRequest(i).Target || r.info.Shell != sessions[i].metadata.Shell || r.info.Capabilities != sessions[i].metadata.Capabilities {
			t.Fatalf("publication %d: %+v", i, r)
		}
		ids[r.info.ID] = true
		infos[i] = r.info
		capacityExercise(t, m, r.info.ID, sessions[i], 1)
	}
	reservationCounts(t, m, 0, 4, 0)
	reservationLimit(t, h, m)
	readCtx, cancelRead := context.WithCancel(context.Background())
	cancelRead()
	if _, err := m.Read(readCtx, infos[1].ID, make([]byte, 16)); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消读取未保留错误: %v", err)
	}

	if err := m.Close(infos[1].ID); err != nil {
		t.Fatal(err)
	}
	if ErrorCode(m.Close(infos[1].ID)) != "TERMINAL_SESSION_NOT_FOUND" {
		t.Fatal("duplicate close succeeded")
	}
	if err := m.Write(infos[1].ID, []byte("retired")); ErrorCode(err) != "TERMINAL_SESSION_NOT_FOUND" {
		t.Fatal("旧 ID 仍可输入")
	}
	if err := m.Resize(infos[1].ID, 30, 90); ErrorCode(err) != "TERMINAL_SESSION_NOT_FOUND" {
		t.Fatal("旧 ID 仍可修改尺寸")
	}
	if _, err := m.Read(context.Background(), infos[1].ID, make([]byte, 16)); ErrorCode(err) != "TERMINAL_SESSION_NOT_FOUND" {
		t.Fatal("旧 ID 仍可输出")
	}

	for _, i := range []int{0, 2, 3} {
		capacityExercise(t, m, infos[i].ID, sessions[i], 2)
		sessions[i].mu.Lock()
		closed := sessions[i].closes
		sessions[i].mu.Unlock()
		if closed != 0 {
			t.Fatal("sibling closed")
		}
		if err := m.Close(infos[i].ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range sessions {
		s.mu.Lock()
		n := s.closes
		s.mu.Unlock()
		if n != 1 {
			t.Fatalf("close owners=%d", n)
		}
	}
	reservationCounts(t, m, 0, 0, 0)
}

func TestTerminalCapacityMixedOpeningLiveClosing(t *testing.T) {
	for _, mode := range []string{"late-success", "metadata-cancel", "session-and-error", "publish"} {
		t.Run(mode, func(t *testing.T) {
			h := newReservationHarness(t)
			start := h.barrier()
			metadata := h.barrier()
			closing := h.barrier()
			rejectedClose := h.barrier()
			var calls atomic.Int32
			cleanupErr := errors.New("fake close failed")
			primary := coded("TERMINAL_HOST_START_FAILED", errors.New("fake start failed"))
			sessions := make([]*capacitySession, 5)
			for i := range sessions {
				sessions[i] = &capacitySession{marker: fmt.Sprintf("owner-%d", i)}
			}
			sessions[2].closeBarrier = closing
			sessions[2].closeError = cleanupErr
			if mode != "publish" {
				sessions[3].closeBarrier = rejectedClose
				sessions[3].closeError = cleanupErr
			}
			if mode == "metadata-cancel" {
				sessions[3].metadataBarrier = metadata
			}
			starter := reservationStarter(func(_ context.Context, r StartRequest) (Session, error) {
				calls.Add(1)
				i := int(r.Rows) - 24
				if i == 3 {
					start.block()
					if mode == "session-and-error" {
						return sessions[i], primary
					}
				}
				return sessions[i], nil
			})
			m := NewManager(starter, starter, 4)
			infos := make([]SessionInfo, 3)
			for i := range infos {
				v, e := m.Open(context.Background(), capacityRequest(i))
				if e != nil {
					t.Fatal(e)
				}
				infos[i] = v
			}
			closeResult := h.run(func() reservationResult { return reservationResult{err: m.Close(infos[2].ID)} })
			h.wait(closing.entered)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			opening := h.run(func() reservationResult { v, e := m.Open(ctx, capacityRequest(3)); return reservationResult{v, e} })
			h.wait(start.entered)
			reservationCounts(t, m, 1, 2, 1)
			reservationLimit(t, h, m)
			if calls.Load() != 4 {
				t.Fatal("overflow reached starter")
			}
			if mode == "late-success" {
				cancel()
			}
			start.unblock()
			if mode == "metadata-cancel" {
				h.wait(metadata.entered)
				reservationCounts(t, m, 1, 2, 1)
				reservationLimit(t, h, m)
				cancel()
				metadata.unblock()
			}
			var published SessionInfo
			if mode != "publish" {
				h.wait(rejectedClose.entered)
				reservationCounts(t, m, 0, 2, 2)
				reservationLimit(t, h, m)
				for i := range 2 {
					capacityExercise(t, m, infos[i].ID, sessions[i], 1)
				}
				rejectedClose.unblock()
				r := h.result(opening)
				want := "TERMINAL_SESSION_CANCELED"
				if mode == "session-and-error" {
					want = "TERMINAL_HOST_START_FAILED"
				}
				if ErrorCode(r.err) != want || !errors.Is(r.err, cleanupErr) || !errors.Is(r.err, ErrSessionCleanupFailed) || r.info.ID != "" {
					t.Fatalf("rejected owner: %+v", r)
				}
			} else {
				r := h.result(opening)
				if r.err != nil {
					t.Fatal(r.err)
				}
				published = r.info
				cancel()
				reservationCounts(t, m, 0, 3, 1)
				reservationLimit(t, h, m)
				for i := range 2 {
					capacityExercise(t, m, infos[i].ID, sessions[i], 1)
				}
				if err := m.Close(published.ID); err != nil {
					t.Fatal(err)
				}
			}
			reservationCounts(t, m, 0, 2, 1)
			duplicate := h.result(h.run(func() reservationResult { return reservationResult{err: m.Close(infos[2].ID)} }))
			if ErrorCode(duplicate.err) != "TERMINAL_SESSION_NOT_FOUND" {
				t.Fatal("duplicate close had ownership")
			}
			replacement, e := m.Open(context.Background(), capacityRequest(4))
			if e != nil {
				t.Fatal(e)
			}
			for _, v := range infos {
				if replacement.ID == v.ID {
					t.Fatal("ID reused")
				}
			}
			if published.ID != "" && replacement.ID == published.ID {
				t.Fatal("published ID reused")
			}
			reservationCounts(t, m, 0, 3, 1)
			reservationLimit(t, h, m)
			closing.unblock()
			if r := h.result(closeResult); !errors.Is(r.err, cleanupErr) {
				t.Fatal("close error lost")
			}
			reservationCounts(t, m, 0, 3, 0)
			for i := range 2 {
				capacityExercise(t, m, infos[i].ID, sessions[i], 2)
				if err := m.Close(infos[i].ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.Close(replacement.ID); err != nil {
				t.Fatal(err)
			}
			for _, s := range sessions {
				s.mu.Lock()
				n := s.closes
				s.mu.Unlock()
				if n != 1 {
					t.Fatalf("owner %s closed %d times", s.marker, n)
				}
			}
			reservationCounts(t, m, 0, 0, 0)
		})
	}
}
