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

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/Kkwans/nas-control-plane/internal/docker"
)

var projectJobIDs = []string{strings.Repeat("a", 64), strings.Repeat("b", 64)}

func projectJobInput() docker.ProjectActionRequest {
	return docker.ProjectActionRequest{ProjectID: "standalone", Kind: docker.ProjectKindStandalone, Action: "start", ContainerIDs: append([]string(nil), projectJobIDs...)}
}
func projectJobReceipt(input docker.ProjectActionRequest) docker.ProjectActionResult {
	r := docker.ProjectActionResult{ProjectID: input.ProjectID, Kind: input.Kind, Action: input.Action, State: "running", Completed: true}
	if input.Action == "stop" {
		r.State = "stopped"
	}
	for _, id := range input.ContainerIDs {
		r.Containers = append(r.Containers, docker.ProjectContainerActionResult{ContainerID: id, Name: "secret-name", Action: input.Action, State: r.State, Success: true})
	}
	return r
}

type projectJobAgent struct {
	fakeAgentClient
	calls     atomic.Int32
	inventory func(context.Context) (docker.Inventory, error)
	control   func(context.Context, docker.ProjectActionRequest) (docker.ProjectActionResult, error)
}

func (a *projectJobAgent) CollectDockerInventory(ctx context.Context, _ string) (docker.Inventory, error) {
	return a.inventory(ctx)
}
func (a *projectJobAgent) ControlStandaloneProject(ctx context.Context, _ string, input docker.ProjectActionRequest) (docker.ProjectActionResult, error) {
	a.calls.Add(1)
	return a.control(ctx, input)
}
func projectJobInventory() docker.Inventory {
	return docker.Inventory{Containers: []docker.InventoryContainer{{ID: projectJobIDs[0], ProjectID: "standalone", State: "running"}, {ID: projectJobIDs[1], ProjectID: "standalone", State: "stopped"}}}
}
func projectJobRequest(body, action string) *http.Request {
	r := httptest.NewRequest("POST", "/api/v1/docker/projects/standalone/action-jobs/"+action, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}
func TestProjectActionJobDecode(t *testing.T) {
	valid := `{"containerIds":["` + projectJobIDs[0] + `"]}`
	for _, raw := range []string{`null`, `{}`, `{"containerIds":null}`, `{"containerIds":[]}`, `{"containerIds":["short"]}`, `{"containerIds":["` + strings.Repeat("A", 64) + `"]}`, `{"containerIds":["` + projectJobIDs[0] + `","` + projectJobIDs[0] + `"]}`, strings.TrimSuffix(valid, "}") + `,"containerIds":[]}`, strings.TrimSuffix(valid, "}") + `,"action":"start"}`, valid + `{}`, `{"ContainerIds":[]}`} {
		if _, ok := decodeProjectActionIDs([]byte(raw)); ok {
			t.Fatalf("accepted %s", raw)
		}
	}
	if ids, ok := decodeProjectActionIDs([]byte(valid)); !ok || len(ids) != 1 {
		t.Fatal("valid rejected")
	}
	for _, n := range []int{128, 129} {
		ids := make([]string, n)
		for i := range ids {
			ids[i] = strings.Repeat("0", 62) + string("0123456789abcdef"[i/16]) + string("0123456789abcdef"[i%16])
		}
		raw, _ := json.Marshal(map[string]any{"containerIds": ids})
		if _, ok := decodeProjectActionIDs(raw); ok != (n == 128) {
			t.Fatalf("bound %d", n)
		}
	}
}
func TestProjectActionJobReceipt(t *testing.T) {
	for _, action := range []docker.ContainerAction{"start", "stop", "restart"} {
		for _, change := range []string{"valid", "reordered", "mismatch", "unknown", "id", "duplicate", "missing", "action", "project", "kind", "summary", "completed", "name", "error", "state", "success-contradiction"} {
			t.Run(string(action)+"/"+change, func(t *testing.T) {
				input := projectJobInput()
				input.Action = action
				r := projectJobReceipt(input)
				want := "interrupted"
				switch change {
				case "valid":
					want = "completed"
				case "reordered":
					r.Containers[0], r.Containers[1] = r.Containers[1], r.Containers[0]
					want = "completed"
				case "mismatch":
					r.Containers[1].Success = false
					r.Containers[1].ErrorCode = "DOCKER_CONTAINER_STATE_MISMATCH"
					r.Containers[1].State = "stopped"
					if action == "stop" {
						r.Containers[1].State = "running"
					}
					r.Completed = false
					r.State = "degraded"
					want = "failed"
				case "unknown":
					r.Containers[1].Success = false
					r.Containers[1].ErrorCode = "DOCKER_CONTAINER_NOT_FOUND"
					r.Completed = false
				case "id":
					r.Containers[0].ContainerID = strings.Repeat("c", 64)
				case "duplicate":
					r.Containers[1] = r.Containers[0]
				case "missing":
					r.Containers = r.Containers[:1]
				case "action":
					r.Containers[0].Action = "stop"
					if action == "stop" {
						r.Containers[0].Action = "start"
					}
				case "project":
					r.ProjectID = "other"
				case "kind":
					r.Kind = "compose"
				case "summary":
					r.State = "unknown"
				case "completed":
					r.Completed = false
				case "name":
					r.Containers[0].Name = " "
				case "error":
					r.Containers[0].ErrorCode = "secret-error"
				case "state":
					r.Containers[0].State = "paused"
				case "success-contradiction":
					r.Containers[0].Success = false
					r.Containers[0].ErrorCode = "DOCKER_CONTAINER_STATE_MISMATCH"
					r.Completed = false
				}
				result, status, code := projectActionJobResult(input, r)
				if status != want {
					t.Fatalf("status %s code %s", status, code)
				}
				if change == "valid" || change == "reordered" || change == "mismatch" || change == "unknown" {
					if result == nil || result.Items[0].ContainerID != input.ContainerIDs[0] {
						t.Fatal("safe result missing or order drift")
					}
					raw, _ := json.Marshal(result)
					if strings.Contains(string(raw), "secret") {
						t.Fatal("leaked name")
					}
				} else if result != nil {
					t.Fatal("invalid whole receipt kept items")
				}
			})
		}
	}
}
func TestProjectActionJobPreflightAndExecution(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "duplicate", "compose", "error", "unknown", "transport", "mismatch"} {
		t.Run(mode, func(t *testing.T) {
			jobs := newJobRegistry(&snapshotJobStore{})
			defer closeRemoveJobs(t, jobs)
			a := &projectJobAgent{inventory: func(context.Context) (docker.Inventory, error) {
				inv := projectJobInventory()
				switch mode {
				case "missing":
					inv.Containers = inv.Containers[:1]
				case "duplicate":
					inv.Containers = append(inv.Containers, inv.Containers[0])
				case "compose":
					inv.Containers[0].ProjectID = "compose:test"
				case "error":
					return inv, errors.New("secret")
				}
				return inv, nil
			}, control: func(_ context.Context, input docker.ProjectActionRequest) (docker.ProjectActionResult, error) {
				r := projectJobReceipt(input)
				switch mode {
				case "transport":
					return r, errors.New("secret")
				case "unknown":
					r.Containers[1].Success = false
					r.Containers[1].ErrorCode = "DOCKER_CONTAINER_ACTION_FAILED"
					r.Completed = false
				case "mismatch":
					r.Containers[1].Success = false
					r.Containers[1].State = "stopped"
					r.Containers[1].ErrorCode = "DOCKER_CONTAINER_STATE_MISMATCH"
					r.Completed = false
					r.State = "degraded"
				}
				return r, nil
			}}
			api := &handler{agent: a, jobs: jobs}
			job, err := api.submitProjectAction(a, projectJobInput())
			if err != nil {
				t.Fatal(err)
			}
			got := waitRemoveJob(t, jobs, job.ID, func(j jobSnapshot) bool { return jobTerminal(j.Status) })
			expectedCalls := int32(1)
			want := "completed"
			switch mode {
			case "missing", "duplicate", "compose", "error":
				expectedCalls = 0
				want = "failed"
			case "transport", "unknown":
				want = "interrupted"
			case "mismatch":
				want = "failed"
			}
			if got.Status != want || a.calls.Load() != expectedCalls {
				t.Fatalf("%s calls %d", got.Status, a.calls.Load())
			}
			if (mode == "transport" || expectedCalls == 0) && got.ProjectActionResult != nil {
				t.Fatal("unexpected items")
			}
			raw, _ := json.Marshal(got)
			if strings.Contains(string(raw), "secret") {
				t.Fatal("secret leaked")
			}
		})
	}
}
func TestProjectActionJobHTTPAndSnapshotIsolation(t *testing.T) {
	jobs := newJobRegistry(&snapshotJobStore{})
	defer closeRemoveJobs(t, jobs)
	a := &projectJobAgent{inventory: func(context.Context) (docker.Inventory, error) { return projectJobInventory(), nil }, control: func(_ context.Context, i docker.ProjectActionRequest) (docker.ProjectActionResult, error) {
		return projectJobReceipt(i), nil
	}}
	h := NewHandler(Config{Agent: a, JobEngine: jobs})
	for _, body := range []string{`{}`, `{"containerIds":[]}`, `{"containerIds":["short"]}`} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, projectJobRequest(body, "start"))
		if w.Code != 400 {
			t.Fatalf("%d", w.Code)
		}
	}
	raw, _ := json.Marshal(map[string]any{"containerIds": projectJobIDs})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, projectJobRequest(string(raw), "start"))
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var queued jobSnapshot
	if json.Unmarshal(w.Body.Bytes(), &queued) != nil || queued.Status != "queued" || queued.ProjectActionResult != nil || queued.Reference != "standalone" {
		t.Fatal(w.Body.String())
	}
	got := waitRemoveJob(t, jobs, queued.ID, func(j jobSnapshot) bool { return jobTerminal(j.Status) })
	if got.ProjectActionResult == nil {
		t.Fatal("result missing")
	}
	got.ProjectActionResult.Items[0].ContainerID = "tampered"
	again, _ := jobs.get(queued.ID)
	if again.ProjectActionResult.Items[0].ContainerID != projectJobIDs[0] {
		t.Fatal("shallow copy")
	}
	for _, action := range []string{"cancel", "retry"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/jobs/"+queued.ID+"/"+action, nil))
		if w.Code != 409 {
			t.Fatalf("%s %d", action, w.Code)
		}
	}
	record := recordFromSnapshot(again)
	record.ProjectActionResultJSON = `{}`
	if snapshotFromRecord(record).Status != "interrupted" {
		t.Fatal("corrupt success")
	}
	other := record
	other.ProjectActionResultJSON = "different"
	if sameJobRecord(record, other) {
		t.Fatal("ACK ignored result")
	}
}

