package httpapi

import (
	"sync"
	"time"
)

const jobQueueTTL = 10 * time.Minute
const jobQueueTimeoutMessage = "排队等待超时，任务未开始执行"

// 队列只保存未取出的指针；锁内不等待 entry 锁，不执行持久化或回调。
// signal 仅作唤醒，非 payload；取出后接力唤醒其余 worker。
type jobQueue struct {
	mu     sync.Mutex
	items  []*jobEntry
	signal chan struct{}
}

func newJobQueue() *jobQueue { return &jobQueue{signal: make(chan struct{}, 1)} }
func (q *jobQueue) wake() {
	select {
	case q.signal <- struct{}{}:
	default:
	}
}
func (q *jobQueue) push(e *jobEntry) {
	q.mu.Lock()
	q.items = append(q.items, e)
	q.wake()
	q.mu.Unlock()
}
func (q *jobQueue) pop() *jobEntry {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return nil
	}
	e := q.items[0]
	q.items[0] = nil
	q.items = q.items[1:]
	if len(q.items) > 0 {
		q.wake()
	}
	return e
}
func (q *jobQueue) remove(e *jobEntry) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, item := range q.items {
		if item == e {
			copy(q.items[i:], q.items[i+1:])
			q.items[len(q.items)-1] = nil
			q.items = q.items[:len(q.items)-1]
			return
		}
	}
}
func (q *jobQueue) len() int { q.mu.Lock(); defer q.mu.Unlock(); return len(q.items) }

// 时钟与 tick 可独立注入；生产时间保留单调时钟部分用于排队期限。
type jobQueueClock interface {
	Now() time.Time
	NewTicker(time.Duration) jobQueueTicker
}
type jobQueueTicker interface {
	Chan() <-chan time.Time
	Stop()
}
type realJobQueueClock struct{}
type realJobQueueTicker struct{ *time.Ticker }

func (realJobQueueClock) Now() time.Time { return time.Now() }
func (realJobQueueClock) NewTicker(d time.Duration) jobQueueTicker {
	return realJobQueueTicker{time.NewTicker(d)}
}
func (t realJobQueueTicker) Chan() <-chan time.Time { return t.C }

func (r *jobRegistry) activeEntries() []*jobEntry {
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	entries := make([]*jobEntry, 0, len(r.active))
	for e := range r.active {
		entries = append(entries, e)
	}
	return entries
}
func (r *jobRegistry) detachLocked(e *jobEntry) {
	if e.queue != nil {
		e.queue.remove(e)
	}
	e.queued = false
}
func (r *jobRegistry) expired(e *jobEntry) bool {
	return !e.started && !e.deadline.IsZero() && !r.clock.Now().Before(e.deadline)
}

// expireLocked 只冻结本地结果；数据库堵塞不能阻塞其它任务的期限检查。
func (r *jobRegistry) expireLocked(e *jobEntry) bool {
	if e.deleted || e.started || e.pending != nil || jobTerminal(e.snapshot().Status) || !r.expired(e) {
		return false
	}
	r.detachLocked(e)
	j := e.snapshot()
	j.Status = "failed"
	j.ErrorCode = "JOB_QUEUE_TIMEOUT"
	j.Message = jobQueueTimeoutMessage
	j.Error = ""
	r.freezeLocked(e, j)
	return true
}
func (r *jobRegistry) expireQueued() {
	for _, e := range r.activeEntries() {
		if !e.opMu.TryLock() {
			continue
		}
		expired := r.expireLocked(e)
		e.opMu.Unlock()
		if expired && e.cancel != nil {
			e.cancel()
		}
	}
}
func (r *jobRegistry) expiryLoop() {
	defer close(r.expiryDone)
	ticker := r.clock.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.Chan():
			r.expireQueued()
		}
	}
}
