package httpapi

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
)

// 这里只验证 entry 的引用所有权，不依赖 GC 时机或声称物理擦除输入。
func assertJobRunnerReleased(t *testing.T, e *jobEntry) {
	t.Helper()
	e.opMu.Lock()
	defer e.opMu.Unlock()
	if e.run != nil {
		t.Fatal("entry still retains runner")
	}
}

func TestJobRunnerTransferredBeforeExecution(t *testing.T) {
	r, _ := manualQueueRegistry(nil)
	entered := make(chan struct{})
	release, unblock := queueTestRelease(t)
	var calls atomic.Int32
	j, err := r.submit("compose-deploy", "fixture", 0, func(_ context.Context, id string) {
		calls.Add(1)
		close(entered)
		<-release
		r.update(id, "completed", "done", "", 100)
	})
	if err != nil {
		t.Fatal(err)
	}
	e := r.lookup(j.ID)
	e.queue.pop()
	done := make(chan struct{})
	go func() { r.execute(e); close(done) }()
	queueReceive(t, entered)
	assertJobRunnerReleased(t, e)
	// 仍在执行时重放旧指针不能调用 nil，也不能执行第二次。
	r.execute(e)
	if calls.Load() != 1 || len(r.capacity) != 1 {
		t.Fatal("execution ownership changed")
	}
	unblock()
	queueReceive(t, done)
	assertJobRunnerReleased(t, e)
	r.execute(e)
	if e.snapshot().Status != "completed" || calls.Load() != 1 || len(r.capacity) != 0 {
		t.Fatal("local runner did not complete exactly once")
	}
}

func TestJobRunnerFrozenResultOnlyRetriesPersistence(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(fmt.Sprint(started), func(t *testing.T) {
			s := &snapshotJobStore{}
			r, _ := manualQueueRegistry(s)
			var calls atomic.Int32
			j, err := r.submit("compose-deploy", "fixture", 0, func(_ context.Context, id string) {
				calls.Add(1)
				s.mu.Lock()
				s.updateErr = errors.New("down")
				s.mu.Unlock()
				r.update(id, "completed", "done", "", 100)
			})
			if err != nil {
				t.Fatal(err)
			}
			e := r.lookup(j.ID)
			e.queue.pop()
			if started {
				r.execute(e)
			} else {
				s.mu.Lock()
				s.updateErr = errors.New("down")
				s.mu.Unlock()
				r.fail(j.ID, "FIXTURE", "failed", "detail")
			}
			assertJobRunnerReleased(t, e)
			if e.pending == nil || len(r.capacity) != 1 {
				t.Fatal("pending result lost its reservation")
			}
			frozen := cloneJobSnapshot(*e.pending)
			r.execute(e)
			r.flush()
			assertJobRunnerReleased(t, e)
			s.mu.Lock()
			s.updateErr = nil
			s.mu.Unlock()
			r.flush()
			r.execute(e)
			wantCalls := int32(0)
			if started {
				wantCalls = 1
			}
			if calls.Load() != wantCalls || e.pending != nil || len(r.capacity) != 0 || !sameJobRecord(recordFromSnapshot(frozen), recordFromSnapshot(e.snapshot())) {
				t.Fatal("retry changed result or replayed runner")
			}
		})
	}
}

func TestJobRunnerUndispatchedTerminalPaths(t *testing.T) {
	for _, path := range []string{"completed", "failed", "cancelled", "interrupted", "fail", "interrupt", "cancel", "timeout", "context", "closing", "running commit failure"} {
		t.Run(path, func(t *testing.T) {
			s := &snapshotJobStore{}
			r, c := manualQueueRegistry(s)
			var calls atomic.Int32
			e := submitQueueTest(t, r, "docker-image-pull", &calls)
			id := e.snapshot().ID
			// 模拟 worker 已取出，但尚未获取执行资格。
			e.queue.pop()
			switch path {
			case "fail":
				r.fail(id, "FIXTURE", "failed", "detail")
			case "interrupt":
				r.interrupt(id, "FIXTURE", "interrupted")
			case "cancel":
				if _, _, err := r.cancel(id); err != nil {
					t.Fatal(err)
				}
			case "timeout":
				c.advance(jobQueueTTL)
				r.expireQueued()
			case "context":
				e.cancel()
				r.execute(e)
			case "closing":
				r.admission.Lock()
				r.closing = true
				r.admission.Unlock()
				r.execute(e)
			case "running commit failure":
				s.mu.Lock()
				s.updateErr = errors.New("down")
				s.mu.Unlock()
				r.execute(e)
			default:
				r.update(id, path, "terminal", "", 0)
			}
			assertJobRunnerReleased(t, e)
			r.execute(e)
			s.mu.Lock()
			s.updateErr = nil
			s.mu.Unlock()
			r.flush()
			if calls.Load() != 0 || len(r.capacity) != 0 {
				t.Fatal("terminal path executed or leaked reservation")
			}
			if _, _, err := r.delete(id); err != nil {
				t.Fatal(err)
			}
			assertJobRunnerReleased(t, e)
			r.execute(e)
			if calls.Load() != 0 || r.lookup(id) != nil {
				t.Fatal("deleted pointer resurrected runner")
			}
		})
	}
}

