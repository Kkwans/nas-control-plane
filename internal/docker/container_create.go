package docker

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/moby/moby/api/types/mount"
)

const (
	containerCreateTimeout        = 2 * time.Minute
	containerCreateCleanupTimeout = 15 * time.Second
	maxContainerNameSize          = 128
	maxContainerCPU               = 256.0
	maxContainerMemory            = int64(1) << 50
)

var (
	containerNamePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	identifierPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	environmentKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	capabilityPattern     = regexp.MustCompile(`^[A-Z0-9_]+$`)
)

// ContainerCreateRequest is the structured input accepted by the Docker
// control plane. It deliberately has no command-string or shell field.
// Command is passed to Docker as an argv vector after validation.
type ContainerCreateRequest struct {
	Image string `json:"image"`
	Name  string `json:"name,omitempty"`

	// CPU is expressed in CPU cores. CPUNanoCPUs/NanoCPUs are the direct
	// Docker unit aliases; at most one representation may be supplied.
	CPU         float64 `json:"cpu,omitempty"`
	CPUNanoCPUs int64   `json:"cpuNanoCpus,omitempty"`
	NanoCPUs    int64   `json:"nanoCpus,omitempty"`
	CPUShares   int64   `json:"cpuShares,omitempty"`
	MemoryBytes int64   `json:"memoryBytes,omitempty"`
	Memory      int64   `json:"memory,omitempty"`

	RestartPolicy     string `json:"restartPolicy,omitempty"`
	RestartMaxRetries int    `json:"restartMaxRetries,omitempty"`
	AutoRestart       bool   `json:"autoRestart,omitempty"`

	Environment map[string]string `json:"environment,omitempty"`
	Env         []string          `json:"env,omitempty"`
	Mounts      []ContainerMount  `json:"mounts,omitempty"`

	Network        *ContainerNetwork `json:"network,omitempty"`
	NetworkName    string            `json:"networkName,omitempty"`
	NetworkSubnet  string            `json:"networkSubnet,omitempty"`
	NetworkGateway string            `json:"networkGateway,omitempty"`
	NetworkIP      string            `json:"networkIp,omitempty"`

	Ports []ContainerPort `json:"ports,omitempty"`
	// Command is an argv vector, never a shell command string.
	Command []string `json:"command,omitempty"`

	Privileged bool              `json:"privileged,omitempty"`
	CapAdd     []string          `json:"capAdd,omitempty"`
	CapDrop    []string          `json:"capDrop,omitempty"`
	Devices    []ContainerDevice `json:"devices,omitempty"`
	GPU        *ContainerGPU     `json:"gpu,omitempty"`
	GPUs       []ContainerGPU    `json:"gpus,omitempty"`

	RunContainer bool `json:"runContainer,omitempty"`
}

// CreateContainerRequest is kept as a descriptive alias for callers that use
// the operation name rather than the resource name.
type CreateContainerRequest = ContainerCreateRequest

type ContainerMount struct {
	Type          string `json:"type"`
	Source        string `json:"source,omitempty"`
	Target        string `json:"target"`
	ReadOnly      bool   `json:"readOnly,omitempty"`
	VolumeDriver  string `json:"volumeDriver,omitempty"`
	TmpfsSizeByte int64  `json:"tmpfsSizeBytes,omitempty"`
}

type ContainerNetwork struct {
	Name    string `json:"name"`
	Driver  string `json:"driver,omitempty"`
	Subnet  string `json:"subnet,omitempty"`
	Gateway string `json:"gateway,omitempty"`
	IP      string `json:"ip,omitempty"`
}

type ContainerPort struct {
	ContainerPort uint16 `json:"containerPort,omitempty"`
	HostPort      uint16 `json:"hostPort,omitempty"`
	HostIP        string `json:"hostIp,omitempty"`
	Protocol      string `json:"protocol,omitempty"`

	// PrivatePort/PublicPort are accepted as API-compatible aliases.
	PrivatePort uint16 `json:"privatePort,omitempty"`
	PublicPort  uint16 `json:"publicPort,omitempty"`
}

type ContainerDevice struct {
	HostPath          string `json:"hostPath"`
	ContainerPath     string `json:"containerPath"`
	CgroupPermissions string `json:"cgroupPermissions,omitempty"`
}

