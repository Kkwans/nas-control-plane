package httpapi

import (
	"context"
	"errors"
	"fmt"
	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Barrier失败时有界结束；cleanup始终释放阻塞的fake存储/worker。
func queueTestRelease(t *testing.T) (chan struct{}, func()) {
	t.Helper()
	ch := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(ch) }) }
	t.Cleanup(release)
	return ch, release
}
func queueReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case value := <-ch:
		return value
	case <-timer.C:
		t.Fatal("barrier deadline exceeded")
		var zero T
		return zero
	}
}

func queueCleanupRegistry(t *testing.T, r *jobRegistry, unblock func()) {
	t.Helper()
	t.Cleanup(func() {
		unblock()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := r.Close(ctx); err != nil {
			t.Errorf("queue cleanup: %v", err)
		}
	})
}

type queueTestClock struct {
	mu    sync.Mutex
	now   time.Time
	ticks chan time.Time
}

func newQueueTestClock() *queueTestClock {
	return &queueTestClock{now: time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC), ticks: make(chan time.Time, 1)}
}
func (c *queueTestClock) Now() time.Time                         { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *queueTestClock) advance(d time.Duration)                { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }
func (c *queueTestClock) NewTicker(time.Duration) jobQueueTicker { return queueTestTicker{c.ticks} }

type queueTestTicker struct{ ticks <-chan time.Time }

