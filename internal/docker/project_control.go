package docker

import (
	"context"
	"errors"
	"strings"
	"sync"
)

const maxStandaloneProjectConcurrency = 4

// ProjectActionRequest describes a lifecycle operation for the standalone
// project. Standalone containers do not have a Compose configuration, so the
// caller must provide the container collection explicitly.
type ProjectActionRequest struct {
	ProjectID    string          `json:"projectId"`
	Kind         ProjectKind     `json:"kind"`
	ContainerIDs []string        `json:"containerIds"`
	Action       ContainerAction `json:"action"`
}

func (r ProjectActionRequest) Normalize() (ProjectActionRequest, error) {
	r.ProjectID = strings.TrimSpace(r.ProjectID)
	if r.ProjectID == "" {
		return ProjectActionRequest{}, coded("DOCKER_PROJECT_ACTION_INVALID", errors.New("project id is required"))
	}
	if r.Kind == "" {
		r.Kind = ProjectKindStandalone
	}
	if r.Kind != ProjectKindStandalone {
		return ProjectActionRequest{}, coded("DOCKER_PROJECT_ACTION_INVALID", errors.New("only standalone projects use container collection control"))
	}
	if len(r.ContainerIDs) == 0 {
		return ProjectActionRequest{}, coded("DOCKER_PROJECT_ACTION_INVALID", errors.New("container collection is required"))
	}
	seen := make(map[string]struct{}, len(r.ContainerIDs))
	for index, value := range r.ContainerIDs {
		containerID := strings.TrimSpace(value)
		if containerID == "" {
			return ProjectActionRequest{}, coded("DOCKER_PROJECT_ACTION_INVALID", errors.New("container id is required"))
		}
		if _, exists := seen[containerID]; exists {
			return ProjectActionRequest{}, coded("DOCKER_PROJECT_ACTION_INVALID", errors.New("container collection contains duplicate ids"))
		}
		seen[containerID] = struct{}{}
		r.ContainerIDs[index] = containerID
	}
	if _, err := ParseContainerAction(string(r.Action)); err != nil {
		return ProjectActionRequest{}, coded("DOCKER_PROJECT_ACTION_INVALID", err)
	}
	return r, nil
}

func (r ProjectActionRequest) Validate() error {
	_, err := r.Normalize()
	return err
}

// ProjectContainerActionResult keeps a result for every requested container.
// A failed item is represented in-band so a partially successful collection
// does not hide the state of containers that were already processed.
type ProjectContainerActionResult struct {
	ContainerID string          `json:"containerId"`
	Name        string          `json:"name"`
	Action      ContainerAction `json:"action"`
	State       string          `json:"state"`
	Success     bool            `json:"success"`
	ErrorCode   string          `json:"errorCode,omitempty"`
}

type ProjectActionResult struct {
	ProjectID  string                         `json:"projectId"`
	Kind       ProjectKind                    `json:"kind"`
	Action     ContainerAction                `json:"action"`
	State      string                         `json:"state"`
	Completed  bool                           `json:"completed"`
	Containers []ProjectContainerActionResult `json:"containers"`
}

// ProjectController executes standalone project actions with a small bounded
// worker pool. Result order still follows the request, while no more than four
// mutations are sent to Docker at once.
type ProjectController struct {
	controller *ContainerController
}

func NewProjectController(controller *ContainerController) *ProjectController {
	if controller == nil {
		controller = NewContainerController(nil)
	}
	return &ProjectController{controller: controller}
}

func NewLiveProjectController() (*ProjectController, error) {
	controller, err := NewLiveContainerController()
	if err != nil {
		return nil, err
	}
	return NewProjectController(controller), nil
}

// ControlStandaloneProject lets the existing Agent Docker control provider
// serve both the single-container and collection RPCs without changing the
// SocketConfig wiring.
func (c *ContainerController) ControlStandaloneProject(ctx context.Context, request ProjectActionRequest) (ProjectActionResult, error) {
	return NewProjectController(c).Control(ctx, request)
}

func (c *ProjectController) ControlStandaloneProject(ctx context.Context, request ProjectActionRequest) (ProjectActionResult, error) {
	return c.Control(ctx, request)
}