type ContainerGPU struct {
	Driver       string            `json:"driver,omitempty"`
	Count        int               `json:"count,omitempty"`
	DeviceIDs    []string          `json:"deviceIds,omitempty"`
	Capabilities []string          `json:"capabilities,omitempty"`
	Options      map[string]string `json:"options,omitempty"`
}

// ContainerCreateSpec is the normalized, engine-facing representation. It
// contains no user aliases and is safe to map directly to the Moby API.
type ContainerCreateSpec struct {
	Image             string
	Name              string
	NanoCPUs          int64
	MemoryBytes       int64
	CPUShares         int64
	RestartPolicy     string
	RestartMaxRetries int
	Environment       []string
	Mounts            []ContainerMount
	Network           *ContainerNetwork
	Ports             []ContainerPort
	Command           []string
	Privileged        bool
	CapAdd            []string
	CapDrop           []string
	Devices           []ContainerDevice
	GPUs              []ContainerGPU
	RunContainer      bool
}

type ContainerCreateResult struct {
	ContainerID  string `json:"containerId"`
	Name         string `json:"name"`
	Image        string `json:"image"`
	State        string `json:"state"`
	Created      bool   `json:"created"`
	Started      bool   `json:"started"`
	RunContainer bool   `json:"runContainer"`
}

// ContainerCreateGateway is intentionally separate from the lifecycle
// gateway, so tests and alternate engines can implement creation without
// gaining any implicit shell or removal capability.
type ContainerCreateGateway interface {
	CreateContainer(context.Context, ContainerCreateSpec) (ContainerSnapshot, error)
}

type ContainerRemoveGateway interface {
	RemoveContainer(context.Context, string) error
}

type ContainerForceRemoveGateway interface {
	ForceRemoveContainer(context.Context, string) error
}

type ContainerCreator struct {
	createGateway ContainerCreateGateway
	startGateway  interface {
		StartContainer(context.Context, string) error
	}
	inspectGateway interface {
		InspectContainer(context.Context, string) (ContainerSnapshot, error)
	}
	removeGateway      ContainerRemoveGateway
	forceRemoveGateway ContainerForceRemoveGateway
	stopGateway        interface {
		StopContainer(context.Context, string) error
	}
	timeout time.Duration
}

// CreateContainer keeps the default SocketConfig wiring compatible with the
// existing ContainerController while exposing the new operation as an
// optional provider capability.
func (c *ContainerController) CreateContainer(ctx context.Context, request ContainerCreateRequest) (ContainerCreateResult, error) {
	if c == nil {
		return ContainerCreateResult{}, coded("DOCKER_CONTAINER_CREATE_UNAVAILABLE", errors.New("container controller is not configured"))
	}
	gateway, ok := c.gateway.(ContainerCreateGateway)
	if !ok {
		return ContainerCreateResult{}, coded("DOCKER_CONTAINER_CREATE_UNAVAILABLE", errors.New("container create gateway is not configured"))
	}
	return NewContainerCreator(gateway).Create(ctx, request)
}

func NewLiveContainerCreator() (*ContainerCreator, error) {
	gateway, err := NewMobyContainerControlGateway()
	if err != nil {
		return nil, err
	}
	createGateway, ok := gateway.(ContainerCreateGateway)
	if !ok {
		return nil, errors.New("moby gateway does not support container creation")
	}
	return NewContainerCreator(createGateway), nil
}

func NewContainerCreator(createGateway ContainerCreateGateway) *ContainerCreator {
	creator := &ContainerCreator{createGateway: createGateway, timeout: containerCreateTimeout}
	if startGateway, ok := createGateway.(interface {
		StartContainer(context.Context, string) error
	}); ok {
		creator.startGateway = startGateway
	}
	if inspectGateway, ok := createGateway.(interface {
		InspectContainer(context.Context, string) (ContainerSnapshot, error)
	}); ok {
		creator.inspectGateway = inspectGateway
	}
	if removeGateway, ok := createGateway.(ContainerRemoveGateway); ok {
		creator.removeGateway = removeGateway
	}
	if forceRemoveGateway, ok := createGateway.(ContainerForceRemoveGateway); ok {
		creator.forceRemoveGateway = forceRemoveGateway
	}
	if stopGateway, ok := createGateway.(interface {
		StopContainer(context.Context, string) error
	}); ok {
		creator.stopGateway = stopGateway
	}
	return creator
}

