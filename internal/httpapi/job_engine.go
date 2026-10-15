package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
)

var (
	errJobPersist     = errors.New("job persistence failed")
	errJobUnavailable = errors.New("job engine unavailable")
	errJobQueueFull   = errors.New("job queue full")
	errJobActive      = errors.New("active job cannot be deleted")
	errJobCancel      = errors.New("job cannot be cancelled")
)

const jobStoreTimeout = 3 * time.Second
const jobCapacity = 32

type jobPersistence interface {
	CreateJob(context.Context, controlstore.JobRecord) error
	UpdateJob(context.Context, controlstore.JobRecord, int64) (int64, error)
	DeleteJobRevision(context.Context, string, int64) error
	Job(context.Context, string) (controlstore.JobRecord, error)
	Jobs(context.Context, string, int) ([]controlstore.JobRecord, error)
	RecoverInterruptedJobs(context.Context) error
}

type jobEntry struct {
	opMu     sync.Mutex
	viewMu   sync.RWMutex
	view     jobSnapshot
	revision int64
	dirty    uint64
	saved    uint64
	pending  *jobSnapshot
	deleted  bool
	owner    bool
	queued   bool
	release  func()
	ctx      context.Context
	cancel   context.CancelFunc
	run      func(context.Context, string)
}

func (e *jobEntry) snapshot() jobSnapshot {
	e.viewMu.RLock()
	defer e.viewMu.RUnlock()
	return cloneJobSnapshot(e.view)
}

type jobRegistry struct {
	indexLocks     [64]sync.Mutex
	admissions     sync.WaitGroup
	admissionsDone chan struct{}
	mu             sync.RWMutex
	jobs           map[string]*jobEntry
	store          jobPersistence
	initErr        error
	admission      sync.Mutex
	stopped        bool
	once           sync.Once
	stop           chan struct{}
	done           chan struct{}
	workers        sync.WaitGroup
	pulls          chan *jobEntry
	removals       chan *jobEntry
	compose        chan *jobEntry
	capacity       chan struct{}
	closeOnce      sync.Once
	workersDone    chan struct{}
	contextMu      sync.RWMutex
	storeParent    context.Context
}

// JobEngine 必须由单一 server 持有；启动恢复不会重放任何远端操作。
type JobEngine = jobRegistry

func NewJobEngine(store any) (*JobEngine, error) {
	r := newJobRegistry(store)
	return r, r.initErr
}
func newJobRegistry(store any) *jobRegistry {
	r := &jobRegistry{admissionsDone: make(chan struct{}), jobs: map[string]*jobEntry{}, stop: make(chan struct{}), done: make(chan struct{}), pulls: make(chan *jobEntry, jobCapacity), removals: make(chan *jobEntry, jobCapacity), compose: make(chan *jobEntry, jobCapacity), capacity: make(chan struct{}, jobCapacity), workersDone: make(chan struct{}), storeParent: context.Background()}
	if store == nil {
		return r
	}
	var ok bool
	r.store, ok = store.(jobPersistence)
	if !ok {
		r.initErr = errJobUnavailable
		return r
	}
	ctx, cancel := r.storeContext()
	defer cancel()
	if err := r.store.RecoverInterruptedJobs(ctx); err != nil {
		r.initErr = errJobUnavailable
		return r
	}
	records, err := r.store.Jobs(ctx, "", 500)
	if err != nil {
		r.initErr = errJobUnavailable
		return r
	}
	for _, record := range records {
		if !validJobLayers(record.LayersJSON) {
			r.initErr = errJobUnavailable
			return r
		}
		r.jobs[record.ID] = &jobEntry{view: snapshotFromRecord(record), revision: record.Revision}
	}
	return r
}
func validJobLayers(raw string) bool {
	if raw == "" || raw == "[]" {
		return true
	}
	var layers map[string]jobLayer
	return json.Unmarshal([]byte(raw), &layers) == nil
}
func (r *jobRegistry) readiness() string {
	if r.initErr != nil {
		return "unavailable"
	}
	r.admission.Lock()
	stopped := r.stopped
	r.admission.Unlock()
	if stopped {
		return "unavailable"
	}
	for _, e := range r.entries() {
		if e.snapshot().PersistenceStatus != "" {
			return "degraded"
		}
	}
	return "ready"
}
func (r *jobRegistry) storeContext() (context.Context, context.CancelFunc) {
	r.contextMu.RLock()
	parent := r.storeParent
	r.contextMu.RUnlock()
	return context.WithTimeout(parent, jobStoreTimeout)
}
func (r *jobRegistry) lookup(id string) *jobEntry {
	r.mu.RLock()
	e := r.jobs[id]
	r.mu.RUnlock()
	return e
}
func (r *jobRegistry) indexLock(id string) *sync.Mutex {
	var hash uint64
	for i := 0; i < len(id); i++ {
		hash = hash*31 + uint64(id[i])
	}
	return &r.indexLocks[hash%64]
}
func (r *jobRegistry) hydrate(id string) *jobEntry {
	lock := r.indexLock(id)
	lock.Lock()
	defer lock.Unlock()
	return r.hydrateLocked(id)
}
func (r *jobRegistry) hydrateLocked(id string) *jobEntry {
	if e := r.lookup(id); e != nil {
		return e
	}
	if r.store == nil || r.initErr != nil {
		return nil
	}
	ctx, cancel := r.storeContext()
	record, err := r.store.Job(ctx, id)
	cancel()
	if err != nil {
		return nil
	}
	e := &jobEntry{view: snapshotFromRecord(record), revision: record.Revision}
	r.mu.Lock()
	if existing := r.jobs[id]; existing != nil {
		e = existing
	} else {
		r.jobs[id] = e
	}
	r.mu.Unlock()
	return e
}

