package httpapi

import (
	"context"
	"net/http"
	"strings"

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
	input.Reference = strings.TrimSpace(input.Reference)
	if input.Reference == "" {
		api.writeError(response, request, http.StatusBadRequest, "DOCKER_IMAGE_PULL_INVALID", "请输入完整的镜像引用。")
		return
	}
	job := api.jobs.create("docker-image-pull", input.Reference)
	go api.runImagePull(job.ID, input)
	writeJSON(response, http.StatusAccepted, job)
}

func (api *handler) runImagePull(jobID string, input docker.ImagePullRequest) {
	api.jobs.pullSlots <- struct{}{}
	defer func() { <-api.jobs.pullSlots }()
	api.jobs.update(jobID, "running", "正在连接镜像仓库", "", 1)
	requestContext, cancel := context.WithTimeout(context.Background(), defaultDockerImageTimeout)
	defer cancel()
	if client, ok := api.dockerImages.(DockerImageProgressAgentClient); ok {
		_, err := client.PullDockerImageWithProgress(requestContext, api.agentSocketPath, input, func(progress docker.ImagePullProgress) {
			api.jobs.updatePullProgress(jobID, jobLayer{
				ID: progress.LayerID, Status: progress.Status, Current: progress.Current, Total: progress.Total,
			})
		})
		if err != nil {
			api.jobs.update(jobID, "failed", "镜像拉取失败", err.Error(), 100)
			return
		}
	} else {
		requestContext, cancel := context.WithTimeout(context.Background(), defaultDockerImageTimeout)
		defer cancel()
		if _, err := api.dockerImages.PullDockerImage(requestContext, api.agentSocketPath, input); err != nil {
			api.jobs.update(jobID, "failed", "镜像拉取失败", err.Error(), 100)
			return
		}
	}
	api.jobs.update(jobID, "completed", "镜像拉取完成", "", 100)
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
