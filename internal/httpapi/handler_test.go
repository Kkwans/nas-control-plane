package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	ncpcompose "github.com/Kkwans/nas-control-plane/internal/compose"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/Kkwans/nas-control-plane/internal/journal"
	"github.com/Kkwans/nas-control-plane/internal/system"
)

func TestHealthzReturnsStableServiceResponse(t *testing.T) {
	handler := NewHandler(Config{
		RequestID: func() string { return "req-health" },
	})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("X-Request-ID"); got != "req-health" {
		t.Fatalf("X-Request-ID = %q, want req-health", got)
	}

	var body HealthResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "ok" || body.Service != "ncp-server" {
		t.Fatalf("health response = %#v", body)
	}
}

func TestCapabilitiesUsesAgentAndPropagatesRequestContextDeadline(t *testing.T) {
	agent := &fakeAgentClient{capabilities: system.Capabilities{
		Hostname:     "DH4300-PLUS",
		Architecture: "arm64",
		Docker:       true,
	}}
	handler := NewHandler(Config{
		Agent:           agent,
		AgentSocketPath: "/run/ncp/test.sock",
		AgentTimeout:    time.Second,
		RequestID:       func() string { return "req-capabilities" },
	})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/system/capabilities", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if agent.socketPath != "/run/ncp/test.sock" {
		t.Fatalf("agent socket path = %q", agent.socketPath)
	}
	if !agent.deadlineObserved {
		t.Fatal("Agent 调用必须带有超时 Deadline")
	}

	var body system.Capabilities
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Hostname != "DH4300-PLUS" || !body.Docker {
		t.Fatalf("capabilities = %#v", body)
	}
}

func TestCapabilitiesReturnsStableErrorWhenAgentIsUnavailable(t *testing.T) {
	handler := NewHandler(Config{
		Agent:     &fakeAgentClient{capabilitiesErr: errors.New("socket unavailable")},
		RequestID: func() string { return "req-agent-unavailable" },
	})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/system/capabilities", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	var body ErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "SYSTEM_CAPABILITIES_UNAVAILABLE" || body.RequestID != "req-agent-unavailable" {
		t.Fatalf("error response = %#v", body)
	}
}

func TestSystemDetailsAddsControlPlaneNodes(t *testing.T) {
	agent := &fakeAgentClient{details: system.Details{
		CollectedAt: time.Now().UTC(),
		Warnings:    []string{},
		Device:      system.DeviceDetails{Hostname: "DH4300Plus"},
		Control: system.ControlDetails{Nodes: []system.ControlNode{
			{ID: "agent", Name: "Root Agent", Status: "ready", Version: "agent-test"},
		}},
	}}
	handler := NewHandler(Config{
		Agent:           agent,
		AgentSocketPath: "/run/ncp/test.sock",
		AgentTimeout:    time.Second,
		RequestID:       func() string { return "req-details" },
	})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/system/details", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if !agent.deadlineObserved {
		t.Fatal("系统详情 Agent 调用必须带有超时 Deadline")
	}
	var body system.Details
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Device.Hostname != "DH4300Plus" {
		t.Fatalf("hostname = %q", body.Device.Hostname)
	}
	if len(body.Control.Nodes) != 4 {
		t.Fatalf("control nodes = %#v", body.Control.Nodes)
	}
	wantIDs := []string{"web", "server", "socket", "agent"}
	for index, want := range wantIDs {
		if body.Control.Nodes[index].ID != want {
			t.Fatalf("control node %d = %q, want %q", index, body.Control.Nodes[index].ID, want)
		}
	}
}

func TestAgentStatusIsReadOnlyAgentProbe(t *testing.T) {
	handler := NewHandler(Config{
		Agent: &fakeAgentClient{status: agentsocket.AgentStatus{
			ProtocolVersion: "p0-v1",
			AgentEUID:       0,
			Transport:       "unix",
		}},
		RequestID: func() string { return "req-agent-status" },
	})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/system/agent-status", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var body agentsocket.AgentStatus
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.AgentEUID != 0 || body.Transport != "unix" {
		t.Fatalf("agent status = %#v", body)
	}
}

