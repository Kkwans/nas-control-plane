package docker

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	mobycontainer "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

type ContainerMountDetails struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination"`
	Driver      string `json:"driver,omitempty"`
	ReadOnly    bool   `json:"readOnly"`
}

type ContainerNetworkDetails struct {
	Name        string `json:"name"`
	IPAddress   string `json:"ipAddress,omitempty"`
	Gateway     string `json:"gateway,omitempty"`
	IPv6Address string `json:"ipv6Address,omitempty"`
	MacAddress  string `json:"macAddress,omitempty"`
}

// ContainerDetails intentionally excludes environment variables, labels,
// healthcheck output and the raw command because these frequently contain
// credentials. It only exposes operational facts needed by the console.
type ContainerDetails struct {
	ID                    string                    `json:"id"`
	Name                  string                    `json:"name"`
	Image                 string                    `json:"image"`
	State                 string                    `json:"state"`
	Health                string                    `json:"health,omitempty"`
	HealthFailingStreak   int                       `json:"healthFailingStreak"`
	CreatedAt             *time.Time                `json:"createdAt,omitempty"`
	StartedAt             *time.Time                `json:"startedAt,omitempty"`
	FinishedAt            *time.Time                `json:"finishedAt,omitempty"`
	ExitCode              int                       `json:"exitCode"`
	RestartCount          int                       `json:"restartCount"`
	OOMKilled             bool                      `json:"oomKilled"`
	Platform              string                    `json:"platform,omitempty"`
	Driver                string                    `json:"driver,omitempty"`
	NetworkMode           string                    `json:"networkMode,omitempty"`
	RestartPolicy         string                    `json:"restartPolicy,omitempty"`
	RestartMaximumRetries int                       `json:"restartMaximumRetries"`
	AutoRemove            bool                      `json:"autoRemove"`
	Privileged            bool                      `json:"privileged"`
	ReadonlyRootfs        bool                      `json:"readonlyRootfs"`
	NanoCPUs              int64                     `json:"nanoCpus"`
	MemoryBytes           int64                     `json:"memoryBytes"`
	Ports                 []PortMapping             `json:"ports"`
	Mounts                []ContainerMountDetails   `json:"mounts"`
	Networks              []ContainerNetworkDetails `json:"networks"`
}

type ContainerDetailsGateway interface {
	InspectContainerDetails(context.Context, string) (ContainerDetails, error)
}

func InvalidContainerDetailsError() error {
	return coded("DOCKER_CONTAINER_DETAILS_INVALID", errors.New("container id is required"))
}

func (c *ContainerController) InspectDetails(ctx context.Context, containerID string) (ContainerDetails, error) {
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return ContainerDetails{}, InvalidContainerDetailsError()
	}
	gateway, ok := c.gateway.(ContainerDetailsGateway)
	if !ok {
		return ContainerDetails{}, coded("DOCKER_CONTAINER_DETAILS_UNAVAILABLE", errors.New("container details gateway is not configured"))
	}
	operationContext, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	details, err := gateway.InspectContainerDetails(operationContext, containerID)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ContainerDetails{}, err
		}
		if errdefs.IsNotFound(err) {
			return ContainerDetails{}, coded("DOCKER_CONTAINER_NOT_FOUND", err)
		}
		return ContainerDetails{}, coded("DOCKER_CONTAINER_DETAILS_UNAVAILABLE", err)
	}
	return details, nil
}

func (g *mobyContainerControlGateway) InspectContainerDetails(ctx context.Context, containerID string) (ContainerDetails, error) {
	response, err := g.client.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return ContainerDetails{}, err
	}
	return containerDetailsFromInspect(response.Container), nil
}

