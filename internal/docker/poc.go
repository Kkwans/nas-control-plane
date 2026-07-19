package docker

import (
	"context"
	"errors"
	"io"
	"time"
)

const (
	defaultPOCImageReference  = "docker.io/library/alpine:3.21"
	protectedPOCContainerName = "/ncp-p0-docker-poc"
	operationTimeout          = 10 * time.Second
	eventTimeout              = 10 * time.Second
	cleanupTimeout            = 5 * time.Second
	maxObservedLogBytes       = 64 * 1024
)

type POCRequest struct {
	TestContainerID string
}

type POCResult struct {
	DockerAPIVersion     string
	ListedContainerCount int
	InspectedContainerID string
	MemoryUsageBytes     uint64
	ExecMarker           string
	ObservedEventAction  string
	ImageReference       string
	ImagePullCompleted   bool
	ObservedLogBytes     uint64
}

type Container struct {
	ID string
}

type ContainerSnapshot struct {
	ID      string
	Name    string
	Labels  map[string]string
	Running bool
}

type ContainerStats struct {
	MemoryUsageBytes uint64
}

type ContainerEvent struct {
	ContainerID string
	Action      string
}

type Gateway interface {
	Ping(ctx context.Context) (string, error)
	ListContainers(ctx context.Context) ([]Container, error)
	InspectContainer(ctx context.Context, containerID string) (ContainerSnapshot, error)
	ContainerStats(ctx context.Context, containerID string) (ContainerStats, error)
	ContainerLogs(ctx context.Context, containerID string) (io.ReadCloser, error)
	ExecMarker(ctx context.Context, containerID string) (string, error)
	PullImage(ctx context.Context, reference string) (bool, error)
	Events(ctx context.Context, containerID string) (<-chan ContainerEvent, <-chan error)
	PauseContainer(ctx context.Context, containerID string) error
	UnpauseContainer(ctx context.Context, containerID string) error
}

type Runner struct {
	gateway Gateway
}

func NewRunner(gateway Gateway) *Runner {
	return &Runner{gateway: gateway}
}

func (r *Runner) Run(ctx context.Context, request POCRequest) (POCResult, error) {
	if err := ctx.Err(); err != nil {
		return POCResult{}, err
	}

	target, err := r.inspectTarget(ctx, request.TestContainerID)
	if err != nil {
		return POCResult{}, err
	}

	result := POCResult{
		InspectedContainerID: target.ID,
		ImageReference:       defaultPOCImageReference,
	}
	if result.DockerAPIVersion, err = r.ping(ctx); err != nil {
		return POCResult{}, err
	}
	containers, err := r.listContainers(ctx)
	if err != nil {
		return POCResult{}, err
	}
	result.ListedContainerCount = len(containers)
	if result.MemoryUsageBytes, err = r.readStats(ctx, target.ID); err != nil {
		return POCResult{}, err
	}
	if result.ObservedLogBytes, err = r.readLogs(ctx, target.ID); err != nil {
		return POCResult{}, err
	}
	if result.ExecMarker, err = r.execMarker(ctx, target.ID); err != nil {
		return POCResult{}, err
	}
	if result.ImagePullCompleted, err = r.pullImage(ctx); err != nil {
		return POCResult{}, err
	}
	if result.ObservedEventAction, err = r.verifyPauseEvent(ctx, target.ID); err != nil {
		return POCResult{}, err
	}

	return result, nil
}

func (r *Runner) inspectTarget(ctx context.Context, containerID string) (ContainerSnapshot, error) {
	if containerID == "" {
		return ContainerSnapshot{}, coded("DOCKER_POC_TARGET_REJECTED", errors.New("missing test container"))
	}

	operationContext, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	target, err := r.gateway.InspectContainer(operationContext, containerID)
	if err != nil {
		return ContainerSnapshot{}, coded("DOCKER_POC_INSPECT_FAILED", err)
	}
	if target.Name != protectedPOCContainerName || target.Labels["ncp.poc"] != "docker" || !target.Running {
		return ContainerSnapshot{}, coded("DOCKER_POC_TARGET_REJECTED", errors.New("container is not an active POC target"))
	}
	return target, nil
}

func (r *Runner) ping(ctx context.Context) (string, error) {
	operationContext, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	version, err := r.gateway.Ping(operationContext)
	if err != nil {
		return "", coded("DOCKER_POC_PING_FAILED", err)
	}
	return version, nil
}