// 仅淘汰已保存且无执行 owner 的终态；历史仍可按 ID 从数据库读取。
func (r *jobRegistry) trimHistory() {
	if r.store == nil {
		return
	}
	entries := r.entries()
	if len(entries) <= 500 {
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].snapshot().CreatedAt.Before(entries[j].snapshot().CreatedAt) })
	excess := len(entries) - 500
	for _, e := range entries {
		if excess <= 0 {
			return
		}
		indexLock := r.indexLock(e.snapshot().ID)
		if !indexLock.TryLock() {
			continue
		}
		if !e.opMu.TryLock() {
			indexLock.Unlock()
			continue
		}
		j := e.snapshot()
		if jobTerminal(j.Status) && !e.owner && !e.queued && e.pending == nil {
			r.mu.Lock()
			if r.jobs[j.ID] == e {
				delete(r.jobs, j.ID)
				excess--
			}
			r.mu.Unlock()
		}
		e.opMu.Unlock()
		indexLock.Unlock()
	}
}
func (r *jobRegistry) entries() []*jobEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*jobEntry, 0, len(r.jobs))
	for _, e := range r.jobs {
		out = append(out, e)
	}
	return out
}
func jobTerminal(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled" || status == "interrupted"
}
func recordFromSnapshot(j jobSnapshot) controlstore.JobRecord {
	layers, _ := json.Marshal(j.Layers)
	record := controlstore.JobRecord{ID: j.ID, Type: j.Type, Status: j.Status, Reference: j.Reference, Message: j.Message, Error: j.Error, Progress: j.Progress, DownloadedBytes: j.DownloadedBytes, TotalBytes: j.TotalBytes, SpeedBytes: j.SpeedBytes, LayersJSON: string(layers), CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt, CancellationStatus: j.CancellationStatus, ErrorCode: j.ErrorCode}
	if j.CompletedAt != nil {
		record.CompletedAt = *j.CompletedAt
	}
	return record
}

func copyJobProgress(to *jobSnapshot, from jobSnapshot) {
	to.Layers = from.Layers
	to.DownloadedBytes = from.DownloadedBytes
	to.TotalBytes = from.TotalBytes
	to.SpeedBytes = from.SpeedBytes
	to.Progress = from.Progress
}
func sameJobRecord(a, b controlstore.JobRecord) bool {
	a.CreatedAt = a.CreatedAt.Truncate(time.Second)
	b.CreatedAt = b.CreatedAt.Truncate(time.Second)
	a.UpdatedAt = a.UpdatedAt.Truncate(time.Second)
	b.UpdatedAt = b.UpdatedAt.Truncate(time.Second)
	a.CompletedAt = a.CompletedAt.Truncate(time.Second)
	b.CompletedAt = b.CompletedAt.Truncate(time.Second)
	return reflect.DeepEqual(a, b)
}

