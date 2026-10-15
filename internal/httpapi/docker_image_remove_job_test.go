package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/Kkwans/nas-control-plane/internal/docker"
)

const removeJobImageID = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type removeJobAgent struct {
	imageListTimeoutAgent
	calls  atomic.Int32
	remove func(context.Context, docker.ImageRemoveBatchRequest) (docker.ImageRemoveBatchResult, error)
}

func (a *removeJobAgent) RemoveDockerImages(ctx context.Context, _ string, request docker.ImageRemoveBatchRequest) (docker.ImageRemoveBatchResult, error) {
	a.calls.Add(1)
	return a.remove(ctx, request)
}
func removeJobSuccess() docker.ImageRemoveBatchResult {
	return docker.ImageRemoveBatchResult{Items: []docker.ImageRemoveBatchItem{{ImageID: removeJobImageID, Removed: true}}, RemovedCount: 1, Completed: true}
}
func removeJobRequest(id string) *http.Request {
	body, _ := json.Marshal(docker.ImageRemoveRequest{ImageID: id})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/docker/images/remove-job", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	return request
}
func closeRemoveJobs(t *testing.T, jobs *jobRegistry) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := jobs.Close(ctx); err != nil {
		t.Errorf("close jobs: %v", err)
	}
}
func waitRemoveJob(t *testing.T, jobs *jobRegistry, id string, predicate func(jobSnapshot) bool) jobSnapshot {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		j, ok := jobs.get(id)
		if ok && predicate(j) {
			return j
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatalf("job did not reach expected state: %+v", j)
		}
	}
}

func TestImageRemoveJobIdentity(t *testing.T) {
	for _, id := range []string{"", "alpine:latest", "0123456789ab", strings.TrimPrefix(removeJobImageID, "sha256:"), " " + removeJobImageID, removeJobImageID + " ", strings.ToUpper(removeJobImageID), removeJobImageID[:70], removeJobImageID + "0", "sha256:" + strings.Repeat("g", 64)} {
		if validImageRemoveJobID(id) {
			t.Errorf("accepted unsafe identity %q", id)
		}
	}
	if !validImageRemoveJobID(removeJobImageID) {
		t.Fatal("rejected canonical identity")
	}
}