func (t queueTestTicker) Chan() <-chan time.Time { return t.ticks }
func (queueTestTicker) Stop()                    {}
func manualQueueRegistry(store any) (*jobRegistry, *queueTestClock) {
	clock := newQueueTestClock()
	r := newJobRegistryWithClock(store, clock)
	// 手动推进worker/flusher，所有边界由fake clock/barrier决定。
	r.once.Do(func() {})
	return r, clock
}
func submitQueueTest(t *testing.T, r *jobRegistry, kind string, calls *atomic.Int32) *jobEntry {
	t.Helper()
	j, err := r.submit(kind, "fixture", 0, func(_ context.Context, id string) { calls.Add(1); r.update(id, "completed", "done", "", 100) })
	if err != nil {
		t.Fatal(err)
	}
	return r.lookup(j.ID)
}
func TestJobQueueDeadlineBoundaries(t *testing.T) {
	for _, kind := range []string{"docker-image-pull", "compose-deploy", imageRemoveJobType} {
		for _, delta := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
			t.Run(fmt.Sprintf("%s/%s", kind, delta), func(t *testing.T) {
				r, c := manualQueueRegistry(nil)
				var calls atomic.Int32
				e := submitQueueTest(t, r, kind, &calls)
				c.advance(jobQueueTTL + delta)
				e.queue.pop()
				r.execute(e)
				r.flush()
				j := e.snapshot()
				if delta < 0 {
					if calls.Load() != 1 || j.Status != "completed" {
						t.Fatalf("before: %+v", j)
					}
				} else if calls.Load() != 0 || j.Status != "failed" || j.ErrorCode != "JOB_QUEUE_TIMEOUT" || j.Message != jobQueueTimeoutMessage {
					t.Fatalf("expired: %+v calls=%d", j, calls.Load())
				}
				if len(r.capacity) != 0 || len(r.activeEntries()) != 0 {
					t.Fatal("reservation leaked")
				}
			})
		}
	}
}
func TestJobQueueExpiryIndependentAndBounded(t *testing.T) {
	r, c := manualQueueRegistry(nil)
	var calls atomic.Int32
	for cycle := 0; cycle < 4; cycle++ {
		for i := 0; i < jobCapacity; i++ {
			submitQueueTest(t, r, []string{"docker-image-pull", "compose-deploy", imageRemoveJobType}[i%3], &calls)
		}
		if _, err := r.submit("compose-deploy", "full", 0, func(context.Context, string) {}); !errors.Is(err, errJobQueueFull) {
			t.Fatal(err)
		}
		c.advance(jobQueueTTL)
		r.expireQueued()
		if calls.Load() != 0 || r.pulls.len()+r.compose.len()+r.removals.len() != 0 {
			t.Fatal("expiry did not detach")
		}
		if len(r.capacity) != jobCapacity {
			t.Fatal("released before durable terminal")
		}
		r.flush()
		if len(r.capacity) != 0 || len(r.activeEntries()) != 0 {
			t.Fatal("leaked")
		}
	}
}
func TestJobQueueRunningCommitCrossesDeadline(t *testing.T) {
	for _, ackLost := range []bool{false, true} {
		t.Run(fmt.Sprint(ackLost), func(t *testing.T) {
			entered := make(chan struct{})
			release, unblock := queueTestRelease(t)
			store := &snapshotJobStore{committedError: ackLost, beforeUpdate: func(j controlstore.JobRecord) {
				if j.Status == "running" {
					close(entered)
					<-release
				}
			}}
			r, c := manualQueueRegistry(store)
			var calls atomic.Int32
			e := submitQueueTest(t, r, "compose-deploy", &calls)
			e.queue.pop()
			done := make(chan struct{})
			go func() { r.execute(e); close(done) }()
			queueReceive(t, entered)
			c.advance(jobQueueTTL)
			r.expireQueued()
			unblock()
			queueReceive(t, done)
			r.flush()
			if calls.Load() != 0 || e.snapshot().ErrorCode != "JOB_QUEUE_TIMEOUT" || len(r.capacity) != 0 {
				t.Fatalf("crossing ran: %+v", e.snapshot())
			}
		})
	}
}
func TestJobQueuePendingFailureAndConflict(t *testing.T) {
	for _, mode := range []string{"failure", "ack", "conflict", "missing"} {
		t.Run(mode, func(t *testing.T) {
			s := &snapshotJobStore{}
			r, c := manualQueueRegistry(s)
			var calls atomic.Int32
			e := submitQueueTest(t, r, "compose-deploy", &calls)
			s.mu.Lock()
			switch mode {
			case "failure":
				s.updateErr = errors.New("down")
			case "ack":
				s.committedError = true
			case "conflict":
				j := s.records[e.snapshot().ID]
				j.Revision++
				s.records[j.ID] = j
			case "missing":
				delete(s.records, e.snapshot().ID)
			}
			s.mu.Unlock()
			c.advance(jobQueueTTL)
			r.expireQueued()
			frozen := *e.pending.CompletedAt
			r.flush()
			if mode == "ack" {
				if len(r.capacity) != 0 || e.snapshot().Status != "failed" {
					t.Fatal("ack not recovered")
				}
				return
			}
			if len(r.capacity) != 1 || e.snapshot().Status != "queued" || e.snapshot().ErrorCode != "" || e.snapshot().CompletedAt != nil || e.pending == nil {
				t.Fatalf("unconfirmed terminal: %+v", e.snapshot())
			}
			r.execute(e)
			if calls.Load() != 0 {
				t.Fatal("pending executed")
			}
			if mode == "failure" {
				s.mu.Lock()
				s.updateErr = nil
				s.mu.Unlock()
				c.advance(time.Hour)
				r.flush()
				if len(r.capacity) != 0 || !e.snapshot().CompletedAt.Equal(frozen) {
					t.Fatal("frozen result changed")
				}
			}
		})
	}
}
func TestJobQueueCancelExpiryAndOldPointer(t *testing.T) {
	for _, expireFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(expireFirst), func(t *testing.T) {
			r, c := manualQueueRegistry(nil)
			var calls atomic.Int32
			e := submitQueueTest(t, r, "docker-image-pull", &calls)
			e.queue.pop()
			if expireFirst {
				c.advance(jobQueueTTL)
				r.expireQueued()
			}
			_, _, err := r.cancel(e.snapshot().ID)
			if expireFirst && !errors.Is(err, errJobCancel) {
				t.Fatal(err)
			}
			if !expireFirst && err != nil {
				t.Fatal(err)
			}
			c.advance(jobQueueTTL)
			r.expireQueued()
			r.flush()
			want := "cancelled"
			if expireFirst {
				want = "failed"
			}
			if e.snapshot().Status != want {
				t.Fatal(e.snapshot())
			}
			if _, _, err := r.delete(e.snapshot().ID); err != nil {
				t.Fatal(err)
			}
			r.execute(e)
			r.flush()
			if calls.Load() != 0 || len(r.list("")) != 0 || len(r.capacity) != 0 {
				t.Fatal("old pointer resurrected")
			}
		})
	}
}

