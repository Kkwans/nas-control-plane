package agentsocket

import (
	"context"
	"errors"
	"strings"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/protobuf/types/known/structpb"
)

func RemoveDockerImages(ctx context.Context, socketPath string, request docker.ImageRemoveBatchRequest) (docker.ImageRemoveBatchResult, error) {
	request, err := normalizeDockerImagesRequest(request)
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
	return decodeDockerImagesRemoveResponse(response, request.ImageIDs)
}

// 先复制请求，避免归一化修改调用方持有的切片（包括校验失败时）。
func normalizeDockerImagesRequest(request docker.ImageRemoveBatchRequest) (docker.ImageRemoveBatchRequest, error) {
	request.ImageIDs = append([]string(nil), request.ImageIDs...)
	return request.Normalize()
}

// 回执必须完整对应本次请求；无效回执不代表删除失败，也不能安全重试。
func decodeDockerImagesRemoveResponse(response *structpb.Struct, requestedIDs []string) (docker.ImageRemoveBatchResult, error) {
	invalid := func() (docker.ImageRemoveBatchResult, error) {
		return docker.ImageRemoveBatchResult{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("image remove batch receipt is invalid"))
	}
	fields := response.GetFields()
	items, ok := fields["items"].GetKind().(*structpb.Value_ListValue)
	if !ok || items.ListValue == nil || len(requestedIDs) == 0 || len(items.ListValue.Values) != len(requestedIDs) {
		return invalid()
	}
	removedCount, removedOK := fields["removedCount"].GetKind().(*structpb.Value_NumberValue)
	failedCount, failedOK := fields["failedCount"].GetKind().(*structpb.Value_NumberValue)
	completed, completedOK := fields["completed"].GetKind().(*structpb.Value_BoolValue)
	if !removedOK || !failedOK || !completedOK {
		return invalid()
	}
	expected := make(map[string]struct{}, len(requestedIDs))
	for _, id := range requestedIDs {
		if _, exists := expected[id]; exists {
			return invalid()
		}
		expected[id] = struct{}{}
	}
	result := docker.ImageRemoveBatchResult{Items: make([]docker.ImageRemoveBatchItem, 0, len(requestedIDs))}
	for _, value := range items.ListValue.Values {
		item := value.GetStructValue().GetFields()
		imageID, idOK := item["imageId"].GetKind().(*structpb.Value_StringValue)
		removed, removedOK := item["removed"].GetKind().(*structpb.Value_BoolValue)
		if !idOK || !removedOK {
			return invalid()
		}
		if _, exists := expected[imageID.StringValue]; !exists {
			return invalid()
		}
		delete(expected, imageID.StringValue)
		errorCode := ""
		// errorCode 可省略，但若提供则必须为字符串，不能用 null 代替。
		if value, exists := item["errorCode"]; exists {
			code, ok := value.GetKind().(*structpb.Value_StringValue)
			if !ok {
				return invalid()
			}
			errorCode = code.StringValue
		}
		if removed.BoolValue {
			if errorCode != "" {
				return invalid()
			}
			result.RemovedCount++
		} else {
			if strings.TrimSpace(errorCode) == "" {
				return invalid()
			}
			result.FailedCount++
		}
		result.Items = append(result.Items, docker.ImageRemoveBatchItem{ImageID: imageID.StringValue, Removed: removed.BoolValue, ErrorCode: errorCode})
	}
	result.Completed = result.FailedCount == 0
	if len(expected) != 0 || removedCount.NumberValue != float64(result.RemovedCount) || failedCount.NumberValue != float64(result.FailedCount) || completed.BoolValue != result.Completed {
		return invalid()
	}
	return result, nil
}

func RemoveDockerImageBatch(ctx context.Context, socketPath string, request docker.ImageRemoveBatchRequest) (docker.ImageRemoveBatchResult, error) {
	return RemoveDockerImages(ctx, socketPath, request)
}