func TestImageRemoveJobReceiptOutcomes(t *testing.T) {
	tests := []struct {
		name         string
		change       func(*docker.ImageRemoveBatchResult)
		status, code string
	}{
		{"success", func(*docker.ImageRemoveBatchResult) {}, "completed", ""},
		{"missing", func(r *docker.ImageRemoveBatchResult) { r.Items = nil }, "interrupted", "DOCKER_IMAGE_REMOVE_RECEIPT_INVALID"},
		{"duplicate", func(r *docker.ImageRemoveBatchResult) { r.Items = append(r.Items, r.Items[0]) }, "interrupted", "DOCKER_IMAGE_REMOVE_RECEIPT_INVALID"},
		{"wrong identity", func(r *docker.ImageRemoveBatchResult) { r.Items[0].ImageID = "sha256:" + strings.Repeat("f", 64) }, "interrupted", "DOCKER_IMAGE_REMOVE_RECEIPT_INVALID"},
		{"success error", func(r *docker.ImageRemoveBatchResult) { r.Items[0].ErrorCode = "DOCKER_IMAGE_IN_USE" }, "interrupted", "DOCKER_IMAGE_REMOVE_RECEIPT_INVALID"},
		{"incomplete success", func(r *docker.ImageRemoveBatchResult) { r.Completed = false }, "interrupted", "DOCKER_IMAGE_REMOVE_RECEIPT_INVALID"},
		{"negative count", func(r *docker.ImageRemoveBatchResult) { r.FailedCount = -1 }, "interrupted", "DOCKER_IMAGE_REMOVE_RECEIPT_INVALID"},
		{"wrong count", func(r *docker.ImageRemoveBatchResult) { r.RemovedCount = 2 }, "interrupted", "DOCKER_IMAGE_REMOVE_RECEIPT_INVALID"},
		{"success failed count", func(r *docker.ImageRemoveBatchResult) { r.FailedCount = 1 }, "interrupted", "DOCKER_IMAGE_REMOVE_RECEIPT_INVALID"},
		{"in use", func(r *docker.ImageRemoveBatchResult) {
			r.Items[0].Removed = false
			r.Items[0].ErrorCode = "DOCKER_IMAGE_IN_USE"
			r.RemovedCount = 0
			r.FailedCount = 1
			r.Completed = false
		}, "failed", "DOCKER_IMAGE_IN_USE"},
		{"generic deletion error", func(r *docker.ImageRemoveBatchResult) {
			r.Items[0].Removed = false
			r.Items[0].ErrorCode = "DOCKER_IMAGE_REMOVE_FAILED"
			r.RemovedCount = 0
			r.FailedCount = 1
			r.Completed = false
		}, "interrupted", "JOB_EXECUTION_INTERRUPTED"},
		{"future deletion error", func(r *docker.ImageRemoveBatchResult) {
			r.Items[0].Removed = false
			r.Items[0].ErrorCode = "FUTURE_CODE"
			r.RemovedCount = 0
			r.FailedCount = 1
			r.Completed = false
		}, "interrupted", "JOB_EXECUTION_INTERRUPTED"},
		{"missing error", func(r *docker.ImageRemoveBatchResult) {
			r.Items[0].Removed = false
			r.RemovedCount = 0
			r.FailedCount = 1
			r.Completed = false
		}, "interrupted", "DOCKER_IMAGE_REMOVE_RECEIPT_INVALID"},
		{"failed marked completed", func(r *docker.ImageRemoveBatchResult) {
			r.Items[0].Removed = false
			r.Items[0].ErrorCode = "DOCKER_IMAGE_IN_USE"
			r.RemovedCount = 0
			r.FailedCount = 1
		}, "interrupted", "DOCKER_IMAGE_REMOVE_RECEIPT_INVALID"},
		{"failed wrong count", func(r *docker.ImageRemoveBatchResult) {
			r.Items[0].Removed = false
			r.Items[0].ErrorCode = "DOCKER_IMAGE_IN_USE"
			r.Completed = false
		}, "interrupted", "DOCKER_IMAGE_REMOVE_RECEIPT_INVALID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := removeJobSuccess()
			tt.change(&r)
			status, code := imageRemoveJobOutcome(removeJobImageID, r)
			if status != tt.status || code != tt.code {
				t.Fatalf("got %s/%s", status, code)
			}
		})
	}
	for _, code := range []string{"DOCKER_IMAGE_REMOVE_BATCH_INVALID", "DOCKER_IMAGE_REMOVE_BATCH_LIST_FAILED", "AGENT_DOCKER_IMAGES_UNAVAILABLE", "AGENT_RPC_REQUEST_INVALID"} {
		if !imageRemoveRejectedBeforeMutation(code) {
			t.Errorf("preflight rejection %s not recognized", code)
		}
	}
	for _, code := range []string{"", "DOCKER_IMAGE_REMOVE_BATCH_FAILED", "DOCKER_IMAGE_REMOVE_FAILED", "AGENT_RPC_CANCELED", "AGENT_RPC_TIMEOUT", "AGENT_RPC_RESPONSE_INVALID"} {
		if imageRemoveRejectedBeforeMutation(code) {
			t.Errorf("unknown effect %s misclassified as rejection", code)
		}
	}
}