// commit 在每个任务的 opMu 内执行；索引锁和 viewMu 不跨越数据库 I/O。
func (r *jobRegistry) commit(e *jobEntry, j jobSnapshot) error {
	e.viewMu.RLock()
	generation := e.dirty
	if !jobTerminal(j.Status) {
		copyJobProgress(&j, e.view)
		j = cloneJobSnapshot(j)
	}
	e.viewMu.RUnlock()
	if e.deleted {
		return errJobUnavailable
	}
	if r.store != nil {
		ctx, cancel := r.storeContext()
		revision, err := r.store.UpdateJob(ctx, recordFromSnapshot(j), e.revision)
		cancel()
		if err != nil {
			// 提交结果不确定时，只接受完全匹配的目标版本；绝不盲目换版本重试旧快照。
			checkCtx, stop := r.storeContext()
			durable, readErr := r.store.Job(checkCtx, j.ID)
			stop()
			intended := recordFromSnapshot(j)
			intended.Revision = e.revision + 1
			if readErr != nil || !sameJobRecord(durable, intended) {
				return errJobPersist
			}
			revision = durable.Revision
		}
		e.revision = revision
	}
	e.viewMu.Lock()
	j.PersistenceStatus = ""
	if e.dirty != generation && !jobTerminal(j.Status) {
		j.Layers = e.view.Layers
		j.DownloadedBytes = e.view.DownloadedBytes
		j.TotalBytes = e.view.TotalBytes
		j.SpeedBytes = e.view.SpeedBytes
		j.Progress = e.view.Progress
	}
	e.view = cloneJobSnapshot(j)
	e.saved = generation
	e.viewMu.Unlock()
	return nil
}
func (r *jobRegistry) create(kind, reference string) jobSnapshot {
	j, _ := r.createRecord(kind, reference, 0, nil)
	return j
}
func (r *jobRegistry) createRecord(kind, reference string, total int64, run func(context.Context, string)) (jobSnapshot, error) {
	now := time.Now().UTC()
	ctx, cancel := context.WithCancel(context.Background())
	j := jobSnapshot{ID: newJobID(), Type: kind, Reference: reference, Status: "queued", Message: "任务已进入队列", CreatedAt: now, UpdatedAt: now, TotalBytes: max(0, total), Layers: map[string]jobLayer{}, ArtifactState: jobArtifactUnknown, Cancellable: kind == "docker-image-pull"}
	e := &jobEntry{view: j, revision: 1, ctx: ctx, cancel: cancel, run: run, queued: run != nil}
	if run != nil {
		e.release = func() { <-r.capacity }
	}
	if r.store != nil {
		storeCtx, stop := r.storeContext()
		err := r.store.CreateJob(storeCtx, recordFromSnapshot(j))
		stop()
		if err != nil {
			checkCtx, finish := r.storeContext()
			durable, readErr := r.store.Job(checkCtx, j.ID)
			finish()
			intended := recordFromSnapshot(j)
			intended.Revision = 1
			if readErr != nil || !sameJobRecord(durable, intended) {
				cancel()
				return jobSnapshot{}, errJobPersist
			}
		}
	}
	r.mu.Lock()
	r.jobs[j.ID] = e
	r.mu.Unlock()
	return cloneJobSnapshot(j), nil
}
func (r *jobRegistry) start() {
	r.once.Do(func() {
		for i := 0; i < 3; i++ {
			r.workers.Add(1)
			go r.worker(r.pulls)
		}
		r.workers.Add(1)
		go r.worker(r.compose)
		r.workers.Add(1)
		go r.worker(r.removals)
		go r.flushLoop()
		go func() { r.workers.Wait(); close(r.workersDone) }()
	})
}
func (r *jobRegistry) submit(kind, reference string, total int64, run func(context.Context, string)) (jobSnapshot, error) {
	if kind != "docker-image-pull" && kind != "compose-deploy" && kind != imageRemoveJobType || run == nil {
		return jobSnapshot{}, errJobUnavailable
	}
	r.admission.Lock()
	if r.stopped || r.initErr != nil {
		r.admission.Unlock()
		return jobSnapshot{}, errJobUnavailable
	}
	for _, e := range r.entries() {
		if e.snapshot().PersistenceStatus != "" {
			r.admission.Unlock()
			return jobSnapshot{}, errJobPersist
		}
	}
	select {
	case r.capacity <- struct{}{}:
	default:
		r.admission.Unlock()
		return jobSnapshot{}, errJobQueueFull
	}
	r.admissions.Add(1)
	r.admission.Unlock()
	defer r.admissions.Done()
	j, err := r.createRecord(kind, reference, total, run)
	if err != nil {
		<-r.capacity
		return j, err
	}
	e := r.lookup(j.ID)
	r.start()
	if kind == "compose-deploy" {
		r.compose <- e
	} else if kind == imageRemoveJobType {
		r.removals <- e
	} else {
		r.pulls <- e
	}
	return j, nil
}
func (r *jobRegistry) release(e *jobEntry) {
	if e.release != nil && !e.owner && !e.queued {
		e.release()
		e.release = nil
	}
}
func (r *jobRegistry) worker(queue <-chan *jobEntry) {
	defer r.workers.Done()
	for {
		select {
		case <-r.stop:
			return
		case e := <-queue:
			e.opMu.Lock()
			e.queued = false
			j := e.snapshot()
			if jobTerminal(j.Status) {
				r.release(e)
				e.opMu.Unlock()
				continue
			}
			if e.ctx.Err() != nil {
				j.Status = "interrupted"
				j.Message = "控制面停止，任务未开始"
				r.finishLocked(e, j)
				e.opMu.Unlock()
				continue
			}
			j.Status = "running"
			j.Message = "正在执行任务"
			j.UpdatedAt = time.Now().UTC()
			if err := r.commit(e, j); err != nil {
				j = e.snapshot()
				j.Status = "interrupted"
				j.Message = "任务未启动，状态保存失败"
				j.ErrorCode = "JOB_PERSIST_FAILED"
				r.finishLocked(e, j)
				e.opMu.Unlock()
				continue
			}
			e.owner = true
			e.opMu.Unlock()
			e.run(e.ctx, j.ID)
			e.opMu.Lock()
			e.owner = false
			if jobTerminal(e.snapshot().Status) {
				r.release(e)
			}
			e.cancel()
			e.opMu.Unlock()
		}
	}
}
func (r *jobRegistry) finishLocked(e *jobEntry, j jobSnapshot) {
	now := time.Now().UTC()
	e.viewMu.Lock()
	copyJobProgress(&j, e.view)
	j = cloneJobSnapshot(j)
	j.UpdatedAt = now
	j.CompletedAt = &now
	j.SpeedBytes = 0
	if j.Status == "interrupted" && j.CancellationStatus == "requested" {
		j.CancellationStatus = "unknown"
	}
	if j.Status == "completed" {
		j.Progress = 100
	}
	if j.Status == "completed" && j.Type == "docker-image-pull" {
		j.ArtifactState = jobArtifactPresent
		j.DownloadedBytes = max(j.DownloadedBytes, j.TotalBytes)
		j.Progress = 100
	}
	e.pending = &j
	e.viewMu.Unlock()
	if r.commit(e, j) == nil {
		e.viewMu.Lock()
		e.pending = nil
		e.viewMu.Unlock()
		r.release(e)
	} else {
		e.viewMu.Lock()
		e.view.PersistenceStatus = "pending"
		e.view.Message = "执行已返回，结果保存失败，正在重试"
		e.view.UpdatedAt = now
		e.viewMu.Unlock()
	}
}
func (r *jobRegistry) update(id, status, message, errorMessage string, progress int) {
	e := r.lookup(id)
	if e == nil {
		return
	}
	e.opMu.Lock()
	defer e.opMu.Unlock()
	j := e.snapshot()
	if jobTerminal(j.Status) || e.deleted || e.pending != nil {
		return
	}
	j.Status = status
	j.Message = message
	j.Error = errorMessage
	j.Progress = progress
	j.UpdatedAt = time.Now().UTC()
	if status == "interrupted" {
		j.Progress = e.snapshot().Progress
	}
	if jobTerminal(status) {
		if status == "interrupted" && j.CancellationStatus == "requested" {
			j.CancellationStatus = "unknown"
		}
		r.finishLocked(e, j)
	} else {
		_ = r.commit(e, j)
	}
}
func (r *jobRegistry) interrupt(id, code, message string) {
	e := r.lookup(id)
	if e == nil {
		return
	}
	e.opMu.Lock()
	defer e.opMu.Unlock()
	j := e.snapshot()
	if jobTerminal(j.Status) || e.pending != nil {
		return
	}
	j.Status = "interrupted"
	j.ErrorCode = code
	j.Message = message
	if j.CancellationStatus == "requested" {
		j.CancellationStatus = "unknown"
	}
	r.finishLocked(e, j)
}