func (c *ContainerCreator) Create(ctx context.Context, request ContainerCreateRequest) (ContainerCreateResult, error) {
	spec, err := request.Normalize()
	if err != nil {
		return ContainerCreateResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ContainerCreateResult{}, err
	}
	if c == nil || c.createGateway == nil {
		return ContainerCreateResult{}, coded("DOCKER_CONTAINER_CREATE_UNAVAILABLE", errors.New("container create gateway is not configured"))
	}

	operationContext, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	snapshot, err := c.createGateway.CreateContainer(operationContext, spec)
	if err != nil {
		if operationContext.Err() != nil {
			return ContainerCreateResult{}, operationContext.Err()
		}
		return ContainerCreateResult{}, coded("DOCKER_CONTAINER_CREATE_FAILED", err)
	}
	if strings.TrimSpace(snapshot.ID) == "" {
		return ContainerCreateResult{}, coded("DOCKER_CONTAINER_CREATE_FAILED", errors.New("docker returned an empty container id"))
	}
	containerID := strings.TrimSpace(snapshot.ID)
	snapshot.ID = containerID
	startAttempted := false
	cleanup := func(cause error) (ContainerCreateResult, error) {
		if cleanupErr := c.cleanupCreatedContainer(containerID, startAttempted); cleanupErr != nil {
			return ContainerCreateResult{}, coded("DOCKER_CONTAINER_CREATE_CLEANUP_FAILED", cleanupErr)
		}
		return ContainerCreateResult{}, cause
	}
	if err := operationContext.Err(); err != nil {
		return cleanup(err)
	}

	started := false
	if spec.RunContainer {
		if c.startGateway == nil {
			return cleanup(coded("DOCKER_CONTAINER_START_FAILED", errors.New("container start gateway is not configured")))
		}
		startAttempted = true
		if err := c.startGateway.StartContainer(operationContext, containerID); err != nil {
			if operationContext.Err() != nil {
				return cleanup(operationContext.Err())
			}
			return cleanup(coded("DOCKER_CONTAINER_START_FAILED", err))
		}
		if err := operationContext.Err(); err != nil {
			return cleanup(err)
		}
		started = true
		if c.inspectGateway != nil {
			inspected, inspectErr := c.inspectGateway.InspectContainer(operationContext, containerID)
			if inspectErr != nil {
				if operationContext.Err() != nil {
					return cleanup(operationContext.Err())
				}
				return cleanup(coded("DOCKER_CONTAINER_INSPECT_FAILED", inspectErr))
			}
			if strings.TrimSpace(inspected.ID) == "" {
				inspected.ID = containerID
			}
			snapshot = inspected
		}
	}

	name := strings.TrimPrefix(strings.TrimSpace(snapshot.Name), "/")
	if name == "" {
		name = spec.Name
	}
	running := snapshot.Running
	if started && c.inspectGateway == nil {
		running = true
	}
	if err := operationContext.Err(); err != nil {
		return cleanup(err)
	}
	return ContainerCreateResult{
		ContainerID:  containerID,
		Name:         name,
		Image:        spec.Image,
		State:        containerState(running),
		Created:      true,
		Started:      started,
		RunContainer: spec.RunContainer,
	}, nil
}

func (c *ContainerCreator) cleanupCreatedContainer(containerID string, startAttempted bool) error {
	if c == nil || strings.TrimSpace(containerID) == "" {
		return nil
	}
	if c.forceRemoveGateway == nil && c.removeGateway == nil {
		return nil
	}
	cleanupContext, cancel := context.WithTimeout(context.Background(), containerCreateCleanupTimeout)
	defer cancel()
	if c.forceRemoveGateway != nil {
		return c.forceRemoveGateway.ForceRemoveContainer(cleanupContext, containerID)
	}
	if startAttempted && c.stopGateway != nil {
		_ = c.stopGateway.StopContainer(cleanupContext, containerID)
	}
	return c.removeGateway.RemoveContainer(cleanupContext, containerID)
}