func TestProjectActionJobPendingSingleDispatch(t *testing.T) {
	store := &snapshotJobStore{}
	jobs := newJobRegistry(store)
	j := jobs.create("docker-project-start", "standalone")
	jobs.update(j.ID, "running", "running", "", 0)
	store.updateErr = errors.New("disk secret")
	a := &projectJobAgent{inventory: func(context.Context) (docker.Inventory, error) { return projectJobInventory(), nil }, control: func(_ context.Context, i docker.ProjectActionRequest) (docker.ProjectActionResult, error) {
		return projectJobReceipt(i), nil
	}}
	(&handler{agent: a, jobs: jobs}).runProjectAction(context.Background(), j.ID, a, projectJobInput())
	got, _ := jobs.get(j.ID)
	if got.Status != "running" || got.PersistenceStatus != "pending" || got.ProjectActionResult != nil {
		t.Fatalf("premature result %+v", got)
	}
	jobs.flush()
	store.updateErr = nil
	jobs.flush()
	jobs.flush()
	got, _ = jobs.get(j.ID)
	if got.Status != "completed" || got.ProjectActionResult == nil || a.calls.Load() != 1 {
		t.Fatal("lost or replayed result")
	}
}
func TestProjectActionJobRunningPersistenceGate(t *testing.T) {
	store := &snapshotJobStore{updateErr: errors.New("disk")}
	jobs := newJobRegistry(store)
	defer func() { store.mu.Lock(); store.updateErr = nil; store.mu.Unlock(); closeRemoveJobs(t, jobs) }()
	a := &projectJobAgent{inventory: func(context.Context) (docker.Inventory, error) {
		t.Error("preflight before running commit")
		return docker.Inventory{}, nil
	}}
	j, err := (&handler{agent: a, jobs: jobs}).submitProjectAction(a, projectJobInput())
	if err != nil {
		t.Fatal(err)
	}
	got := waitRemoveJob(t, jobs, j.ID, func(j jobSnapshot) bool { return j.PersistenceStatus == "pending" })
	if got.Status != "queued" || a.calls.Load() != 0 {
		t.Fatal("unsafe running")
	}
	store.mu.Lock()
	store.updateErr = nil
	store.mu.Unlock()
	jobs.flush()
	waitRemoveJob(t, jobs, j.ID, func(j jobSnapshot) bool { return j.Status == "interrupted" })
}
func TestProjectActionJobDurableAdmissionDisconnectAndFrozenSubset(t *testing.T) {
	admission, releaseAdmission := queueTestRelease(t)
	release, unblock := queueTestRelease(t)
	store := &removeAdmissionStore{entered: make(chan struct{}), release: admission}
	jobs := newJobRegistry(store)
	queueCleanupRegistry(t, jobs, func() { releaseAdmission(); unblock() })
	started := make(chan struct{})
	a := &projectJobAgent{inventory: func(ctx context.Context) (docker.Inventory, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("unbounded preflight")
		}
		return projectJobInventory(), nil
	}, control: func(ctx context.Context, i docker.ProjectActionRequest) (docker.ProjectActionResult, error) {
		close(started)
		<-release
		if ctx.Err() != nil {
			t.Error("HTTP cancelled execution")
		}
		if len(i.ContainerIDs) != 1 || i.ContainerIDs[0] != projectJobIDs[0] {
			t.Error("subset reselected")
		}
		return projectJobReceipt(i), nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		NewHandler(Config{Agent: a, JobEngine: jobs}).ServeHTTP(w, projectJobRequest(`{"containerIds":["`+projectJobIDs[0]+`"]}`, "restart").WithContext(ctx))
		close(done)
	}()
	queueReceive(t, store.entered)
	if a.calls.Load() != 0 || len(jobs.list("")) != 0 {
		t.Fatal("before durable admission")
	}
	releaseAdmission()
	queueReceive(t, done)
	cancel()
	queueReceive(t, started)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var j jobSnapshot
	_ = json.Unmarshal(w.Body.Bytes(), &j)
	if j.Status != "queued" || j.ProjectActionResult != nil {
		t.Fatal("mutated admission")
	}
	unblock()
	waitRemoveJob(t, jobs, j.ID, func(j jobSnapshot) bool { return j.Status == "completed" })
}
func TestProjectActionJobRouteProtection(t *testing.T) {
	for _, cross := range []bool{false, true} {
		jobs := newJobRegistry(&snapshotJobStore{})
		defer closeRemoveJobs(t, jobs)
		req := projectJobRequest(`{"containerIds":["`+projectJobIDs[0]+`"]}`, "start")
		want := 401
		if cross {
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			want = 403
		}
		w := httptest.NewRecorder()
		NewHandler(Config{JobEngine: jobs, Auth: openTestAuthService(t)}).ServeHTTP(w, req)
		if w.Code != want {
			t.Fatal(w.Code)
		}
	}
}
func TestProjectActionJobErrorClassification(t *testing.T) {
	for _, code := range []string{"DOCKER_PROJECT_ACTION_INVALID", "AGENT_PROTOCOL_MISMATCH", "AGENT_RPC_REQUEST_INVALID", "AGENT_RPC_RESPONSE_INVALID", "AGENT_RPC_TIMEOUT", "AGENT_RPC_CANCELED", "DOCKER_CONTAINER_NOT_FOUND", "secret"} {
		for _, ctxErr := range []error{nil, context.Canceled, context.DeadlineExceeded} {
			status, got := projectActionJobFailure(code, errors.New("secret"), ctxErr)
			if ctxErr != nil && status != "interrupted" {
				t.Fatal("cancel trusted rejection")
			}
			if ctxErr == context.DeadlineExceeded && got != "JOB_EXECUTION_TIMEOUT" {
				t.Fatal(got)
			}
			if ctxErr == nil && (code == "DOCKER_PROJECT_ACTION_INVALID" || code == "AGENT_PROTOCOL_MISMATCH" || code == "AGENT_RPC_REQUEST_INVALID") && status != "failed" {
				t.Fatal("rejection unknown")
			}
		}
	}
}