func TestAgentStatusReturnsStableErrorWhenProbeFails(t *testing.T) {
	handler := NewHandler(Config{
		Agent:     &fakeAgentClient{statusErr: errors.New("socket unavailable")},
		RequestID: func() string { return "req-agent-status-unavailable" },
	})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/system/agent-status", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	var body ErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "AGENT_STATUS_UNAVAILABLE" || body.RequestID != "req-agent-status-unavailable" {
		t.Fatalf("error response = %#v", body)
	}
}

func TestContainerActionUsesRootAgentAndReturnsState(t *testing.T) {
	agent := &fakeAgentClient{actionResult: docker.ContainerActionResult{
		ContainerID: "abc123",
		Name:        "web",
		Action:      docker.ContainerActionRestart,
		State:       "running",
	}}
	handler := NewHandler(Config{
		Agent:           agent,
		AgentSocketPath: "/run/ncp/test.sock",
		AgentTimeout:    time.Second,
		RequestID:       func() string { return "req-container-action" },
	})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/docker/containers/abc123/actions/restart", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if agent.actionRequest.ContainerID != "abc123" || agent.actionRequest.Action != docker.ContainerActionRestart {
		t.Fatalf("action request = %#v", agent.actionRequest)
	}
	if agent.socketPath != "/run/ncp/test.sock" || !agent.deadlineObserved {
		t.Fatalf("agent call = socket %q deadline=%v", agent.socketPath, agent.deadlineObserved)
	}
	if agent.deadlineRemaining < 40*time.Second {
		t.Fatalf("container action deadline = %s, want at least 40s", agent.deadlineRemaining)
	}
	var body docker.ContainerActionResult
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Name != "web" || body.State != "running" {
		t.Fatalf("action response = %#v", body)
	}
}

func TestContainerActionMapsDeadlineToGatewayTimeout(t *testing.T) {
	agent := &fakeAgentClient{actionErr: context.DeadlineExceeded}
	handler := NewHandler(Config{
		Agent: agent, AgentTimeout: time.Second,
		RequestID: func() string { return "req-container-timeout" },
	})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/docker/containers/abc123/actions/stop", nil))

	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusGatewayTimeout, response.Body.String())
	}
	var body ErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "AGENT_RPC_TIMEOUT" || body.RequestID != "req-container-timeout" {
		t.Fatalf("error response = %#v", body)
	}
}