func TestJobRunnerFailedCancellationRetainsExecution(t *testing.T) {
	s := &snapshotJobStore{}
	r, _ := manualQueueRegistry(s)
	var calls atomic.Int32
	e := submitQueueTest(t, r, "docker-image-pull", &calls)
	s.mu.Lock()
	s.updateErr = errors.New("down")
	s.mu.Unlock()
	if _, _, err := r.cancel(e.snapshot().ID); !errors.Is(err, errJobPersist) {
		t.Fatal(err)
	}
	e.opMu.Lock()
	retained := e.run != nil && !e.started && e.ctx.Err() == nil
	e.opMu.Unlock()
	if !retained {
		t.Fatal("unsaved cancellation discarded runner")
	}
	s.mu.Lock()
	s.updateErr = nil
	s.mu.Unlock()
	e.queue.pop()
	r.execute(e)
	assertJobRunnerReleased(t, e)
	if calls.Load() != 1 || e.snapshot().Status != "completed" {
		t.Fatal("failed cancellation prevented execution")
	}
}

func TestJobRunnerCreatingRelease(t *testing.T) {
	for _, mode := range []string{"failure", "expired", "expired failure", "closing", "expired closing"} {
		t.Run(mode, func(t *testing.T) {
			release, unblock := queueTestRelease(t)
			s := &queueInsertBarrierStore{entered: make(chan struct{}), release: release, fail: mode == "failure" || mode == "expired failure"}
			r, c := manualQueueRegistry(s)
			var calls atomic.Int32
			done := make(chan error, 1)
			go func() {
				_, err := r.submit("compose-deploy", "fixture", 0, func(context.Context, string) { calls.Add(1) })
				done <- err
			}()
			queueReceive(t, s.entered)
			active := r.activeEntries()
			if len(active) != 1 {
				t.Fatal("missing creating entry")
			}
			e := active[0]
			if mode == "expired" || mode == "expired failure" || mode == "expired closing" {
				c.advance(jobQueueTTL)
				r.expireQueued()
				assertJobRunnerReleased(t, e)
				if len(r.capacity) != 1 {
					t.Fatal("creating expiry released reservation too early")
				}
			}
			if mode == "closing" || mode == "expired closing" {
				r.admission.Lock()
				r.closing = true
				r.admission.Unlock()
			}
			unblock()
			err := queueReceive(t, done)
			if s.fail && !errors.Is(err, errJobPersist) || !s.fail && err != nil {
				t.Fatal(err)
			}
			assertJobRunnerReleased(t, e)
			r.flush()
			if calls.Load() != 0 || len(r.capacity) != 0 || len(r.activeEntries()) != 0 || r.compose.len() != 0 {
				t.Fatal("creating entry retained work")
			}
		})
	}
}

func TestJobRunnerCloseReleasesQueuedAndExecuting(t *testing.T) {
	r := newJobRegistry(nil)
	entered := make(chan struct{})
	release, unblock := queueTestRelease(t)
	queueCleanupRegistry(t, r, unblock)
	var calls atomic.Int32
	j, err := r.submit("compose-deploy", "running", 0, func(ctx context.Context, id string) {
		calls.Add(1)
		close(entered)
		<-ctx.Done()
		<-release
		r.update(id, "interrupted", "stopped", "", 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	queueReceive(t, entered)
	running := r.lookup(j.ID)
	assertJobRunnerReleased(t, running)
	queued := submitQueueTest(t, r, "compose-deploy", &calls)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Close(ctx) }()
	queueReceive(t, running.ctx.Done())
	unblock()
	if err := queueReceive(t, done); err != nil {
		t.Fatal(err)
	}
	assertJobRunnerReleased(t, queued)
	assertJobRunnerReleased(t, running)
	r.execute(queued)
	r.execute(running)
	if calls.Load() != 1 || len(r.capacity) != 0 {
		t.Fatal("close replayed or retained work")
	}
}

func TestJobRunnerSQLiteRecoveryHasNoExecutableReference(t *testing.T) {
	s, err := controlstore.Open(filepath.Join(t.TempDir(), "jobs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, c := manualQueueRegistry(s)
	var calls atomic.Int32
	expired := submitQueueTest(t, r, "compose-deploy", &calls)
	c.advance(jobQueueTTL)
	r.expireQueued()
	assertJobRunnerReleased(t, expired)
	r.flush()
	queued := submitQueueTest(t, r, "docker-image-pull", &calls)
	recovered := newJobRegistry(s)
	if recovered.initErr != nil {
		t.Fatal(recovered.initErr)
	}
	for _, id := range []string{expired.snapshot().ID, queued.snapshot().ID} {
		e := recovered.lookup(id)
		if e == nil {
			t.Fatal("recovery lost history")
		}
		assertJobRunnerReleased(t, e)
		recovered.execute(e)
		if !jobTerminal(e.snapshot().Status) {
			t.Fatal("recovery did not interrupt queued job")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("recovery replayed work")
	}
}