func TestImageRemoveJobRunnerNeverReplays(t *testing.T) {
	for _, outcome := range []string{"success", "in-use", "malformed", "generic-error", "canceled", "deadline"} {
		t.Run(outcome, func(t *testing.T) {
			jobs := newJobRegistry(&snapshotJobStore{})
			job := jobs.create(imageRemoveJobType, removeJobImageID)
			jobs.update(job.ID, "running", "running", "", 0)
			ctx := context.Background()
			if outcome == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if outcome == "deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
			}
			agent := &removeJobAgent{remove: func(ctx context.Context, r docker.ImageRemoveBatchRequest) (docker.ImageRemoveBatchResult, error) {
				if len(r.ImageIDs) != 1 || r.ImageIDs[0] != removeJobImageID {
					t.Errorf("wrong RPC target: %+v", r)
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Error("missing execution deadline")
				}
				result := removeJobSuccess()
				switch outcome {
				case "generic-error":
					return docker.ImageRemoveBatchResult{}, errors.New("secret transport detail")
				case "canceled", "deadline":
					return docker.ImageRemoveBatchResult{}, ctx.Err()
				case "malformed":
					result.Items[0].ImageID = "other"
				case "in-use":
					result.Items[0].Removed = false
					result.Items[0].ErrorCode = "DOCKER_IMAGE_IN_USE"
					result.RemovedCount = 0
					result.FailedCount = 1
					result.Completed = false
				}
				return result, nil
			}}
			api := &handler{jobs: jobs}
			api.runImageRemove(ctx, job.ID, removeJobImageID, agent)
			j, _ := jobs.get(job.ID)
			want := "interrupted"
			if outcome == "success" {
				want = "completed"
			}
			if outcome == "in-use" {
				want = "failed"
			}
			if j.Status != want || j.Cancellable || j.CompletedAt == nil || agent.calls.Load() != 1 {
				t.Fatalf("outcome=%+v calls=%d", j, agent.calls.Load())
			}
			if strings.Contains(j.Message+j.Error, "secret") {
				t.Fatal("transport error exposed")
			}
			if outcome == "deadline" && j.ErrorCode != "JOB_EXECUTION_TIMEOUT" {
				t.Fatalf("timeout code=%s", j.ErrorCode)
			}
			if j.ArtifactState != jobArtifactUnknown {
				t.Fatal("completion invented current artifact state")
			}
			jobs.flush()
			jobs.flush()
			if agent.calls.Load() != 1 {
				t.Fatal("flush replayed deletion")
			}
		})
	}
}

func TestImageRemoveJobHTTPAdmissionFailures(t *testing.T) {
	for _, scenario := range []string{"identity", "unsupported", "persist", "full", "stopped"} {
		t.Run(scenario, func(t *testing.T) {
			store := &snapshotJobStore{}
			if scenario == "persist" {
				store.createErr = errors.New("disk failure")
			}
			jobs := newJobRegistry(store)
			defer closeRemoveJobs(t, jobs)
			if scenario == "full" {
				for i := 0; i < jobCapacity; i++ {
					jobs.capacity <- struct{}{}
				}
			}
			if scenario == "stopped" {
				jobs.StopAdmission()
			}
			agent := &removeJobAgent{remove: func(context.Context, docker.ImageRemoveBatchRequest) (docker.ImageRemoveBatchResult, error) {
				t.Error("unexpected deletion")
				return removeJobSuccess(), nil
			}}
			var images DockerImageAgentClient = agent
			if scenario == "unsupported" {
				images = &imageListTimeoutAgent{}
			}
			api := NewHandler(Config{DockerImages: images, JobEngine: jobs})
			id := removeJobImageID
			if scenario == "identity" {
				id = "alpine:latest"
			}
			recorder := httptest.NewRecorder()
			api.ServeHTTP(recorder, removeJobRequest(id))
			want := http.StatusServiceUnavailable
			if scenario == "identity" {
				want = http.StatusBadRequest
			}
			if recorder.Code != want || agent.calls.Load() != 0 || len(jobs.list("")) != 0 {
				t.Fatalf("admission %d %s calls=%d", recorder.Code, recorder.Body.String(), agent.calls.Load())
			}
		})
	}
}

type removeAdmissionStore struct {
	snapshotJobStore
	entered, release chan struct{}
}

func (s *removeAdmissionStore) CreateJob(ctx context.Context, r controlstore.JobRecord) error {
	close(s.entered)
	<-s.release
	return s.snapshotJobStore.CreateJob(ctx, r)
}

