package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/moby/moby/api/pkg/stdcopy"
	mobycontainer "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

const (
	fixedExecMarker = "NCP_P0_EXEC_OK"
	localDockerHost = "unix:///var/run/docker.sock"
)

// mobyGateway 只实现 P0 Docker PoC 所需的固定操作，不向上层暴露通用 Docker 命令接口。
type mobyGateway struct {
	client *client.Client
}

func NewMobyGateway() (Gateway, error) {
	apiClient, err := client.New(client.WithHost(localDockerHost), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return newMobyGateway(apiClient), nil
}

func newMobyGateway(apiClient *client.Client) *mobyGateway {
	return &mobyGateway{client: apiClient}
}

func (g *mobyGateway) Ping(ctx context.Context) (string, error) {
	response, err := g.client.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true})
	if err != nil {
		return "", err
	}
	return response.APIVersion, nil
}

func (g *mobyGateway) ListContainers(ctx context.Context) ([]Container, error) {
	response, err := g.client.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, err
	}

	containers := make([]Container, 0, len(response.Items))
	for _, item := range response.Items {
		containers = append(containers, Container{ID: item.ID})
	}
	return containers, nil
}

func (g *mobyGateway) InspectContainer(ctx context.Context, containerID string) (ContainerSnapshot, error) {
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

func (g *mobyGateway) ContainerStats(ctx context.Context, containerID string) (ContainerStats, error) {
	response, err := g.client.ContainerStats(ctx, containerID, client.ContainerStatsOptions{})
	if err != nil {
		return ContainerStats{}, err
	}
	defer response.Body.Close()

	var stats mobycontainer.StatsResponse
	if err := json.NewDecoder(response.Body).Decode(&stats); err != nil {
		return ContainerStats{}, err
	}
	return ContainerStats{MemoryUsageBytes: stats.MemoryStats.Usage}, nil
}

func (g *mobyGateway) ContainerLogs(ctx context.Context, containerID string) (io.ReadCloser, error) {
	return g.client.ContainerLogs(ctx, containerID, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       "10",
	})
}

func (g *mobyGateway) ExecMarker(ctx context.Context, containerID string) (string, error) {
	exec, err := g.client.ExecCreate(ctx, containerID, protectedExecOptions())
	if err != nil {
		return "", err
	}

	attachment, err := g.client.ExecAttach(ctx, exec.ID, client.ExecAttachOptions{})
	if err != nil {
		return "", err
	}
	defer attachment.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, io.LimitReader(attachment.Reader, maxObservedLogBytes)); err != nil {
		return "", err
	}

	inspection, err := g.client.ExecInspect(ctx, exec.ID, client.ExecInspectOptions{})
	if err != nil {
		return "", err
	}
	if inspection.ExitCode != 0 {
		return "", fmt.Errorf("fixed exec exited with code %d", inspection.ExitCode)
	}
	if strings.TrimSpace(stdout.String()) != fixedExecMarker {
		return "", errors.New("fixed exec marker was not observed")
	}
	return fixedExecMarker, nil
}

func protectedExecOptions() client.ExecCreateOptions {
	return client.ExecCreateOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          []string{"/bin/sh", "-c", "printf " + fixedExecMarker},
	}
}

func (g *mobyGateway) PullImage(ctx context.Context, reference string) (bool, error) {
	if reference != defaultPOCImageReference {
		return false, errors.New("image reference is outside POC scope")
	}

	response, err := g.client.ImagePull(ctx, reference, client.ImagePullOptions{})
	if err != nil {
		return false, err
	}
	defer response.Close()
	if err := response.Wait(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (g *mobyGateway) Events(ctx context.Context, containerID string) (<-chan ContainerEvent, <-chan error) {
	filters := make(client.Filters)
	filters.Add("type", "container")
	filters.Add("container", containerID)
	stream := g.client.Events(ctx, client.EventsListOptions{Filters: filters})

	events := make(chan ContainerEvent)
	errorsChannel := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errorsChannel)
		for {
			select {
			case message, open := <-stream.Messages:
				if !open {
					return
				}
				event := ContainerEvent{
					ContainerID: message.Actor.ID,
					Action:      string(message.Action),
				}
				select {
				case events <- event:
				case <-ctx.Done():
					return
				}
			case err, open := <-stream.Err:
				if !open || err == nil || errors.Is(err, context.Canceled) {
					return
				}
				select {
				case errorsChannel <- err:
				case <-ctx.Done():
				}
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, errorsChannel
}

func (g *mobyGateway) PauseContainer(ctx context.Context, containerID string) error {
	_, err := g.client.ContainerPause(ctx, containerID, client.ContainerPauseOptions{})
	return err
}

func (g *mobyGateway) UnpauseContainer(ctx context.Context, containerID string) error {
	_, err := g.client.ContainerUnpause(ctx, containerID, client.ContainerUnpauseOptions{})
	return err
}