func (c *ProjectController) Control(ctx context.Context, request ProjectActionRequest) (ProjectActionResult, error) {
	request, err := request.Normalize()
	if err != nil {
		return ProjectActionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ProjectActionResult{}, err
	}

	result := ProjectActionResult{
		ProjectID:  request.ProjectID,
		Kind:       request.Kind,
		Action:     request.Action,
		State:      "unknown",
		Containers: make([]ProjectContainerActionResult, 0, len(request.ContainerIDs)),
	}
	type actionSlot struct {
		item      ProjectContainerActionResult
		completed bool
		err       error
	}
	slots := make([]actionSlot, len(request.ContainerIDs))
	jobs := make(chan int)
	workerCount := min(maxStandaloneProjectConcurrency, len(request.ContainerIDs))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				item, controlErr := c.controlContainer(ctx, request.ContainerIDs[index], request.Action)
				slots[index] = actionSlot{item: item, completed: controlErr == nil, err: controlErr}
				if errors.Is(controlErr, context.Canceled) || errors.Is(controlErr, context.DeadlineExceeded) {
					return
				}
			}
		}()
	}

sendJobs:
	for index := range request.ContainerIDs {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break sendJobs
		}
	}
	close(jobs)
	workers.Wait()

	var contextErr error
	for _, slot := range slots {
		if slot.completed {
			result.Containers = append(result.Containers, slot.item)
		}
		if contextErr == nil && (errors.Is(slot.err, context.Canceled) || errors.Is(slot.err, context.DeadlineExceeded)) {
			contextErr = slot.err
		}
	}
	if contextErr == nil {
		contextErr = ctx.Err()
	}
	if contextErr != nil {
		result.State = projectActionState(result.Containers)
		return result, contextErr
	}

	result.Completed = len(result.Containers) == len(request.ContainerIDs) && allProjectItemsSucceeded(result.Containers)
	result.State = projectActionState(result.Containers)
	return result, nil
}

func (c *ProjectController) controlContainer(ctx context.Context, containerID string, action ContainerAction) (ProjectContainerActionResult, error) {
	containerResult, controlErr := c.controller.Control(ctx, ContainerActionRequest{ContainerID: containerID, Action: action})
	item := ProjectContainerActionResult{ContainerID: containerID, Action: action, State: "unknown"}
	if controlErr == nil {
		item.ContainerID = containerResult.ContainerID
		item.Name = containerResult.Name
		item.State = containerResult.State
		item.Success = true
		return item, nil
	}
	if errors.Is(controlErr, context.Canceled) || errors.Is(controlErr, context.DeadlineExceeded) {
		return item, controlErr
	}
	item.ErrorCode = ErrorCode(controlErr)
	if item.ErrorCode == "" {
		item.ErrorCode = "DOCKER_CONTAINER_ACTION_FAILED"
	}
	// An action may fail after Docker has applied part of the request. Read back
	// the state where possible instead of reporting a guessed state.
	if snapshot, inspectErr := c.inspect(ctx, containerID); inspectErr == nil {
		item.ContainerID = snapshot.ID
		item.Name = strings.TrimPrefix(strings.TrimSpace(snapshot.Name), "/")
		item.State = containerState(snapshot.Running)
	}
	return item, nil
}

func (c *ProjectController) inspect(ctx context.Context, containerID string) (ContainerSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return ContainerSnapshot{}, err
	}
	operationContext, cancel := context.WithTimeout(ctx, containerControlTimeout)
	defer cancel()
	return c.controller.gateway.InspectContainer(operationContext, containerID)
}

func allProjectItemsSucceeded(items []ProjectContainerActionResult) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if !item.Success {
			return false
		}
	}
	return true
}

func projectActionState(items []ProjectContainerActionResult) string {
	if len(items) == 0 {
		return "unknown"
	}
	running, stopped, unknown := 0, 0, 0
	for _, item := range items {
		switch item.State {
		case "running":
			running++
		case "stopped":
			stopped++
		default:
			unknown++
		}
	}
	if unknown > 0 || (running > 0 && stopped > 0) {
		return "degraded"
	}
	if running == len(items) {
		return "running"
	}
	if stopped == len(items) {
		return "stopped"
	}
	return "unknown"
}

func containerState(running bool) string {
	if running {
		return "running"
	}
	return "stopped"
}
