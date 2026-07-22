package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/Kkwans/nas-control-plane/internal/system"
)

func TestSystemSummaryUsesRootAgentData(t *testing.T) {
	collectedAt := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	agent := &fakeAgentClient{summary: system.Summary{
		CollectedAt: collectedAt,
		Host:        system.HostSnapshot{Hostname: "DH4300-PLUS"},
		CPU:         system.CPUStats{UsagePercent: 12.5},
	}}
	handler := NewHandler(Config{Agent: agent, AgentTimeout: time.Second, RequestID: func() string { return "req-summary" }})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/system/summary", nil))

	if response.Code != http.StatusOK || !agent.deadlineObserved {
		t.Fatalf("status = %d, deadlineObserved = %t", response.Code, agent.deadlineObserved)
	}
	var body system.Summary
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.CollectedAt != collectedAt || body.Host.Hostname != "DH4300-PLUS" || body.CPU.UsagePercent != 12.5 {
		t.Fatalf("summary = %#v", body)
	}
}

func TestDockerInventoryAndServicesShareAgentInventory(t *testing.T) {
	collectedAt := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	agent := &fakeAgentClient{inventory: docker.Inventory{
		CollectedAt: collectedAt,
		Engine:      docker.EngineInfo{ContainersRunning: 2},
		Containers: []docker.InventoryContainer{{
			ID: "container-1", Name: "ncp-server", Labels: map[string]string{"private": "do-not-send"},
		}},
		Projects: []docker.Project{{ID: "compose:ncp", Name: "ncp", Kind: docker.ProjectKindCompose, ContainerCount: 1, RunningCount: 1, State: "running"}},
	}}
	handler := NewHandler(Config{Agent: agent, AgentTimeout: time.Second, RequestID: func() string { return "req-docker" }})

	inventoryResponse := httptest.NewRecorder()
	handler.ServeHTTP(inventoryResponse, httptest.NewRequest(http.MethodGet, "/api/v1/docker/inventory", nil))
	if inventoryResponse.Code != http.StatusOK || !agent.deadlineObserved {
		t.Fatalf("inventory status = %d, deadlineObserved = %t", inventoryResponse.Code, agent.deadlineObserved)
	}
	var inventory map[string]any
	if err := json.NewDecoder(inventoryResponse.Body).Decode(&inventory); err != nil {
		t.Fatalf("decode inventory: %v", err)
	}
	container := inventory["containers"].([]any)[0].(map[string]any)
	if _, found := container["labels"]; found {
		t.Fatalf("raw labels must not reach HTTP response: %#v", container)
	}

	servicesResponse := httptest.NewRecorder()
	handler.ServeHTTP(servicesResponse, httptest.NewRequest(http.MethodGet, "/api/v1/services", nil))
	if servicesResponse.Code != http.StatusOK {
		t.Fatalf("services status = %d", servicesResponse.Code)
	}
	var services ServiceListResponse
	if err := json.NewDecoder(servicesResponse.Body).Decode(&services); err != nil {
		t.Fatalf("decode services: %v", err)
	}
	if services.CollectedAt != collectedAt || len(services.Services) != 1 || services.Services[0].Name != "ncp" {
		t.Fatalf("services = %#v", services)
	}
}

func TestDashboardRoutesReturnStableUnavailableErrors(t *testing.T) {
	handler := NewHandler(Config{Agent: &fakeAgentClient{summaryErr: errors.New("agent unavailable"), inventoryErr: errors.New("docker unavailable")}, RequestID: func() string { return "req-dashboard" }})
	for _, route := range []struct {
		path string
		code string
	}{
		{path: "/api/v1/system/summary", code: "SYSTEM_SUMMARY_UNAVAILABLE"},
		{path: "/api/v1/docker/inventory", code: "DOCKER_INVENTORY_UNAVAILABLE"},
		{path: "/api/v1/services", code: "DOCKER_INVENTORY_UNAVAILABLE"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, route.path, nil))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d", route.path, response.Code)
		}
		var body ErrorResponse
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatalf("decode %s error: %v", route.path, err)
		}
		if body.Code != route.code || body.RequestID != "req-dashboard" {
			t.Fatalf("%s body = %#v", route.path, body)
		}
	}
}
