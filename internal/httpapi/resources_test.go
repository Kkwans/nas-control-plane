package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/Kkwans/nas-control-plane/internal/filesystem"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

func TestDockerResourcesRouteReturnsEngineInventory(t *testing.T) {
	collectedAt := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	resources := &fakeDockerResourcesAgent{result: docker.Resources{
		CollectedAt: collectedAt,
		Networks:    []docker.Network{{ID: "network-id", Name: "bridge", Driver: "bridge", Scope: "local", Subnets: []string{}, Gateways: []string{}}},
		Volumes:     []docker.Volume{{Name: "app-data", Driver: "local", Scope: "local", Mountpoint: "/var/lib/docker/volumes/app-data/_data"}},
	}}
	handler := NewHandler(Config{
		DockerResources: resources,
		AgentSocketPath: "/run/ncp/test.sock",
		AgentTimeout:    time.Second,
		RequestID:       func() string { return "req-resources" },
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/docker/resources", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	var body docker.Resources
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.CollectedAt.Equal(collectedAt) || len(body.Networks) != 1 || len(body.Volumes) != 1 {
		t.Fatalf("resources = %#v", body)
	}
	if resources.socketPath != "/run/ncp/test.sock" || !resources.deadlineObserved {
		t.Fatalf("agent call did not preserve request context: %#v", resources)
	}
}

func TestDockerResourcesRouteReturnsStableUnavailableError(t *testing.T) {
	handler := NewHandler(Config{
		DockerResources: &fakeDockerResourcesAgent{err: errors.New("docker unavailable")},
		RequestID:       func() string { return "req-resources-error" },
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/docker/resources", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	var body ErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "DOCKER_RESOURCES_UNAVAILABLE" || body.RequestID != "req-resources-error" {
		t.Fatalf("error response = %#v", body)
	}
}

func TestFileEntriesRouteSupportsAbsolutePathPagination(t *testing.T) {
	entries := &fakeFilesystemAgent{result: filesystem.Page{
		Path:        "/volume2",
		Parent:      "/",
		Entries:     []filesystem.Entry{{Name: "Docker", Path: "/volume2/Docker", Type: filesystem.EntryDirectory, Readable: true}},
		NextCursor:  "1",
		CollectedAt: time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC),
	}}
	handler := NewHandler(Config{
		Filesystem:   entries,
		AgentTimeout: time.Second,
		RequestID:    func() string { return "req-files" },
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/files/entries?path=%2Fvolume2&cursor=0&limit=20", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if entries.request.Path != "/volume2" || entries.request.Cursor != "0" || entries.request.Limit != 20 || !entries.deadlineObserved {
		t.Fatalf("request = %#v", entries.request)
	}
	var body filesystem.Page
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Path != "/volume2" || body.NextCursor != "1" || len(body.Entries) != 1 {
		t.Fatalf("page = %#v", body)
	}
}

func TestFileEntriesRouteRejectsRelativePathBeforeAgentCall(t *testing.T) {
	entries := &fakeFilesystemAgent{}
	handler := NewHandler(Config{Filesystem: entries, RequestID: func() string { return "req-files-invalid" }})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/files/entries?path=relative", nil))

	if response.Code != http.StatusBadRequest || entries.called {
		t.Fatalf("status = %d, called = %t", response.Code, entries.called)
	}
	var body ErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "FILES_PATH_INVALID" || body.RequestID != "req-files-invalid" {
		t.Fatalf("error response = %#v", body)
	}
}

func TestFileEntriesRouteMapsAgentPathErrors(t *testing.T) {
	handler := NewHandler(Config{
		Filesystem: &fakeFilesystemAgent{err: grpcstatus.Error(codes.NotFound, "FILES_PATH_NOT_FOUND")},
		RequestID:  func() string { return "req-files-not-found" },
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/files/entries?path=%2Fmissing", nil))

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	var body ErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "FILES_PATH_NOT_FOUND" {
		t.Fatalf("error response = %#v", body)
	}
}

type fakeDockerResourcesAgent struct {
	result           docker.Resources
	err              error
	deadlineObserved bool
	socketPath       string
}

func (f *fakeDockerResourcesAgent) CollectDockerResources(ctx context.Context, socketPath string) (docker.Resources, error) {
	f.socketPath = socketPath
	_, f.deadlineObserved = ctx.Deadline()
	return f.result, f.err
}

type fakeFilesystemAgent struct {
	result           filesystem.Page
	err              error
	request          filesystem.Request
	called           bool
	deadlineObserved bool
}

func (f *fakeFilesystemAgent) ListPath(ctx context.Context, _ string, request filesystem.Request) (filesystem.Page, error) {
	f.called = true
	f.request = request
	_, f.deadlineObserved = ctx.Deadline()
	return f.result, f.err
}