type differentProjectAckStore struct {
	snapshotJobStore
	raw string
}

func (s *differentProjectAckStore) UpdateJob(ctx context.Context, j controlstore.JobRecord, revision int64) (int64, error) {
	if j.Status == "completed" {
		j.ProjectActionResultJSON = s.raw
		next, err := s.snapshotJobStore.UpdateJob(ctx, j, revision)
		if err != nil {
			return next, err
		}
		return next, errors.New("ACK lost")
	}
	return s.snapshotJobStore.UpdateJob(ctx, j, revision)
}
func TestProjectActionJobAcknowledgementAndFrozenQueue(t *testing.T) {
	for _, raw := range []string{"!", ""} {
		store := &differentProjectAckStore{raw: raw}
		jobs, _ := manualQueueRegistry(store)
		a := &projectJobAgent{inventory: func(context.Context) (docker.Inventory, error) { return projectJobInventory(), nil }, control: func(_ context.Context, i docker.ProjectActionRequest) (docker.ProjectActionResult, error) {
			if i.ContainerIDs[0] != projectJobIDs[0] {
				t.Error("input aliased")
			}
			return projectJobReceipt(i), nil
		}}
		input := projectJobInput()
		j, err := (&handler{agent: a, jobs: jobs}).submitProjectAction(a, input)
		if err != nil {
			t.Fatal(err)
		}
		input.ContainerIDs[0] = "tampered"
		e := jobs.removals.pop()
		if e == nil {
			t.Fatal("wrong queue")
		}
		jobs.execute(e)
		jobs.flush()
		jobs.execute(e)
		got, _ := jobs.get(j.ID)
		if got.Status != "running" || got.PersistenceStatus != "pending" || got.ProjectActionResult != nil || len(jobs.capacity) != 1 || a.calls.Load() != 1 {
			t.Fatalf("unsafe ACK %+v", got)
		}
		assertJobRunnerReleased(t, e)
	}
}
func TestProjectActionJobCancelledNilReceiptIsUnknown(t *testing.T) {
	jobs := newJobRegistry(&snapshotJobStore{})
	j := jobs.create("docker-project-start", "standalone")
	jobs.update(j.ID, "running", "running", "", 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &projectJobAgent{inventory: func(context.Context) (docker.Inventory, error) { return projectJobInventory(), nil }, control: func(_ context.Context, i docker.ProjectActionRequest) (docker.ProjectActionResult, error) {
		cancel()
		return projectJobReceipt(i), nil
	}}
	(&handler{agent: a, jobs: jobs}).runProjectAction(ctx, j.ID, a, projectJobInput())
	got, _ := jobs.get(j.ID)
	if got.Status != "interrupted" || got.ProjectActionResult != nil || a.calls.Load() != 1 {
		t.Fatalf("trusted expired receipt %+v", got)
	}
}

func TestProjectActionJobQueueCapacityTTLAndRecovery(t *testing.T) {
	jobs, clock := manualQueueRegistry(&snapshotJobStore{})
	a := &projectJobAgent{inventory: func(context.Context) (docker.Inventory, error) {
		t.Error("expired runner called")
		return docker.Inventory{}, nil
	}}
	api := &handler{agent: a, jobs: jobs}
	var first jobSnapshot
	for i := 0; i < 32; i++ {
		j, err := api.submitProjectAction(a, projectJobInput())
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = j
		}
	}
	if _, err := api.submitProjectAction(a, projectJobInput()); err == nil {
		t.Fatal("unbounded capacity")
	}
	if jobs.removals.len() != 32 || jobs.creates.len() != 0 {
		t.Fatal("incorrect shared queue")
	}
	clock.advance(jobQueueTTL)
	jobs.expireQueued()
	jobs.flush()
	got, _ := jobs.get(first.ID)
	if got.Status != "failed" || got.ErrorCode != "JOB_QUEUE_TIMEOUT" || len(jobs.capacity) != 0 {
		t.Fatalf("expired %+v", got)
	}
	store, err := controlstore.Open(filepath.Join(t.TempDir(), "jobs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	original, _ := manualQueueRegistry(store)
	j, err := (&handler{agent: a, jobs: original}).submitProjectAction(a, projectJobInput())
	if err != nil {
		t.Fatal(err)
	}
	recovered := newJobRegistry(store)
	got, _ = recovered.get(j.ID)
	if got.Status != "interrupted" || got.ProjectActionResult != nil {
		t.Fatal("unsafe recovery")
	}
	e := recovered.lookup(j.ID)
	assertJobRunnerReleased(t, e)
	recovered.execute(e)
}

func TestProjectActionJobCrossResultCorruption(t *testing.T) {
	safe, _, _ := projectActionJobResult(projectJobInput(), projectJobReceipt(projectJobInput()))
	raw, _ := json.Marshal(safe)
	for _, record := range []controlstore.JobRecord{
		{Type: "docker-project-start", Reference: "standalone", Status: "completed", ProjectActionResultJSON: string(raw), CreateResultJSON: `{}`, LayersJSON: `{}`},
		{Type: "docker-container-create", Status: "completed", CreateResultJSON: `{"containerId":"` + projectJobIDs[0] + `","created":true,"started":false}`, ProjectActionResultJSON: `{}`, LayersJSON: `{}`},
	} {
		got := snapshotFromRecord(record)
		if got.Status != "interrupted" || got.ErrorCode != "JOB_HISTORY_CORRUPT" || got.ProjectActionResult != nil || got.CreateResult != nil {
			t.Fatalf("contradictory proof %+v", got)
		}
	}
}