type queueInsertBarrierStore struct {
	snapshotJobStore
	entered, release chan struct{}
	fail             bool
}

func (s *queueInsertBarrierStore) CreateJob(ctx context.Context, j controlstore.JobRecord) error {
	close(s.entered)
	<-s.release
	if s.fail {
		return errors.New("insert failed")
	}
	return s.snapshotJobStore.CreateJob(ctx, j)
}
func TestJobQueueInsertCrossesDeadline(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			release, unblock := queueTestRelease(t)
			s := &queueInsertBarrierStore{entered: make(chan struct{}), release: release, fail: fail}
			r, c := manualQueueRegistry(s)
			var calls atomic.Int32
			done := make(chan error, 1)
			go func() {
				_, err := r.submit("compose-deploy", "slow insert", 0, func(context.Context, string) { calls.Add(1) })
				done <- err
			}()
			queueReceive(t, s.entered)
			c.advance(jobQueueTTL)
			r.expireQueued()
			r.flush()
			if len(r.list("")) != 0 || len(r.capacity) != 1 {
				t.Fatal("phantom/released INSERT")
			}
			unblock()
			err := queueReceive(t, done)
			if fail {
				if !errors.Is(err, errJobPersist) || len(r.list("")) != 0 || len(r.activeEntries()) != 0 || len(r.capacity) != 0 {
					t.Fatal("failed insert leaked")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			r.flush()
			if calls.Load() != 0 || r.compose.len() != 0 || len(r.capacity) != 0 || r.list("")[0].ErrorCode != "JOB_QUEUE_TIMEOUT" {
				t.Fatal("slow insert executed")
			}
		})
	}
}
func TestJobQueueSQLiteRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	s, err := controlstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r, c := manualQueueRegistry(s)
	var calls atomic.Int32
	expired := submitQueueTest(t, r, "compose-deploy", &calls)
	c.advance(jobQueueTTL)
	r.expireQueued()
	r.flush()
	pending := submitQueueTest(t, r, "docker-image-pull", &calls)
	c.advance(jobQueueTTL)
	r.expireQueued()
	s.Close()
	s, err = controlstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	recovered := newJobRegistry(s)
	a, _ := recovered.get(expired.snapshot().ID)
	b, _ := recovered.get(pending.snapshot().ID)
	if a.Status != "failed" || a.ErrorCode != "JOB_QUEUE_TIMEOUT" || b.Status != "interrupted" || len(recovered.capacity) != 0 || calls.Load() != 0 {
		t.Fatalf("recovery %+v %+v", a, b)
	}
}
func TestJobQueueStopAdmissionDoesNotStopAccepted(t *testing.T) {
	r, _ := manualQueueRegistry(nil)
	var calls atomic.Int32
	e := submitQueueTest(t, r, "compose-deploy", &calls)
	r.StopAdmission()
	e.queue.pop()
	r.execute(e)
	if calls.Load() != 1 {
		t.Fatal("StopAdmission stopped accepted job")
	}
}

