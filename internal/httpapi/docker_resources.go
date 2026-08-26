package httpapi

import (
	"context"
	"net/http"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/docker"
)

func (socketAgentClient) CollectDockerResources(ctx context.Context, socketPath string) (docker.Resources, error) {
	return agentsocket.ListDockerResources(ctx, socketPath)
}

func (api *handler) dockerResourcesInventory(response http.ResponseWriter, request *http.Request) {
	requestContext, cancel := context.WithTimeout(request.Context(), api.agentTimeout)
	defer cancel()
	result, err := api.dockerResources.CollectDockerResources(requestContext, api.agentSocketPath)
	if err != nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "DOCKER_RESOURCES_UNAVAILABLE", "Docker 网络和卷清单暂不可用，请确认 Root Agent 与 Docker Engine 正常运行。")
		return
	}
	writeJSON(response, http.StatusOK, result)
}
