package agentsocket

import (
	"context"
	"errors"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/protobuf/types/known/structpb"
)

func DeleteDockerProject(ctx context.Context, socketPath string, request docker.ProjectDeleteRequest) (docker.ProjectDeleteResult, error) {
	request, err := request.Normalize()
	if err != nil {
		return docker.ProjectDeleteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return docker.ProjectDeleteResult{}, contextError(err)
	}
	if err := ensureDockerAgentProtocol(ctx, socketPath); err != nil {
		return docker.ProjectDeleteResult{}, err
	}
	connection, err := dialSocket(socketPath)
	if err != nil {
		return docker.ProjectDeleteResult{}, err
	}
	defer connection.Close()
	payloadValues := map[string]any{
		"project_id":    request.ProjectID,
		"kind":          string(request.Kind),
		"registry_name": request.RegistryName,
	}
	payload, err := structpb.NewStruct(payloadValues)
	if err != nil {
		return docker.ProjectDeleteResult{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := NewAgentDockerControlServiceClient(connection).DeleteProject(ctx, payload)
	if err != nil {
		return docker.ProjectDeleteResult{}, dockerRPCError(err)
	}
	var result docker.ProjectDeleteResult
	if err := decodeDashboardResponse(response, &result); err != nil {
		return docker.ProjectDeleteResult{}, err
	}
	if result.ProjectID != request.ProjectID || result.Kind != request.Kind || !result.Completed || result.Containers == nil {
		return docker.ProjectDeleteResult{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("project delete result is incomplete"))
	}
	return result, nil
}

func DeleteProject(ctx context.Context, socketPath string, request docker.ProjectDeleteRequest) (docker.ProjectDeleteResult, error) {
	return DeleteDockerProject(ctx, socketPath, request)
}
