package agentsocket

import (
	"context"
	"errors"

	ncpcompose "github.com/Kkwans/nas-control-plane/internal/compose"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

type DockerControlProvider interface {
	Control(context.Context, docker.ContainerActionRequest) (docker.ContainerActionResult, error)
}

type DockerProjectControlProvider interface {
	ControlStandaloneProject(context.Context, docker.ProjectActionRequest) (docker.ProjectActionResult, error)
}

type DockerComposeControlProvider interface {
	ControlComposeProject(context.Context, ncpcompose.LifecycleRequest) (ncpcompose.LifecycleResult, error)
}

type dockerControlService struct {
	provider        DockerControlProvider
	composeProvider ComposeProvider
}

func newDockerControlService(provider DockerControlProvider, composeProviders ...ComposeProvider) *dockerControlService {
	service := &dockerControlService{provider: provider}
	if len(composeProviders) > 0 {
		service.composeProvider = composeProviders[0]
	}
	return service
}

func (s *dockerControlService) ControlContainer(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_DOCKER_CONTROL_UNAVAILABLE")
	}
	decoded, err := decodeContainerActionRequest(request)
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "DOCKER_CONTAINER_ACTION_INVALID")
	}
	result, err := s.provider.Control(ctx, decoded)
	if err != nil {
		code := docker.ErrorCode(err)
		switch code {
		case "DOCKER_CONTAINER_NOT_FOUND":
			return nil, grpcstatus.Error(codes.NotFound, code)
		case "DOCKER_CONTAINER_ACTION_FAILED", "DOCKER_CONTAINER_INSPECT_FAILED":
			return nil, grpcstatus.Error(codes.FailedPrecondition, code)
		default:
			return nil, grpcstatus.Error(codes.Unavailable, "DOCKER_CONTAINER_ACTION_UNAVAILABLE")
		}
	}
	return dashboardStruct(result, "AGENT_DOCKER_CONTROL_RESPONSE_INVALID")
}

func (s *dockerControlService) ControlStandaloneProject(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
	}
	provider, ok := s.provider.(DockerProjectControlProvider)
	if !ok {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_DOCKER_CONTROL_UNAVAILABLE")
	}
	decoded, err := decodeProjectActionRequest(request)
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "DOCKER_PROJECT_ACTION_INVALID")
	}
	result, err := provider.ControlStandaloneProject(ctx, decoded)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, grpcstatus.Error(codes.DeadlineExceeded, "AGENT_RPC_TIMEOUT")
		}
		code := docker.ErrorCode(err)
		if code == "DOCKER_PROJECT_ACTION_INVALID" {
			return nil, grpcstatus.Error(codes.InvalidArgument, code)
		}
		return nil, grpcstatus.Error(codes.FailedPrecondition, "DOCKER_PROJECT_ACTION_FAILED")
	}
	return dashboardStruct(result, "AGENT_DOCKER_CONTROL_RESPONSE_INVALID")
}

func (s *dockerControlService) ControlComposeProject(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
	}
	provider, ok := s.composeProvider.(DockerComposeControlProvider)
	if !ok {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_DOCKER_CONTROL_UNAVAILABLE")
	}
	decoded, err := decodeComposeProjectRequest(request)
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "COMPOSE_LIFECYCLE_INVALID")
	}
	result, err := provider.ControlComposeProject(ctx, decoded)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, grpcstatus.Error(codes.DeadlineExceeded, "AGENT_RPC_TIMEOUT")
		}
		code := ncpcompose.ErrorCode(err)
		switch code {
		case "COMPOSE_LIFECYCLE_INVALID":
			return nil, grpcstatus.Error(codes.InvalidArgument, code)
		case "COMPOSE_LIFECYCLE_FAILED", "COMPOSE_LIFECYCLE_VERIFY_FAILED":
			return nil, grpcstatus.Error(codes.FailedPrecondition, code)
		default:
			return nil, grpcstatus.Error(codes.Unavailable, "COMPOSE_LIFECYCLE_UNAVAILABLE")
		}
	}
	return dashboardStruct(result, "AGENT_DOCKER_CONTROL_RESPONSE_INVALID")
}

func decodeContainerActionRequest(request *structpb.Struct) (docker.ContainerActionRequest, error) {
	if request == nil {
		return docker.ContainerActionRequest{}, errors.New("container control request is required")
	}
	values := request.AsMap()
	containerID, ok := values["container_id"].(string)
	if !ok {
		return docker.ContainerActionRequest{}, errors.New("container id is required")
	}
	actionValue, ok := values["action"].(string)
	if !ok {
		return docker.ContainerActionRequest{}, errors.New("container action is required")
	}
	action, err := docker.ParseContainerAction(actionValue)
	if err != nil {
		return docker.ContainerActionRequest{}, err
	}
	result := docker.ContainerActionRequest{ContainerID: containerID, Action: action}
	if err := result.Validate(); err != nil {
		return docker.ContainerActionRequest{}, err
	}
	return result, nil
}

func decodeProjectActionRequest(request *structpb.Struct) (docker.ProjectActionRequest, error) {
	if request == nil {
		return docker.ProjectActionRequest{}, errors.New("project control request is required")
	}
	projectID, err := requiredString(request, "project_id")
	if err != nil {
		return docker.ProjectActionRequest{}, err
	}
	containerIDs, err := requiredStringList(request, "container_ids")
	if err != nil {
		return docker.ProjectActionRequest{}, err
	}
	actionValue, err := requiredString(request, "action")
	if err != nil {
		return docker.ProjectActionRequest{}, err
	}
	action, err := docker.ParseContainerAction(actionValue)
	if err != nil {
		return docker.ProjectActionRequest{}, err
	}
	kind := docker.ProjectKindStandalone
	if rawKind, ok := request.AsMap()["kind"].(string); ok && rawKind != "" {
		kind = docker.ProjectKind(rawKind)
	}
	result := docker.ProjectActionRequest{
		ProjectID: projectID, Kind: kind, ContainerIDs: containerIDs, Action: action,
	}
	if _, err := result.Normalize(); err != nil {
		return docker.ProjectActionRequest{}, err
	}
	return result, nil
}

func decodeComposeProjectRequest(request *structpb.Struct) (ncpcompose.LifecycleRequest, error) {
	if request == nil {
		return ncpcompose.LifecycleRequest{}, errors.New("compose lifecycle request is required")
	}
	projectID, err := requiredString(request, "project_id")
	if err != nil {
		return ncpcompose.LifecycleRequest{}, err
	}
	workingDirectory, err := requiredString(request, "working_directory")
	if err != nil {
		return ncpcompose.LifecycleRequest{}, err
	}
	configFiles, err := requiredStringList(request, "config_files")
	if err != nil {
		return ncpcompose.LifecycleRequest{}, err
	}
	actionValue, err := requiredString(request, "action")
	if err != nil {
		return ncpcompose.LifecycleRequest{}, err
	}
	action, err := ncpcompose.ParseLifecycleAction(actionValue)
	if err != nil {
		return ncpcompose.LifecycleRequest{}, err
	}
	result := ncpcompose.LifecycleRequest{
		ProjectID: projectID, WorkingDirectory: workingDirectory, ConfigFiles: configFiles, Action: action,
	}
	if _, err := result.Normalize(); err != nil {
		return ncpcompose.LifecycleRequest{}, err
	}
	return result, nil
}
