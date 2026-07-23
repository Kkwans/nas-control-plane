package agentsocket

import (
	"context"
	"errors"
	"math"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

type DockerLogsProvider interface {
	Read(context.Context, docker.ContainerLogsRequest) (docker.ContainerLogsResult, error)
}

type dockerLogsService struct {
	provider DockerLogsProvider
}

func newDockerLogsService(provider DockerLogsProvider) *dockerLogsService {
	return &dockerLogsService{provider: provider}
}

func (s *dockerLogsService) ReadLogs(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_DOCKER_LOGS_UNAVAILABLE")
	}
	decoded, err := decodeContainerLogsRequest(request)
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "DOCKER_LOGS_INPUT_INVALID")
	}
	result, err := s.provider.Read(ctx, decoded)
	if err != nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_DOCKER_LOGS_UNAVAILABLE")
	}
	return dashboardStruct(result, "AGENT_DOCKER_LOGS_RESPONSE_INVALID")
}

func decodeContainerLogsRequest(request *structpb.Struct) (docker.ContainerLogsRequest, error) {
	if request == nil {
		return docker.ContainerLogsRequest{}, errors.New("container logs request is required")
	}
	values := request.AsMap()
	containerID, ok := values["container_id"].(string)
	if !ok {
		return docker.ContainerLogsRequest{}, errors.New("container id is required")
	}
	tailValue, ok := values["tail"].(float64)
	if !ok || math.Trunc(tailValue) != tailValue || tailValue < 1 || tailValue > docker.MaxContainerLogTail {
		return docker.ContainerLogsRequest{}, errors.New("tail is invalid")
	}
	result := docker.ContainerLogsRequest{ContainerID: containerID, Tail: int(tailValue)}
	if _, err := result.Normalize(); err != nil {
		return docker.ContainerLogsRequest{}, err
	}
	return result, nil
}
