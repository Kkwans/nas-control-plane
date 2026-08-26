package agentsocket

import (
	"context"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/protobuf/types/known/emptypb"
)

func ListDockerResources(ctx context.Context, socketPath string) (docker.Resources, error) {
	connection, err := dialSocket(socketPath)
	if err != nil {
		return docker.Resources{}, err
	}
	defer connection.Close()
	response, err := NewAgentDockerResourcesServiceClient(connection).ListResources(ctx, &emptypb.Empty{})
	if err != nil {
		return docker.Resources{}, rpcError(err)
	}
	var result docker.Resources
	if err := decodeDashboardResponse(response, &result); err != nil {
		return docker.Resources{}, err
	}
	if result.Networks == nil {
		result.Networks = []docker.Network{}
	}
	if result.Volumes == nil {
		result.Volumes = []docker.Volume{}
	}
	return result, nil
}