func (r ContainerCreateRequest) Normalize() (ContainerCreateSpec, error) {
	image := strings.TrimSpace(r.Image)
	if image == "" || len(image) > maxImageReferenceSize || strings.ContainsAny(image, " \t\r\n\x00") || !validImageReferenceCharacters(image) {
		return ContainerCreateSpec{}, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("image reference is invalid"))
	}
	if _, err := normalizeImageReference(image); err != nil {
		return ContainerCreateSpec{}, coded("DOCKER_CONTAINER_CREATE_INVALID", err)
	}

	name := strings.TrimSpace(r.Name)
	if name != "" && (len(name) > maxContainerNameSize || !containerNamePattern.MatchString(name)) {
		return ContainerCreateSpec{}, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("container name is invalid"))
	}

	nanoCPUs, err := normalizeCPU(r)
	if err != nil {
		return ContainerCreateSpec{}, err
	}
	memoryBytes, err := normalizeMemory(r)
	if err != nil {
		return ContainerCreateSpec{}, err
	}
	if r.CPUShares < 0 || r.CPUShares > 4_000_000_000 {
		return ContainerCreateSpec{}, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("cpu shares are invalid"))
	}
	restartPolicy, err := normalizeRestartPolicy(r.RestartPolicy, r.AutoRestart)
	if err != nil {
		return ContainerCreateSpec{}, err
	}
	if r.RestartMaxRetries < 0 || (r.RestartMaxRetries > 0 && restartPolicy != "on-failure") {
		return ContainerCreateSpec{}, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("restart retry count is invalid"))
	}

	environment, err := normalizeEnvironment(r.Environment, r.Env)
	if err != nil {
		return ContainerCreateSpec{}, err
	}
	mounts, err := normalizeMounts(r.Mounts)
	if err != nil {
		return ContainerCreateSpec{}, err
	}
	network, err := normalizeNetwork(r)
	if err != nil {
		return ContainerCreateSpec{}, err
	}
	ports, err := normalizePorts(r.Ports)
	if err != nil {
		return ContainerCreateSpec{}, err
	}
	command, err := normalizeCommand(r.Command)
	if err != nil {
		return ContainerCreateSpec{}, err
	}
	capAdd, err := normalizeCapabilities(r.CapAdd)
	if err != nil {
		return ContainerCreateSpec{}, err
	}
	capDrop, err := normalizeCapabilities(r.CapDrop)
	if err != nil {
		return ContainerCreateSpec{}, err
	}
	devices, err := normalizeDevices(r.Devices)
	if err != nil {
		return ContainerCreateSpec{}, err
	}
	gpus, err := normalizeGPUs(r.GPU, r.GPUs)
	if err != nil {
		return ContainerCreateSpec{}, err
	}
	return ContainerCreateSpec{
		Image: image, Name: name, NanoCPUs: nanoCPUs, MemoryBytes: memoryBytes,
		CPUShares: r.CPUShares, RestartPolicy: restartPolicy, RestartMaxRetries: r.RestartMaxRetries,
		Environment: environment, Mounts: mounts, Network: network, Ports: ports,
		Command: command, Privileged: r.Privileged, CapAdd: capAdd, CapDrop: capDrop,
		Devices: devices, GPUs: gpus, RunContainer: r.RunContainer,
	}, nil
}

func (r ContainerCreateRequest) Validate() error {
	_, err := r.Normalize()
	return err
}

func normalizeCPU(request ContainerCreateRequest) (int64, error) {
	direct := request.CPUNanoCPUs
	if request.NanoCPUs != 0 {
		if direct != 0 {
			return 0, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("duplicate cpu nanocpu fields"))
		}
		direct = request.NanoCPUs
	}
	if direct < 0 || direct > int64(maxContainerCPU*1_000_000_000) {
		return 0, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("cpu limit is invalid"))
	}
	if request.CPU != 0 {
		if direct != 0 || math.IsNaN(request.CPU) || math.IsInf(request.CPU, 0) || request.CPU <= 0 || request.CPU > maxContainerCPU {
			return 0, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("cpu limit is invalid"))
		}
		nano := request.CPU * 1_000_000_000
		if math.Trunc(nano) != nano || nano > float64(math.MaxInt64) {
			return 0, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("cpu limit precision is invalid"))
		}
		return int64(nano), nil
	}
	return direct, nil
}

func normalizeMemory(request ContainerCreateRequest) (int64, error) {
	memory := request.MemoryBytes
	if request.Memory != 0 {
		if memory != 0 {
			return 0, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("duplicate memory fields"))
		}
		memory = request.Memory
	}
	if memory < 0 || memory > maxContainerMemory {
		return 0, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("memory limit is invalid"))
	}
	return memory, nil
}

