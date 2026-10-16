package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/Kkwans/nas-control-plane/internal/docker"
)

const actionJobContainerID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type actionJobAgent struct {
	fakeAgentClient
	calls   atomic.Int32
	control func(context.Context, docker.ContainerActionRequest) (docker.ContainerActionResult, error)
}

func (a *actionJobAgent) ControlContainer(ctx context.Context, _ string, r docker.ContainerActionRequest) (docker.ContainerActionResult, error) {
	a.calls.Add(1)
	return a.control(ctx, r)
}
func actionJobSuccess(r docker.ContainerActionRequest) docker.ContainerActionResult {
	state := "running"
	if r.Action == docker.ContainerActionStop {
		state = "stopped"
	}
	return docker.ContainerActionResult{ContainerID: r.ContainerID, Action: r.Action, Name: "fixture", State: state}
}
func actionJobRequest(id, action string) *http.Request {
	return httptest.NewRequest(http.MethodPost, "/api/v1/docker/containers/"+id+"/action-jobs/"+action, nil)
}
func TestContainerActionJobReceipt(t *testing.T) {
	for _, action := range []docker.ContainerAction{"start", "stop", "restart"} {
		input := docker.ContainerActionRequest{ContainerID: actionJobContainerID, Action: action}
		for _, field := range []string{"valid", "opposite", "id", "action", "name", "unknown", "empty-state", "normalized-action"} {
			t.Run(string(action)+"/"+field, func(t *testing.T) {
				r := actionJobSuccess(input)
				want, code := "interrupted", "DOCKER_CONTAINER_ACTION_RECEIPT_INVALID"
				switch field {
				case "valid":
					want, code = "completed", ""
				case "opposite":
					if r.State == "running" {
						r.State = "stopped"
					} else {
						r.State = "running"
					}
					want, code = "failed", "DOCKER_CONTAINER_STATE_MISMATCH"
				case "id":
					r.ContainerID = strings.Repeat("f", 64)
				case "action":
					r.Action = ""
				case "name":
					r.Name = " \t"
				case "unknown":
					r.State = "paused"
				case "empty-state":
					r.State = ""
				case "normalized-action":
					r.Action = docker.ContainerAction(" " + strings.ToUpper(string(action)) + " ")
				}
				got, c := containerActionJobOutcome(input, r)
				if got != want || c != code {
					t.Fatalf("got %s/%s", got, c)
				}
			})
		}
	}
}
func TestContainerActionJobAdmissionValidation(t *testing.T) {
	for _, tt := range []struct{ id, action, body string }{
		{"name", "start", ""}, {actionJobContainerID[:12], "start", ""}, {strings.ToUpper(actionJobContainerID), "start", ""}, {"sha256:" + actionJobContainerID, "start", ""}, {strings.Repeat("g", 64), "start", ""}, {actionJobContainerID, "START", ""}, {actionJobContainerID, "%20start", ""}, {actionJobContainerID, "%73tart", ""}, {"%30" + actionJobContainerID[1:], "start", ""}, {actionJobContainerID, "kill", ""}, {actionJobContainerID, "stop", "{}"},
	} {
		jobs := newJobRegistry(&snapshotJobStore{})
		a := &actionJobAgent{control: func(context.Context, docker.ContainerActionRequest) (docker.ContainerActionResult, error) {
			t.Error("unexpected RPC")
			return docker.ContainerActionResult{}, nil
		}}
		req := actionJobRequest(tt.id, tt.action)
		if tt.body != "" {
			req = httptest.NewRequest(http.MethodPost, req.URL.String(), strings.NewReader(tt.body))
		}
		w := httptest.NewRecorder()
		NewHandler(Config{Agent: a, JobEngine: jobs}).ServeHTTP(w, req)
		if w.Code != 400 || a.calls.Load() != 0 || len(jobs.list("")) != 0 {
			t.Fatalf("invalid admitted %d %s", w.Code, w.Body.String())
		}
		closeRemoveJobs(t, jobs)
	}
}
func TestContainerActionJobRouteProtection(t *testing.T) {
	for _, crossOrigin := range []bool{false, true} {
		jobs := newJobRegistry(&snapshotJobStore{})
		defer closeRemoveJobs(t, jobs)
		a := &actionJobAgent{control: func(context.Context, docker.ContainerActionRequest) (docker.ContainerActionResult, error) {
			t.Error("unexpected RPC")
			return docker.ContainerActionResult{}, nil
		}}
		req := actionJobRequest(actionJobContainerID, "start")
		want := http.StatusUnauthorized
		if crossOrigin {
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			want = http.StatusForbidden
		}
		w := httptest.NewRecorder()
		NewHandler(Config{Agent: a, JobEngine: jobs, Auth: openTestAuthService(t)}).ServeHTTP(w, req)
		if w.Code != want || a.calls.Load() != 0 {
			t.Fatalf("protection %d %s", w.Code, w.Body.String())
		}
	}
}
func TestContainerActionJobDurableAdmissionAndDisconnect(t *testing.T) {
	admissionRelease, unblockAdmission := queueTestRelease(t)
	store := &removeAdmissionStore{entered: make(chan struct{}), release: admissionRelease}
	jobs := newJobRegistry(store)
	release, unblock := queueTestRelease(t)
	queueCleanupRegistry(t, jobs, func() { unblockAdmission(); unblock() })
	started := make(chan struct{})
	a := &actionJobAgent{control: func(ctx context.Context, r docker.ContainerActionRequest) (docker.ContainerActionResult, error) {
		if r.ContainerID != actionJobContainerID || r.Action != "restart" {
			t.Error("request not frozen")
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > defaultDockerContainerActionTimeout {
			t.Error("missing bounded context")
		}
		close(started)
		<-release
		if ctx.Err() != nil {
			t.Errorf("HTTP canceled execution: %v", ctx.Err())
		}
		return actionJobSuccess(r), nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		NewHandler(Config{Agent: a, JobEngine: jobs}).ServeHTTP(w, actionJobRequest(actionJobContainerID, "restart").WithContext(ctx))
		close(done)
	}()
	queueReceive(t, store.entered)
	if a.calls.Load() != 0 || len(jobs.list("")) != 0 {
		t.Fatal("execution before durable admission")
	}
	unblockAdmission()
	queueReceive(t, done)
	if w.Code != 202 {
		t.Fatalf("admission %d %s", w.Code, w.Body.String())
	}
	var j jobSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	if j.Type != "docker-container-restart" || j.Reference != actionJobContainerID || j.Status != "queued" || j.Cancellable || j.ArtifactState != jobArtifactUnknown || j.Progress != 0 || j.DownloadedBytes != 0 || j.TotalBytes != 0 || j.SpeedBytes != 0 || len(j.Layers) != 0 || j.CompletedAt != nil || j.ErrorCode != "" || j.Error != "" || j.CancellationStatus != "" || j.PersistenceStatus != "" {
		t.Fatalf("202 snapshot %+v", j)
	}
	cancel()
	queueReceive(t, started)
	unblock()
	waitRemoveJob(t, jobs, j.ID, func(j jobSnapshot) bool { return j.Status == "completed" })
	if a.calls.Load() != 1 {
		t.Fatal("replayed action")
	}
}

func TestContainerActionJobErrorClassification(t *testing.T) {
	for _, code := range []string{"DOCKER_CONTAINER_NOT_FOUND", "DOCKER_CONTAINER_ACTION_FAILED", "DOCKER_CONTAINER_INSPECT_FAILED", "AGENT_RPC_RESPONSE_INVALID", "AGENT_RPC_TIMEOUT", "AGENT_RPC_CANCELED", "future", "DOCKER_CONTAINER_ACTION_INVALID", "AGENT_PROTOCOL_MISMATCH", "AGENT_RPC_REQUEST_INVALID"} {
		for _, ctxErr := range []error{nil, context.Canceled, context.DeadlineExceeded} {
			status, got := containerActionJobFailure(code, errors.New("private error"), ctxErr)
			want, wantCode := "interrupted", "JOB_EXECUTION_INTERRUPTED"
			if code == "AGENT_RPC_TIMEOUT" || ctxErr == context.DeadlineExceeded {
				wantCode = "JOB_EXECUTION_TIMEOUT"
			} else if ctxErr == nil {
				if code == "AGENT_RPC_RESPONSE_INVALID" {
					wantCode = "DOCKER_CONTAINER_ACTION_RECEIPT_INVALID"
				}
				if containerActionRejectedBeforeMutation(code) {
					want, wantCode = "failed", code
				}
			}
			if status != want || got != wantCode {
				t.Fatalf("%s %v got %s/%s", code, ctxErr, status, got)
			}
		}
	}
}
func TestContainerActionJobRunnerUnknownAndTimeout(t *testing.T) {
	for _, err := range []error{errors.New("private transport detail"), context.Canceled, context.DeadlineExceeded} {
		jobs := newJobRegistry(&snapshotJobStore{})
		j := jobs.create("docker-container-start", actionJobContainerID)
		jobs.update(j.ID, "running", "running", "", 0)
		a := &actionJobAgent{control: func(context.Context, docker.ContainerActionRequest) (docker.ContainerActionResult, error) {
			return docker.ContainerActionResult{}, err
		}}
		(&handler{agent: a, jobs: jobs}).runContainerAction(context.Background(), j.ID, docker.ContainerActionRequest{ContainerID: actionJobContainerID, Action: "start"})
		got, _ := jobs.get(j.ID)
		if got.Status != "interrupted" || got.ArtifactState != jobArtifactUnknown || got.CompletedAt == nil || strings.Contains(got.Error+got.Message, "private") {
			t.Fatalf("outcome %+v", got)
		}
		if err == context.DeadlineExceeded && got.ErrorCode != "JOB_EXECUTION_TIMEOUT" {
			t.Fatalf("timeout %+v", got)
		}
		jobs.flush()
		jobs.flush()
		if a.calls.Load() != 1 {
			t.Fatal("replayed")
		}
	}
}
func TestContainerActionJobPendingResultSingleDispatch(t *testing.T) {
	store := &snapshotJobStore{}
	jobs := newJobRegistry(store)
	j := jobs.create("docker-container-stop", actionJobContainerID)
	jobs.update(j.ID, "running", "running", "", 0)
	store.updateErr = errors.New("disk failed")
	a := &actionJobAgent{control: func(_ context.Context, r docker.ContainerActionRequest) (docker.ContainerActionResult, error) {
		return actionJobSuccess(r), nil
	}}
	(&handler{jobs: jobs, agent: a}).runContainerAction(context.Background(), j.ID, docker.ContainerActionRequest{ContainerID: actionJobContainerID, Action: "stop"})
	got, _ := jobs.get(j.ID)
	if got.Status != "running" || got.PersistenceStatus != "pending" || got.CompletedAt != nil {
		t.Fatalf("published unsaved terminal %+v", got)
	}
	jobs.flush()
	store.updateErr = nil
	jobs.flush()
	jobs.flush()
	got, _ = jobs.get(j.ID)
	if got.Status != "completed" || a.calls.Load() != 1 {
		t.Fatalf("lost or replayed result %+v", got)
	}
}
func TestContainerActionJobQueueTypesTTL(t *testing.T) {
	for _, kind := range []string{"docker-container-start", "docker-container-stop", "docker-container-restart"} {
		for _, delta := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
			r, clock := manualQueueRegistry(&snapshotJobStore{})
			var calls atomic.Int32
			e := submitQueueTest(t, r, kind, &calls)
			if e.queue != r.removals || e.snapshot().Cancellable {
				t.Fatal("incorrect queue or cancellation")
			}
			clock.advance(jobQueueTTL + delta)
			e.queue.pop()
			r.execute(e)
			r.flush()
			if delta < 0 {
				if calls.Load() != 1 {
					t.Fatal("missing execution")
				}
			} else if calls.Load() != 0 || e.snapshot().ErrorCode != "JOB_QUEUE_TIMEOUT" {
				t.Fatal("expired action dispatched")
			}
		}
	}
	r, _ := manualQueueRegistry(nil)
	for _, kind := range []string{"docker-container-START", "docker-container-start-extra", "docker-container-", "unknown"} {
		if _, err := r.submit(kind, actionJobContainerID, 0, func(context.Context, string) {}); err == nil {
			t.Fatal("unknown type admitted")
		}
	}
}
func TestContainerActionJobPersistenceBoundaries(t *testing.T) {
	for _, mode := range []string{"create-failure", "create-ack", "running-failure", "running-ack", "terminal-ack"} {
		t.Run(mode, func(t *testing.T) {
			store := &snapshotJobStore{}
			switch mode {
			case "create-failure":
				store.createErr = errors.New("disk")
			case "create-ack":
				store.createCommittedError = true
			case "running-failure":
				store.updateErr = errors.New("disk")
			case "running-ack", "terminal-ack":
				store.committedError = true
			}
			r, _ := manualQueueRegistry(store)
			var calls atomic.Int32
			j, err := r.submit("docker-container-start", actionJobContainerID, 0, func(_ context.Context, id string) { calls.Add(1); r.update(id, "completed", "done", "", 100) })
			if mode == "create-failure" {
				if err == nil || calls.Load() != 0 || len(r.capacity) != 0 {
					t.Fatal("failed create executed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			e := r.lookup(j.ID)
			e.queue.pop()
			r.execute(e)
			if mode == "running-failure" {
				if calls.Load() != 0 || e.snapshot().PersistenceStatus != "pending" {
					t.Fatal("running persistence bypassed")
				}
				store.updateErr = nil
				r.flush()
				if e.snapshot().ErrorCode != "JOB_PERSIST_FAILED" {
					t.Fatal("unsafe replay recovery")
				}
			} else if calls.Load() != 1 || e.snapshot().Status != "completed" {
				t.Fatalf("ack lost %+v", e.snapshot())
			}
			r.flush()
			r.flush()
			if calls.Load() > 1 {
				t.Fatal("replayed")
			}
		})
	}
}
func TestContainerActionJobSharedRemovalSerialQueue(t *testing.T) {
	jobs := newJobRegistry(&snapshotJobStore{})
	release, unblock := queueTestRelease(t)
	queueCleanupRegistry(t, jobs, unblock)
	started := make(chan struct{})
	var calls atomic.Int32
	_, err := jobs.submit(imageRemoveJobType, removeJobImageID, 0, func(_ context.Context, id string) {
		close(started)
		<-release
		jobs.update(id, "completed", "done", "", 100)
	})
	if err != nil {
		t.Fatal(err)
	}
	queueReceive(t, started)
	var ids []string
	for _, kind := range []string{"docker-container-start", "docker-container-stop", "docker-container-restart"} {
		j, err := jobs.submit(kind, actionJobContainerID, 0, func(_ context.Context, id string) { calls.Add(1); jobs.update(id, "completed", "done", "", 100) })
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, j.ID)
	}
	if calls.Load() != 0 || jobs.removals.len() != 3 {
		t.Fatal("actions bypassed removal worker")
	}
	unblock()
	for _, id := range ids {
		waitRemoveJob(t, jobs, id, func(j jobSnapshot) bool { return j.Status == "completed" })
	}
	if calls.Load() != 3 {
		t.Fatal("incorrect dispatch count")
	}
}
func TestContainerActionJobSQLiteRecoveryAndRetryRejection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	store, err := controlstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := manualQueueRegistry(store)
	var ids []string
	for _, kind := range []string{"docker-container-start", "docker-container-stop", "docker-container-restart"} {
		for _, state := range []string{"queued", "running", "completed", "failed", "interrupted"} {
			j, err := r.createRecord(kind, actionJobContainerID, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			if state != "queued" {
				r.update(j.ID, state, "fixture", "", 0)
			}
			ids = append(ids, j.ID)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = controlstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	recovered := newJobRegistry(store)
	defer closeRemoveJobs(t, recovered)
	a := &actionJobAgent{control: func(context.Context, docker.ContainerActionRequest) (docker.ContainerActionResult, error) {
		t.Error("history replayed")
		return docker.ContainerActionResult{}, nil
	}}
	api := NewHandler(Config{Agent: a, JobEngine: recovered})
	for _, id := range ids {
		j, ok := recovered.get(id)
		if !ok || j.Cancellable || j.Reference != actionJobContainerID || j.Status == "queued" || j.Status == "running" || !isContainerActionJob(j.Type) {
			t.Fatalf("bad recovery %+v", j)
		}
		e := recovered.lookup(id)
		if e.run != nil || e.queued || e.owner {
			t.Fatal("recovery scheduled mutation")
		}
		for _, action := range []string{"cancel", "retry"} {
			w := httptest.NewRecorder()
			api.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+id+"/"+action, nil))
			if w.Code != 409 {
				t.Fatalf("%s %d", action, w.Code)
			}
		}
	}

	for _, kind := range []string{"docker-container-start", "docker-container-stop", "docker-container-restart"} {
		page, err := store.JobHistory(context.Background(), controlstore.JobHistoryQuery{Type: kind, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 5 {
			t.Fatalf("history %s: %+v", kind, page)
		}
		for _, entry := range page.Items {
			if entry.Type != kind || entry.Reference != actionJobContainerID {
				t.Fatalf("history identity %+v", entry)
			}
		}
	}
	if a.calls.Load() != 0 || len(recovered.capacity) != 0 || recovered.removals.len() != 0 {
		t.Fatal("recovery executed")
	}
}
func TestContainerActionJobTerminalPendingRetainsCapacity(t *testing.T) {
	store := &snapshotJobStore{}
	r, _ := manualQueueRegistry(store)
	a := &actionJobAgent{control: func(_ context.Context, input docker.ContainerActionRequest) (docker.ContainerActionResult, error) {
		store.updateErr = errors.New("disk failed")
		result := actionJobSuccess(input)
		result.Name = "private container name"
		return result, nil
	}}
	api := &handler{jobs: r, agent: a}
	j, err := r.submit("docker-container-restart", actionJobContainerID, 0, func(ctx context.Context, id string) {
		api.runContainerAction(ctx, id, docker.ContainerActionRequest{ContainerID: actionJobContainerID, Action: "restart"})
	})
	if err != nil {
		t.Fatal(err)
	}
	e := r.lookup(j.ID)
	e.queue.pop()
	r.execute(e)
	got := e.snapshot()
	if got.Status != "running" || got.PersistenceStatus != "pending" || len(r.capacity) != 1 || got.CompletedAt != nil {
		t.Fatalf("pending released or exposed %+v cap=%d", got, len(r.capacity))
	}
	r.flush()
	if a.calls.Load() != 1 || len(r.capacity) != 1 {
		t.Fatal("pending replayed/released")
	}
	store.updateErr = nil
	r.flush()
	r.flush()
	got = e.snapshot()
	if got.Status != "completed" || len(r.capacity) != 0 || a.calls.Load() != 1 || strings.Contains(got.Message+got.Error, "private") {
		t.Fatalf("flush %+v cap=%d calls=%d", got, len(r.capacity), a.calls.Load())
	}
}
func TestContainerActionJobHTTPAdmissionFailures(t *testing.T) {
	for _, mode := range []string{"create", "full", "stopped"} {
		store := &snapshotJobStore{}
		if mode == "create" {
			store.createErr = errors.New("disk")
		}
		r, _ := manualQueueRegistry(store)
		if mode == "full" {
			for i := 0; i < jobCapacity; i++ {
				r.capacity <- struct{}{}
			}
		}
		if mode == "stopped" {
			r.StopAdmission()
		}
		a := &actionJobAgent{control: func(context.Context, docker.ContainerActionRequest) (docker.ContainerActionResult, error) {
			t.Error("unexpected dispatch")
			return docker.ContainerActionResult{}, nil
		}}
		w := httptest.NewRecorder()
		NewHandler(Config{Agent: a, JobEngine: r}).ServeHTTP(w, actionJobRequest(actionJobContainerID, "stop"))
		if w.Code != 503 || a.calls.Load() != 0 || len(r.list("")) != 0 {
			t.Fatalf("%s: %d %s", mode, w.Code, w.Body.String())
		}
	}
}
