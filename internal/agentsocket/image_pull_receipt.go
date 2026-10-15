package agentsocket

import (
	"errors"
	"io"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/protobuf/types/known/structpb"
)

func decodeImagePullResponse(response *structpb.Struct, request docker.ImagePullRequest) (docker.ImagePullResult, error) {
	var result docker.ImagePullResult
	if err := decodeDashboardResponse(response, &result); err != nil {
		return docker.ImagePullResult{}, err
	}
	return validateImagePullResponse(request, result)
}

func validateImagePullResponse(request docker.ImagePullRequest, result docker.ImagePullResult) (docker.ImagePullResult, error) {
	if err := docker.ValidateImagePullResult(request, result); err != nil {
		return docker.ImagePullResult{}, coded("AGENT_RPC_RESPONSE_INVALID", err)
	}
	return result, nil
}

type imagePullReceiver interface {
	Recv() (*structpb.Struct, error)
}

func receiveImagePullStream(stream imagePullReceiver, request docker.ImagePullRequest, onProgress func(docker.ImagePullProgress)) (docker.ImagePullResult, error) {
	for {
		message, receiveError := stream.Recv()
		if errors.Is(receiveError, io.EOF) {
			break
		}
		if receiveError != nil {
			return docker.ImagePullResult{}, rpcError(receiveError)
		}
		fields := message.GetFields()
		if fields["type"].GetStringValue() == "completed" {
			result := docker.ImagePullResult{
				Reference: fields["reference"].GetStringValue(),
				Completed: fields["completed"].GetBoolValue(),
			}
			return validateImagePullResponse(request, result)
		}
		if onProgress != nil {
			onProgress(docker.ImagePullProgress{
				LayerID: fields["layerId"].GetStringValue(),
				Status:  fields["status"].GetStringValue(),
				Current: int64(fields["current"].GetNumberValue()),
				Total:   int64(fields["total"].GetNumberValue()),
			})
		}
	}
	return docker.ImagePullResult{}, coded("AGENT_RPC_RESPONSE_INVALID", errors.New("image pull stream ended without completion"))
}