func normalizeRestartPolicy(value string, autoRestart bool) (string, error) {
	policy := strings.ToLower(strings.TrimSpace(value))
	if policy == "" && autoRestart {
		policy = "unless-stopped"
	}
	if policy == "" {
		policy = "no"
	}
	switch policy {
	case "no", "always", "on-failure", "unless-stopped":
		if autoRestart && policy == "no" {
			return "", coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("auto restart conflicts with no restart policy"))
		}
		return policy, nil
	default:
		return "", coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("restart policy is invalid"))
	}
}

func normalizeEnvironment(values map[string]string, entries []string) ([]string, error) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(values)+len(entries))
	seen := make(map[string]struct{}, len(values)+len(entries))
	for _, key := range keys {
		if !environmentKeyPattern.MatchString(key) || strings.ContainsAny(values[key], "\x00\r\n") {
			return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("environment variable is invalid"))
		}
		seen[key] = struct{}{}
		result = append(result, key+"="+values[key])
	}
	for _, entry := range entries {
		if strings.ContainsAny(entry, "\x00\r\n") {
			return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("environment variable is invalid"))
		}
		separator := strings.IndexByte(entry, '=')
		if separator <= 0 {
			return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("environment variable must use KEY=VALUE form"))
		}
		key := entry[:separator]
		if !environmentKeyPattern.MatchString(key) {
			return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("environment variable key is invalid"))
		}
		if _, exists := seen[key]; exists {
			return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("environment variable is duplicated"))
		}
		seen[key] = struct{}{}
		result = append(result, entry)
	}
	return result, nil
}

func normalizeMounts(values []ContainerMount) ([]ContainerMount, error) {
	result := make([]ContainerMount, 0, len(values))
	seenTargets := make(map[string]struct{}, len(values))
	for _, value := range values {
		value.Type = strings.ToLower(strings.TrimSpace(value.Type))
		value.Source = strings.TrimSpace(value.Source)
		value.Target = strings.TrimSpace(value.Target)
		value.VolumeDriver = strings.TrimSpace(value.VolumeDriver)
		if value.Type != string(mount.TypeBind) && value.Type != string(mount.TypeVolume) && value.Type != string(mount.TypeTmpfs) {
			return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("mount type is invalid"))
		}
		if value.Target == "" || !strings.HasPrefix(value.Target, "/") || path.Clean(value.Target) != value.Target || strings.Contains(value.Target, "\x00") || strings.Contains(value.Target, ":") {
			return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("mount target is invalid"))
		}
		if _, exists := seenTargets[value.Target]; exists {
			return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("mount target is duplicated"))
		}
		seenTargets[value.Target] = struct{}{}
		switch value.Type {
		case string(mount.TypeBind):
			if !validMountSource(value.Source) || value.VolumeDriver != "" || value.TmpfsSizeByte != 0 {
				return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("bind mount is invalid"))
			}
		case string(mount.TypeVolume):
			if value.Source == "" || !identifierPattern.MatchString(value.Source) || value.TmpfsSizeByte != 0 || (value.VolumeDriver != "" && !identifierPattern.MatchString(value.VolumeDriver)) {
				return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("volume mount is invalid"))
			}
		case string(mount.TypeTmpfs):
			if value.Source != "" || value.VolumeDriver != "" || value.TmpfsSizeByte < 0 {
				return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("tmpfs mount is invalid"))
			}
		}
		result = append(result, value)
	}
	return result, nil
}

func validMountSource(value string) bool {
	if value == "" || !strings.HasPrefix(value, "/") || path.Clean(value) != value || strings.ContainsAny(value, "\x00\r\n:") {
		return false
	}
	return true
}