func containerDetailsFromInspect(inspect mobycontainer.InspectResponse) ContainerDetails {
	details := ContainerDetails{
		ID:           inspect.ID,
		Name:         strings.TrimPrefix(strings.TrimSpace(inspect.Name), "/"),
		Image:        inspect.Image,
		RestartCount: inspect.RestartCount,
		Platform:     strings.TrimSpace(inspect.Platform),
		Driver:       strings.TrimSpace(inspect.Driver),
		CreatedAt:    parseDockerTimestamp(inspect.Created),
		Ports:        make([]PortMapping, 0),
		Mounts:       make([]ContainerMountDetails, 0, len(inspect.Mounts)),
		Networks:     make([]ContainerNetworkDetails, 0),
	}
	if inspect.Config != nil && strings.TrimSpace(inspect.Config.Image) != "" {
		details.Image = strings.TrimSpace(inspect.Config.Image)
	}
	if inspect.State != nil {
		details.State = string(inspect.State.Status)
		details.StartedAt = parseDockerTimestamp(inspect.State.StartedAt)
		details.FinishedAt = parseDockerTimestamp(inspect.State.FinishedAt)
		details.ExitCode = inspect.State.ExitCode
		details.OOMKilled = inspect.State.OOMKilled
		if inspect.State.Health != nil {
			details.Health = string(inspect.State.Health.Status)
			details.HealthFailingStreak = inspect.State.Health.FailingStreak
		}
	}
	if inspect.HostConfig != nil {
		details.NetworkMode = string(inspect.HostConfig.NetworkMode)
		details.RestartPolicy = string(inspect.HostConfig.RestartPolicy.Name)
		details.RestartMaximumRetries = inspect.HostConfig.RestartPolicy.MaximumRetryCount
		details.AutoRemove = inspect.HostConfig.AutoRemove
		details.Privileged = inspect.HostConfig.Privileged
		details.ReadonlyRootfs = inspect.HostConfig.ReadonlyRootfs
		details.NanoCPUs = inspect.HostConfig.NanoCPUs
		details.MemoryBytes = inspect.HostConfig.Memory
	}
	for _, mount := range inspect.Mounts {
		details.Mounts = append(details.Mounts, ContainerMountDetails{
			Type: string(mount.Type), Name: mount.Name, Source: mount.Source,
			Destination: mount.Destination, Driver: mount.Driver, ReadOnly: !mount.RW,
		})
	}
	sort.Slice(details.Mounts, func(left, right int) bool {
		return details.Mounts[left].Destination < details.Mounts[right].Destination
	})
	if inspect.NetworkSettings != nil {
		for port, bindings := range inspect.NetworkSettings.Ports {
			if len(bindings) == 0 {
				details.Ports = append(details.Ports, PortMapping{PrivatePort: port.Num(), Protocol: string(port.Proto())})
				continue
			}
			for _, binding := range bindings {
				hostPort, _ := strconv.ParseUint(binding.HostPort, 10, 16)
				hostIP := ""
				if binding.HostIP.IsValid() {
					hostIP = binding.HostIP.String()
				}
				details.Ports = append(details.Ports, PortMapping{
					HostIP: hostIP, PrivatePort: port.Num(), PublicPort: uint16(hostPort), Protocol: string(port.Proto()),
				})
			}
		}
		for name, endpoint := range inspect.NetworkSettings.Networks {
			if endpoint == nil {
				continue
			}
			network := ContainerNetworkDetails{Name: name, MacAddress: endpoint.MacAddress.String()}
			if endpoint.IPAddress.IsValid() {
				network.IPAddress = endpoint.IPAddress.String()
			}
			if endpoint.Gateway.IsValid() {
				network.Gateway = endpoint.Gateway.String()
			}
			if endpoint.GlobalIPv6Address.IsValid() {
				network.IPv6Address = endpoint.GlobalIPv6Address.String()
			}
			details.Networks = append(details.Networks, network)
		}
	}
	sort.Slice(details.Ports, func(left, right int) bool {
		if details.Ports[left].PrivatePort != details.Ports[right].PrivatePort {
			return details.Ports[left].PrivatePort < details.Ports[right].PrivatePort
		}
		return details.Ports[left].PublicPort < details.Ports[right].PublicPort
	})
	sort.Slice(details.Networks, func(left, right int) bool { return details.Networks[left].Name < details.Networks[right].Name })
	return details
}

func parseDockerTimestamp(value string) *time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil || parsed.Year() <= 1 {
		return nil
	}
	parsed = parsed.UTC()
	return &parsed
}
