package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/go-chi/chi/v5"
)

type createJobAgent struct {
	fakeAgentClient
	calls  atomic.Int32
	create func(context.Context, docker.ContainerCreateRequest) (docker.ContainerCreateResult, error)
}

func (a *createJobAgent) CreateDockerContainer(ctx context.Context, _ string, in docker.ContainerCreateRequest) (docker.ContainerCreateResult, error) {
	a.calls.Add(1)
	return a.create(ctx, in)
}
func createJobSuccess(in docker.ContainerCreateRequest) docker.ContainerCreateResult {
	spec, _ := in.Normalize()
	return docker.ContainerCreateResult{ContainerID: actionJobContainerID, Name: in.Name, Image: spec.Image, State: "stopped", Created: true, Started: in.RunContainer, RunContainer: in.RunContainer}
}
func createJobRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/docker/containers/create-jobs", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}
func TestContainerCreateJobDeepCopy(t *testing.T) {
	gpu := docker.ContainerGPU{Driver: "nvidia", DeviceIDs: []string{"0"}, Capabilities: []string{"gpu"}, Options: map[string]string{"x": "original"}}
	input := docker.ContainerCreateRequest{Image: "alpine:latest", Environment: map[string]string{"SECRET": "original"}, Env: []string{"A=original"}, Mounts: []docker.ContainerMount{{Source: "original"}}, Network: &docker.ContainerNetwork{Name: "original"}, Ports: []docker.ContainerPort{{HostIP: "original"}}, Command: []string{"original"}, CapAdd: []string{"original"}, CapDrop: []string{"original"}, Devices: []docker.ContainerDevice{{HostPath: "original"}}, GPU: &gpu, GPUs: []docker.ContainerGPU{gpu}}
	frozen := cloneContainerCreateRequest(input)
	expected := cloneContainerCreateRequest(frozen)
	input.Environment["SECRET"] = "changed"
	input.Env[0] = "changed"
	input.Mounts[0].Source = "changed"
	input.Network.Name = "changed"
	input.Ports[0].HostIP = "changed"
	input.Command[0] = "changed"
	input.CapAdd[0] = "changed"
	input.CapDrop[0] = "changed"
	input.Devices[0].HostPath = "changed"
	input.GPU.Driver = "changed"
	input.GPU.DeviceIDs[0] = "changed"
	input.GPU.Capabilities[0] = "changed"
	input.GPU.Options["x"] = "changed"
	input.GPUs[0].Driver = "changed"
	if !reflect.DeepEqual(frozen, expected) {
		t.Fatal("request retains aliases")
	}
	frozen.GPU.Options["x"] = "only pointer"
	frozen.GPU.DeviceIDs[0] = "only pointer"
	frozen.GPU.Capabilities[0] = "only pointer"
	if frozen.GPUs[0].Options["x"] != "original" || frozen.GPUs[0].DeviceIDs[0] != "0" || frozen.GPUs[0].Capabilities[0] != "gpu" {
		t.Fatal("GPU aliases inside copy")
	}
}
func TestContainerCreateJobReceipt(t *testing.T) {
	for _, run := range []bool{false, true} {
		input := docker.ContainerCreateRequest{Image: "alpine:latest", RunContainer: run}
		for _, field := range []string{"valid-stopped", "valid-running", "id", "upper-id", "short-id", "image", "created", "started", "run", "state"} {
			r := createJobSuccess(input)
			switch field {
			case "valid-running":
				r.State = "running"
			case "id":
				r.ContainerID = ""
			case "upper-id":
				r.ContainerID = strings.ToUpper(r.ContainerID)
			case "short-id":
				r.ContainerID = r.ContainerID[:12]
			case "image":
				r.Image = "other:latest"
			case "created":
				r.Created = false
			case "started":
				r.Started = !run
			case "run":
				r.RunContainer = !run
			case "state":
				r.State = "paused"
			}
			got := containerCreateJobResult(input.Image, run, r)
			if (got != nil) != strings.HasPrefix(field, "valid-") {
				t.Fatalf("run=%v field=%s result=%+v", run, field, got)
			}
		}
	}
}
func TestContainerCreateJobAdmissionAndProtection(t *testing.T) {
	for _, tc := range []struct {
		body, media string
		status      int
	}{{`{"image":""}`, "application/json", 400}, {`{"image":"alpine:latest"}`, "text/plain", 415}, {`{"image":"alpine:latest"}` + strings.Repeat(" ", 16384), "application/json", 413}, {`{"image":"alpine:latest"} {}`, "application/json", 400}} {
		r := newJobRegistry(&snapshotJobStore{})
		defer closeRemoveJobs(t, r)
		a := &createJobAgent{create: func(context.Context, docker.ContainerCreateRequest) (docker.ContainerCreateResult, error) {
			t.Error("unexpected RPC")
			return docker.ContainerCreateResult{}, nil
		}}
		req := createJobRequest(tc.body)
		req.Header.Set("Content-Type", tc.media)
		w := httptest.NewRecorder()
		NewHandler(Config{Agent: a, JobEngine: r}).ServeHTTP(w, req)
		if w.Code != tc.status || a.calls.Load() != 0 || len(r.list("")) != 0 {
			t.Fatalf("admitted invalid %d %s", w.Code, w.Body.String())
		}
	}
	for _, cross := range []bool{false, true} {
		r := newJobRegistry(&snapshotJobStore{})
		defer closeRemoveJobs(t, r)
		req := createJobRequest(`{"image":"alpine:latest"}`)
		want := 401
		if cross {
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			want = 403
		}
		w := httptest.NewRecorder()
		NewHandler(Config{Agent: &createJobAgent{}, JobEngine: r, Auth: openTestAuthService(t)}).ServeHTTP(w, req)
		if w.Code != want || len(r.list("")) != 0 {
			t.Fatalf("route protection %d", w.Code)
		}
	}
}
func TestContainerCreateJobDurableAdmissionAndHTTPDisconnect(t *testing.T) {
	admissionRelease, unblockAdmission := queueTestRelease(t)
	store := &removeAdmissionStore{entered: make(chan struct{}), release: admissionRelease}
	r := newJobRegistry(store)
	release, unblock := queueTestRelease(t)
	queueCleanupRegistry(t, r, func() { unblockAdmission(); unblock() })
	started := make(chan struct{})
	a := &createJobAgent{create: func(ctx context.Context, in docker.ContainerCreateRequest) (docker.ContainerCreateResult, error) {
		if d, ok := ctx.Deadline(); !ok || time.Until(d) > defaultDockerImageTimeout {
			t.Error("missing timeout")
		}
		close(started)
		<-release
		if ctx.Err() != nil {
			t.Error("HTTP cancellation reached RPC")
		}
		return createJobSuccess(in), nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		NewHandler(Config{Agent: a, JobEngine: r}).ServeHTTP(w, createJobRequest(`{"image":"alpine:latest","runContainer":true}`).WithContext(ctx))
		close(done)
	}()
	queueReceive(t, store.entered)
	if a.calls.Load() != 0 || len(r.list("")) != 0 {
		t.Fatal("RPC before durable admission")
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
	if j.Type != containerCreateJobType || j.Status != "queued" || j.Reference != "" || j.Cancellable || j.ArtifactState != jobArtifactUnknown || j.Progress != 0 || j.DownloadedBytes != 0 || j.TotalBytes != 0 || j.SpeedBytes != 0 || len(j.Layers) != 0 || j.CompletedAt != nil || j.CreateResult != nil || j.ErrorCode != "" || j.Error != "" || j.PersistenceStatus != "" {
		t.Fatalf("bad202 %+v", j)
	}
	cancel()
	queueReceive(t, started)
	unblock()
	final := waitRemoveJob(t, r, j.ID, func(j jobSnapshot) bool { return j.Status == "completed" })
	if final.CreateResult == nil || !final.CreateResult.Started || a.calls.Load() != 1 {
		t.Fatalf("bad completion %+v", final)
	}
}
func TestContainerCreateJobFrozenBeforeQueue(t *testing.T) {
	r, _ := manualQueueRegistry(nil)
	input := docker.ContainerCreateRequest{Image: "alpine:latest", Environment: map[string]string{"KEY": "original"}, Command: []string{"original"}}
	a := &createJobAgent{create: func(_ context.Context, in docker.ContainerCreateRequest) (docker.ContainerCreateResult, error) {
		if in.Environment["KEY"] != "original" || in.Command[0] != "original" {
			t.Error("captured mutable request")
		}
		return createJobSuccess(in), nil
	}}
	api := &handler{jobs: r}
	j, err := api.submitContainerCreate(a, input)
	if err != nil {
		t.Fatal(err)
	}
	input.Environment["KEY"] = "changed"
	input.Command[0] = "changed"
	r.execute(r.creates.pop())
	assertJobRunnerReleased(t, r.lookup(j.ID))
	if a.calls.Load() != 1 {
		t.Fatal("missing RPC")
	}
}
func TestContainerCreateJobErrorClassification(t *testing.T) {
	for _, code := range []string{"DOCKER_CONTAINER_CREATE_FAILED", "DOCKER_CONTAINER_START_FAILED", "DOCKER_CONTAINER_CREATE_CLEANUP_FAILED", "DOCKER_CONTAINER_INSPECT_FAILED", "AGENT_RPC_RESPONSE_INVALID", "AGENT_RPC_TIMEOUT", "AGENT_RPC_CANCELED", "future", "DOCKER_CONTAINER_CREATE_INVALID", "AGENT_PROTOCOL_MISMATCH", "AGENT_RPC_REQUEST_INVALID"} {
		for _, ctxErr := range []error{nil, context.Canceled, context.DeadlineExceeded} {
			status, got := containerCreateJobFailure(code, errors.New("PRIVATE_SENTINEL"), ctxErr)
			want, wantCode := "interrupted", "JOB_EXECUTION_INTERRUPTED"
			if code == "AGENT_RPC_TIMEOUT" || ctxErr == context.DeadlineExceeded {
				wantCode = "JOB_EXECUTION_TIMEOUT"
			} else if ctxErr == nil {
				if code == "AGENT_RPC_RESPONSE_INVALID" {
					wantCode = "DOCKER_CONTAINER_CREATE_RECEIPT_INVALID"
				}
				if code == "DOCKER_CONTAINER_CREATE_INVALID" || code == "AGENT_PROTOCOL_MISMATCH" || code == "AGENT_RPC_REQUEST_INVALID" {
					want, wantCode = "failed", code
				}
			}
			if status != want || got != wantCode {
				t.Fatalf("%s %v => %s %s", code, ctxErr, status, got)
			}
		}
	}
}
func TestContainerCreateJobUnknownRunnerOutcomes(t *testing.T) {
	for _, kind := range []string{"transport", "cancel", "timeout", "invalid-receipt", "late-success"} {
		r, _ := manualQueueRegistry(nil)
		a := &createJobAgent{create: func(ctx context.Context, in docker.ContainerCreateRequest) (docker.ContainerCreateResult, error) {
			switch kind {
			case "transport":
				return docker.ContainerCreateResult{}, errors.New("PRIVATE_SENTINEL")
			case "cancel":
				return docker.ContainerCreateResult{}, context.Canceled
			case "timeout":
				return docker.ContainerCreateResult{}, context.DeadlineExceeded
			case "invalid-receipt":
				v := createJobSuccess(in)
				v.Image = "PRIVATE_SENTINEL"
				return v, nil
			case "late-success":
				for _, e := range r.entries() {
					e.cancel()
				}
				return createJobSuccess(in), nil
			}
			panic("bad case")
		}}
		j, err := (&handler{jobs: r}).submitContainerCreate(a, docker.ContainerCreateRequest{Image: "alpine:latest"})
		if err != nil {
			t.Fatal(err)
		}
		r.execute(r.creates.pop())
		got, _ := r.get(j.ID)
		encoded, _ := json.Marshal(got)
		if got.Status != "interrupted" || got.CreateResult != nil || strings.Contains(string(encoded), "PRIVATE_SENTINEL") || a.calls.Load() != 1 {
			t.Fatalf("%s bad unknown %s", kind, encoded)
		}
	}
}

func TestContainerCreateJobCapacityAndOtherQueues(t *testing.T) {
	r, _ := manualQueueRegistry(nil)
	a := &createJobAgent{create: func(_ context.Context, in docker.ContainerCreateRequest) (docker.ContainerCreateResult, error) {
		return createJobSuccess(in), nil
	}}
	api := &handler{jobs: r}
	first, err := api.submitContainerCreate(a, docker.ContainerCreateRequest{Image: "alpine:latest"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = api.submitContainerCreate(a, docker.ContainerCreateRequest{Image: "alpine:latest"}); !errors.Is(err, errJobQueueFull) {
		t.Fatalf("second create %v", err)
	}
	if len(r.capacity) != 1 || len(r.createCapacity) != 1 || r.creates.len() != 1 {
		t.Fatal("failed admission leaked capacity")
	}
	var shortCalls atomic.Int32
	short := submitQueueTest(t, r, "docker-container-start", &shortCalls)
	if short.queue != r.removals || r.pulls == r.creates || r.compose == r.creates {
		t.Fatal("wrong queue")
	}
	r.execute(r.removals.pop())
	if shortCalls.Load() != 1 || a.calls.Load() != 0 {
		t.Fatal("short action blocked")
	}
	for i := 1; i < jobCapacity; i++ {
		submitQueueTest(t, r, "docker-image-pull", &shortCalls)
	}
	if _, err = r.submit("compose-deploy", "", 0, func(context.Context, string) {}); !errors.Is(err, errJobQueueFull) {
		t.Fatal("global capacity exceeded")
	}
	r.execute(r.creates.pop())
	r.execute(r.lookup(first.ID))
	if len(r.capacity) != jobCapacity-1 || len(r.createCapacity) != 0 {
		t.Fatal("create release count")
	}
	// Global-full rejection must not retain the create-specific slot.
	submitQueueTest(t, r, "compose-deploy", &shortCalls)
	if _, err = api.submitContainerCreate(a, docker.ContainerCreateRequest{Image: "alpine:latest"}); !errors.Is(err, errJobQueueFull) || len(r.createCapacity) != 0 {
		t.Fatal("global rejection retained create slot")
	}
	r.execute(r.compose.pop())
	if _, err = api.submitContainerCreate(a, docker.ContainerCreateRequest{Image: "alpine:latest"}); err != nil {
		t.Fatal(err)
	}
	r.execute(r.creates.pop())
	for r.pulls.len() > 0 {
		r.execute(r.pulls.pop())
	}
	if len(r.capacity) != 0 || len(r.createCapacity) != 0 {
		t.Fatal("reservations leaked")
	}
}

func TestContainerCreateJobPendingAndAcknowledgements(t *testing.T) {
	for _, ack := range []bool{false, true} {
		s := &snapshotJobStore{createCommittedError: ack, committedError: ack}
		r, _ := manualQueueRegistry(s)
		a := &createJobAgent{create: func(_ context.Context, in docker.ContainerCreateRequest) (docker.ContainerCreateResult, error) {
			if !ack {
				s.mu.Lock()
				s.updateErr = errors.New("PRIVATE_SENTINEL")
				s.mu.Unlock()
			}
			return createJobSuccess(in), nil
		}}
		api := &handler{jobs: r}
		j, err := api.submitContainerCreate(a, docker.ContainerCreateRequest{Image: "alpine:latest", RunContainer: true})
		if err != nil {
			t.Fatal(err)
		}
		e := r.creates.pop()
		r.execute(e)
		assertJobRunnerReleased(t, e)
		if !ack {
			current, _ := r.get(j.ID)
			if current.Status != "running" || current.PersistenceStatus != "pending" || current.CreateResult != nil || e.pending == nil || e.pending.CreateResult == nil || len(r.createCapacity) != 1 || len(r.capacity) != 1 {
				t.Fatalf("pending leaked %+v", current)
			}
			copyPending := cloneJobSnapshot(*e.pending)
			copyPending.CreateResult.ContainerID = "mutated"
			if e.pending.CreateResult.ContainerID != actionJobContainerID {
				t.Fatal("pending result aliases")
			}
			r.execute(e)
			r.flush()
			if a.calls.Load() != 1 {
				t.Fatal("pending replayed")
			}
			s.mu.Lock()
			s.updateErr = nil
			s.mu.Unlock()
			r.flush()
		}
		r.execute(e)
		r.flush()
		got, _ := r.get(j.ID)
		durable, err := s.Job(context.Background(), j.ID)
		if err != nil {
			t.Fatal(err)
		}
		result, err := controlstore.ParseContainerCreateJobResult(durable)
		if err != nil || result == nil || got.Status != "completed" || got.CreateResult == nil || a.calls.Load() != 1 || len(r.capacity) != 0 || len(r.createCapacity) != 0 {
			t.Fatalf("lost completion %+v %v", got, err)
		}
		got.CreateResult.ContainerID = "escaped"
		listed := r.list("")
		listed[0].CreateResult.Started = false
		again, _ := r.get(j.ID)
		if again.CreateResult.ContainerID != actionJobContainerID || !again.CreateResult.Started {
			t.Fatal("snapshot aliases result")
		}
	}
}

func TestContainerCreateJobUndispatchedRelease(t *testing.T) {
	for _, path := range []string{"create-failure", "running-failure", "timeout", "closing"} {
		s := &snapshotJobStore{}
		r, c := manualQueueRegistry(s)
		a := &createJobAgent{create: func(context.Context, docker.ContainerCreateRequest) (docker.ContainerCreateResult, error) {
			t.Error("unexpected RPC")
			return docker.ContainerCreateResult{}, nil
		}}
		if path == "create-failure" {
			s.createErr = errors.New("PRIVATE_SENTINEL")
		}
		j, err := (&handler{jobs: r}).submitContainerCreate(a, docker.ContainerCreateRequest{Image: "alpine:latest"})
		if path == "create-failure" {
			if !errors.Is(err, errJobPersist) || len(r.capacity) != 0 || len(r.createCapacity) != 0 || len(r.activeEntries()) != 0 {
				t.Fatal("failed create retained capacity")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		e := r.lookup(j.ID)
		switch path {
		case "running-failure":
			s.updateErr = errors.New("PRIVATE_SENTINEL")
			r.execute(r.creates.pop())
		case "timeout":
			c.advance(jobQueueTTL)
			r.expireQueued()
		case "closing":
			r.admission.Lock()
			r.closing = true
			r.admission.Unlock()
			r.execute(r.creates.pop())
		}
		assertJobRunnerReleased(t, e)
		if a.calls.Load() != 0 || len(r.createCapacity) != 1 || len(r.capacity) != 1 {
			t.Fatal("undispatched pending reservation invalid")
		}
		s.mu.Lock()
		s.updateErr = nil
		s.mu.Unlock()
		r.flush()
		r.execute(e)
		got, _ := r.get(j.ID)
		if a.calls.Load() != 0 || len(r.createCapacity) != 0 || len(r.capacity) != 0 || got.CreateResult != nil || !jobTerminal(got.Status) {
			t.Fatalf("undispatched final %+v", got)
		}
	}
}

func TestContainerCreateJobRunningPersistenceBeforeRPC(t *testing.T) {
	entered := make(chan struct{})
	release, unblock := queueTestRelease(t)
	s := &snapshotJobStore{beforeUpdate: func(j controlstore.JobRecord) {
		if j.Status == "running" {
			close(entered)
			<-release
		}
	}}
	r := newJobRegistry(s)
	queueCleanupRegistry(t, r, unblock)
	a := &createJobAgent{create: func(_ context.Context, in docker.ContainerCreateRequest) (docker.ContainerCreateResult, error) {
		return createJobSuccess(in), nil
	}}
	j, err := (&handler{jobs: r}).submitContainerCreate(a, docker.ContainerCreateRequest{Image: "alpine:latest"})
	if err != nil {
		t.Fatal(err)
	}
	queueReceive(t, entered)
	if a.calls.Load() != 0 {
		t.Fatal("RPC before running persisted")
	}
	unblock()
	waitRemoveJob(t, r, j.ID, func(j jobSnapshot) bool { return j.Status == "completed" })
}

func TestContainerCreateJobCloseCancelsWithoutReplay(t *testing.T) {
	r := newJobRegistry(&snapshotJobStore{})
	started := make(chan struct{})
	a := &createJobAgent{create: func(ctx context.Context, _ docker.ContainerCreateRequest) (docker.ContainerCreateResult, error) {
		close(started)
		<-ctx.Done()
		return docker.ContainerCreateResult{}, ctx.Err()
	}}
	j, err := (&handler{jobs: r}).submitContainerCreate(a, docker.ContainerCreateRequest{Image: "alpine:latest"})
	if err != nil {
		t.Fatal(err)
	}
	queueReceive(t, started)
	closeRemoveJobs(t, r)
	got, _ := r.get(j.ID)
	if got.Status != "interrupted" || got.CreateResult != nil || a.calls.Load() != 1 || len(r.capacity) != 0 || len(r.createCapacity) != 0 {
		t.Fatalf("close %+v", got)
	}
	assertJobRunnerReleased(t, r.lookup(j.ID))
}

func TestContainerCreateJobSafeResultPersistenceAndSurfaces(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "control.db")
	store, err := controlstore.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	r := newJobRegistry(store)
	defer closeRemoveJobs(t, r)
	secret := "PRIVATE_CREATE_SECRET_SENTINEL"
	a := &createJobAgent{create: func(_ context.Context, in docker.ContainerCreateRequest) (docker.ContainerCreateResult, error) {
		return createJobSuccess(in), nil
	}}
	var logs bytes.Buffer
	api := NewHandler(Config{Agent: a, JobEngine: r, ControlStore: store, Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	body := `{"image":"private-image-sentinel:latest","name":"private-name-sentinel","environment":{"SECRET":"` + secret + `"},"command":["` + secret + `"],"runContainer":true}`
	w := httptest.NewRecorder()
	api.ServeHTTP(w, createJobRequest(body))
	if w.Code != 202 {
		t.Fatalf("admit %d %s", w.Code, w.Body.String())
	}
	var initial jobSnapshot
	if err = json.Unmarshal(w.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	final := waitRemoveJob(t, r, initial.ID, func(j jobSnapshot) bool { return j.Status == "completed" })
	record, err := store.Job(context.Background(), initial.ID)
	if err != nil {
		t.Fatal(err)
	}
	rawRecord, _ := json.Marshal(record)
	surfaces := []string{w.Body.String(), string(rawRecord), record.CreateResultJSON}
	for _, path := range []string{"/api/v1/jobs/" + initial.ID, "/api/v1/jobs", "/api/v1/job-history?type=" + containerCreateJobType} {
		response := httptest.NewRecorder()
		api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != 200 {
			t.Fatalf("surface %s %d %s", path, response.Code, response.Body.String())
		}
		surfaces = append(surfaces, response.Body.String())
		if strings.Contains(path, "history") && strings.Contains(response.Body.String(), "createResult") {
			t.Fatal("history leaked result")
		}
	}
	route := chi.NewRouteContext()
	route.URLParams.Add("jobID", initial.ID)
	req := httptest.NewRequest(http.MethodGet, "/fixture", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, route))
	writer := &jobEventWriter{header: http.Header{}, events: make(chan string, 4)}
	(&handler{jobs: r}).jobEvents(writer, req)
	surfaces = append(surfaces, queueReceive(t, writer.events))
	surfaces = append(surfaces, logs.String())
	files, err := filepath.Glob(dbPath + "*")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{secret, "private-image-sentinel", "private-name-sentinel"} {
			if bytes.Contains(data, []byte(value)) {
				t.Fatal("private payload persisted in SQLite files")
			}
		}
	}
	for _, surface := range surfaces {
		for _, value := range []string{secret, "private-image-sentinel", "private-name-sentinel", "environment", "command", "runContainer"} {
			if strings.Contains(surface, value) {
				t.Fatalf("private payload leaked: %s", surface)
			}
		}
	}
	if final.CreateResult == nil || final.CreateResult.ContainerID != actionJobContainerID || record.Reference != "" {
		t.Fatal("missing minimal result")
	}
	// Reopening the engine reads completed safe results and never dispatches old work.
	recovered := newJobRegistry(store)
	defer closeRemoveJobs(t, recovered)
	got, ok := recovered.get(initial.ID)
	if !ok || got.Status != "completed" || got.CreateResult == nil || a.calls.Load() != 1 {
		t.Fatalf("recovery %+v", got)
	}
	for _, operation := range []string{"cancel", "retry"} {
		response := httptest.NewRecorder()
		NewHandler(Config{Agent: a, JobEngine: recovered}).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+initial.ID+"/"+operation, nil))
		if response.Code != 409 {
			t.Fatalf("%s allowed %d", operation, response.Code)
		}
	}
}

func TestContainerCreateJobCorruptRecordsCannotBecomeSuccess(t *testing.T) {
	valid := `{"containerId":"` + actionJobContainerID + `","created":true,"started":false}`
	for _, tc := range []struct{ kind, status, raw string }{{containerCreateJobType, "completed", ""}, {containerCreateJobType, "completed", `{"containerId":"short","created":true,"started":false}`}, {containerCreateJobType, "completed", strings.Replace(valid, `}`, `,"name":"PRIVATE_SENTINEL"}`, 1)}, {containerCreateJobType, "completed", strings.Repeat(" ", 257) + valid}, {containerCreateJobType, "running", valid}, {"docker-image-pull", "completed", valid}, {containerCreateJobType, "failed", valid}} {
		j := snapshotFromRecord(controlstore.JobRecord{ID: "bad", Type: tc.kind, Status: tc.status, CreateResultJSON: tc.raw, LayersJSON: "{}"})
		raw, _ := json.Marshal(j)
		if j.CreateResult != nil || j.Status != "interrupted" || j.ErrorCode != "JOB_HISTORY_CORRUPT" || strings.Contains(string(raw), "PRIVATE_SENTINEL") {
			t.Fatalf("bad record usable %s", raw)
		}
	}
}

// 仅暴露基础接口，避免嵌入fake的额外能力意外通过类型断言。
type noCreateJobAgent struct{ AgentClient }

func TestContainerCreateJobMissingCapability(t *testing.T) {
	r := newJobRegistry(&snapshotJobStore{})
	defer closeRemoveJobs(t, r)
	w := httptest.NewRecorder()
	NewHandler(Config{Agent: noCreateJobAgent{&fakeAgentClient{}}, JobEngine: r}).ServeHTTP(w, createJobRequest(`{"image":"alpine:latest"}`))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "AGENT_DOCKER_CONTROL_UNAVAILABLE") || len(r.list("")) != 0 || len(r.capacity) != 0 || len(r.createCapacity) != 0 {
		t.Fatalf("missing capability admitted %d %s", w.Code, w.Body.String())
	}
}

type differentCreateAckStore struct {
	snapshotJobStore
	raw string
}

func (s *differentCreateAckStore) UpdateJob(ctx context.Context, j controlstore.JobRecord, revision int64) (int64, error) {
	if j.Status == "completed" {
		j.CreateResultJSON = s.raw
		next, err := s.snapshotJobStore.UpdateJob(ctx, j, revision)
		if err != nil {
			return next, err
		}
		return next, errors.New("ACK lost")
	}
	return s.snapshotJobStore.UpdateJob(ctx, j, revision)
}
func TestContainerCreateJobAcknowledgementMustMatchResult(t *testing.T) {
	for _, raw := range []string{`{"containerId":"` + strings.Repeat("f", 64) + `","created":true,"started":false}`, "!", ""} {
		s := &differentCreateAckStore{raw: raw}
		r, _ := manualQueueRegistry(s)
		a := &createJobAgent{create: func(_ context.Context, in docker.ContainerCreateRequest) (docker.ContainerCreateResult, error) {
			return createJobSuccess(in), nil
		}}
		j, err := (&handler{jobs: r}).submitContainerCreate(a, docker.ContainerCreateRequest{Image: "alpine:latest"})
		if err != nil {
			t.Fatal(err)
		}
		e := r.creates.pop()
		r.execute(e)
		r.flush()
		r.execute(e)
		got, _ := r.get(j.ID)
		if got.Status != "running" || got.PersistenceStatus != "pending" || got.CreateResult != nil || e.pending == nil || len(r.createCapacity) != 1 || len(r.capacity) != 1 || a.calls.Load() != 1 {
			t.Fatalf("nonmatching ACK published %+v", got)
		}
		assertJobRunnerReleased(t, e)
	}
}
func TestContainerCreateJobWorkerDoesNotBlockShortActions(t *testing.T) {
	r := newJobRegistry(&snapshotJobStore{})
	release, unblock := queueTestRelease(t)
	queueCleanupRegistry(t, r, unblock)
	started := make(chan struct{})
	a := &createJobAgent{create: func(_ context.Context, in docker.ContainerCreateRequest) (docker.ContainerCreateResult, error) {
		close(started)
		<-release
		return createJobSuccess(in), nil
	}}
	j, err := (&handler{jobs: r}).submitContainerCreate(a, docker.ContainerCreateRequest{Image: "alpine:latest"})
	if err != nil {
		t.Fatal(err)
	}
	queueReceive(t, started)
	shortDone := make(chan struct{})
	short, err := r.submit("docker-container-start", actionJobContainerID, 0, func(_ context.Context, id string) { r.update(id, "completed", "done", "", 100); close(shortDone) })
	if err != nil {
		t.Fatal(err)
	}
	queueReceive(t, shortDone)
	got, _ := r.get(j.ID)
	action, _ := r.get(short.ID)
	if got.Status != "running" || action.Status != "completed" || len(r.createCapacity) != 1 {
		t.Fatal("creation blocked short action")
	}
	unblock()
	waitRemoveJob(t, r, j.ID, func(j jobSnapshot) bool { return j.Status == "completed" })
}
func TestContainerCreateJobRestartDoesNotReplay(t *testing.T) {
	store, err := controlstore.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, status := range []string{"queued", "running"} {
		if err := store.CreateJob(context.Background(), controlstore.JobRecord{ID: status, Type: containerCreateJobType, Status: status, LayersJSON: "{}", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	recovered := newJobRegistry(store)
	defer closeRemoveJobs(t, recovered)
	for _, id := range []string{"queued", "running"} {
		j, ok := recovered.get(id)
		e := recovered.lookup(id)
		if !ok || j.Status != "interrupted" || j.Cancellable || j.CreateResult != nil || e.run != nil || len(recovered.capacity) != 0 || len(recovered.createCapacity) != 0 {
			t.Fatalf("recovery replayed %+v", j)
		}
	}
}

func TestContainerCreateJobRunningCommitCrossesQueueDeadline(t *testing.T) {
	s := &snapshotJobStore{}
	r, c := manualQueueRegistry(s)
	s.beforeUpdate = func(j controlstore.JobRecord) {
		if j.Status == "running" {
			c.advance(jobQueueTTL)
		}
	}
	a := &createJobAgent{create: func(context.Context, docker.ContainerCreateRequest) (docker.ContainerCreateResult, error) {
		t.Error("RPC after queue deadline")
		return docker.ContainerCreateResult{}, nil
	}}
	j, err := (&handler{jobs: r}).submitContainerCreate(a, docker.ContainerCreateRequest{Image: "alpine:latest"})
	if err != nil {
		t.Fatal(err)
	}
	e := r.creates.pop()
	r.execute(e)
	assertJobRunnerReleased(t, e)
	if e.pending == nil || e.pending.ErrorCode != "JOB_QUEUE_TIMEOUT" || len(r.capacity) != 1 || len(r.createCapacity) != 1 {
		t.Fatal("deadline did not freeze pending reservation")
	}
	r.flush()
	r.execute(e)
	got, _ := r.get(j.ID)
	if got.Status != "failed" || got.ErrorCode != "JOB_QUEUE_TIMEOUT" || got.CreateResult != nil || a.calls.Load() != 0 || len(r.capacity) != 0 || len(r.createCapacity) != 0 {
		t.Fatalf("deadline result %+v", got)
	}
}
