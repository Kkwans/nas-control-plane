package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	ncpcompose "github.com/Kkwans/nas-control-plane/internal/compose"
	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const composeJobBody = `{"workingDirectory":"/volume1/project","configFiles":["compose.yaml"]}`

func composeJobInput() ncpcompose.LifecycleRequest {
	i, _ := decodeComposeActionJob([]byte(composeJobBody), "demo", "start")
	return i
}
func composeJobReceipt(i ncpcompose.LifecycleRequest) ncpcompose.LifecycleResult {
	state := "running"
	running := true
	if i.Action == "stop" {
		state = "stopped"
		running = false
	}
	return ncpcompose.LifecycleResult{ProjectID: i.ProjectID, Action: i.Action, State: state, Completed: true, Output: "secret output", Services: []ncpcompose.LifecycleServiceStatus{{Name: "secret service", ContainerID: "id", State: state, Running: running}}}
}

type composeJobAgent struct {
	fakeAgentClient
	calls   atomic.Int32
	control func(context.Context, ncpcompose.LifecycleRequest) (ncpcompose.LifecycleResult, error)
}

func (a *composeJobAgent) ControlComposeProject(ctx context.Context, _ string, i ncpcompose.LifecycleRequest) (ncpcompose.LifecycleResult, error) {
	a.calls.Add(1)
	return a.control(ctx, i)
}
func TestComposeActionJobDecode(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"workingDirectory":null,"configFiles":[]}`, `{"workingDirectory":"/volume1/a","configFiles":[null]}`, `{"workingDirectory":"/volume1/a","configFiles":["a.yaml","./a.yaml"]}`, `{"workingDirectory":"/volume1/a","configFiles":["../a.yaml"]}`, strings.TrimSuffix(composeJobBody, "}") + `,"action":"stop"}`, strings.TrimSuffix(composeJobBody, "}") + `,"workingDirectory":"/volume1/b"}`, composeJobBody + `{}`, strings.Replace(composeJobBody, "workingDirectory", "WorkingDirectory", 1), strings.Replace(composeJobBody, "compose.yaml", "bad.txt", 1), strings.Replace(composeJobBody, "project", "pr\\noject", 1), string([]byte{255})} {
		if _, ok := decodeComposeActionJob([]byte(body), "demo", "start"); ok {
			t.Fatalf("accepted %q", body)
		}
	}
	for _, action := range []string{"start", "stop", "restart"} {
		i, ok := decodeComposeActionJob([]byte(composeJobBody), " demo ", action)
		if !ok || i.ProjectID != "demo" || i.ConfigFiles[0] != "/volume1/project/compose.yaml" {
			t.Fatalf("normalize %+v %v", i, ok)
		}
	}
	for _, action := range []string{"START", " start", "deploy", ""} {
		if _, ok := decodeComposeActionJob([]byte(composeJobBody), "demo", action); ok {
			t.Fatal(action)
		}
	}
	for _, id := range []string{"", strings.Repeat("x", 257), "de\nmo"} {
		if _, ok := decodeComposeActionJob([]byte(composeJobBody), id, "start"); ok {
			t.Fatal("id bound")
		}
	}
	for _, n := range []int{0, 1, 32, 33} {
		files := make([]string, n)
		for k := range files {
			files[k] = fmt.Sprintf("%d.yaml", k)
		}
		raw, _ := json.Marshal(map[string]any{"workingDirectory": "/volume1/a", "configFiles": files})
		if _, ok := decodeComposeActionJob(raw, "demo", "start"); ok != (n >= 1 && n <= 32) {
			t.Fatal(n)
		}
	}
}
func TestComposeActionJobReceipt(t *testing.T) {
	for _, action := range []ncpcompose.LifecycleAction{"start", "stop", "restart"} {
		for _, change := range []string{"valid", "project", "action", "completed", "empty", "large", "duplicate", "empty-id", "identity-control", "identity-bound", "state", "summary", "running"} {
			t.Run(string(action)+"/"+change, func(t *testing.T) {
				i := composeJobInput()
				i.Action = action
				r := composeJobReceipt(i)
				switch change {
				case "project":
					r.ProjectID = "other"
				case "action":
					r.Action = "other"
				case "completed":
					r.Completed = false
				case "empty":
					r.Services = nil
				case "large":
					r.Services = make([]ncpcompose.LifecycleServiceStatus, 1025)
				case "duplicate":
					r.Services = append(r.Services, r.Services[0])
				case "empty-id":
					r.Services[0].Name = ""
					r.Services[0].ContainerID = ""
				case "identity-control":
					r.Services[0].Name = "a\n"
				case "identity-bound":
					r.Services[0].Name = strings.Repeat("a", 4097)
				case "state":
					r.Services[0].State = "paused"
				case "summary":
					r.State = "unknown"
				case "running":
					r.Services[0].Running = !r.Services[0].Running
				}
				got := composeActionJobResult(i, r)
				if (got != nil) != (change == "valid") {
					t.Fatalf("%+v", got)
				}
			})
		}
	}
	i := composeJobInput()
	r := composeJobReceipt(i)
	r.Services = append(r.Services, r.Services[0])
	r.Services[1].ContainerID = "replica"
	if composeActionJobResult(i, r) == nil {
		t.Fatal("replica name rejected")
	}
	i.Action = "stop"
	for _, state := range []string{"stopped", "created", "exited", "dead"} {
		r = composeJobReceipt(i)
		r.Services[0].State = state
		if composeActionJobResult(i, r) == nil {
			t.Fatal(state)
		}
	}
}
func TestComposeActionJobFailure(t *testing.T) {
	for _, code := range []string{"COMPOSE_LIFECYCLE_INVALID", "AGENT_PROTOCOL_MISMATCH", "AGENT_RPC_REQUEST_INVALID", "COMPOSE_LIFECYCLE_FAILED", "COMPOSE_LIFECYCLE_VERIFY_FAILED", "COMPOSE_LIFECYCLE_UNAVAILABLE", "AGENT_RPC_RESPONSE_INVALID", "other"} {
		status, _ := composeActionJobFailure(code, errors.New("secret"), nil)
		want := "interrupted"
		if code == "COMPOSE_LIFECYCLE_INVALID" || code == "AGENT_PROTOCOL_MISMATCH" || code == "AGENT_RPC_REQUEST_INVALID" {
			want = "failed"
		}
		if status != want {
			t.Fatal(code, status)
		}
		status, c := composeActionJobFailure(code, nil, context.DeadlineExceeded)
		if status != "interrupted" || c != "JOB_EXECUTION_TIMEOUT" {
			t.Fatal(code)
		}
	}
}
func TestComposeActionJobHTTP(t *testing.T) {
	jobs := newJobRegistry(&snapshotJobStore{})
	defer closeRemoveJobs(t, jobs)
	a := &composeJobAgent{control: func(ctx context.Context, i ncpcompose.LifecycleRequest) (ncpcompose.LifecycleResult, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 90*time.Second {
			t.Error("budget")
		}
		return composeJobReceipt(i), nil
	}}
	h := NewHandler(Config{Agent: a, JobEngine: jobs})
	for _, tc := range []struct {
		body, action string
		status       int
	}{{composeJobBody, "start", 202}, {`{}`, "start", 400}, {composeJobBody, "START", 400}, {strings.Repeat(" ", 65537), "start", 413}} {
		r := httptest.NewRequest("POST", "/api/v1/docker/compose/projects/demo/action-jobs/"+tc.action, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		if w.Code != 202 {
			continue
		}
		var j jobSnapshot
		_ = json.Unmarshal(w.Body.Bytes(), &j)
		if j.Status != "queued" || j.ComposeActionResult != nil || j.Cancellable {
			t.Fatal(j)
		}
		got := waitRemoveJob(t, jobs, j.ID, func(j jobSnapshot) bool { return jobTerminal(j.Status) })
		if got.Status != "completed" || got.ComposeActionResult == nil {
			t.Fatal(got)
		}
		raw, _ := json.Marshal(got)
		for _, secret := range []string{"secret", "volume1", "compose.yaml", "services"} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("leak", string(raw))
			}
		}
		got.ComposeActionResult.ServiceCount = 9
		again, _ := jobs.get(j.ID)
		if again.ComposeActionResult.ServiceCount != 1 {
			t.Fatal("shallow copy")
		}
		for _, op := range []string{"cancel", "retry"} {
			w = httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/jobs/"+j.ID+"/"+op, nil))
			if w.Code != 409 {
				t.Fatal(op, w.Code)
			}
		}
	}
}
func TestComposeActionJobPendingSingleDispatch(t *testing.T) {
	store := &snapshotJobStore{}
	jobs := newJobRegistry(store)
	j := jobs.create("compose-start", "demo")
	jobs.update(j.ID, "running", "running", "", 0)
	store.updateErr = errors.New("disk")
	a := &composeJobAgent{control: func(_ context.Context, i ncpcompose.LifecycleRequest) (ncpcompose.LifecycleResult, error) {
		return composeJobReceipt(i), nil
	}}
	(&handler{jobs: jobs}).runComposeAction(context.Background(), j.ID, a, composeJobInput())
	got, _ := jobs.get(j.ID)
	if got.Status != "running" || got.PersistenceStatus != "pending" || got.ComposeActionResult != nil {
		t.Fatal(got)
	}
	jobs.flush()
	store.updateErr = nil
	jobs.flush()
	got, _ = jobs.get(j.ID)
	if got.Status != "completed" || got.ComposeActionResult == nil || a.calls.Load() != 1 {
		t.Fatal(got)
	}
	one := recordFromSnapshot(got)
	two := one
	two.ComposeActionResultJSON = "different"
	if sameJobRecord(one, two) {
		t.Fatal("ACK ignores result")
	}
	one.ComposeActionResultJSON = `{}`
	if snapshotFromRecord(one).Status != "interrupted" {
		t.Fatal("corrupt success")
	}
}
func TestComposeActionJobSharesDeployQueue(t *testing.T) {
	jobs := newJobRegistry(&snapshotJobStore{})
	release, unblock := queueTestRelease(t)
	queueCleanupRegistry(t, jobs, unblock)
	started := make(chan struct{})
	_, err := jobs.submit("compose-deploy", "", 0, func(_ context.Context, id string) { close(started); <-release; jobs.fail(id, "test", "done", "") })
	if err != nil {
		t.Fatal(err)
	}
	queueReceive(t, started)
	a := &composeJobAgent{control: func(_ context.Context, i ncpcompose.LifecycleRequest) (ncpcompose.LifecycleResult, error) {
		if i.ConfigFiles[0] != "/volume1/project/compose.yaml" {
			t.Error("not frozen")
		}
		return composeJobReceipt(i), nil
	}}
	input := composeJobInput()
	j, err := (&handler{jobs: jobs}).submitComposeAction(a, input)
	if err != nil {
		t.Fatal(err)
	}
	input.ConfigFiles[0] = "tampered"
	e := jobs.lookup(j.ID)
	e.opMu.Lock()
	same := e.queue == jobs.compose
	e.opMu.Unlock()
	if !same || a.calls.Load() != 0 {
		t.Fatal("not serialized")
	}
	unblock()
	waitRemoveJob(t, jobs, j.ID, func(j jobSnapshot) bool { return j.Status == "completed" })
}

func TestComposeActionJobQueueCapacityTTLAndRecovery(t *testing.T) {
	jobs, clock := manualQueueRegistry(&snapshotJobStore{})
	a := &composeJobAgent{control: func(context.Context, ncpcompose.LifecycleRequest) (ncpcompose.LifecycleResult, error) {
		t.Error("expired/recovered RPC")
		return ncpcompose.LifecycleResult{}, nil
	}}
	api := &handler{jobs: jobs}
	var first jobSnapshot
	for i := 0; i < 32; i++ {
		j, err := api.submitComposeAction(a, composeJobInput())
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = j
		}
	}
	if _, err := api.submitComposeAction(a, composeJobInput()); err == nil {
		t.Fatal("unbounded")
	}
	if jobs.compose.len() != 32 || jobs.removals.len() != 0 {
		t.Fatal("queue")
	}
	clock.advance(jobQueueTTL)
	jobs.expireQueued()
	jobs.flush()
	got, _ := jobs.get(first.ID)
	if got.Status != "failed" || got.ErrorCode != "JOB_QUEUE_TIMEOUT" || len(jobs.capacity) != 0 || got.ComposeActionResult != nil {
		t.Fatal(got)
	}
	store, err := controlstore.Open(filepath.Join(t.TempDir(), "jobs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	original, _ := manualQueueRegistry(store)
	j, err := (&handler{jobs: original}).submitComposeAction(a, composeJobInput())
	if err != nil {
		t.Fatal(err)
	}
	recovered := newJobRegistry(store)
	got, _ = recovered.get(j.ID)
	if got.Status != "interrupted" || got.ComposeActionResult != nil {
		t.Fatal(got)
	}
	e := recovered.lookup(j.ID)
	assertJobRunnerReleased(t, e)
	recovered.execute(e)
	if a.calls.Load() != 0 {
		t.Fatal("replay")
	}
}
func TestComposeActionJobRunningPersistenceGate(t *testing.T) {
	store := &snapshotJobStore{updateErr: errors.New("disk")}
	jobs := newJobRegistry(store)
	defer func() { store.mu.Lock(); store.updateErr = nil; store.mu.Unlock(); closeRemoveJobs(t, jobs) }()
	a := &composeJobAgent{control: func(context.Context, ncpcompose.LifecycleRequest) (ncpcompose.LifecycleResult, error) {
		t.Error("before durable running")
		return ncpcompose.LifecycleResult{}, nil
	}}
	j, err := (&handler{jobs: jobs}).submitComposeAction(a, composeJobInput())
	if err != nil {
		t.Fatal(err)
	}
	got := waitRemoveJob(t, jobs, j.ID, func(j jobSnapshot) bool { return j.PersistenceStatus == "pending" })
	if got.Status != "queued" || a.calls.Load() != 0 {
		t.Fatal(got)
	}
	store.mu.Lock()
	store.updateErr = nil
	store.mu.Unlock()
	jobs.flush()
	waitRemoveJob(t, jobs, j.ID, func(j jobSnapshot) bool { return j.Status == "interrupted" })
}
func TestComposeActionJobRouteProtection(t *testing.T) {
	for _, cross := range []bool{false, true} {
		jobs := newJobRegistry(&snapshotJobStore{})
		defer closeRemoveJobs(t, jobs)
		r := httptest.NewRequest("POST", "/api/v1/docker/compose/projects/demo/action-jobs/start", strings.NewReader(composeJobBody))
		r.Header.Set("Content-Type", "application/json")
		want := 401
		if cross {
			r.Header.Set("Sec-Fetch-Site", "cross-site")
			want = 403
		}
		w := httptest.NewRecorder()
		NewHandler(Config{JobEngine: jobs, Auth: openTestAuthService(t)}).ServeHTTP(w, r)
		if w.Code != want {
			t.Fatal(w.Code)
		}
	}
}

func TestComposeActionJobURLIdentityAndEscaping(t *testing.T) {
	for _, id := range []string{"compose:demo", "%3Ademo", "中文", strings.Repeat("<", 256)} {
		t.Run(id, func(t *testing.T) {
			jobs := newJobRegistry(&snapshotJobStore{})
			defer closeRemoveJobs(t, jobs)
			a := &composeJobAgent{control: func(_ context.Context, i ncpcompose.LifecycleRequest) (ncpcompose.LifecycleResult, error) {
				if i.ProjectID != id {
					t.Errorf("identity got %q want %q", i.ProjectID, id)
				}
				return composeJobReceipt(i), nil
			}}
			h := NewHandler(Config{Agent: a, JobEngine: jobs})
			r := httptest.NewRequest("POST", "/api/v1/docker/compose/projects/"+strings.ReplaceAll(url.PathEscape(id), ":", "%3A")+"/action-jobs/start", strings.NewReader(composeJobBody))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			var j jobSnapshot
			_ = json.Unmarshal(w.Body.Bytes(), &j)
			if j.Reference != id {
				t.Fatal(j.Reference)
			}
			got := waitRemoveJob(t, jobs, j.ID, func(j jobSnapshot) bool { return jobTerminal(j.Status) })
			if got.Status != "completed" || got.ComposeActionResult == nil {
				t.Fatal(got)
			}
		})
	}
	jobs := newJobRegistry(&snapshotJobStore{})
	defer closeRemoveJobs(t, jobs)
	r := httptest.NewRequest("POST", "/api/v1/docker/compose/projects/de%0Amo/action-jobs/start", strings.NewReader(composeJobBody))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	NewHandler(Config{JobEngine: jobs}).ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
func TestComposeActionJobInvalidFinishInterrupts(t *testing.T) {
	jobs := newJobRegistry(&snapshotJobStore{})
	j := jobs.create("compose-start", "demo")
	jobs.update(j.ID, "running", "running", "", 0)
	jobs.finishComposeAction(j.ID, &controlstore.ComposeActionJobResult{ProjectID: "other", Action: "start", State: "running", ServiceCount: 1})
	got, _ := jobs.get(j.ID)
	if got.Status != "interrupted" || got.ComposeActionResult != nil {
		t.Fatal(got)
	}
}

func TestComposeActionJobDurableAdmissionAndHTTPDisconnect(t *testing.T) {
	admission, releaseAdmission := queueTestRelease(t)
	release, unblock := queueTestRelease(t)
	store := &removeAdmissionStore{entered: make(chan struct{}), release: admission}
	jobs := newJobRegistry(store)
	queueCleanupRegistry(t, jobs, func() { releaseAdmission(); unblock() })
	started := make(chan struct{})
	a := &composeJobAgent{control: func(ctx context.Context, i ncpcompose.LifecycleRequest) (ncpcompose.LifecycleResult, error) {
		close(started)
		<-release
		if ctx.Err() != nil {
			t.Error("HTTP canceled execution")
		}
		return composeJobReceipt(i), nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "/api/v1/docker/compose/projects/demo/action-jobs/start", strings.NewReader(composeJobBody)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { NewHandler(Config{Agent: a, JobEngine: jobs}).ServeHTTP(w, r); close(done) }()
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
	if j.Status != "queued" || j.ComposeActionResult != nil {
		t.Fatal(j)
	}
	unblock()
	waitRemoveJob(t, jobs, j.ID, func(j jobSnapshot) bool { return j.Status == "completed" })
}
func TestComposeActionJobLateSuccessAndTransportFailure(t *testing.T) {
	for _, mode := range []string{"canceled", "timeout", "transport", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			jobs := newJobRegistry(&snapshotJobStore{})
			j := jobs.create("compose-start", "demo")
			jobs.update(j.ID, "running", "running", "", 0)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "timeout" {
				var c context.CancelFunc
				ctx, c = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer c()
			}
			a := &composeJobAgent{control: func(_ context.Context, i ncpcompose.LifecycleRequest) (ncpcompose.LifecycleResult, error) {
				r := composeJobReceipt(i)
				switch mode {
				case "canceled":
					cancel()
				case "transport":
					return r, errors.New("secret output")
				case "invalid":
					r.Services[0].State = "unknown"
				}
				return r, nil
			}}
			(&handler{jobs: jobs}).runComposeAction(ctx, j.ID, a, composeJobInput())
			got, _ := jobs.get(j.ID)
			if got.Status != "interrupted" || got.ComposeActionResult != nil {
				t.Fatal(got)
			}
			if mode == "timeout" && got.ErrorCode != "JOB_EXECUTION_TIMEOUT" {
				t.Fatal(got)
			}
		})
	}
}

func TestComposeActionJobInputByteBounds(t *testing.T) {
	for _, n := range []int{65536, 65537} {
		raw := []byte(composeJobBody + strings.Repeat(" ", n-len(composeJobBody)))
		if _, ok := decodeComposeActionJob(raw, "demo", "start"); ok != (n == 65536) {
			t.Fatal("body", n)
		}
	}
	for _, id := range []string{strings.Repeat("a", 256), strings.Repeat("界", 85) + "a"} {
		if _, ok := decodeComposeActionJob([]byte(composeJobBody), id, "start"); !ok {
			t.Fatal("valid ID boundary")
		}
	}
	// 单个原始相对路径未超限，但拼接目录后超限也拒绝。
	for _, file := range []string{strings.Repeat("a", 4091) + ".yaml", strings.Repeat("a", 4092) + ".yaml"} {
		raw, _ := json.Marshal(map[string]any{"workingDirectory": "/volume1/a", "configFiles": []string{file}})
		if _, ok := decodeComposeActionJob(raw, "demo", "start"); ok {
			t.Fatal("normalized path bound")
		}
	}
	raw, _ := json.Marshal(map[string]any{"workingDirectory": "/volume1/" + strings.Repeat("a", 4096), "configFiles": []string{"a.yaml"}})
	if _, ok := decodeComposeActionJob(raw, "demo", "start"); ok {
		t.Fatal("directory bound")
	}
}
