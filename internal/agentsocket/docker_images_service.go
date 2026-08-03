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

type DockerImageProvider interface {
	List(context.Context) (docker.ImageInventory, error)
	Search(context.Context, docker.HubSearchRequest) (docker.HubSearchResult, error)
	Tags(context.Context, docker.HubTagsRequest) (docker.HubTagsResult, error)
	Pull(context.Context, docker.ImagePullRequest) (docker.ImagePullResult, error)
	Remove(context.Context, docker.ImageRemoveRequest) (docker.ImageRemoveResult, error)
}

type DockerImageProgressProvider interface {
	PullWithProgress(context.Context, docker.ImagePullRequest, func(docker.ImagePullProgress)) (docker.ImagePullResult, error)
}

type DockerImageBatchProvider interface {
	RemoveBatch(context.Context, docker.ImageRemoveBatchRequest) (docker.ImageRemoveBatchResult, error)
}

func (s *dockerImageService) SearchImages(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	if s.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_DOCKER_IMAGES_UNAVAILABLE")
	}
	query, err := requiredString(request, "query")
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "DOCKER_HUB_QUERY_INVALID")
	}
	page, pageSize, err := requiredPagination(request)
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "DOCKER_HUB_QUERY_INVALID")
	}
	sortOrder, _ := request.AsMap()["sort"].(string)
	result, err := s.provider.Search(ctx, docker.HubSearchRequest{Query: query, Page: page, PageSize: pageSize, Sort: sortOrder})
	if err != nil {
		return nil, grpcstatus.Error(codes.Unavailable, "DOCKER_HUB_SEARCH_FAILED")
	}
	return dashboardStruct(result, "AGENT_DOCKER_IMAGES_RESPONSE_INVALID")
}

func (s *dockerImageService) ListTags(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	if s.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_DOCKER_IMAGES_UNAVAILABLE")
	}
	namespace, err := requiredString(request, "namespace")
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "DOCKER_HUB_REPOSITORY_INVALID")
	}
	repository, err := requiredString(request, "repository")
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "DOCKER_HUB_REPOSITORY_INVALID")
	}
	page, pageSize, err := requiredPagination(request)
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "DOCKER_HUB_REPOSITORY_INVALID")
	}
	result, err := s.provider.Tags(ctx, docker.HubTagsRequest{
		Namespace: namespace, Repository: repository, Page: page, PageSize: pageSize,
	})
	if err != nil {
		return nil, grpcstatus.Error(codes.Unavailable, "DOCKER_HUB_TAGS_FAILED")
	}
	return dashboardStruct(result, "AGENT_DOCKER_IMAGES_RESPONSE_INVALID")
}

type dockerImageService struct {
	provider DockerImageProvider
}

func newDockerImageService(provider DockerImageProvider) *dockerImageService {
	return &dockerImageService{provider: provider}
}

func (s *dockerImageService) ListImages(ctx context.Context, _ *emptypb.Empty) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_DOCKER_IMAGES_UNAVAILABLE")
	}
	result, err := s.provider.List(ctx)
	if err != nil {
		return nil, grpcstatus.Error(codes.Unavailable, "DOCKER_IMAGE_LIST_FAILED")
	}
	return dashboardStruct(result, "AGENT_DOCKER_IMAGES_RESPONSE_INVALID")
}

func (s *dockerImageService) PullImage(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	if s.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_DOCKER_IMAGES_UNAVAILABLE")
	}
	reference, err := requiredString(request, "reference")
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "DOCKER_IMAGE_PULL_INVALID")
	}
	result, err := s.provider.Pull(ctx, docker.ImagePullRequest{Reference: reference})
	if err != nil {
		return nil, grpcstatus.Error(codes.Unavailable, "DOCKER_IMAGE_PULL_FAILED")
	}
	return dashboardStruct(result, "AGENT_DOCKER_IMAGES_RESPONSE_INVALID")
}

