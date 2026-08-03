package agentsocket

import (
	"context"
	"errors"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/protobuf/types/known/structpb"
)

func RemoveDockerImages(ctx context.Context, socketPath string, request docker.ImageRemoveBatchRequest) (docker.ImageRemoveBatchResult, error) {
	request, err := request.Normalize()
	if err != nil {
		return docker.ImageRemoveBatchResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return docker.ImageRemoveBatchResult{}, contextError(err)
	}
	connection, err := dialSocket(socketPath)
	if err != nil {
		return docker.ImageRemoveBatchResult{}, err
	}
	defer connection.Close()

	imageIDs := make([]any, 0, len(request.ImageIDs))
	for _, imageID := range request.ImageIDs {
		imageIDs = append(imageIDs, imageID)
	}
	payload, err := structpb.NewStruct(map[string]any{"image_ids": imageIDs})
	if err != nil {
		return docker.ImageRemoveBatchResult{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := NewAgentDockerImagesServiceClient(connection).RemoveImages(ctx, payload)
	if err != nil {
		return docker.ImageRemoveBatchResult{}, dockerRPCError(err)
	}
	var result docker.ImageRemoveBatchResult
	if err := decodeDashboardResponse(response, &result); err != nil {
		return docker.ImageRemoveBatchResult{}, err
	}
	if result.Items == nil || len(result.Items) != len(request.ImageIDs) || result.RemovedCount < 0 || result.FailedCount < 0 || result.RemovedCount+result.FailedCount != len(result.Items) {
		return docker.ImageRemoveBatchResult{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("image remove batch result is incomplete"))
	}
	return result, nil
}

func RemoveDockerImageBatch(ctx context.Context, socketPath string, request docker.ImageRemoveBatchRequest) (docker.ImageRemoveBatchResult, error) {
	return RemoveDockerImages(ctx, socketPath, request)
}