func (r *Runner) listContainers(ctx context.Context) ([]Container, error) {
	operationContext, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	containers, err := r.gateway.ListContainers(operationContext)
	if err != nil {
		return nil, coded("DOCKER_POC_LIST_FAILED", err)
	}
	return containers, nil
}

func (r *Runner) readStats(ctx context.Context, containerID string) (uint64, error) {
	operationContext, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	stats, err := r.gateway.ContainerStats(operationContext, containerID)
	if err != nil {
		return 0, coded("DOCKER_POC_STATS_FAILED", err)
	}
	return stats.MemoryUsageBytes, nil
}

func (r *Runner) readLogs(ctx context.Context, containerID string) (uint64, error) {
	operationContext, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	reader, err := r.gateway.ContainerLogs(operationContext, containerID)
	if err != nil {
		return 0, coded("DOCKER_POC_LOGS_FAILED", err)
	}
	defer reader.Close()

	count, err := io.Copy(io.Discard, io.LimitReader(reader, maxObservedLogBytes))
	if err != nil {
		return 0, coded("DOCKER_POC_LOGS_FAILED", err)
	}
	return uint64(count), nil
}

func (r *Runner) execMarker(ctx context.Context, containerID string) (string, error) {
	operationContext, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	marker, err := r.gateway.ExecMarker(operationContext, containerID)
	if err != nil {
		return "", coded("DOCKER_POC_EXEC_FAILED", err)
	}
	if marker != "NCP_P0_EXEC_OK" {
		return "", coded("DOCKER_POC_EXEC_FAILED", errors.New("unexpected exec marker"))
	}
	return marker, nil
}

func (r *Runner) pullImage(ctx context.Context) (bool, error) {
	operationContext, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	completed, err := r.gateway.PullImage(operationContext, defaultPOCImageReference)
	if err != nil {
		return false, coded("DOCKER_POC_IMAGE_PULL_FAILED", err)
	}
	if !completed {
		return false, coded("DOCKER_POC_IMAGE_PULL_FAILED", errors.New("image pull did not complete"))
	}
	return true, nil
}

func (r *Runner) verifyPauseEvent(ctx context.Context, containerID string) (string, error) {
	eventContext, cancelEvent := context.WithTimeout(ctx, eventTimeout)
	defer cancelEvent()
	events, eventErrors := r.gateway.Events(eventContext, containerID)

	operationContext, cancelOperation := context.WithTimeout(ctx, operationTimeout)
	err := r.gateway.PauseContainer(operationContext, containerID)
	cancelOperation()
	if err != nil {
		return "", coded("DOCKER_POC_EVENT_FAILED", err)
	}

	paused := true
	defer func() {
		if !paused {
			return
		}
		cleanupContext, cancelCleanup := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancelCleanup()
		_ = r.gateway.UnpauseContainer(cleanupContext, containerID)
	}()

	for {
		select {
		case event, open := <-events:
			if !open {
				return "", coded("DOCKER_POC_EVENT_FAILED", errors.New("event stream closed"))
			}
			if event.ContainerID != containerID || event.Action != "pause" {
				continue
			}
			operationContext, cancelOperation := context.WithTimeout(ctx, operationTimeout)
			err := r.gateway.UnpauseContainer(operationContext, containerID)
			cancelOperation()
			if err != nil {
				return "", coded("DOCKER_POC_EVENT_FAILED", err)
			}
			paused = false
			return event.Action, nil
		case err, open := <-eventErrors:
			if !open {
				return "", coded("DOCKER_POC_EVENT_FAILED", errors.New("event error stream closed"))
			}
			if err != nil {
				return "", coded("DOCKER_POC_EVENT_FAILED", err)
			}
		case <-eventContext.Done():
			return "", coded("DOCKER_POC_EVENT_FAILED", eventContext.Err())
		}
	}
}

type codedError struct {
	code string
	err  error
}

func coded(code string, err error) error {
	return &codedError{code: code, err: err}
}

func (e *codedError) Error() string {
	return e.code
}

func (e *codedError) Unwrap() error {
	return e.err
}

func ErrorCode(err error) string {
	var coded *codedError
	if errors.As(err, &coded) {
		return coded.code
	}
	return ""
}