func (s *dockerImageService) PullImageStream(request *structpb.Struct, stream AgentDockerImagesPullStreamServer) error {
	provider, ok := s.provider.(DockerImageProgressProvider)
	if !ok {
		return grpcstatus.Error(codes.Unimplemented, "DOCKER_IMAGE_PULL_PROGRESS_UNAVAILABLE")
	}
	reference, err := requiredString(request, "reference")
	if err != nil {
		return grpcstatus.Error(codes.InvalidArgument, "DOCKER_IMAGE_PULL_INVALID")
	}
	result, err := provider.PullWithProgress(stream.Context(), docker.ImagePullRequest{Reference: reference}, func(progress docker.ImagePullProgress) {
		message, conversionError := dashboardStruct(map[string]any{
			"type": "progress", "layerId": progress.LayerID, "status": progress.Status,
			"current": progress.Current, "total": progress.Total,
		}, "AGENT_DOCKER_IMAGES_RESPONSE_INVALID")
		if conversionError == nil {
			_ = stream.Send(message)
		}
	})
	if err != nil {
		return grpcstatus.Error(codes.Unavailable, "DOCKER_IMAGE_PULL_FAILED")
	}
	message, err := dashboardStruct(map[string]any{
		"type": "completed", "reference": result.Reference, "completed": result.Completed,
	}, "AGENT_DOCKER_IMAGES_RESPONSE_INVALID")
	if err != nil {
		return grpcstatus.Error(codes.Internal, "AGENT_DOCKER_IMAGES_RESPONSE_INVALID")
	}
	return stream.Send(message)
}

func (s *dockerImageService) RemoveImage(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	if s.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_DOCKER_IMAGES_UNAVAILABLE")
	}
	imageID, err := requiredString(request, "image_id")
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "DOCKER_IMAGE_REMOVE_INVALID")
	}
	result, err := s.provider.Remove(ctx, docker.ImageRemoveRequest{ImageID: imageID})
	if err != nil {
		return nil, grpcstatus.Error(codes.Unavailable, "DOCKER_IMAGE_REMOVE_FAILED")
	}
	return dashboardStruct(result, "AGENT_DOCKER_IMAGES_RESPONSE_INVALID")
}

func (s *dockerImageService) RemoveImages(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
	}
	provider, ok := s.provider.(DockerImageBatchProvider)
	if !ok {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_DOCKER_IMAGES_UNAVAILABLE")
	}
	imageIDs, err := requiredStringList(request, "image_ids")
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "DOCKER_IMAGE_REMOVE_BATCH_INVALID")
	}
	result, err := provider.RemoveBatch(ctx, docker.ImageRemoveBatchRequest{ImageIDs: imageIDs})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, grpcstatus.Error(codes.DeadlineExceeded, "AGENT_RPC_TIMEOUT")
		}
		code := docker.ErrorCode(err)
		if code == "DOCKER_IMAGE_REMOVE_BATCH_INVALID" {
			return nil, grpcstatus.Error(codes.InvalidArgument, code)
		}
		if code == "DOCKER_IMAGE_REMOVE_BATCH_LIST_FAILED" {
			return nil, grpcstatus.Error(codes.Unavailable, code)
		}
		return nil, grpcstatus.Error(codes.Unavailable, "DOCKER_IMAGE_REMOVE_BATCH_FAILED")
	}
	return dashboardStruct(result, "AGENT_DOCKER_IMAGES_RESPONSE_INVALID")
}

func requiredString(request *structpb.Struct, name string) (string, error) {
	if request == nil {
		return "", errors.New("request is required")
	}
	value, ok := request.AsMap()[name].(string)
	if !ok || value == "" {
		return "", errors.New("required string is missing")
	}
	return value, nil
}

func requiredPagination(request *structpb.Struct) (int, int, error) {
	if request == nil {
		return 0, 0, errors.New("request is required")
	}
	values := request.AsMap()
	page, pageOK := values["page"].(float64)
	pageSize, pageSizeOK := values["page_size"].(float64)
	if !pageOK || !pageSizeOK || page < 1 || pageSize < 1 {
		return 0, 0, errors.New("pagination is invalid")
	}
	return int(page), int(pageSize), nil
}