func TestImageRemoveJobAdmissionDurableBeforeExecutionAndHTTPDisconnect(t *testing.T) {
	store := &removeAdmissionStore{entered: make(chan struct{}), release: make(chan struct{})}
	jobs := newJobRegistry(store)
	defer closeRemoveJobs(t, jobs)
	started, release := make(chan struct{}), make(chan struct{})
	agent := &removeJobAgent{remove: func(ctx context.Context, r docker.ImageRemoveBatchRequest) (docker.ImageRemoveBatchResult, error) {
		if len(r.ImageIDs) != 1 || r.ImageIDs[0] != removeJobImageID {
			t.Error("wrong target")
		}
		records := jobs.list("")
		if len(records) != 1 {
			t.Error("job not published")
		} else if durable, err := store.Job(context.Background(), records[0].ID); err != nil || durable.Status != "running" {
			t.Errorf("execution preceded running persistence: %+v %v", durable, err)
		}
		close(started)
		<-release
		if ctx.Err() != nil {
			t.Errorf("HTTP cancellation reached accepted job: %v", ctx.Err())
		}
		return removeJobSuccess(), nil
	}}
	api := NewHandler(Config{DockerImages: agent, JobEngine: jobs})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { api.ServeHTTP(recorder, removeJobRequest(removeJobImageID).WithContext(ctx)); close(done) }()
	<-store.entered
	if agent.calls.Load() != 0 || len(jobs.list("")) != 0 {
		t.Fatal("execution/publication preceded persistence")
	}
	select {
	case <-done:
		t.Fatal("response preceded persistence")
	default:
	}
	close(store.release)
	<-done
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("admission: %d %s", recorder.Code, recorder.Body.String())
	}
	var admitted jobSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &admitted); err != nil {
		t.Fatal(err)
	}
	if admitted.Type != imageRemoveJobType || admitted.Reference != removeJobImageID || admitted.Cancellable || admitted.Status != "queued" {
		t.Fatalf("bad accepted contract: %+v", admitted)
	}
	if _, err := store.Job(context.Background(), admitted.ID); err != nil {
		t.Fatal("202 without durable job")
	}
	cancel()
	<-started
	if _, _, err := jobs.cancel(admitted.ID); !errors.Is(err, errJobCancel) {
		t.Fatalf("removal cancellation allowed: %v", err)
	}
	close(release)
	waitRemoveJob(t, jobs, admitted.ID, func(j jobSnapshot) bool { return j.Status == "completed" })
	if agent.calls.Load() != 1 {
		t.Fatalf("RPC count=%d", agent.calls.Load())
	}
}

func TestImageRemoveJobPendingResultDoesNotReexecute(t *testing.T) {
	store := &snapshotJobStore{}
	jobs := newJobRegistry(store)
	job := jobs.create(imageRemoveJobType, removeJobImageID)
	jobs.update(job.ID, "running", "running", "", 0)
	store.updateErr = errors.New("disk failure")
	agent := &removeJobAgent{remove: func(context.Context, docker.ImageRemoveBatchRequest) (docker.ImageRemoveBatchResult, error) {
		return removeJobSuccess(), nil
	}}
	api := &handler{jobs: jobs}
	api.runImageRemove(context.Background(), job.ID, removeJobImageID, agent)
	current, _ := jobs.get(job.ID)
	if current.Status != "running" || current.PersistenceStatus != "pending" || current.CompletedAt != nil {
		t.Fatalf("premature completion: %+v", current)
	}
	jobs.flush()
	store.updateErr = nil
	jobs.flush()
	jobs.flush()
	current, _ = jobs.get(job.ID)
	durable, err := store.Job(context.Background(), job.ID)
	if err != nil || current.Status != "completed" || durable.Status != "completed" || agent.calls.Load() != 1 {
		t.Fatalf("lost/replayed completion: %+v %+v calls=%d", current, durable, agent.calls.Load())
	}
}

