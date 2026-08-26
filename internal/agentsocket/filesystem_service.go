package agentsocket

import (
	"context"
	"errors"
	"math"

	"github.com/Kkwans/nas-control-plane/internal/filesystem"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

type FilesystemProvider interface {
	List(context.Context, filesystem.Request) (filesystem.Page, error)
}

type filesystemService struct{ provider FilesystemProvider }

func newFilesystemService(provider FilesystemProvider) *filesystemService {
	return &filesystemService{provider: provider}
}

func (s *filesystemService) ListPath(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_FILESYSTEM_UNAVAILABLE")
	}
	decoded, err := decodeFilesystemRequest(request)
	if err != nil {
		code := filesystem.ErrorCode(err)
		if code == "" {
			code = "FILES_REQUEST_INVALID"
		}
		return nil, grpcstatus.Error(codes.InvalidArgument, code)
	}
	result, err := s.provider.List(ctx, decoded)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, grpcstatus.Error(codes.DeadlineExceeded, "AGENT_RPC_TIMEOUT")
		}
		code := filesystem.ErrorCode(err)
		switch code {
		case "FILES_PATH_INVALID", "FILES_CURSOR_INVALID", "FILES_LIMIT_INVALID":
			return nil, grpcstatus.Error(codes.InvalidArgument, code)
		case "FILES_PATH_NOT_FOUND":
			return nil, grpcstatus.Error(codes.NotFound, code)
		case "FILES_PATH_UNREADABLE", "FILES_DIRECTORY_READ_FAILED":
			return nil, grpcstatus.Error(codes.FailedPrecondition, code)
		default:
			return nil, grpcstatus.Error(codes.Unavailable, "FILES_DIRECTORY_READ_FAILED")
		}
	}
	return dashboardStruct(result, "AGENT_FILESYSTEM_RESPONSE_INVALID")
}

func decodeFilesystemRequest(request *structpb.Struct) (filesystem.Request, error) {
	if request == nil {
		return filesystem.Request{}, errors.New("filesystem request is required")
	}
	path, err := requiredString(request, "path")
	if err != nil {
		return filesystem.Request{}, err
	}
	result := filesystem.Request{Path: path}
	values := request.AsMap()
	if cursor, ok := values["cursor"].(string); ok {
		result.Cursor = cursor
	}
	if rawLimit, ok := values["limit"]; ok {
		limit, ok := rawLimit.(float64)
		if !ok || math.Trunc(limit) != limit {
			return filesystem.Request{}, errors.New("filesystem limit is invalid")
		}
		result.Limit = int(limit)
	}
	_, err = result.Normalize()
	return result, err
}
