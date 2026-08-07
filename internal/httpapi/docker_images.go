package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/docker"
)

type DockerContainerCreateAgentClient interface {
	CreateDockerContainer(context.Context, string, docker.ContainerCreateRequest) (docker.ContainerCreateResult, error)
}

func (socketAgentClient) CreateDockerContainer(ctx context.Context, socketPath string, request docker.ContainerCreateRequest) (docker.ContainerCreateResult, error) {
	return agentsocket.CreateDockerContainer(ctx, socketPath, request)
}

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
	api.jobs.setExpectedTotal(job.ID, input.ExpectedBytes)
	job, _ = api.jobs.get(job.ID)
	go api.runImagePull(job.ID, input)
	writeJSON(response, http.StatusAccepted, job)
}

func (api *handler) runImagePull(jobID string, input docker.ImagePullRequest) {
	requestContext, cancel := context.WithTimeout(context.Background(), defaultDockerImageTimeout)
	api.jobs.setCancel(jobID, cancel)
	defer api.jobs.clearCancel(jobID)
	defer cancel()
	select {
	case api.jobs.pullSlots <- struct{}{}:
		defer func() { <-api.jobs.pullSlots }()
	case <-requestContext.Done():
		api.jobs.update(jobID, "cancelled", "镜像拉取已停止", "", 0)
		return
	}
	api.jobs.update(jobID, "running", "正在连接镜像仓库", "", 1)
	if client, ok := api.dockerImages.(DockerImageProgressAgentClient); ok {
		_, err := client.PullDockerImageWithProgress(requestContext, api.agentSocketPath, input, func(progress docker.ImagePullProgress) {
			api.jobs.updatePullProgress(jobID, jobLayer{
				ID: progress.LayerID, Status: progress.Status, Current: progress.Current, Total: progress.Total,
			})
		})
		if err != nil {
			if errors.Is(requestContext.Err(), context.Canceled) {
				api.jobs.update(jobID, "cancelled", "镜像拉取已停止", "", 0)
				return
			}
			api.jobs.update(jobID, "failed", "镜像拉取失败", err.Error(), 100)
			return
		}
	} else {
		if _, err := api.dockerImages.PullDockerImage(requestContext, api.agentSocketPath, input); err != nil {
			if errors.Is(requestContext.Err(), context.Canceled) {
				api.jobs.update(jobID, "cancelled", "镜像拉取已停止", "", 0)
				return
			}
			api.jobs.update(jobID, "failed", "镜像拉取失败", err.Error(), 100)
			return
		}
	}
	api.jobs.completePull(jobID)
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

// createDockerContainer consumes only the structured image-to-container
// contract. The command field remains an argv vector all the way to Docker;
// NCP never joins it into, or evaluates it as, a host shell command.
func (api *handler) createDockerContainer(response http.ResponseWriter, request *http.Request) {
	var input docker.ContainerCreateRequest
	if !api.decodeControlBody(response, request, &input) {
		return
	}
	if _, err := input.Normalize(); err != nil {
		api.writeError(response, request, http.StatusBadRequest, "DOCKER_CONTAINER_CREATE_INVALID", "容器创建参数无效。")
		return
	}
	client, ok := api.agent.(DockerContainerCreateAgentClient)
	if !ok {
		api.writeError(response, request, http.StatusServiceUnavailable, "AGENT_DOCKER_CONTROL_UNAVAILABLE", "Root Agent 尚未提供 Docker 容器创建能力。")
		return
	}
	requestContext, cancel := context.WithTimeout(request.Context(), defaultDockerImageTimeout)
	defer cancel()
	result, err := client.CreateDockerContainer(requestContext, api.agentSocketPath, input)
	if err != nil {
		code := agentsocket.ErrorCode(err)
		switch code {
		case "DOCKER_CONTAINER_CREATE_INVALID":
			api.writeError(response, request, http.StatusBadRequest, code, "容器创建参数无效。")
		case "DOCKER_CONTAINER_CREATE_FAILED", "DOCKER_CONTAINER_START_FAILED", "DOCKER_CONTAINER_INSPECT_FAILED", "DOCKER_CONTAINER_CREATE_CLEANUP_FAILED":
			api.writeError(response, request, http.StatusConflict, code, "Docker 容器创建或启动失败，请刷新状态后重试。")
		default:
			api.writeError(response, request, http.StatusServiceUnavailable, "AGENT_DOCKER_CONTROL_UNAVAILABLE", "Docker 容器创建暂不可用，请确认 Root Agent 与 Docker Engine 已启动。")
		}
		return
	}
	writeJSON(response, http.StatusCreated, result)
}
