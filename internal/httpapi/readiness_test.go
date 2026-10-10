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
)

func TestReadinessDistinguishesDependencyFailuresAndRecovers(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		agentErr, dockerErr     error
		code                    int
		agentState, dockerState string
	}{
		{"ready", nil, nil, 200, "ready", "ready"},
		{"agent unavailable", errors.New("secret-path-and-token"), nil, 503, "unavailable", "not_checked"},
		{"docker unavailable", nil, errors.New("secret-path-and-token"), 503, "ready", "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent := &fakeAgentClient{statusErr: tc.agentErr, inventoryErr: tc.dockerErr}
			handler := NewHandler(Config{Agent: agent, AgentTimeout: time.Second})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if response.Code != tc.code {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			var body ReadinessResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Checks["agent"] != tc.agentState || body.Checks["docker"] != tc.dockerState {
				t.Fatalf("checks = %v", body.Checks)
			}
			if strings.Contains(response.Body.String(), "secret") {
				t.Fatal("readiness leaked internal error")
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("readiness must not be cached")
			}
			agent.statusErr, agent.inventoryErr = nil, nil
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if response.Code != 200 {
				t.Fatal("dependency recovery must not require HTTP server restart")
			}
		})
	}
}

type deadlineProbeClient struct{ fakeAgentClient }

func (deadlineProbeClient) Probe(ctx context.Context, _ string) (agentsocket.AgentStatus, error) {
	<-ctx.Done()
	return agentsocket.AgentStatus{}, ctx.Err()
}

func TestReadinessIsBoundedAndLivenessIndependent(t *testing.T) {
	handler := NewHandler(Config{Agent: &deadlineProbeClient{}, AgentTimeout: 20 * time.Millisecond})
	start := time.Now()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != 503 || time.Since(start) > time.Second {
		t.Fatalf("readiness did not respect timeout: %d", response.Code)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != 200 {
		t.Fatal("dependency outage must not fail HTTP liveness")
	}
}

type readinessAuth struct {
	Authenticator
	err error
}

func (a *readinessAuth) Initialized(context.Context) (bool, error) { return false, a.err }

func TestReadinessIncludesConfiguredAuthenticationDatabase(t *testing.T) {
	auth := &readinessAuth{err: errors.New("private-database-path")}
	handler := NewHandler(Config{Agent: &fakeAgentClient{}, Auth: auth})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != 503 || strings.Contains(response.Body.String(), "private") {
		t.Fatalf("database readiness = %d: %s", response.Code, response.Body.String())
	}
	auth.err = nil
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != 200 {
		t.Fatal("database recovery must restore readiness")
	}
}
