package caddy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRunPOCAddsPersistsRestartsAndDeletesRoute(t *testing.T) {
	sandbox := newFakeSandbox(t, false)
	defer sandbox.server.Close()

	result, err := RunPOC(context.Background(), sandbox)

	if err != nil {
		t.Fatalf("run caddy POC: %v", err)
	}
	if !result.RouteAdded || !result.ConfigPersisted || !result.RestartRecovered || !result.RouteDeleted {
		t.Fatalf("result = %#v", result)
	}
	if sandbox.restarts != 1 || !sandbox.closed {
		t.Fatalf("sandbox restarts=%d closed=%t", sandbox.restarts, sandbox.closed)
	}
}

func TestRunPOCCleansUpSandboxWhenRouteAddFails(t *testing.T) {
	sandbox := newFakeSandbox(t, true)
	defer sandbox.server.Close()

	_, err := RunPOC(context.Background(), sandbox)

	if ErrorCode(err) != "CADDY_POC_ROUTE_ADD_FAILED" {
		t.Fatalf("error code = %q", ErrorCode(err))
	}
	if !sandbox.closed {
		t.Fatal("sandbox was not closed after route-add failure")
	}
}

type fakeSandbox struct {
	server      *httptest.Server
	routeActive bool
	restarts    int
	closed      bool
	rejectAdd   bool
}

func newFakeSandbox(t *testing.T, rejectAdd bool) *fakeSandbox {
	t.Helper()
	sandbox := &fakeSandbox{rejectAdd: rejectAdd}
	sandbox.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/config/apps/http/servers/ncp-p0/routes":
			if sandbox.rejectAdd {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			sandbox.routeActive = true
			writer.WriteHeader(http.StatusOK)
		case request.Method == http.MethodDelete && request.URL.Path == "/id/"+pocRouteID:
			sandbox.routeActive = false
			writer.WriteHeader(http.StatusOK)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	return sandbox
}

func (s *fakeSandbox) Start(context.Context) (string, error) {
	return s.server.URL, nil
}

func (s *fakeSandbox) Restart(context.Context) error {
	s.restarts++
	return nil
}

func (s *fakeSandbox) Request(context.Context, string) (Response, error) {
	if !s.routeActive {
		return Response{StatusCode: http.StatusNotFound}, nil
	}
	return Response{StatusCode: http.StatusOK, Body: pocRouteBody}, nil
}

func (s *fakeSandbox) Close(context.Context) error {
	s.closed = true
	return nil
}
