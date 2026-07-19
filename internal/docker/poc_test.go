package docker

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRunnerCompletesProtectedDockerPOC(t *testing.T) {
	t.Parallel()

	gateway := newCompleteGateway()
	result, err := NewRunner(gateway).Run(context.Background(), POCRequest{TestContainerID: "container-123"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.DockerAPIVersion != "1.54" {
		t.Errorf("DockerAPIVersion = %q, want 1.54", result.DockerAPIVersion)
	}
	if result.ListedContainerCount != 2 || result.InspectedContainerID != "container-123" {
		t.Errorf("result = %#v", result)
	}
	if result.MemoryUsageBytes != 8192 {
		t.Errorf("MemoryUsageBytes = %d, want 8192", result.MemoryUsageBytes)
	}
	if result.ExecMarker != "NCP_P0_EXEC_OK" || result.ObservedEventAction != "pause" {
		t.Errorf("exec=%q event=%q", result.ExecMarker, result.ObservedEventAction)
	}
	if !result.ImagePullCompleted || result.ImageReference != defaultPOCImageReference {
		t.Errorf("image result = %#v", result)
	}
	if result.ObservedLogBytes != uint64(len("NCP_P0_CONTAINER_READY\n")) {
		t.Errorf("ObservedLogBytes = %d", result.ObservedLogBytes)
	}
	if gateway.paused {
		t.Error("temporary container remained paused after a successful POC")
	}
}

func TestRunnerRejectsUnlabelledContainerBeforeSideEffects(t *testing.T) {
	t.Parallel()

	gateway := newCompleteGateway()
	gateway.target.Labels = nil

	_, err := NewRunner(gateway).Run(context.Background(), POCRequest{TestContainerID: "container-123"})
	if ErrorCode(err) != "DOCKER_POC_TARGET_REJECTED" {
		t.Fatalf("ErrorCode() = %q, error = %v", ErrorCode(err), err)
	}
	if len(gateway.sideEffects) != 0 {
		t.Errorf("unsafe calls occurred for an unlabelled container: %#v", gateway.sideEffects)
	}
}

func TestRunnerRestoresContainerWhenEventVerificationFails(t *testing.T) {
	t.Parallel()

	gateway := newCompleteGateway()
	gateway.eventError = errors.New("event stream disconnected")

	_, err := NewRunner(gateway).Run(context.Background(), POCRequest{TestContainerID: "container-123"})
	if ErrorCode(err) != "DOCKER_POC_EVENT_FAILED" {
		t.Fatalf("ErrorCode() = %q, error = %v", ErrorCode(err), err)
	}
	if gateway.paused {
		t.Error("temporary container was not restored after event verification failed")
	}
}

func TestRunnerHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewRunner(newCompleteGateway()).Run(ctx, POCRequest{TestContainerID: "container-123"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}

func newCompleteGateway() *fakeGateway {
	return &fakeGateway{
		apiVersion: "1.54",
		containers: []Container{
			{ID: "container-123"},
			{ID: "container-456"},
		},
		target: ContainerSnapshot{
			ID:      "container-123",
			Name:    "/ncp-p0-docker-poc",
			Running: true,
			Labels: map[string]string{
				"ncp.poc": "docker",
			},
		},
		stats:  ContainerStats{MemoryUsageBytes: 8192},
		logs:   "NCP_P0_CONTAINER_READY\n",
		exec:   "NCP_P0_EXEC_OK",
		events: make(chan ContainerEvent, 1),
		errors: make(chan error, 1),
	}
}

type fakeGateway struct {
	apiVersion  string
	containers  []Container
	target      ContainerSnapshot
	stats       ContainerStats
	logs        string
	exec        string
	eventError  error
	events      chan ContainerEvent
	errors      chan error
	paused      bool
	sideEffects []string
}

func (f *fakeGateway) Ping(context.Context) (string, error) {
	return f.apiVersion, nil
}

func (f *fakeGateway) ListContainers(context.Context) ([]Container, error) {
	return append([]Container(nil), f.containers...), nil
}

func (f *fakeGateway) InspectContainer(context.Context, string) (ContainerSnapshot, error) {
	return f.target, nil
}

func (f *fakeGateway) ContainerStats(context.Context, string) (ContainerStats, error) {
	return f.stats, nil
}

func (f *fakeGateway) ContainerLogs(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(f.logs)), nil
}

func (f *fakeGateway) ExecMarker(context.Context, string) (string, error) {
	f.sideEffects = append(f.sideEffects, "exec")
	return f.exec, nil
}

func (f *fakeGateway) PullImage(context.Context, string) (bool, error) {
	f.sideEffects = append(f.sideEffects, "pull")
	return true, nil
}

func (f *fakeGateway) Events(context.Context, string) (<-chan ContainerEvent, <-chan error) {
	return f.events, f.errors
}

func (f *fakeGateway) PauseContainer(context.Context, string) error {
	f.sideEffects = append(f.sideEffects, "pause")
	f.paused = true
	if f.eventError != nil {
		f.errors <- f.eventError
		return nil
	}
	f.events <- ContainerEvent{ContainerID: f.target.ID, Action: "pause"}
	return nil
}

func (f *fakeGateway) UnpauseContainer(context.Context, string) error {
	f.sideEffects = append(f.sideEffects, "unpause")
	f.paused = false
	return nil
}
