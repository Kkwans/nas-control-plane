package docker

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/moby/moby/client"
)

const containerControlTimeout = 30 * time.Second

// ContainerAction is the small, explicit set of lifecycle operations exposed by NCP.
type ContainerAction string

const (
	ContainerActionStart   ContainerAction = "start"
	ContainerActionStop    ContainerAction = "stop"
	ContainerActionRestart ContainerAction = "restart"
)

type ContainerActionRequest struct {
	ContainerID string          `json:"containerId"`
	Action      ContainerAction `json:"action"`
}

func (r ContainerActionRequest) Validate() error {
	if strings.TrimSpace(r.ContainerID) == "" {
		return coded("DOCKER_CONTAINER_ACTION_INVALID", errors.New("container id is required"))
	}
	if _, err := ParseContainerAction(string(r.Action)); err != nil {
		return err
	}
	return nil
}

func ParseContainerAction(value string) (ContainerAction, error) {
	action := ContainerAction(strings.ToLower(strings.TrimSpace(value)))
	switch action {
	case ContainerActionStart, ContainerActionStop, ContainerActionRestart:
		return action, nil
	default:
		return "", coded("DOCKER_CONTAINER_ACTION_INVALID", errors.New("unsupported container action"))
	}
}

type ContainerActionResult struct {
	ContainerID string          `json:"containerId"`
	Name        string          `json:"name"`
	Action      ContainerAction `json:"action"`
	State       string          `json:"state"`
}

type ContainerControlGateway interface {
	StartContainer(context.Context, string) error
	StopContainer(context.Context, string) error
	RestartContainer(context.Context, string) error
	InspectContainer(context.Context, string) (ContainerSnapshot, error)
}

type ContainerController struct {
	gateway ContainerControlGateway
	timeout time.Duration
}

func NewContainerController(gateway ContainerControlGateway) *ContainerController {
	if gateway == nil {
		gateway = unavailableContainerControlGateway{}
	}
	return &ContainerController{gateway: gateway, timeout: containerControlTimeout}
}

func NewLiveContainerController() (*ContainerController, error) {
	gateway, err := NewMobyContainerControlGateway()
	if err != nil {
		return nil, err
	}
	return NewContainerController(gateway), nil
}

func (c *ContainerController) Control(ctx context.Context, request ContainerActionRequest) (ContainerActionResult, error) {
	if err := request.Validate(); err != nil {
		return ContainerActionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ContainerActionResult{}, err
	}

	operationContext, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var err error
	switch request.Action {
	case ContainerActionStart:
		err = c.gateway.StartContainer(operationContext, request.ContainerID)
	case ContainerActionStop:
		err = c.gateway.StopContainer(operationContext, request.ContainerID)
	case ContainerActionRestart:
		err = c.gateway.RestartContainer(operationContext, request.ContainerID)
	}
	if err != nil {
		return ContainerActionResult{}, coded("DOCKER_CONTAINER_ACTION_FAILED", err)
	}

	snapshot, err := c.gateway.InspectContainer(operationContext, request.ContainerID)
	if err != nil {
		return ContainerActionResult{}, coded("DOCKER_CONTAINER_INSPECT_FAILED", err)
	}
	name := strings.TrimPrefix(strings.TrimSpace(snapshot.Name), "/")
	state := "stopped"
	if snapshot.Running {
		state = "running"
	}
	return ContainerActionResult{
		ContainerID: snapshot.ID,
		Name:        name,
		Action:      request.Action,
		State:       state,
	}, nil
}

type mobyContainerControlGateway struct {
	client *client.Client
}

func NewMobyContainerControlGateway() (ContainerControlGateway, error) {
	apiClient, err := client.New(client.WithHost(localDockerHost), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &mobyContainerControlGateway{client: apiClient}, nil
}

func (g *mobyContainerControlGateway) StartContainer(ctx context.Context, containerID string) error {
	_, err := g.client.ContainerStart(ctx, containerID, client.ContainerStartOptions{})
	return err
}

func (g *mobyContainerControlGateway) StopContainer(ctx context.Context, containerID string) error {
	timeout := int((10 * time.Second) / time.Second)
	_, err := g.client.ContainerStop(ctx, containerID, client.ContainerStopOptions{Timeout: &timeout})
	return err
}

func (g *mobyContainerControlGateway) RestartContainer(ctx context.Context, containerID string) error {
	timeout := int((10 * time.Second) / time.Second)
	_, err := g.client.ContainerRestart(ctx, containerID, client.ContainerRestartOptions{Timeout: &timeout})
	return err
}

func (g *mobyContainerControlGateway) InspectContainer(ctx context.Context, containerID string) (ContainerSnapshot, error) {
	response, err := g.client.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return ContainerSnapshot{}, err
	}
	labels := make(map[string]string)
	if response.Container.Config != nil {
		for key, value := range response.Container.Config.Labels {
			labels[key] = value
		}
	}
	return ContainerSnapshot{
		ID:      response.Container.ID,
		Name:    response.Container.Name,
		Labels:  labels,
		Running: response.Container.State != nil && response.Container.State.Running,
	}, nil
}

type unavailableContainerControlGateway struct{}

func (unavailableContainerControlGateway) StartContainer(context.Context, string) error {
	return errors.New("container control gateway is not configured")
}

func (unavailableContainerControlGateway) StopContainer(context.Context, string) error {
	return errors.New("container control gateway is not configured")
}

func (unavailableContainerControlGateway) RestartContainer(context.Context, string) error {
	return errors.New("container control gateway is not configured")
}

func (unavailableContainerControlGateway) InspectContainer(context.Context, string) (ContainerSnapshot, error) {
	return ContainerSnapshot{}, errors.New("container control gateway is not configured")
}
