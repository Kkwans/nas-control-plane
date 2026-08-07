package agentsocket

import (
	"context"
	"errors"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

type DockerProjectDeleteProvider interface {
	DeleteProject(context.Context, docker.ProjectDeleteRequest) (docker.ProjectDeleteResult, error)
}

func (s *dockerControlService) DeleteProject(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
	}
	provider, ok := s.provider.(DockerProjectDeleteProvider)
	if !ok {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_DOCKER_CONTROL_UNAVAILABLE")
	}
	decoded, err := decodeProjectDeleteRequest(request)
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "DOCKER_PROJECT_DELETE_INVALID")
	}
	result, err := provider.DeleteProject(ctx, decoded)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, grpcstatus.Error(codes.DeadlineExceeded, "AGENT_RPC_TIMEOUT")
		}
		code := docker.ErrorCode(err)
		switch code {
		case "DOCKER_PROJECT_DELETE_INVALID":
			return nil, grpcstatus.Error(codes.InvalidArgument, code)
		case "DOCKER_PROJECT_DELETE_NOT_FOUND":
			return nil, grpcstatus.Error(codes.NotFound, code)
		case "DOCKER_PROJECT_DELETE_PROTECTED", "DOCKER_PROJECT_DELETE_RUNNING", "DOCKER_PROJECT_DELETE_REGISTRY_NOT_FOUND", "DOCKER_PROJECT_DELETE_FAILED", "DOCKER_PROJECT_DELETE_INSPECT_FAILED", "DOCKER_PROJECT_DELETE_INVENTORY_FAILED", "DOCKER_PROJECT_DELETE_REGISTRY_FAILED", "DOCKER_PROJECT_DELETE_INTEGRITY_FAILED", "DOCKER_PROJECT_DELETE_ROLLBACK_FAILED", "DOCKER_PROJECT_DELETE_REGISTRY_BACKUP_FAILED":
			return nil, grpcstatus.Error(codes.FailedPrecondition, code)
		case "DOCKER_PROJECT_DELETE_REGISTRY_UNAVAILABLE":
			return nil, grpcstatus.Error(codes.Unavailable, code)
		default:
			return nil, grpcstatus.Error(codes.Unavailable, "DOCKER_PROJECT_DELETE_UNAVAILABLE")
		}
	}
	return dashboardStruct(result, "AGENT_DOCKER_CONTROL_RESPONSE_INVALID")
}