func normalizeNetwork(request ContainerCreateRequest) (*ContainerNetwork, error) {
	network := request.Network
	if network != nil && (request.NetworkName != "" || request.NetworkSubnet != "" || request.NetworkGateway != "" || request.NetworkIP != "") {
		return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("network aliases conflict with network object"))
	}
	if network == nil && (request.NetworkName != "" || request.NetworkSubnet != "" || request.NetworkGateway != "" || request.NetworkIP != "") {
		network = &ContainerNetwork{Name: request.NetworkName, Subnet: request.NetworkSubnet, Gateway: request.NetworkGateway, IP: request.NetworkIP}
	}
	if network == nil {
		return nil, nil
	}
	copyNetwork := *network
	copyNetwork.Name = strings.TrimSpace(copyNetwork.Name)
	copyNetwork.Driver = strings.ToLower(strings.TrimSpace(copyNetwork.Driver))
	copyNetwork.Subnet = strings.TrimSpace(copyNetwork.Subnet)
	copyNetwork.Gateway = strings.TrimSpace(copyNetwork.Gateway)
	copyNetwork.IP = strings.TrimSpace(copyNetwork.IP)
	if copyNetwork.Name == "" || !identifierPattern.MatchString(copyNetwork.Name) || strings.Contains(copyNetwork.Name, ":") {
		return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("network name is invalid"))
	}
	if (copyNetwork.Name == "host" || copyNetwork.Name == "none") && (copyNetwork.Driver != "" || copyNetwork.Subnet != "" || copyNetwork.Gateway != "" || copyNetwork.IP != "") {
		return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("host and none networks do not accept custom network settings"))
	}
	if copyNetwork.Driver != "" && !identifierPattern.MatchString(copyNetwork.Driver) {
		return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("network driver is invalid"))
	}
	var subnet netip.Prefix
	var err error
	if copyNetwork.Subnet != "" {
		subnet, err = netip.ParsePrefix(copyNetwork.Subnet)
		if err != nil || !subnet.IsValid() {
			return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("network subnet is invalid"))
		}
		copyNetwork.Subnet = subnet.String()
	}
	if copyNetwork.Gateway != "" {
		gateway, parseErr := netip.ParseAddr(copyNetwork.Gateway)
		if parseErr != nil || (subnet.IsValid() && !subnet.Contains(gateway)) {
			return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("network gateway is invalid"))
		}
		copyNetwork.Gateway = gateway.String()
	}
	if copyNetwork.IP != "" {
		address, parseErr := netip.ParseAddr(copyNetwork.IP)
		if parseErr != nil || (subnet.IsValid() && !subnet.Contains(address)) {
			return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("network ip is invalid"))
		}
		copyNetwork.IP = address.String()
	}
	if copyNetwork.Gateway != "" && copyNetwork.Subnet == "" {
		return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("network gateway requires subnet"))
	}
	return &copyNetwork, nil
}

func normalizePorts(values []ContainerPort) ([]ContainerPort, error) {
	result := make([]ContainerPort, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		containerPort := value.ContainerPort
		if value.PrivatePort != 0 {
			if containerPort != 0 && containerPort != value.PrivatePort {
				return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("container port aliases conflict"))
			}
			containerPort = value.PrivatePort
		}
		hostPort := value.HostPort
		if value.PublicPort != 0 {
			if hostPort != 0 && hostPort != value.PublicPort {
				return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("host port aliases conflict"))
			}
			hostPort = value.PublicPort
		}
		if containerPort == 0 {
			return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("container port is required"))
		}
		protocol := strings.ToLower(strings.TrimSpace(value.Protocol))
		if protocol == "" {
			protocol = "tcp"
		}
		if protocol != "tcp" && protocol != "udp" && protocol != "sctp" {
			return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("container port protocol is invalid"))
		}
		hostIP := strings.TrimSpace(value.HostIP)
		if hostIP != "" {
			address, parseErr := netip.ParseAddr(hostIP)
			if parseErr != nil {
				return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("host ip is invalid"))
			}
			hostIP = address.String()
		}
		key := fmt.Sprintf("%d/%s", containerPort, protocol)
		if _, exists := seen[key]; exists {
			return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("container port is duplicated"))
		}
		seen[key] = struct{}{}
		result = append(result, ContainerPort{ContainerPort: containerPort, HostPort: hostPort, HostIP: hostIP, Protocol: protocol})
	}
	return result, nil
}

func normalizeCommand(values []string) ([]string, error) {
	if values == nil {
		return nil, nil
	}
	if len(values) == 0 {
		return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("command must contain at least one argument"))
	}
	result := make([]string, len(values))
	for index, value := range values {
		if value == "" || strings.ContainsAny(value, "\x00\r\n") {
			return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("command argument is invalid"))
		}
		result[index] = value
	}
	return result, nil
}