func queueAwait(t *testing.T, condition func() bool) {
	t.Helper()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	for !condition() {
		select {
		case <-timeout.C:
			t.Fatal("condition did not converge")
		default:
			runtime.Gosched()
		}
	}
}
func TestJobQueueTickerExpiresWhileEveryWorkerBlocked(t *testing.T) {
	c := newQueueTestClock()
	r := newJobRegistryWithClock(nil, c)
	started := make(chan struct{}, 5)
	release, unblock := queueTestRelease(t)
	queueCleanupRegistry(t, r, unblock)
	var calls atomic.Int32
	for _, kind := range []string{"docker-image-pull", "docker-image-pull", "docker-image-pull", "compose-deploy", imageRemoveJobType} {
		_, err := r.submit(kind, "blocked", 0, func(_ context.Context, id string) {
			started <- struct{}{}
			<-release
			r.update(id, "completed", "done", "", 100)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		queueReceive(t, started)
	}
	entries := []*jobEntry{}
	for _, kind := range []string{"docker-image-pull", "compose-deploy", imageRemoveJobType} {
		entries = append(entries, submitQueueTest(t, r, kind, &calls))
	}
	c.advance(jobQueueTTL)
	c.ticks <- c.Now()
	queueAwait(t, func() bool {
		for _, e := range entries {
			j := e.snapshot()
			if j.PersistenceStatus != "pending" && j.Status != "failed" {
				return false
			}
		}
		return true
	})
	r.flush()
	if len(r.capacity) != 5 || calls.Load() != 0 || r.pulls.len()+r.compose.len()+r.removals.len() != 0 {
		t.Fatal("blocked workers prevented reclamation")
	}
	unblock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.capacity) != 0 || len(r.activeEntries()) != 0 {
		t.Fatal("owner reservation leaked")
	}
	select {
	case <-r.expiryDone:
	default:
		t.Fatal("expiry loop did not stop")
	}
}
func TestJobQueueExpirySkipsBlockedPersistence(t *testing.T) {
	entered := make(chan struct{})
	release, unblock := queueTestRelease(t)
	s := &snapshotJobStore{beforeUpdate: func(j controlstore.JobRecord) {
		if j.Reference == "blocked" {
			close(entered)
			<-release
		}
	}}
	r, c := manualQueueRegistry(s)
	var calls atomic.Int32
	j, err := r.submit("compose-deploy", "blocked", 0, func(context.Context, string) { calls.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	first := r.lookup(j.ID)
	first.queue.pop()
	done := make(chan struct{})
	go func() { r.execute(first); close(done) }()
	queueReceive(t, entered)
	second := submitQueueTest(t, r, "docker-image-pull", &calls)
	c.advance(jobQueueTTL)
	r.expireQueued()
	if second.snapshot().PersistenceStatus != "pending" || r.pulls.len() != 0 {
		t.Fatal("blocked persistence stalled expiry")
	}
	unblock()
	queueReceive(t, done)
}

type queueReadBarrierStore struct {
	snapshotJobStore
	entered, release chan struct{}
	once             sync.Once
}

func (s *queueReadBarrierStore) Job(ctx context.Context, id string) (controlstore.JobRecord, error) {
	s.once.Do(func() { close(s.entered); <-s.release })
	return s.snapshotJobStore.Job(ctx, id)
}
func TestJobQueueRunningReadbackCrossesDeadline(t *testing.T) {
	release, unblock := queueTestRelease(t)
	s := &queueReadBarrierStore{snapshotJobStore: snapshotJobStore{committedError: true}, entered: make(chan struct{}), release: release}
	r, c := manualQueueRegistry(s)
	var calls atomic.Int32
	e := submitQueueTest(t, r, imageRemoveJobType, &calls)
	e.queue.pop()
	done := make(chan struct{})
	go func() { r.execute(e); close(done) }()
	queueReceive(t, s.entered)
	c.advance(jobQueueTTL)
	unblock()
	queueReceive(t, done)
	r.flush()
	if calls.Load() != 0 || e.snapshot().ErrorCode != "JOB_QUEUE_TIMEOUT" || len(r.capacity) != 0 {
		t.Fatal("readback started expired job")
	}
}
func TestJobQueueCloseDuringInsertPreservesTimeout(t *testing.T) {
	release, unblock := queueTestRelease(t)
	s := &queueInsertBarrierStore{entered: make(chan struct{}), release: release}
	c := newQueueTestClock()
	r := newJobRegistryWithClock(s, c)
	queueCleanupRegistry(t, r, unblock)
	var calls atomic.Int32
	submitted := make(chan jobSnapshot, 1)
	go func() {
		j, err := r.submit("compose-deploy", "slow", 0, func(context.Context, string) { calls.Add(1) })
		if err != nil {
			t.Error(err)
		}
		submitted <- j
	}()
	queueReceive(t, s.entered)
	c.advance(jobQueueTTL)
	r.expireQueued()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	unblock()
	receipt := queueReceive(t, submitted)
	if receipt.Status != "queued" || receipt.PersistenceStatus != "" || receipt.Message != "任务已进入队列" || receipt.ErrorCode != "" || receipt.CompletedAt != nil || !receipt.UpdatedAt.Equal(receipt.CreatedAt) {
		t.Fatalf("receipt changed: %+v", receipt)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	j, _ := r.get(receipt.ID)
	if calls.Load() != 0 || j.ErrorCode != "JOB_QUEUE_TIMEOUT" || len(r.capacity) != 0 {
		t.Fatalf("close replaced timeout: %+v", j)
	}
}

func TestJobQueueFailedCancelCanExpire(t *testing.T) {
	s := &snapshotJobStore{}
	r, c := manualQueueRegistry(s)
	var calls atomic.Int32
	e := submitQueueTest(t, r, "docker-image-pull", &calls)
	s.mu.Lock()
	s.updateErr = errors.New("down")
	s.mu.Unlock()
	if _, _, err := r.cancel(e.snapshot().ID); !errors.Is(err, errJobPersist) {
		t.Fatal(err)
	}
	if e.ctx.Err() != nil || !e.queued {
		t.Fatal("failed cancellation took effect")
	}
	c.advance(jobQueueTTL)
	r.expireQueued()
	s.mu.Lock()
	s.updateErr = nil
	s.mu.Unlock()
	r.flush()
	if e.snapshot().ErrorCode != "JOB_QUEUE_TIMEOUT" || e.snapshot().CancellationStatus != "" || len(r.capacity) != 0 || calls.Load() != 0 {
		t.Fatal("failed cancellation prevented expiry")
	}
}
func TestJobQueueCloseReleasesDurableQueuedTerminal(t *testing.T) {
	r, _ := manualQueueRegistry(nil)
	var calls atomic.Int32
	e := submitQueueTest(t, r, "compose-deploy", &calls)
	r.fail(e.snapshot().ID, "TEST_FAILURE", "fixture", "")
	if len(r.capacity) != 1 || !e.queued {
		t.Fatal("fixture not queued")
	}
	// 手动模式没有循环，明确提供已停止信号以只验证Close最终清理。
	close(r.workersDone)
	close(r.done)
	close(r.expiryDone)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.capacity) != 0 || r.compose.len() != 0 || len(r.activeEntries()) != 0 {
		t.Fatal("durable terminal token leaked")
	}
}

func TestJobQueueCloseDuringRunningCommitPreventsAuthorization(t *testing.T) {
	entered := make(chan struct{})
	release, unblock := queueTestRelease(t)
	s := &snapshotJobStore{beforeUpdate: func(j controlstore.JobRecord) {
		if j.Status == "running" {
			close(entered)
			<-release
		}
	}}
	c := newQueueTestClock()
	r := newJobRegistryWithClock(s, c)
	queueCleanupRegistry(t, r, unblock)
	var calls atomic.Int32
	j, err := r.submit(imageRemoveJobType, "closing", 0, func(context.Context, string) { calls.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	queueReceive(t, entered)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	unblock()
	ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	current, _ := r.get(j.ID)
	if calls.Load() != 0 || current.Status != "interrupted" || len(r.capacity) != 0 {
		t.Fatalf("closing authorized execution: %+v", current)
	}
}
