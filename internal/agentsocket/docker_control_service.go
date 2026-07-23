package agentsocket

import (
	"context"
	"errors"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

type DockerControlProvider interface {
	Control(context.Context, docker.ContainerActionRequest) (docker.ContainerActionResult, error)
}

type dockerControlService struct {
	provider DockerControlProvider
}

func newDockerControlService(provider DockerControlProvider) *dockerControlService {
	return &dockerControlService{provider: provider}
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
		return nil, grpcstatus.Error(codes.Unavailable, "DOCKER_CONTAINER_ACTION_UNAVAILABLE")
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
