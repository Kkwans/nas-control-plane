package httpapi

import (
	"context"
	"net/http"
	"time"
)

// Readiness exposes only dependency states, never host inventory or raw errors.
// Liveness remains independent so an external dependency outage does not cause
// the HTTP service to be restarted in a loop.
type ReadinessResponse struct {
	Status  string            `json:"status"`
	Service string            `json:"service"`
	Checks  map[string]string `json:"checks"`
}

func (api *handler) readyz(response http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), min(api.agentTimeout, 3*time.Second))
	defer cancel()
	checks := map[string]string{"agent": "unavailable", "docker": "not_checked"}
	if api.jobs != nil {
		checks["jobs"] = api.jobs.readiness()
	}
	if _, err := api.agent.Probe(ctx, api.agentSocketPath); err == nil {
		checks["agent"] = "ready"
		checks["docker"] = "unavailable"
		if _, err := api.agent.CollectDockerInventory(ctx, api.agentSocketPath); err == nil {
			checks["docker"] = "ready"
		}
	}
	if api.auth != nil {
		checks["database"] = "unavailable"
		if _, err := api.auth.Initialized(ctx); err == nil {
			checks["database"] = "ready"
		}
	}
	code, status := http.StatusOK, "ready"
	for _, check := range checks {
		if check != "ready" {
			code, status = http.StatusServiceUnavailable, "unavailable"
			break
		}
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, code, ReadinessResponse{Status: status, Service: "ncp-server", Checks: checks})
}