// fail 沿用终态持久化与版本检查；保存失败时不提前公开终态，也不覆盖待保存结果。
func (r *jobRegistry) fail(id, code, message, detail string) {
	e := r.lookup(id)
	if e == nil {
		return
	}
	e.opMu.Lock()
	defer e.opMu.Unlock()
	j := e.snapshot()
	if jobTerminal(j.Status) || e.deleted || e.pending != nil {
		return
	}
	j.Status = "failed"
	j.ErrorCode = code
	j.Message = message
	j.Error = detail
	r.finishLocked(e, j)
}

func (r *jobRegistry) cancel(id string) (jobSnapshot, bool, error) {
	e := r.hydrate(id)
	if e == nil {
		return jobSnapshot{}, false, nil
	}
	e.opMu.Lock()
	j := e.snapshot()
	if e.deleted {
		e.opMu.Unlock()
		return jobSnapshot{}, false, nil
	}
	if j.Status == "cancelled" || (j.CancellationStatus == "requested" && !jobTerminal(j.Status)) {
		e.opMu.Unlock()
		return j, true, nil
	}
	if !j.Cancellable || jobTerminal(j.Status) || e.pending != nil {
		e.opMu.Unlock()
		return j, true, errJobCancel
	}
	j.UpdatedAt = time.Now().UTC()
	j.CancellationStatus = "requested"
	j.Message = "取消请求已接收，远端执行状态尚未确认"
	if j.Status == "queued" {
		j.Status = "cancelled"
		j.CancellationStatus = "confirmed"
		j.Message = "排队任务已取消，未开始执行"
		j.CompletedAt = &j.UpdatedAt
		j.SpeedBytes = 0
	}
	if err := r.commit(e, j); err != nil {
		e.opMu.Unlock()
		return e.snapshot(), true, err
	}
	cancel := e.cancel
	if jobTerminal(j.Status) {
		r.release(e)
	}
	e.opMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return cloneJobSnapshot(j), true, nil
}
func (r *jobRegistry) updatePullProgress(id string, layer jobLayer) {
	e := r.lookup(id)
	if e == nil {
		return
	}
	e.viewMu.Lock()
	defer e.viewMu.Unlock()
	if jobTerminal(e.view.Status) || e.pending != nil || e.deleted {
		return
	}
	j := &e.view
	if layer.ID == "" {
		layer.ID = "status"
	}
	if j.Layers == nil {
		j.Layers = map[string]jobLayer{}
	}
	previous := j.Layers[layer.ID]
	if layer.Current <= 0 {
		layer.Current = previous.Current
	}
	if layer.Total <= 0 {
		layer.Total = previous.Total
	}
	if (layer.Status == "Pull complete" || layer.Status == "Already exists") && layer.Total > 0 {
		layer.Current = layer.Total
	}
	j.Layers[layer.ID] = layer
	var current, total int64
	for _, v := range j.Layers {
		current += v.Current
		total += v.Total
	}
	if elapsed := time.Since(j.UpdatedAt).Seconds(); elapsed > 0 {
		j.SpeedBytes = max(0, int64(float64(current-j.DownloadedBytes)/elapsed))
	}
	j.DownloadedBytes = max(current, j.DownloadedBytes)
	j.TotalBytes = max(total, j.TotalBytes)
	if j.TotalBytes > 0 {
		j.Progress = min(99, max(1, int(float64(j.DownloadedBytes)/float64(j.TotalBytes)*100)))
	}
	if j.CancellationStatus != "requested" {
		j.Message = layer.Status
	}
	j.UpdatedAt = time.Now().UTC()
	e.dirty++
}
func (r *jobRegistry) get(id string) (jobSnapshot, bool) {
	e := r.lookup(id)
	if e != nil {
		return normalizeJobSnapshot(e.snapshot()), true
	}
	if r.store != nil && r.initErr == nil {
		ctx, cancel := r.storeContext()
		defer cancel()
		record, err := r.store.Job(ctx, id)
		if err == nil {
			return normalizeJobSnapshot(snapshotFromRecord(record)), true
		}
	}
	return jobSnapshot{}, false
}
func (r *jobRegistry) list(kind string) []jobSnapshot {
	out := []jobSnapshot{}
	for _, e := range r.entries() {
		j := e.snapshot()
		if kind == "" || kind == j.Type {
			out = append(out, normalizeJobSnapshot(j))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}
func (r *jobRegistry) delete(id string) (jobSnapshot, bool, error) {
	lock := r.indexLock(id)
	lock.Lock()
	defer lock.Unlock()
	e := r.hydrateLocked(id)
	if e == nil {
		return jobSnapshot{}, false, nil
	}
	e.opMu.Lock()
	defer e.opMu.Unlock()
	j := e.snapshot()
	if e.deleted {
		return jobSnapshot{}, false, nil
	}
	if !jobTerminal(j.Status) || e.owner || e.queued || e.pending != nil {
		return j, true, errJobActive
	}
	if r.store != nil {
		ctx, cancel := r.storeContext()
		err := r.store.DeleteJobRevision(ctx, id, e.revision)
		cancel()
		if err != nil {
			checkCtx, finish := r.storeContext()
			_, readErr := r.store.Job(checkCtx, id)
			finish()
			if !errors.Is(readErr, controlstore.ErrJobNotFound) {
				return j, true, errJobPersist
			}
		}
	}
	e.viewMu.Lock()
	e.deleted = true
	e.viewMu.Unlock()
	r.mu.Lock()
	delete(r.jobs, id)
	r.mu.Unlock()
	return j, true, nil
}
func (r *jobRegistry) flush() {
	for _, e := range r.entries() {
		e.opMu.Lock()
		if e.pending != nil {
			j := cloneJobSnapshot(*e.pending)
			if r.commit(e, j) == nil {
				e.viewMu.Lock()
				e.pending = nil
				e.viewMu.Unlock()
				r.release(e)
			}
		} else {
			e.viewMu.RLock()
			dirty := e.dirty != e.saved
			e.viewMu.RUnlock()
			if dirty && !jobTerminal(e.snapshot().Status) {
				_ = r.commit(e, e.snapshot())
			}
		}
		e.opMu.Unlock()
	}
}
func (r *jobRegistry) flushLoop() {
	defer close(r.done)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.flush()
			r.trimHistory()
		case <-r.stop:
			return
		}
	}
}
func (r *jobRegistry) StopAdmission() { r.admission.Lock(); r.stopped = true; r.admission.Unlock() }
func lockJobContext(ctx context.Context, mu *sync.Mutex) bool {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return false
		}
		if mu.TryLock() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}
