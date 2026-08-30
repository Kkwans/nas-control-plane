package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/docker"
)

func TestDockerImageInventoryUsesDedicatedListTimeout(t *testing.T) {
	agent := &imageListTimeoutAgent{}
	handler := NewHandler(Config{
		DockerImages: agent,
		// A short general AgentTimeout must not truncate the image catalogue
		// request; this is the regression that reproduces the NAS failure.
		AgentTimeout: time.Millisecond,
		RequestID:    func() string { return "req-docker-image-list" },
	})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/docker/images", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if !agent.deadlineObserved {
		t.Fatal("image list Agent call must receive a deadline")
	}
}

type imageListTimeoutAgent struct {
	deadlineObserved bool
}

func (a *imageListTimeoutAgent) ListDockerImages(ctx context.Context, _ string) (docker.ImageInventory, error) {
	deadline, ok := ctx.Deadline()
	a.deadlineObserved = ok
	if !ok || time.Until(deadline) < 20*time.Second {
		return docker.ImageInventory{}, errors.New("image list deadline too short")
	}
	return docker.ImageInventory{Images: []docker.ImageSummary{}}, nil
}

func (a *imageListTimeoutAgent) SearchDockerHub(context.Context, string, docker.HubSearchRequest) (docker.HubSearchResult, error) {
	return docker.HubSearchResult{}, nil
}

func (a *imageListTimeoutAgent) ListDockerHubTags(context.Context, string, docker.HubTagsRequest) (docker.HubTagsResult, error) {
	return docker.HubTagsResult{}, nil
}

func (a *imageListTimeoutAgent) PullDockerImage(context.Context, string, docker.ImagePullRequest) (docker.ImagePullResult, error) {
	return docker.ImagePullResult{}, nil
}

func (a *imageListTimeoutAgent) RemoveDockerImage(context.Context, string, docker.ImageRemoveRequest) (docker.ImageRemoveResult, error) {
	return docker.ImageRemoveResult{}, nil
}
