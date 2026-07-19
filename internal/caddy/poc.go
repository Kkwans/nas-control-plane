package caddy

import (
	"context"
	"errors"
	"net/http"
	"time"
)

const (
	pocOperationTimeout = 8 * time.Second
	pocCleanupTimeout   = 5 * time.Second
	pocRetryInterval    = 100 * time.Millisecond
)

// Sandbox owns the isolated runtime used solely for P0 Caddy verification.
// Its API deliberately does not expose arbitrary Docker, port, or command input.
type Sandbox interface {
	Start(context.Context) (string, error)
	Restart(context.Context) error
	Request(context.Context, string) (Response, error)
	Close(context.Context) error
}

// Response is the small HTTP result required to verify the fixed P0 route.
type Response struct {
	StatusCode int
	Body       string
}

// POCResult reports only phase acceptance flags, never endpoint or config data.
type POCResult struct {
	RouteAdded       bool `json:"routeAdded"`
	ConfigPersisted  bool `json:"configPersisted"`
	RestartRecovered bool `json:"restartRecovered"`
	RouteDeleted     bool `json:"routeDeleted"`
}

// RunPOC verifies adding, persisting, restoring, and removing the fixed P0
// Caddy route. Sandbox cleanup is always attempted with an independent timeout.
func RunPOC(ctx context.Context, sandbox Sandbox) (result POCResult, runErr error) {
	if sandbox == nil {
		return POCResult{}, coded("CADDY_POC_UNAVAILABLE", errors.New("caddy POC sandbox is required"))
	}
	defer func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), pocCleanupTimeout)
		defer cancel()
		if err := sandbox.Close(cleanupContext); err != nil {
			runErr = coded("CADDY_POC_CLEANUP_FAILED", errors.Join(runErr, err))
		}
	}()

	operationContext, cancel := context.WithTimeout(ctx, pocOperationTimeout)
	defer cancel()
	endpoint, err := sandbox.Start(operationContext)
	if err != nil {
		return POCResult{}, coded("CADDY_POC_START_FAILED", err)
	}
	client, err := NewClient(endpoint, http.DefaultClient)
	if err != nil {
		return POCResult{}, coded("CADDY_POC_START_FAILED", err)
	}

	if err := client.AddPOCRoute(operationContext); err != nil {
		return POCResult{}, coded("CADDY_POC_ROUTE_ADD_FAILED", err)
	}
	if err := waitForResponse(operationContext, sandbox, http.StatusOK, pocRouteBody); err != nil {
		return POCResult{}, coded("CADDY_POC_ROUTE_VERIFY_FAILED", err)
	}
	result.RouteAdded = true

	if err := sandbox.Restart(operationContext); err != nil {
		return POCResult{}, coded("CADDY_POC_RESTART_FAILED", err)
	}
	if err := waitForResponse(operationContext, sandbox, http.StatusOK, pocRouteBody); err != nil {
		return POCResult{}, coded("CADDY_POC_PERSISTENCE_FAILED", err)
	}
	result.ConfigPersisted = true
	result.RestartRecovered = true

	if err := client.DeletePOCRoute(operationContext); err != nil {
		return POCResult{}, coded("CADDY_POC_ROUTE_DELETE_FAILED", err)
	}
	if err := waitForResponse(operationContext, sandbox, http.StatusNotFound, ""); err != nil {
		return POCResult{}, coded("CADDY_POC_ROUTE_DELETE_FAILED", err)
	}
	result.RouteDeleted = true
	return result, nil
}

func waitForResponse(ctx context.Context, sandbox Sandbox, expectedStatus int, expectedBody string) error {
	for {
		response, err := sandbox.Request(ctx, pocRoutePath)
		if err == nil && response.StatusCode == expectedStatus && (expectedBody == "" || response.Body == expectedBody) {
			return nil
		}
		timer := time.NewTimer(pocRetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