func normalizeCapabilities(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		capability := strings.ToUpper(strings.TrimSpace(value))
		if strings.HasPrefix(capability, "CAP_") {
			capability = strings.TrimPrefix(capability, "CAP_")
		}
		if !capabilityPattern.MatchString(capability) {
			return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("container capability is invalid"))
		}
		if _, exists := seen[capability]; exists {
			return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("container capability is duplicated"))
		}
		seen[capability] = struct{}{}
		result = append(result, capability)
	}
	return result, nil
}

func normalizeDevices(values []ContainerDevice) ([]ContainerDevice, error) {
	result := make([]ContainerDevice, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value.HostPath = strings.TrimSpace(value.HostPath)
		value.ContainerPath = strings.TrimSpace(value.ContainerPath)
		value.CgroupPermissions = strings.ToLower(strings.TrimSpace(value.CgroupPermissions))
		if !validMountSource(value.HostPath) || !validMountSource(value.ContainerPath) {
			return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("device path is invalid"))
		}
		if value.CgroupPermissions == "" {
			value.CgroupPermissions = "rwm"
		}
		if value.CgroupPermissions != "r" && value.CgroupPermissions != "w" && value.CgroupPermissions != "m" && !validDevicePermissions(value.CgroupPermissions) {
			return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("device permissions are invalid"))
		}
		if _, exists := seen[value.ContainerPath]; exists {
			return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("device target is duplicated"))
		}
		seen[value.ContainerPath] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func validDevicePermissions(value string) bool {
	if len(value) > 3 {
		return false
	}
	for _, character := range value {
		if character != 'r' && character != 'w' && character != 'm' {
			return false
		}
	}
	return true
}

func normalizeGPUs(single *ContainerGPU, values []ContainerGPU) ([]ContainerGPU, error) {
	all := make([]ContainerGPU, 0, len(values)+1)
	if single != nil {
		all = append(all, *single)
	}
	all = append(all, values...)
	result := make([]ContainerGPU, 0, len(all))
	for _, value := range all {
		value.DeviceIDs = append([]string(nil), value.DeviceIDs...)
		value.Capabilities = append([]string(nil), value.Capabilities...)
		value.Options = cloneStringMap(value.Options)
		value.Driver = strings.TrimSpace(value.Driver)
		if value.Driver != "" && !identifierPattern.MatchString(value.Driver) {
			return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("gpu driver is invalid"))
		}
		if value.Count < -1 || value.Count > 128 || (value.Count == 0 && len(value.DeviceIDs) == 0) {
			return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("gpu count is invalid"))
		}
		seenIDs := make(map[string]struct{}, len(value.DeviceIDs))
		for index, deviceID := range value.DeviceIDs {
			deviceID = strings.TrimSpace(deviceID)
			if deviceID == "" || strings.ContainsAny(deviceID, "\x00\r\n") {
				return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("gpu device id is invalid"))
			}
			if _, exists := seenIDs[deviceID]; exists {
				return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("gpu device id is duplicated"))
			}
			seenIDs[deviceID] = struct{}{}
			value.DeviceIDs[index] = deviceID
		}
		capabilities := make([]string, 0, len(value.Capabilities))
		seenCapabilities := make(map[string]struct{}, len(value.Capabilities))
		for _, capability := range value.Capabilities {
			capability = strings.ToLower(strings.TrimSpace(capability))
			if capability == "" || !capabilityPattern.MatchString(strings.ToUpper(capability)) {
				return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("gpu capability is invalid"))
			}
			if _, exists := seenCapabilities[capability]; exists {
				return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("gpu capability is duplicated"))
			}
			seenCapabilities[capability] = struct{}{}
			capabilities = append(capabilities, capability)
		}
		value.Capabilities = capabilities
		for key, option := range value.Options {
			if !identifierPattern.MatchString(key) || strings.ContainsAny(option, "\x00\r\n") {
				return nil, coded("DOCKER_CONTAINER_CREATE_INVALID", errors.New("gpu option is invalid"))
			}
		}
		result = append(result, value)
	}
	return result, nil
}

func validImageReferenceCharacters(value string) bool {
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') {
			continue
		}
		switch character {
		case '.', '_', '-', '/', ':', '@':
		default:
			return false
		}
	}
	return true
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