func (r *jobRegistry) Close(ctx context.Context) error {
	r.contextMu.Lock()
	r.storeParent = ctx
	r.contextMu.Unlock()
	r.StopAdmission()
	r.start()
	r.closeOnce.Do(func() {
		go func() {
			r.admissions.Wait()
			for _, e := range r.entries() {
				if e.cancel != nil {
					e.cancel()
				}
			}
			close(r.stop)
			close(r.admissionsDone)
		}()
	})
	select {
	case <-r.admissionsDone:
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case <-r.workersDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-r.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	for _, e := range r.entries() {
		if !lockJobContext(ctx, &e.opMu) {
			return ctx.Err()
		}
		e.queued = false
		r.release(e)
		j := e.snapshot()
		if !jobTerminal(j.Status) && e.pending == nil {
			j.Status = "interrupted"
			j.Message = "控制面停止，执行结果未确认"
			r.finishLocked(e, j)
		}
		if e.pending != nil {
			j := cloneJobSnapshot(*e.pending)
			if r.commit(e, j) == nil {
				e.viewMu.Lock()
				e.pending = nil
				e.viewMu.Unlock()
				r.release(e)
			}
		}
		e.opMu.Unlock()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, e := range r.entries() {
		if e.snapshot().PersistenceStatus != "" {
			return errJobPersist
		}
	}
	return nil
}
func (api *handler) writeJobError(w http.ResponseWriter, request *http.Request, err error) {
	code := "JOB_PERSIST_FAILED"
	message := "任务状态保存失败，请稍后重试。"
	if errors.Is(err, errJobQueueFull) {
		code = "JOB_QUEUE_FULL"
		message = "任务队列已满，请稍后重试。"
	}
	if errors.Is(err, errJobUnavailable) {
		code = "JOB_ENGINE_UNAVAILABLE"
		message = "任务服务暂不可用。"
	}
	api.writeError(w, request, http.StatusServiceUnavailable, code, message)
}
