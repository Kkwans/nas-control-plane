package docker

import (
	"context"
	"errors"
	"net/netip"
	"strconv"
	"strings"

	mobycontainer "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	mobynetwork "github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

func mapContainerCreateToMoby(spec ContainerCreateSpec) (client.ContainerCreateOptions, error) {
	config := &mobycontainer.Config{Image: spec.Image, Env: append([]string(nil), spec.Environment...), Cmd: append([]string(nil), spec.Command...)}
	hostConfig := &mobycontainer.HostConfig{
		Resources:     mobycontainer.Resources{NanoCPUs: spec.NanoCPUs, Memory: spec.MemoryBytes, CPUShares: spec.CPUShares},
		RestartPolicy: mobycontainer.RestartPolicy{Name: mobycontainer.RestartPolicyMode(spec.RestartPolicy), MaximumRetryCount: spec.RestartMaxRetries},
		Privileged:    spec.Privileged,
		CapAdd:        append([]string(nil), spec.CapAdd...),
		CapDrop:       append([]string(nil), spec.CapDrop...),
	}
	for _, value := range spec.Mounts {
		mapped := mount.Mount{Type: mount.Type(value.Type), Source: value.Source, Target: value.Target, ReadOnly: value.ReadOnly}
		if value.Type == string(mount.TypeVolume) && value.VolumeDriver != "" {
			mapped.VolumeOptions = &mount.VolumeOptions{DriverConfig: &mount.Driver{Name: value.VolumeDriver}}
		}
		if value.Type == string(mount.TypeTmpfs) && value.TmpfsSizeByte > 0 {
			mapped.TmpfsOptions = &mount.TmpfsOptions{SizeBytes: value.TmpfsSizeByte}
		}
		hostConfig.Mounts = append(hostConfig.Mounts, mapped)
	}
	for _, value := range spec.Devices {
		hostConfig.Devices = append(hostConfig.Devices, mobycontainer.DeviceMapping{PathOnHost: value.HostPath, PathInContainer: value.ContainerPath, CgroupPermissions: value.CgroupPermissions})
	}
	for _, value := range spec.GPUs {
		capabilities := value.Capabilities
		if len(capabilities) == 0 {
			capabilities = []string{"gpu"}
		}
		hostConfig.DeviceRequests = append(hostConfig.DeviceRequests, mobycontainer.DeviceRequest{
			Driver: value.Driver, Count: value.Count, DeviceIDs: append([]string(nil), value.DeviceIDs...),
			Capabilities: [][]string{append([]string(nil), capabilities...)}, Options: cloneStringMap(value.Options),
		})
	}

	portSet := make(mobynetwork.PortSet)
	portMap := make(mobynetwork.PortMap)
	for _, value := range spec.Ports {
		port, ok := mobynetwork.PortFrom(value.ContainerPort, mobynetwork.IPProtocol(value.Protocol))
		if !ok {
			return client.ContainerCreateOptions{}, errors.New("container port cannot be represented by Docker")
		}
		portSet[port] = struct{}{}
		binding := mobynetwork.PortBinding{HostPort: strconv.Itoa(int(value.HostPort))}
		if value.HostIP != "" {
			address, err := netip.ParseAddr(value.HostIP)
			if err != nil {
				return client.ContainerCreateOptions{}, err
			}
			binding.HostIP = address
		}
		portMap[port] = append(portMap[port], binding)
	}
	config.ExposedPorts = portSet
	hostConfig.PortBindings = portMap

	var networking *mobynetwork.NetworkingConfig
	if spec.Network != nil {
		if spec.Network.Name == "host" || spec.Network.Name == "none" {
			hostConfig.NetworkMode = mobycontainer.NetworkMode(spec.Network.Name)
		} else {
			endpoint := &mobynetwork.EndpointSettings{}
			if spec.Network.IP != "" {
				address, err := netip.ParseAddr(spec.Network.IP)
				if err != nil {
					return client.ContainerCreateOptions{}, err
				}
				ipam := &mobynetwork.EndpointIPAMConfig{}
				if address.Is4() {
					ipam.IPv4Address = address
				} else {
					ipam.IPv6Address = address
				}
				endpoint.IPAMConfig = ipam
			}
			networking = &mobynetwork.NetworkingConfig{EndpointsConfig: map[string]*mobynetwork.EndpointSettings{spec.Network.Name: endpoint}}
		}
	}
	return client.ContainerCreateOptions{Config: config, HostConfig: hostConfig, NetworkingConfig: networking, Name: spec.Name}, nil
}

func (g *mobyContainerControlGateway) CreateContainer(ctx context.Context, spec ContainerCreateSpec) (ContainerSnapshot, error) {
	options, err := mapContainerCreateToMoby(spec)
	if err != nil {
		return ContainerSnapshot{}, err
	}
	createdNetwork := ""
	if spec.Network != nil && spec.Network.Subnet != "" {
		inspection, inspectErr := g.client.NetworkInspect(ctx, spec.Network.Name, client.NetworkInspectOptions{})
		if inspectErr != nil {
			subnet, parseErr := netip.ParsePrefix(spec.Network.Subnet)
			if parseErr != nil {
				return ContainerSnapshot{}, parseErr
			}
			var gateway netip.Addr
			if spec.Network.Gateway != "" {
				gateway, parseErr = netip.ParseAddr(spec.Network.Gateway)
				if parseErr != nil {
					return ContainerSnapshot{}, parseErr
				}
			}
			var enableIPv6 *bool
			if subnet.Addr().Is6() {
				enabled := true
				enableIPv6 = &enabled
			}
			driver := spec.Network.Driver
			if driver == "" {
				driver = "bridge"
			}
			created, createErr := g.client.NetworkCreate(ctx, spec.Network.Name, client.NetworkCreateOptions{
				Driver: driver, Scope: "local", EnableIPv6: enableIPv6,
				IPAM: &mobynetwork.IPAM{Driver: "default", Config: []mobynetwork.IPAMConfig{{Subnet: subnet, Gateway: gateway}}},
			})
			if createErr != nil {
				return ContainerSnapshot{}, createErr
			}
			createdNetwork = created.ID
		} else if !networkHasSubnet(inspection.Network.IPAM.Config, spec.Network.Subnet) {
			return ContainerSnapshot{}, errors.New("existing network subnet does not match request")
		}
	}
	created, err := g.client.ContainerCreate(ctx, options)
	if err != nil {
		if createdNetwork != "" {
			_, _ = g.client.NetworkRemove(context.Background(), createdNetwork, client.NetworkRemoveOptions{})
		}
		return ContainerSnapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		g.rollbackCreatedResources(created.ID, createdNetwork)
		return ContainerSnapshot{}, err
	}
	snapshot, err := g.InspectContainer(ctx, created.ID)
	if err != nil {
		if ctx.Err() != nil {
			g.rollbackCreatedResources(created.ID, createdNetwork)
			return ContainerSnapshot{}, ctx.Err()
		}
		return ContainerSnapshot{ID: created.ID, Name: "/" + spec.Name}, nil
	}
	if err := ctx.Err(); err != nil {
		g.rollbackCreatedResources(created.ID, createdNetwork)
		return ContainerSnapshot{}, err
	}
	return snapshot, nil
}

func (g *mobyContainerControlGateway) rollbackCreatedResources(containerID, networkID string) {
	cleanupContext, cancel := context.WithTimeout(context.Background(), containerCreateCleanupTimeout)
	defer cancel()
	if strings.TrimSpace(containerID) != "" {
		_ = g.ForceRemoveContainer(cleanupContext, containerID)
	}
	if strings.TrimSpace(networkID) != "" {
		_, _ = g.client.NetworkRemove(cleanupContext, networkID, client.NetworkRemoveOptions{})
	}
}

func networkHasSubnet(config []mobynetwork.IPAMConfig, requested string) bool {
	want, err := netip.ParsePrefix(requested)
	if err != nil {
		return false
	}
	for _, item := range config {
		if item.Subnet == want {
			return true
		}
	}
	return false
}

func (g *mobyContainerControlGateway) RemoveContainer(ctx context.Context, containerID string) error {
	_, err := g.client.ContainerRemove(ctx, containerID, client.ContainerRemoveOptions{Force: false, RemoveVolumes: false, RemoveLinks: false})
	return err
}

func (g *mobyContainerControlGateway) ForceRemoveContainer(ctx context.Context, containerID string) error {
	_, err := g.client.ContainerRemove(ctx, containerID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: false, RemoveLinks: false})
	return err
}
