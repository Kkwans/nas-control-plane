package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
)

type snapshotJobStore struct {
	createCommittedError, deleteCommittedError           bool
	committedError                                       bool
	mu                                                   sync.Mutex
	records                                              map[string]controlstore.JobRecord
	createErr, updateErr, deleteErr, recoverErr, listErr error
	beforeUpdate                                         func(controlstore.JobRecord)
}

func (s *snapshotJobStore) CreateJob(_ context.Context, j controlstore.JobRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createErr != nil {
		return s.createErr
	}
	if s.records == nil {
		s.records = map[string]controlstore.JobRecord{}
	}
	if _, ok := s.records[j.ID]; ok {
		return controlstore.ErrJobExists
	}
	j.Revision = 1
	s.records[j.ID] = j
	if s.createCommittedError {
		return errors.New("create acknowledgement lost")
	}
	return nil
}
func (s *snapshotJobStore) UpdateJob(_ context.Context, j controlstore.JobRecord, revision int64) (int64, error) {
	if s.beforeUpdate != nil {
		s.beforeUpdate(j)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.updateErr != nil {
		return 0, s.updateErr
	}
	old, ok := s.records[j.ID]
	if !ok {
		return 0, controlstore.ErrJobNotFound
	}
	if old.Revision != revision {
		return 0, controlstore.ErrJobConflict
	}
	j.Revision = revision + 1
	s.records[j.ID] = j
	if s.committedError {
		return 0, errors.New("commit acknowledgement lost")
	}
	return j.Revision, nil
}
func (s *snapshotJobStore) DeleteJobRevision(_ context.Context, id string, revision int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteErr != nil {
		return s.deleteErr
	}
	old, ok := s.records[id]
	if !ok {
		return controlstore.ErrJobNotFound
	}
	if old.Revision != revision {
		return controlstore.ErrJobConflict
	}
	delete(s.records, id)
	if s.deleteCommittedError {
		return errors.New("delete acknowledgement lost")
	}
	return nil
}
func (s *snapshotJobStore) Job(_ context.Context, id string) (controlstore.JobRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.records[id]
	if !ok {
		return j, controlstore.ErrJobNotFound
	}
	return j, nil
}
func (s *snapshotJobStore) Jobs(context.Context, string, int) ([]controlstore.JobRecord, error) {
	return nil, s.listErr
}
func (s *snapshotJobStore) RecoverInterruptedJobs(context.Context) error { return s.recoverErr }

func TestJobSnapshotEscapeIsolation(t *testing.T) {
	for _, method := range []string{"get", "list", "cancel", "delete"} {
		t.Run(method, func(t *testing.T) {
			r := newJobRegistry(nil)
			created := r.create("docker-image-pull", "fixture")
			r.updatePullProgress(created.ID, jobLayer{ID: "layer", Current: 1, Total: 100})
			if method != "cancel" {
				r.update(created.ID, "completed", "done", "", 100)
			}
			e := r.lookup(created.ID)
			var j jobSnapshot
			switch method {
			case "get":
				j, _ = r.get(created.ID)
			case "list":
				j = r.list("")[0]
			case "cancel":
				j, _, _ = r.cancel(created.ID)
			case "delete":
				j, _, _ = r.delete(created.ID)
			}
			before := e.snapshot()
			j.Layers["layer"] = jobLayer{Current: 99}
			*j.CompletedAt = j.CompletedAt.Add(time.Hour)
			after := e.snapshot()
			if after.Layers["layer"].Current != before.Layers["layer"].Current || !after.CompletedAt.Equal(*before.CompletedAt) {
				t.Fatal("escaped snapshot alias")
			}
		})
	}
}
func TestJobSnapshotCreateIsolation(t *testing.T) {
	r := newJobRegistry(nil)
	j := r.create("docker-image-pull", "fixture")
	r.updatePullProgress(j.ID, jobLayer{ID: "layer", Current: 1})
	if len(j.Layers) != 0 {
		t.Fatal("create alias")
	}
	j.Layers["external"] = jobLayer{}
	current, _ := r.get(j.ID)
	if _, ok := current.Layers["external"]; ok {
		t.Fatal("create escaped")
	}
}
func TestJobSnapshotConcurrentProgressJSONAndPersistence(t *testing.T) {
	store := &snapshotJobStore{}
	r := newJobRegistry(store)
	j := r.create("docker-image-pull", "fixture")
	r.update(j.ID, "running", "running", "", 0)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for n := 0; n < 7; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			for i := 0; i < 100; i++ {
				if n == 0 {
					r.updatePullProgress(j.ID, jobLayer{ID: fmt.Sprint(i % 16), Current: int64(i), Total: 100})
				} else if n == 1 {
					r.flush()
				} else {
					if n == 2 {
						cancelled, _, _ := r.cancel(j.ID)
						_, _ = json.Marshal(cancelled)
					}
					v, _ := r.get(j.ID)
					_, _ = json.Marshal(v)
					v.Layers["external"] = jobLayer{}
					for _, v := range r.list("") {
						_, _ = json.Marshal(v)
					}
				}
			}
		}(n)
	}
	close(start)
	wg.Wait()
	r.update(j.ID, "completed", "done", "", 100)
	r.updatePullProgress(j.ID, jobLayer{ID: "late"})
	record, _ := store.Job(context.Background(), j.ID)
	if record.Status != "completed" {
		t.Fatal(record.Status)
	}
	var layers map[string]jobLayer
	_ = json.Unmarshal([]byte(record.LayersJSON), &layers)
	if _, ok := layers["external"]; ok {
		t.Fatal("escaped persisted alias")
	}
	if _, ok := layers["late"]; ok {
		t.Fatal("late progress")
	}
}
func TestJobCreationAndRecoveryFailClosed(t *testing.T) {
	store := &snapshotJobStore{createErr: errors.New("failure")}
	r := newJobRegistry(store)
	_, err := r.submit("docker-image-pull", "fixture", 0, func(context.Context, string) { t.Error("must not execute") })
	if !errors.Is(err, errJobPersist) || len(r.list("")) != 0 || len(r.capacity) != 0 {
		t.Fatal("creation did not fail closed")
	}
	for _, s := range []*snapshotJobStore{{recoverErr: errors.New("failure")}, {listErr: errors.New("failure")}} {
		if _, err := NewJobEngine(s); err == nil {
			t.Fatal("startup failure hidden")
		}
	}
}
func TestJobPendingOutcomeRetriesWithoutExecution(t *testing.T) {
	store := &snapshotJobStore{}
	r := newJobRegistry(store)
	j := r.create("docker-image-pull", "fixture")
	r.update(j.ID, "running", "running", "", 1)
	store.updateErr = errors.New("failure")
	r.update(j.ID, "completed", "done", "", 100)
	current, _ := r.get(j.ID)
	if current.Status != "running" || current.PersistenceStatus != "pending" || current.CompletedAt != nil {
		t.Fatalf("premature terminal: %+v", current)
	}
	store.updateErr = nil
	r.flush()
	current, _ = r.get(j.ID)
	if current.Status != "completed" || current.PersistenceStatus != "" {
		t.Fatal(current)
	}
	r.update(j.ID, "failed", "late", "", 0)
	current, _ = r.get(j.ID)
	if current.Status != "completed" {
		t.Fatal("terminal regressed")
	}
}
func TestJobCancelPersistsBeforeSignalAndReportsUnknown(t *testing.T) {
	store := &snapshotJobStore{}
	r := newJobRegistry(store)
	j := r.create("docker-image-pull", "fixture")
	r.update(j.ID, "running", "running", "", 1)
	e := r.lookup(j.ID)
	called := false
	e.cancel = func() { called = true; r.updatePullProgress(j.ID, jobLayer{ID: "last", Current: 2}) }
	store.updateErr = errors.New("failure")
	if _, _, err := r.cancel(j.ID); !errors.Is(err, errJobPersist) || called {
		t.Fatal("cancel before persistence")
	}
	store.updateErr = nil
	cancelled, _, err := r.cancel(j.ID)
	if err != nil || !called || cancelled.Status != "running" || cancelled.CompletedAt != nil || cancelled.CancellationStatus != "requested" {
		t.Fatal(cancelled, err)
	}
	if _, _, err := r.delete(j.ID); !errors.Is(err, errJobActive) {
		t.Fatal(err)
	}
	r.update(j.ID, "interrupted", "remote unknown", "", 0)
	final, _ := r.get(j.ID)
	if final.CancellationStatus != "unknown" || final.Status != "interrupted" {
		t.Fatal(final)
	}
}
func TestJobDeleteNeverResurrects(t *testing.T) {
	store := &snapshotJobStore{}
	r := newJobRegistry(store)
	j := r.create("docker-image-pull", "fixture")
	r.update(j.ID, "completed", "done", "", 100)
	store.deleteErr = errors.New("failure")
	if _, _, err := r.delete(j.ID); err == nil {
		t.Fatal("delete failure hidden")
	}
	if _, ok := r.get(j.ID); !ok {
		t.Fatal("failed delete removed entry")
	}
	store.deleteErr = nil
	if _, _, err := r.delete(j.ID); err != nil {
		t.Fatal(err)
	}
	r.updatePullProgress(j.ID, jobLayer{ID: "late"})
	r.update(j.ID, "running", "late", "", 0)
	if _, ok := r.get(j.ID); ok {
		t.Fatal("resurrection")
	}
}
func TestJobStorageDoesNotHoldRegistryLock(t *testing.T) {
	store := &snapshotJobStore{}
	r := newJobRegistry(store)
	a := r.create("docker-image-pull", "a")
	b := r.create("docker-image-pull", "b")
	entered := make(chan struct{})
	release := make(chan struct{})
	store.beforeUpdate = func(j controlstore.JobRecord) {
		if j.ID == a.ID {
			close(entered)
			<-release
		}
	}
	done := make(chan struct{})
	go func() { r.update(a.ID, "running", "running", "", 1); close(done) }()
	<-entered
	if _, ok := r.get(b.ID); !ok {
		t.Fatal("missing other job")
	}
	r.list("")
	close(release)
	<-done
}