func TestImageRemoveJobQueueShutdownDoesNotReplay(t *testing.T) {
	jobs := newJobRegistry(&snapshotJobStore{})
	started := make(chan struct{})
	calls := atomic.Int32{}
	first, err := jobs.submit(imageRemoveJobType, removeJobImageID, 0, func(ctx context.Context, id string) {
		calls.Add(1)
		close(started)
		<-ctx.Done()
		jobs.interrupt(id, "JOB_EXECUTION_INTERRUPTED", "unknown")
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	second, err := jobs.submit(imageRemoveJobType, removeJobImageID, 0, func(context.Context, string) { calls.Add(1); t.Error("queued removal executed during shutdown") })
	if err != nil {
		t.Fatal(err)
	}
	closeRemoveJobs(t, jobs)
	for _, id := range []string{first.ID, second.ID} {
		j, _ := jobs.get(id)
		if j.Status != "interrupted" || j.Cancellable {
			t.Fatalf("shutdown outcome: %+v", j)
		}
	}
	if calls.Load() != 1 || len(jobs.capacity) != 0 {
		t.Fatalf("calls=%d capacity=%d", calls.Load(), len(jobs.capacity))
	}
}

func TestImageRemoveJobRunningPersistenceFailureSkipsRPC(t *testing.T) {
	store := &snapshotJobStore{updateErr: errors.New("disk failure")}
	jobs := newJobRegistry(store)
	defer func() { store.mu.Lock(); store.updateErr = nil; store.mu.Unlock(); closeRemoveJobs(t, jobs) }()
	calls := atomic.Int32{}
	job, err := jobs.submit(imageRemoveJobType, removeJobImageID, 0, func(context.Context, string) { calls.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	current := waitRemoveJob(t, jobs, job.ID, func(j jobSnapshot) bool { return j.PersistenceStatus == "pending" })
	if current.Status != "queued" || calls.Load() != 0 {
		t.Fatalf("execution without running persistence: %+v calls=%d", current, calls.Load())
	}
	store.mu.Lock()
	store.updateErr = nil
	store.mu.Unlock()
	jobs.flush()
	current = waitRemoveJob(t, jobs, job.ID, func(j jobSnapshot) bool { return j.Status == "interrupted" })
	if current.ErrorCode != "JOB_PERSIST_FAILED" || calls.Load() != 0 {
		t.Fatalf("unsafe recovery: %+v calls=%d", current, calls.Load())
	}
}

func TestImageRemoveJobHTTPCancelAndRetryUnavailable(t *testing.T) {
	jobs := newJobRegistry(&snapshotJobStore{})
	defer closeRemoveJobs(t, jobs)
	agent := &removeJobAgent{remove: func(context.Context, docker.ImageRemoveBatchRequest) (docker.ImageRemoveBatchResult, error) {
		t.Error("must not execute")
		return removeJobSuccess(), nil
	}}
	api := NewHandler(Config{DockerImages: agent, JobEngine: jobs})
	for _, state := range []string{"queued", "running", "completed", "failed", "interrupted"} {
		job := jobs.create(imageRemoveJobType, removeJobImageID)
		if state != "queued" {
			jobs.update(job.ID, state, "fixture", "", 0)
		}
		for _, action := range []string{"cancel", "retry"} {
			recorder := httptest.NewRecorder()
			api.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+job.ID+"/"+action, nil))
			if recorder.Code != http.StatusConflict {
				t.Fatalf("%s %s = %d %s", state, action, recorder.Code, recorder.Body.String())
			}
		}
	}
	if agent.calls.Load() != 0 {
		t.Fatal("cancel/retry executed deletion")
	}
}

type removeRecoveryStore struct{ snapshotJobStore }

func (s *removeRecoveryStore) RecoverInterruptedJobs(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, r := range s.records {
		if r.Status == "queued" || r.Status == "running" {
			r.Status = "interrupted"
			r.Revision++
			r.CompletedAt = time.Now().UTC()
			s.records[id] = r
		}
	}
	return nil
}
func (s *removeRecoveryStore) Jobs(context.Context, string, int) ([]controlstore.JobRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records := make([]controlstore.JobRecord, 0, len(s.records))
	for _, r := range s.records {
		records = append(records, r)
	}
	return records, nil
}
func TestImageRemoveJobRecoveredHistoryNeverReplays(t *testing.T) {
	store := &removeRecoveryStore{snapshotJobStore: snapshotJobStore{records: map[string]controlstore.JobRecord{}}}
	for _, state := range []string{"queued", "running", "completed"} {
		store.records[state] = controlstore.JobRecord{ID: state, Type: imageRemoveJobType, Reference: removeJobImageID, Status: state, Revision: 1, LayersJSON: "{}"}
	}
	jobs := newJobRegistry(store)
	if jobs.initErr != nil {
		t.Fatal(jobs.initErr)
	}
	defer closeRemoveJobs(t, jobs)
	jobs.start()
	for _, state := range []string{"queued", "running", "completed"} {
		j, _ := jobs.get(state)
		want := "interrupted"
		if state == "completed" {
			want = "completed"
		}
		if j.Status != want || j.Cancellable || j.Reference != removeJobImageID {
			t.Fatalf("recovered job: %+v", j)
		}
		e := jobs.lookup(state)
		if e.run != nil || e.queued || e.owner {
			t.Fatalf("recovered task scheduled: %+v", j)
		}
	}
	if len(jobs.removals) != 0 || len(jobs.capacity) != 0 {
		t.Fatal("history recovery enqueued effects")
	}
}
