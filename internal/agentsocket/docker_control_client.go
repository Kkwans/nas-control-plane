package agentsocket

import (
	"context"
	"errors"
	"fmt"

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
		"DOCKER_IMAGE_REMOVE_BATCH_INVALID", "DOCKER_IMAGE_REMOVE_BATCH_LIST_FAILED",
		"DOCKER_IMAGE_REMOVE_BATCH_FAILED", "COMPOSE_LIFECYCLE_INVALID",
		"COMPOSE_LIFECYCLE_FAILED", "COMPOSE_LIFECYCLE_VERIFY_FAILED",
		"COMPOSE_LIFECYCLE_UNAVAILABLE":
		return coded(message, err)
	default:
		return rpcError(err)
	}
}