func TestJobQueueCancellationKeepsReservationsBounded(t *testing.T) {
	r := newJobRegistry(nil)
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	for i := 0; i < 3; i++ {
		if _, err := r.submit("docker-image-pull", "blocking", 0, func(ctx context.Context, id string) {
			started <- struct{}{}
			<-release
			r.update(id, "completed", "done", "", 100)
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		<-started
	}
	for i := 0; i < jobCapacity*3; i++ {
		j, err := r.submit("docker-image-pull", "queued", 0, func(context.Context, string) { t.Error("cancelled queued task executed") })
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.cancel(j.ID); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.capacity) != 3 || r.pulls.len() != 0 {
		t.Fatalf("cancelled queue retained capacity: %d", len(r.capacity))
	}
	for i := 0; i < jobCapacity-3; i++ {
		if _, err := r.submit("docker-image-pull", "queued", 0, func(context.Context, string) {}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.submit("docker-image-pull", "overflow", 0, func(context.Context, string) {}); !errors.Is(err, errJobQueueFull) {
		t.Fatal(err)
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.capacity) != 0 {
		t.Fatalf("leaked reservations: %d", len(r.capacity))
	}
}
func TestJobCancelThenTrustedCompletionCannotCancelAgain(t *testing.T) {
	r := newJobRegistry(nil)
	j := r.create("docker-image-pull", "fixture")
	r.update(j.ID, "running", "running", "", 1)
	if _, _, err := r.cancel(j.ID); err != nil {
		t.Fatal(err)
	}
	r.update(j.ID, "completed", "done", "", 100)
	if _, _, err := r.cancel(j.ID); !errors.Is(err, errJobCancel) {
		t.Fatal(err)
	}
}
func TestJobCloseHonorsUnresponsiveExecutorBudget(t *testing.T) {
	r := newJobRegistry(nil)
	started := make(chan struct{})
	release := make(chan struct{})
	_, err := r.submit("docker-image-pull", "fixture", 0, func(context.Context, string) { close(started); <-release })
	if err != nil {
		t.Fatal(err)
	}
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := r.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-r.workersDone:
	case <-time.After(time.Second):
		t.Fatal("workers leaked")
	}
}
func TestJobHistoricRecordCanBeDeleted(t *testing.T) {
	s := &snapshotJobStore{records: map[string]controlstore.JobRecord{"old": {ID: "old", Status: "completed", Revision: 1}}}
	r := newJobRegistry(s)
	if _, ok := r.get("old"); !ok {
		t.Fatal("GET miss")
	}
	if _, ok, err := r.delete("old"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if _, ok := r.get("old"); ok {
		t.Fatal("delete resurrected")
	}
}

func TestJobUncertainCommitReadbackAcceptsOnlyMatchingOutcome(t *testing.T) {
	s := &snapshotJobStore{}
	r := newJobRegistry(s)
	j := r.create("docker-image-pull", "fixture")
	s.committedError = true
	r.update(j.ID, "running", "running", "", 1)
	current, _ := r.get(j.ID)
	if current.Status != "running" {
		t.Fatal(current)
	}
	r.update(j.ID, "completed", "done", "", 100)
	current, _ = r.get(j.ID)
	if current.Status != "completed" || current.PersistenceStatus != "" {
		t.Fatal(current)
	}
}
func TestJobProgressDuringStoreIOIsNotLost(t *testing.T) {
	s := &snapshotJobStore{}
	r := newJobRegistry(s)
	j := r.create("docker-image-pull", "fixture")
	entered := make(chan struct{})
	release := make(chan struct{})
	s.beforeUpdate = func(controlstore.JobRecord) { close(entered); <-release }
	done := make(chan struct{})
	go func() { r.update(j.ID, "running", "running", "", 1); close(done) }()
	<-entered
	r.updatePullProgress(j.ID, jobLayer{ID: "late", Current: 9, Total: 10})
	close(release)
	<-done
	s.beforeUpdate = nil
	r.flush()
	current, _ := r.get(j.ID)
	if current.DownloadedBytes != 9 {
		t.Fatal(current)
	}
	stored, _ := s.Job(context.Background(), j.ID)
	if stored.DownloadedBytes != 9 {
		t.Fatal(stored)
	}
}
func TestComposeCompletedProgressIsOneHundred(t *testing.T) {
	r := newJobRegistry(nil)
	j := r.create("compose-deploy", "fixture")
	r.update(j.ID, "completed", "done", "", 100)
	current, _ := r.get(j.ID)
	if current.Progress != 100 || current.Cancellable {
		t.Fatal(current)
	}
}

func TestJobHTTPAllCreationPathsRejectFailedInsert(t *testing.T) {
	for _, kind := range []string{"pull", "retry", "compose"} {
		t.Run(kind, func(t *testing.T) {
			s := &snapshotJobStore{}
			r := newJobRegistry(s)
			api := &handler{jobs: r}
			var serve http.HandlerFunc
			body := "{}"
			request := httptest.NewRequest(http.MethodPost, "/fixture", nil)
			switch kind {
			case "pull":
				request.Header.Set("Content-Type", "application/json")
				serve = api.pullDockerImage
				body = `{"reference":"fixture:latest"}`
			case "compose":
				request.Header.Set("Content-Type", "application/json")
				serve = api.deployComposeConfig
				body = `{"projectId":"fixture","baseContentHash":"0000000000000000000000000000000000000000000000000000000000000000"}`
			case "retry":
				previous := r.create("docker-image-pull", "fixture")
				r.update(previous.ID, "completed", "done", "", 100)
				serve = api.retryJob
				route := chi.NewRouteContext()
				route.URLParams.Add("jobID", previous.ID)
				request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, route))
			}
			s.createErr = errors.New("fixture unavailable")
			request.Body = http.NoBody
			if body != "" {
				request.Body = io.NopCloser(strings.NewReader(body))
			}
			response := httptest.NewRecorder()
			serve(response, request)
			if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "JOB_PERSIST_FAILED") {
				t.Fatal(response.Code, response.Body.String())
			}
			if len(r.capacity) != 0 {
				t.Fatal("admission leaked")
			}
		})
	}
}

type jobEventWriter struct {
	header   http.Header
	buffer   strings.Builder
	events   chan string
	deadline time.Time
}

func (w *jobEventWriter) Header() http.Header { return w.header }
func (w *jobEventWriter) WriteHeader(int)     {}
func (w *jobEventWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}
func (w *jobEventWriter) Write(p []byte) (int, error) {
	if w.deadline.IsZero() || !w.deadline.After(time.Now()) {
		return 0, context.DeadlineExceeded
	}
	return w.buffer.Write(p)
}

// fake 始终非阻塞，并要求每帧同时为 Write 和 Flush 设置预算。
func (w *jobEventWriter) FlushError() error {
	if w.deadline.IsZero() || !w.deadline.After(time.Now()) {
		return context.DeadlineExceeded
	}
	select {
	case w.events <- w.buffer.String():
		w.buffer.Reset()
		return nil
	default:
		return io.ErrShortWrite
	}
}
func TestJobSSEEmitsTerminalAfterPendingAtSameTimestamp(t *testing.T) {
	s := &snapshotJobStore{}
	r := newJobRegistry(s)
	j := r.create("docker-image-pull", "fixture")
	r.update(j.ID, "running", "running", "", 0)
	s.updateErr = errors.New("failure")
	r.update(j.ID, "completed", "done", "", 100)
	route := chi.NewRouteContext()
	route.URLParams.Add("jobID", j.ID)
	request := httptest.NewRequest(http.MethodGet, "/fixture", nil)
	ctx, cancel := context.WithCancel(context.WithValue(request.Context(), chi.RouteCtxKey, route))
	defer cancel()
	request = request.WithContext(ctx)
	writer := &jobEventWriter{header: http.Header{}, events: make(chan string, 4)}
	done := make(chan struct{})
	go func() { (&handler{jobs: r}).jobEvents(writer, request); close(done) }()
	select {
	case payload := <-writer.events:
		if !strings.Contains(payload, `"persistenceStatus":"pending"`) {
			t.Fatal(payload)
		}
	case <-time.After(time.Second):
		t.Fatal("pending event missing")
	}
	s.mu.Lock()
	s.updateErr = nil
	s.mu.Unlock()
	r.flush()
	select {
	case payload := <-writer.events:
		if !strings.Contains(payload, `"status":"completed"`) {
			t.Fatal(payload)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal event missing")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE did not close")
	}
}

func TestJobUncertainCreateAndDeleteReadback(t *testing.T) {
	s := &snapshotJobStore{createCommittedError: true}
	r := newJobRegistry(s)
	j, err := r.createRecord("docker-image-pull", "fixture", 0, nil)
	if err != nil || j.ID == "" {
		t.Fatal(j, err)
	}
	r.update(j.ID, "completed", "done", "", 100)
	s.deleteCommittedError = true
	if _, ok, err := r.delete(j.ID); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if _, ok := r.get(j.ID); ok {
		t.Fatal("phantom record after committed delete")
	}
}
func TestJobCorruptHistoryValidation(t *testing.T) {
	for _, raw := range []string{"{", `[{"id":"bad"}]`, `"bad"`} {
		if validJobLayers(raw) {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{"[]", "{}", "", `{"layer":{"id":"layer"}}`} {
		if !validJobLayers(raw) {
			t.Fatal(raw)
		}
	}
}

func TestJobReadinessDegradesUntilPendingResultSaved(t *testing.T) {
	s := &snapshotJobStore{}
	r := newJobRegistry(s)
	j := r.create("docker-image-pull", "fixture")
	s.updateErr = errors.New("failure")
	r.update(j.ID, "completed", "done", "", 100)
	api := NewHandler(Config{Agent: &fakeAgentClient{}, JobEngine: r})
	w := httptest.NewRecorder()
	api.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), `"jobs":"degraded"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	s.updateErr = nil
	r.flush()
	w = httptest.NewRecorder()
	api.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"jobs":"ready"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestJobTransportErrorsNeverProveExecutionFailure(t *testing.T) {
	for _, err := range []error{io.EOF, context.Canceled, context.DeadlineExceeded, errors.New("connection reset")} {
		if !jobRPCOutcomeUnknown(err) {
			t.Fatal(err)
		}
	}
}
