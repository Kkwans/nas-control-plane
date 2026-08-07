package agentsocket

import (
	"encoding/json"
	"errors"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/protobuf/types/known/structpb"
)

// The RPC wire format is snake_case, matching the existing hand-written
// Agent protocol. Keeping this wire type separate from the HTTP/domain type
// prevents a client from smuggling a command string or an unvalidated mount
// representation into the Docker package.
type containerCreateWire struct {
	Image             string                `json:"image"`
	Name              string                `json:"name"`
	CPU               float64               `json:"cpu"`
	CPUNanoCPUs       int64                 `json:"cpu_nano_cpus"`
	NanoCPUs          int64                 `json:"nano_cpus"`
	CPUShares         int64                 `json:"cpu_shares"`
	MemoryBytes       int64                 `json:"memory_bytes"`
	Memory            int64                 `json:"memory"`
	RestartPolicy     string                `json:"restart_policy"`
	RestartMaxRetries int                   `json:"restart_max_retries"`
	AutoRestart       bool                  `json:"auto_restart"`
	Environment       map[string]string     `json:"environment"`
	Env               []string              `json:"env"`
	Mounts            []containerMountWire  `json:"mounts"`
	Network           *containerNetworkWire `json:"network"`
	NetworkName       string                `json:"network_name"`
	NetworkSubnet     string                `json:"network_subnet"`
	NetworkGateway    string                `json:"network_gateway"`
	NetworkIP         string                `json:"network_ip"`
	Ports             []containerPortWire   `json:"ports"`
	Command           []string              `json:"command"`
	Privileged        bool                  `json:"privileged"`
	CapAdd            []string              `json:"cap_add"`
	CapDrop           []string              `json:"cap_drop"`
	Devices           []containerDeviceWire `json:"devices"`
	GPU               *containerGPUWire     `json:"gpu"`
	GPUs              []containerGPUWire    `json:"gpus"`
	RunContainer      bool                  `json:"run_container"`
}

type containerMountWire struct {
	Type          string `json:"type"`
	Source        string `json:"source"`
	Target        string `json:"target"`
	ReadOnly      bool   `json:"read_only"`
	VolumeDriver  string `json:"volume_driver"`
	TmpfsSizeByte int64  `json:"tmpfs_size_bytes"`
}

type containerNetworkWire struct {
	Name    string `json:"name"`
	Driver  string `json:"driver"`
	Subnet  string `json:"subnet"`
	Gateway string `json:"gateway"`
	IP      string `json:"ip"`
}

type containerPortWire struct {
	ContainerPort uint16 `json:"container_port"`
	HostPort      uint16 `json:"host_port"`
	HostIP        string `json:"host_ip"`
	Protocol      string `json:"protocol"`
	PrivatePort   uint16 `json:"private_port"`
	PublicPort    uint16 `json:"public_port"`
}

type containerDeviceWire struct {
	HostPath          string `json:"host_path"`
	ContainerPath     string `json:"container_path"`
	CgroupPermissions string `json:"cgroup_permissions"`
}

type containerGPUWire struct {
	Driver       string            `json:"driver"`
	Count        int               `json:"count"`
	DeviceIDs    []string          `json:"device_ids"`
	Capabilities []string          `json:"capabilities"`
	Options      map[string]string `json:"options"`
}

func decodeContainerCreateRequest(request *structpb.Struct) (docker.ContainerCreateRequest, error) {
	if request == nil {
		return docker.ContainerCreateRequest{}, errors.New("container create request is required")
	}
	encoded, err := json.Marshal(request.AsMap())
	if err != nil {
		return docker.ContainerCreateRequest{}, err
	}
	var wire containerCreateWire
	if err := json.Unmarshal(encoded, &wire); err != nil {
		return docker.ContainerCreateRequest{}, err
	}
	result := docker.ContainerCreateRequest{
		Image: wire.Image, Name: wire.Name, CPU: wire.CPU, CPUNanoCPUs: wire.CPUNanoCPUs, NanoCPUs: wire.NanoCPUs,
		CPUShares: wire.CPUShares, MemoryBytes: wire.MemoryBytes, Memory: wire.Memory,
		RestartPolicy: wire.RestartPolicy, RestartMaxRetries: wire.RestartMaxRetries, AutoRestart: wire.AutoRestart,
		Environment: wire.Environment, Env: wire.Env, Command: wire.Command, Privileged: wire.Privileged,
		CapAdd: wire.CapAdd, CapDrop: wire.CapDrop, RunContainer: wire.RunContainer,
		NetworkName: wire.NetworkName, NetworkSubnet: wire.NetworkSubnet, NetworkGateway: wire.NetworkGateway, NetworkIP: wire.NetworkIP,
	}
	for _, value := range wire.Mounts {
		result.Mounts = append(result.Mounts, docker.ContainerMount{Type: value.Type, Source: value.Source, Target: value.Target, ReadOnly: value.ReadOnly, VolumeDriver: value.VolumeDriver, TmpfsSizeByte: value.TmpfsSizeByte})
	}
	if wire.Network != nil {
		result.Network = &docker.ContainerNetwork{Name: wire.Network.Name, Driver: wire.Network.Driver, Subnet: wire.Network.Subnet, Gateway: wire.Network.Gateway, IP: wire.Network.IP}
	}
	for _, value := range wire.Ports {
		result.Ports = append(result.Ports, docker.ContainerPort{ContainerPort: value.ContainerPort, HostPort: value.HostPort, HostIP: value.HostIP, Protocol: value.Protocol, PrivatePort: value.PrivatePort, PublicPort: value.PublicPort})
	}
	for _, value := range wire.Devices {
		result.Devices = append(result.Devices, docker.ContainerDevice{HostPath: value.HostPath, ContainerPath: value.ContainerPath, CgroupPermissions: value.CgroupPermissions})
	}
	if wire.GPU != nil {
		result.GPU = &docker.ContainerGPU{Driver: wire.GPU.Driver, Count: wire.GPU.Count, DeviceIDs: wire.GPU.DeviceIDs, Capabilities: wire.GPU.Capabilities, Options: wire.GPU.Options}
	}
	for _, value := range wire.GPUs {
		result.GPUs = append(result.GPUs, docker.ContainerGPU{Driver: value.Driver, Count: value.Count, DeviceIDs: value.DeviceIDs, Capabilities: value.Capabilities, Options: value.Options})
	}
	if err := result.Validate(); err != nil {
		return docker.ContainerCreateRequest{}, err
	}
	return result, nil
}
