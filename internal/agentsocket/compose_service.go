package agentsocket

import (
	"context"
	"errors"

	ncpcompose "github.com/Kkwans/nas-control-plane/internal/compose"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

type ComposeProvider interface {
	Read(context.Context, ncpcompose.ReadRequest) (ncpcompose.ProjectConfig, error)
	Validate(context.Context, ncpcompose.ValidateRequest) (ncpcompose.ValidationResult, error)
	Deploy(context.Context, ncpcompose.DeployRequest) (ncpcompose.DeployResult, error)
}

type composeService struct{ provider ComposeProvider }

func newComposeService(provider ComposeProvider) *composeService {
	return &composeService{provider: provider}
}

func (service *composeService) ReadConfig(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	if service.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_COMPOSE_UNAVAILABLE")
	}
	projectID, err := requiredString(request, "project_id")
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "COMPOSE_REQUEST_INVALID")
	}
	workingDirectory, err := requiredString(request, "working_directory")
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "COMPOSE_REQUEST_INVALID")
	}
	files, err := requiredStringList(request, "config_files")
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "COMPOSE_REQUEST_INVALID")
	}
	result, err := service.provider.Read(ctx, ncpcompose.ReadRequest{ProjectID: projectID, WorkingDirectory: workingDirectory, ConfigFiles: files})
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "COMPOSE_CONFIG_READ_FAILED")
	}
	return dashboardStruct(result, "AGENT_COMPOSE_RESPONSE_INVALID")
}

func (service *composeService) ValidateConfig(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	if service.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_COMPOSE_UNAVAILABLE")
	}
	path, err := requiredString(request, "path")
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "COMPOSE_REQUEST_INVALID")
	}
	content, err := requiredString(request, "content")
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "COMPOSE_REQUEST_INVALID")
	}
	result, err := service.provider.Validate(ctx, ncpcompose.ValidateRequest{Path: path, Content: content})
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "COMPOSE_CONFIG_INVALID")
	}
	return dashboardStruct(result, "AGENT_COMPOSE_RESPONSE_INVALID")
}

func (service *composeService) DeployConfig(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	if service.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_COMPOSE_UNAVAILABLE")
	}
	projectID, err := requiredString(request, "project_id")
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "COMPOSE_REQUEST_INVALID")
	}
	workingDirectory, err := requiredString(request, "working_directory")
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "COMPOSE_REQUEST_INVALID")
	}
	configFiles, err := requiredStringList(request, "config_files")
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "COMPOSE_REQUEST_INVALID")
	}
	targetPath, err := requiredString(request, "target_path")
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "COMPOSE_REQUEST_INVALID")
	}
	content, err := requiredString(request, "content")
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "COMPOSE_REQUEST_INVALID")
	}
	result, err := service.provider.Deploy(ctx, ncpcompose.DeployRequest{
		ProjectID: projectID, WorkingDirectory: workingDirectory, ConfigFiles: configFiles,
		TargetPath: targetPath, Content: content,
	})
	if err != nil {
		return nil, grpcstatus.Error(codes.Internal, "COMPOSE_DEPLOY_FAILED")
	}
	return dashboardStruct(result, "AGENT_COMPOSE_RESPONSE_INVALID")
}

func requiredStringList(request *structpb.Struct, name string) ([]string, error) {
	if request == nil {
		return nil, errors.New("request is required")
	}
	raw, ok := request.AsMap()[name].([]any)
	if !ok || len(raw) == 0 {
		return nil, errors.New("required string list is missing")
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		value, ok := item.(string)
		if !ok || value == "" {
			return nil, errors.New("string list contains invalid value")
		}
		result = append(result, value)
	}
	return result, nil
}
