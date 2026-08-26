package agentsocket

import (
	"context"
	"errors"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

type DockerResourcesProvider interface {
	ListResources(context.Context) (docker.Resources, error)
}

type dockerResourcesService struct{ provider DockerResourcesProvider }

func newDockerResourcesService(provider DockerResourcesProvider) *dockerResourcesService {
	return &dockerResourcesService{provider: provider}
}

func (s *dockerResourcesService) ListResources(ctx context.Context, _ *emptypb.Empty) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_DOCKER_RESOURCES_UNAVAILABLE")
	}
	result, err := s.provider.ListResources(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, grpcstatus.Error(codes.DeadlineExceeded, "AGENT_RPC_TIMEOUT")
		}
		return nil, grpcstatus.Error(codes.Unavailable, "DOCKER_RESOURCES_UNAVAILABLE")
	}
	return dashboardStruct(result, "AGENT_DOCKER_RESOURCES_RESPONSE_INVALID")
}