func TestComposeProjectActionUsesDedicatedTimeout(t *testing.T) {
	agent := &fakeAgentClient{composeActionResult: ncpcompose.LifecycleResult{
		ProjectID: "compose:demo", Action: ncpcompose.LifecycleActionStop, State: "stopped", Completed: true,
		Services: []ncpcompose.LifecycleServiceStatus{{Name: "web", State: "stopped"}},
	}}
	handler := NewHandler(Config{
		Agent: agent, AgentTimeout: time.Second,
		RequestID: func() string { return "req-compose-timeout" },
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/docker/compose/projects/compose%3Ademo/actions/stop", strings.NewReader(`{
		"projectId":"compose:forged",
		"workingDirectory":"/volume2/DockerProject/demo",
		"configFiles":["/volume2/DockerProject/demo/compose.yaml"]
	}`))
	request.Header.Set("Content-Type", "application/json")

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if agent.composeActionRequest.ProjectID != "compose:demo" || agent.composeActionRequest.Action != ncpcompose.LifecycleActionStop {
		t.Fatalf("compose action request = %#v", agent.composeActionRequest)
	}
	if agent.deadlineRemaining < 85*time.Second {
		t.Fatalf("compose action deadline = %s, want at least 85s", agent.deadlineRemaining)
	}
}

func TestContainerActionRejectsUnknownAction(t *testing.T) {
	agent := &fakeAgentClient{}
	handler := NewHandler(Config{Agent: agent, RequestID: func() string { return "req-container-action-invalid" }})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/docker/containers/abc123/actions/scale", nil))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	var body ErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "DOCKER_CONTAINER_ACTION_INVALID" || body.RequestID != "req-container-action-invalid" {
		t.Fatalf("error response = %#v", body)
	}
}

func TestCreateDockerContainerUsesStructuredAgentRequest(t *testing.T) {
	agent := &fakeAgentClient{createResult: docker.ContainerCreateResult{
		ContainerID: "created-123", Name: "redis-cache", Image: "redis:8-alpine",
		State: "stopped", Created: true, RunContainer: false,
	}}
	handler := NewHandler(Config{
		Agent: agent, AgentSocketPath: "/run/ncp/test.sock", AgentTimeout: time.Second,
		RequestID: func() string { return "req-container-create" },
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/docker/containers", strings.NewReader(`{
		"image":"redis:8-alpine","name":"redis-cache","command":["redis-server","--appendonly","yes"],"runContainer":false
	}`))
	request.Header.Set("Content-Type", "application/json")

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if agent.createRequest.Image != "redis:8-alpine" || len(agent.createRequest.Command) != 3 || agent.createRequest.Command[0] != "redis-server" {
		t.Fatalf("create request = %#v", agent.createRequest)
	}
	if agent.socketPath != "/run/ncp/test.sock" || !agent.deadlineObserved {
		t.Fatalf("agent call = socket %q deadline=%v", agent.socketPath, agent.deadlineObserved)
	}
}

func TestDeleteDockerProjectUsesPathIdentityAndRegistryName(t *testing.T) {
	agent := &fakeAgentClient{deleteResult: docker.ProjectDeleteResult{
		ProjectID: "compose:demo", Kind: docker.ProjectKindCompose, Completed: true,
		RegistryDeleted: true, Containers: []docker.ProjectDeleteContainerResult{},
	}}
	handler := NewHandler(Config{
		Agent: agent, AgentSocketPath: "/run/ncp/test.sock", AgentTimeout: time.Second,
		RequestID: func() string { return "req-project-delete" },
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/docker/compose/projects/compose%3Ademo", strings.NewReader(`{
		"projectId":"compose:forged","kind":"standalone","registryName":"demo"
	}`))
	request.Header.Set("Content-Type", "application/json")

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if agent.deleteRequest.ProjectID != "compose:demo" || agent.deleteRequest.Kind != docker.ProjectKindCompose || agent.deleteRequest.RegistryName != "demo" {
		t.Fatalf("delete request = %#v", agent.deleteRequest)
	}
	if !agent.deadlineObserved {
		t.Fatal("项目删除 Agent 调用必须带有超时 Deadline")
	}
}

func TestContainerLogsUsesDefaultTailAndReturnsEntries(t *testing.T) {
	agent := &fakeAgentClient{logsResult: docker.ContainerLogsResult{
		ContainerID: "abc123",
		Tail:        docker.DefaultContainerLogTail,
		CollectedAt: time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC),
		Entries:     []docker.ContainerLogEntry{{Stream: "stdout", Message: "ready"}},
	}}
	handler := NewHandler(Config{
		Agent:           agent,
		AgentSocketPath: "/run/ncp/test.sock",
		AgentTimeout:    time.Second,
		RequestID:       func() string { return "req-container-logs" },
	})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/docker/containers/abc123/logs", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if agent.logsRequest.ContainerID != "abc123" || agent.logsRequest.Tail != docker.DefaultContainerLogTail {
		t.Fatalf("logs request = %#v", agent.logsRequest)
	}
	var body docker.ContainerLogsResult
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Entries) != 1 || body.Entries[0].Message != "ready" {
		t.Fatalf("logs response = %#v", body)
	}
}

func TestContainerLogsRejectsTailOutsideSupportedRange(t *testing.T) {
	agent := &fakeAgentClient{}
	handler := NewHandler(Config{Agent: agent, RequestID: func() string { return "req-container-logs-invalid" }})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/docker/containers/abc123/logs?tail=999", nil))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	var body ErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "DOCKER_LOGS_INPUT_INVALID" || body.RequestID != "req-container-logs-invalid" {
		t.Fatalf("error response = %#v", body)
	}
}

type fakeAgentClient struct {
	status               agentsocket.AgentStatus
	statusErr            error
	capabilities         system.Capabilities
	capabilitiesErr      error
	summary              system.Summary
	summaryErr           error
	details              system.Details
	detailsErr           error
	inventory            docker.Inventory
	inventoryErr         error
	actionResult         docker.ContainerActionResult
	actionErr            error
	actionRequest        docker.ContainerActionRequest
	createResult         docker.ContainerCreateResult
	createErr            error
	createRequest        docker.ContainerCreateRequest
	deleteResult         docker.ProjectDeleteResult
	deleteErr            error
	deleteRequest        docker.ProjectDeleteRequest
	projectActionResult  docker.ProjectActionResult
	projectActionErr     error
	projectActionRequest docker.ProjectActionRequest
	composeActionResult  ncpcompose.LifecycleResult
	composeActionErr     error
	composeActionRequest ncpcompose.LifecycleRequest
	logsResult           docker.ContainerLogsResult
	logsErr              error
	logsRequest          docker.ContainerLogsRequest
	journalPage          journal.Page
	journalErr           error
	journalQuery         journal.Query
	socketPath           string
	deadlineObserved     bool
	deadlineRemaining    time.Duration
}

func (f *fakeAgentClient) Probe(_ context.Context, socketPath string) (agentsocket.AgentStatus, error) {
	f.socketPath = socketPath
	return f.status, f.statusErr
}

func (f *fakeAgentClient) CollectCapabilities(ctx context.Context, socketPath string) (system.Capabilities, error) {
	f.socketPath = socketPath
	_, f.deadlineObserved = ctx.Deadline()
	return f.capabilities, f.capabilitiesErr
}

func (f *fakeAgentClient) CollectSystemSummary(ctx context.Context, socketPath string) (system.Summary, error) {
	f.socketPath = socketPath
	_, f.deadlineObserved = ctx.Deadline()
	return f.summary, f.summaryErr
}

func (f *fakeAgentClient) CollectSystemDetails(ctx context.Context, socketPath string) (system.Details, error) {
	f.socketPath = socketPath
	_, f.deadlineObserved = ctx.Deadline()
	return f.details, f.detailsErr
}

func (f *fakeAgentClient) CollectDockerInventory(ctx context.Context, socketPath string) (docker.Inventory, error) {
	f.socketPath = socketPath
	_, f.deadlineObserved = ctx.Deadline()
	return f.inventory, f.inventoryErr
}

func (f *fakeAgentClient) ControlContainer(ctx context.Context, socketPath string, request docker.ContainerActionRequest) (docker.ContainerActionResult, error) {
	f.socketPath = socketPath
	f.actionRequest = request
	deadline, observed := ctx.Deadline()
	f.deadlineObserved = observed
	if observed {
		f.deadlineRemaining = time.Until(deadline)
	}
	return f.actionResult, f.actionErr
}

func (f *fakeAgentClient) ControlStandaloneProject(ctx context.Context, socketPath string, request docker.ProjectActionRequest) (docker.ProjectActionResult, error) {
	f.socketPath = socketPath
	f.projectActionRequest = request
	deadline, observed := ctx.Deadline()
	f.deadlineObserved = observed
	if observed {
		f.deadlineRemaining = time.Until(deadline)
	}
	return f.projectActionResult, f.projectActionErr
}

func (f *fakeAgentClient) ControlComposeProject(ctx context.Context, socketPath string, request ncpcompose.LifecycleRequest) (ncpcompose.LifecycleResult, error) {
	f.socketPath = socketPath
	f.composeActionRequest = request
	deadline, observed := ctx.Deadline()
	f.deadlineObserved = observed
	if observed {
		f.deadlineRemaining = time.Until(deadline)
	}
	return f.composeActionResult, f.composeActionErr
}

func (f *fakeAgentClient) CreateDockerContainer(ctx context.Context, socketPath string, request docker.ContainerCreateRequest) (docker.ContainerCreateResult, error) {
	f.socketPath = socketPath
	f.createRequest = request
	_, f.deadlineObserved = ctx.Deadline()
	return f.createResult, f.createErr
}

func (f *fakeAgentClient) DeleteDockerProject(ctx context.Context, socketPath string, request docker.ProjectDeleteRequest) (docker.ProjectDeleteResult, error) {
	f.socketPath = socketPath
	f.deleteRequest = request
	_, f.deadlineObserved = ctx.Deadline()
	return f.deleteResult, f.deleteErr
}

func (f *fakeAgentClient) ReadContainerLogs(ctx context.Context, socketPath string, request docker.ContainerLogsRequest) (docker.ContainerLogsResult, error) {
	f.socketPath = socketPath
	f.logsRequest = request
	_, f.deadlineObserved = ctx.Deadline()
	return f.logsResult, f.logsErr
}

func (f *fakeAgentClient) QueryJournal(ctx context.Context, socketPath string, query journal.Query) (journal.Page, error) {
	f.socketPath = socketPath
	f.journalQuery = query
	_, f.deadlineObserved = ctx.Deadline()
	return f.journalPage, f.journalErr
}
