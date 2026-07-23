package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/docker"
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
	var body docker.ContainerActionResult
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Name != "web" || body.State != "running" {
		t.Fatalf("action response = %#v", body)
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

type fakeAgentClient struct {
	status           agentsocket.AgentStatus
	statusErr        error
	capabilities     system.Capabilities
	capabilitiesErr  error
	summary          system.Summary
	summaryErr       error
	inventory        docker.Inventory
	inventoryErr     error
	actionResult     docker.ContainerActionResult
	actionErr        error
	actionRequest    docker.ContainerActionRequest
	socketPath       string
	deadlineObserved bool
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

func (f *fakeAgentClient) CollectDockerInventory(ctx context.Context, socketPath string) (docker.Inventory, error) {
	f.socketPath = socketPath
	_, f.deadlineObserved = ctx.Deadline()
	return f.inventory, f.inventoryErr
}

func (f *fakeAgentClient) ControlContainer(ctx context.Context, socketPath string, request docker.ContainerActionRequest) (docker.ContainerActionResult, error) {
	f.socketPath = socketPath
	f.actionRequest = request
	_, f.deadlineObserved = ctx.Deadline()
	return f.actionResult, f.actionErr
}
