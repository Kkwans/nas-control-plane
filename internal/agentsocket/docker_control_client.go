package agentsocket

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ncpcompose "github.com/Kkwans/nas-control-plane/internal/compose"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

func ControlStandaloneProject(ctx context.Context, socketPath string, request docker.ProjectActionRequest) (docker.ProjectActionResult, error) {
	request, err := request.Normalize()
	if err != nil {
		return docker.ProjectActionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return docker.ProjectActionResult{}, contextError(err)
	}
	if err := ensureDockerAgentProtocol(ctx, socketPath); err != nil {
		return docker.ProjectActionResult{}, err
	}
	connection, err := dialSocket(socketPath)
	if err != nil {
		return docker.ProjectActionResult{}, err
	}
	defer connection.Close()

	containerIDs := make([]any, 0, len(request.ContainerIDs))
	for _, containerID := range request.ContainerIDs {
		containerIDs = append(containerIDs, containerID)
	}
	payload, err := structpb.NewStruct(map[string]any{
		"project_id":    request.ProjectID,
		"kind":          string(request.Kind),
		"action":        string(request.Action),
		"container_ids": containerIDs,
	})
	if err != nil {
		return docker.ProjectActionResult{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := NewAgentDockerControlServiceClient(connection).ControlStandaloneProject(ctx, payload)
	if err != nil {
		return docker.ProjectActionResult{}, dockerRPCError(err)
	}
	var result docker.ProjectActionResult
	if err := decodeDashboardResponse(response, &result); err != nil {
		return docker.ProjectActionResult{}, err
	}
	if result.ProjectID == "" || result.Kind != docker.ProjectKindStandalone || result.State == "" || result.Containers == nil || len(result.Containers) != len(request.ContainerIDs) {
		return docker.ProjectActionResult{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("standalone project action result is incomplete"))
	}
	if _, err := docker.ParseContainerAction(string(result.Action)); err != nil {
		return docker.ProjectActionResult{}, coded("AGENT_RPC_RESPONSE_INVALID", err)
	}
	return result, nil
}

func ControlComposeProject(ctx context.Context, socketPath string, request ncpcompose.LifecycleRequest) (ncpcompose.LifecycleResult, error) {
	request, err := request.Normalize()
	if err != nil {
		return ncpcompose.LifecycleResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ncpcompose.LifecycleResult{}, contextError(err)
	}
	if err := ensureDockerAgentProtocol(ctx, socketPath); err != nil {
		return ncpcompose.LifecycleResult{}, err
	}
	connection, err := dialSocket(socketPath)
	if err != nil {
		return ncpcompose.LifecycleResult{}, err
	}
	defer connection.Close()
	configFiles := make([]any, 0, len(request.ConfigFiles))
	for _, configFile := range request.ConfigFiles {
		configFiles = append(configFiles, configFile)
	}
	payload, err := structpb.NewStruct(map[string]any{
		"project_id":        request.ProjectID,
		"working_directory": request.WorkingDirectory,
		"config_files":      configFiles,
		"action":            string(request.Action),
	})
	if err != nil {
		return ncpcompose.LifecycleResult{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := NewAgentDockerControlServiceClient(connection).ControlComposeProject(ctx, payload)
	if err != nil {
		return ncpcompose.LifecycleResult{}, dockerRPCError(err)
	}
	var result ncpcompose.LifecycleResult
	if err := decodeDashboardResponse(response, &result); err != nil {
		return ncpcompose.LifecycleResult{}, err
	}
	if result.ProjectID == "" || result.State == "" || result.Services == nil || result.Action == "" {
		return ncpcompose.LifecycleResult{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("compose lifecycle result is incomplete"))
	}
	if _, err := ncpcompose.ParseLifecycleAction(string(result.Action)); err != nil {
		return ncpcompose.LifecycleResult{}, coded("AGENT_RPC_RESPONSE_INVALID", err)
	}
	return result, nil
}

func CreateDockerContainer(ctx context.Context, socketPath string, request docker.ContainerCreateRequest) (docker.ContainerCreateResult, error) {
	spec, err := request.Normalize()
	if err != nil {
		return docker.ContainerCreateResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return docker.ContainerCreateResult{}, contextError(err)
	}
	if err := ensureDockerAgentProtocol(ctx, socketPath); err != nil {
		return docker.ContainerCreateResult{}, err
	}
	connection, err := dialSocket(socketPath)
	if err != nil {
		return docker.ContainerCreateResult{}, err
	}
	defer connection.Close()
	payload, err := containerCreatePayload(spec)
	if err != nil {
		return docker.ContainerCreateResult{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := NewAgentDockerControlServiceClient(connection).CreateContainer(ctx, payload)
	if err != nil {
		return docker.ContainerCreateResult{}, dockerRPCError(err)
	}
	var result docker.ContainerCreateResult
	if err := decodeDashboardResponse(response, &result); err != nil {
		return docker.ContainerCreateResult{}, err
	}
	if result.ContainerID == "" || result.Image == "" || result.State == "" || !result.Created || result.RunContainer != spec.RunContainer || result.Started != spec.RunContainer {
		return docker.ContainerCreateResult{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("container create result is incomplete"))
	}
	return result, nil
}

func CreateContainer(ctx context.Context, socketPath string, request docker.ContainerCreateRequest) (docker.ContainerCreateResult, error) {
	return CreateDockerContainer(ctx, socketPath, request)
}

func InspectDockerContainer(ctx context.Context, socketPath string, containerID string) (docker.ContainerDetails, error) {
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return docker.ContainerDetails{}, docker.InvalidContainerDetailsError()
	}
	if err := ctx.Err(); err != nil {
		return docker.ContainerDetails{}, contextError(err)
	}
	if err := ensureDockerAgentProtocol(ctx, socketPath); err != nil {
		return docker.ContainerDetails{}, err
	}
	connection, err := dialSocket(socketPath)
	if err != nil {
		return docker.ContainerDetails{}, err
	}
	defer connection.Close()
	payload, err := structpb.NewStruct(map[string]any{"container_id": containerID})
	if err != nil {
		return docker.ContainerDetails{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := NewAgentDockerControlServiceClient(connection).InspectContainer(ctx, payload)
	if err != nil {
		return docker.ContainerDetails{}, dockerRPCError(err)
	}
	var result docker.ContainerDetails
	if err := decodeDashboardResponse(response, &result); err != nil {
		return docker.ContainerDetails{}, err
	}
	if result.ID == "" || result.Name == "" || result.State == "" || result.Ports == nil || result.Mounts == nil || result.Networks == nil {
		return docker.ContainerDetails{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("container details result is incomplete"))
	}
	return result, nil
}

func containerCreatePayload(spec docker.ContainerCreateSpec) (*structpb.Struct, error) {
	mounts := make([]any, 0, len(spec.Mounts))
	for _, value := range spec.Mounts {
		mounts = append(mounts, map[string]any{
			"type": value.Type, "source": value.Source, "target": value.Target, "read_only": value.ReadOnly,
			"volume_driver": value.VolumeDriver, "tmpfs_size_bytes": value.TmpfsSizeByte,
		})
	}
	ports := make([]any, 0, len(spec.Ports))
	for _, value := range spec.Ports {
		ports = append(ports, map[string]any{
			"container_port": int64(value.ContainerPort), "host_port": int64(value.HostPort), "host_ip": value.HostIP, "protocol": value.Protocol,
		})
	}
	devices := make([]any, 0, len(spec.Devices))
	for _, value := range spec.Devices {
		devices = append(devices, map[string]any{"host_path": value.HostPath, "container_path": value.ContainerPath, "cgroup_permissions": value.CgroupPermissions})
	}
	gpus := make([]any, 0, len(spec.GPUs))
	for _, value := range spec.GPUs {
		ids := make([]any, 0, len(value.DeviceIDs))
		for _, id := range value.DeviceIDs {
			ids = append(ids, id)
		}
		capabilities := make([]any, 0, len(value.Capabilities))
		for _, capability := range value.Capabilities {
			capabilities = append(capabilities, capability)
		}
		gpus = append(gpus, map[string]any{"driver": value.Driver, "count": value.Count, "device_ids": ids, "capabilities": capabilities, "options": value.Options})
	}
	values := map[string]any{
		"image": spec.Image, "name": spec.Name, "cpu_nano_cpus": spec.NanoCPUs, "memory_bytes": spec.MemoryBytes,
		"cpu_shares": spec.CPUShares, "restart_policy": spec.RestartPolicy, "restart_max_retries": spec.RestartMaxRetries,
		"environment": environmentMap(spec.Environment), "env": []any{}, "mounts": mounts, "ports": ports,
		"command": stringSliceAny(spec.Command), "privileged": spec.Privileged, "cap_add": stringSliceAny(spec.CapAdd),
		"cap_drop": stringSliceAny(spec.CapDrop), "devices": devices, "gpus": gpus, "run_container": spec.RunContainer,
	}
	if spec.Network != nil {
		values["network"] = map[string]any{"name": spec.Network.Name, "driver": spec.Network.Driver, "subnet": spec.Network.Subnet, "gateway": spec.Network.Gateway, "ip": spec.Network.IP}
	}
	return structpb.NewStruct(values)
}

func environmentMap(values []string) map[string]any {
	result := make(map[string]any)
	for _, value := range values {
		separator := strings.IndexByte(value, '=')
		if separator > 0 {
			result[value[:separator]] = value[separator+1:]
		}
	}
	return result
}

func stringSliceAny(values []string) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func ensureDockerAgentProtocol(ctx context.Context, socketPath string) error {
	status, err := Probe(ctx, socketPath)
	if err != nil {
		return err
	}
	if status.ProtocolVersion != ProtocolVersion {
		return coded("AGENT_PROTOCOL_MISMATCH", fmt.Errorf(
			"server protocol %s does not match agent protocol %s",
			ProtocolVersion,
			status.ProtocolVersion,
		))
	}
	return nil
}

func dockerRPCError(err error) error {
	if errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
		return coded("AGENT_RPC_CANCELED", err)
	}
	if errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded {
		return coded("AGENT_RPC_TIMEOUT", err)
	}
	message := status.Convert(err).Message()
	switch message {
	case "DOCKER_PROJECT_ACTION_INVALID", "DOCKER_PROJECT_ACTION_FAILED",
		"DOCKER_CONTAINER_DETAILS_INVALID", "DOCKER_CONTAINER_DETAILS_UNAVAILABLE", "DOCKER_CONTAINER_NOT_FOUND",
		"DOCKER_CONTAINER_CREATE_INVALID", "DOCKER_CONTAINER_CREATE_FAILED", "DOCKER_CONTAINER_START_FAILED",
		"DOCKER_CONTAINER_INSPECT_FAILED", "DOCKER_CONTAINER_CREATE_CLEANUP_FAILED", "DOCKER_CONTAINER_CREATE_UNAVAILABLE",
		"DOCKER_PROJECT_DELETE_INVALID", "DOCKER_PROJECT_DELETE_PROTECTED", "DOCKER_PROJECT_DELETE_RUNNING",
		"DOCKER_PROJECT_DELETE_NOT_FOUND", "DOCKER_PROJECT_DELETE_FAILED", "DOCKER_PROJECT_DELETE_INSPECT_FAILED",
		"DOCKER_PROJECT_DELETE_INVENTORY_FAILED", "DOCKER_PROJECT_DELETE_REGISTRY_NOT_FOUND",
		"DOCKER_PROJECT_DELETE_REGISTRY_FAILED", "DOCKER_PROJECT_DELETE_INTEGRITY_FAILED",
		"DOCKER_PROJECT_DELETE_ROLLBACK_FAILED", "DOCKER_PROJECT_DELETE_REGISTRY_BACKUP_FAILED",
		"DOCKER_PROJECT_DELETE_REGISTRY_UNAVAILABLE",
		"DOCKER_IMAGE_REMOVE_BATCH_INVALID", "DOCKER_IMAGE_REMOVE_BATCH_LIST_FAILED",
		"DOCKER_IMAGE_REMOVE_BATCH_FAILED", "COMPOSE_LIFECYCLE_INVALID",
		"COMPOSE_LIFECYCLE_FAILED", "COMPOSE_LIFECYCLE_VERIFY_FAILED",
		"COMPOSE_LIFECYCLE_UNAVAILABLE":
		return coded(message, err)
	default:
		return rpcError(err)
	}
}
