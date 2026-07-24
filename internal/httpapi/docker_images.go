package httpapi

import (
	"context"
	"net/http"

	"github.com/Kkwans/nas-control-plane/internal/docker"
)

func (api *handler) dockerImageInventory(response http.ResponseWriter, request *http.Request) {
	requestContext, cancel := context.WithTimeout(request.Context(), api.agentTimeout)
	defer cancel()
	result, err := api.dockerImages.ListDockerImages(requestContext, api.agentSocketPath)
	if err != nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "DOCKER_IMAGE_LIST_FAILED", "本地镜像读取失败，请确认 Root Agent 与 Docker Engine 正常运行。")
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (api *handler) pullDockerImage(response http.ResponseWriter, request *http.Request) {
	var input docker.ImagePullRequest
	if !api.decodeControlBody(response, request, &input) {
		return
	}
	requestContext, cancel := context.WithTimeout(request.Context(), defaultDockerImageTimeout)
	defer cancel()
	result, err := api.dockerImages.PullDockerImage(requestContext, api.agentSocketPath, input)
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "DOCKER_IMAGE_PULL_FAILED", "镜像拉取失败，请检查镜像地址、标签和仓库连接。")
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (api *handler) removeDockerImage(response http.ResponseWriter, request *http.Request) {
	var input docker.ImageRemoveRequest
	if !api.decodeControlBody(response, request, &input) {
		return
	}
	requestContext, cancel := context.WithTimeout(request.Context(), defaultDockerImageTimeout)
	defer cancel()
	result, err := api.dockerImages.RemoveDockerImage(requestContext, api.agentSocketPath, input)
	if err != nil {
		api.writeError(response, request, http.StatusConflict, "DOCKER_IMAGE_REMOVE_FAILED", "镜像删除失败；正在被容器使用的镜像不能删除。")
		return
	}
	writeJSON(response, http.StatusOK, result)
}
