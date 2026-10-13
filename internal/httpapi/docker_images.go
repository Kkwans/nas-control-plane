package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	ncpcompose "github.com/Kkwans/nas-control-plane/internal/compose"
	"github.com/Kkwans/nas-control-plane/internal/docker"
)

type DockerContainerCreateAgentClient interface {
	CreateDockerContainer(context.Context, string, docker.ContainerCreateRequest) (docker.ContainerCreateResult, error)
}

func (socketAgentClient) CreateDockerContainer(ctx context.Context, socketPath string, request docker.ContainerCreateRequest) (docker.ContainerCreateResult, error) {
	return agentsocket.CreateDockerContainer(ctx, socketPath, request)
}

func (api *handler) dockerImageInventory(response http.ResponseWriter, request *http.Request) {
	requestContext, cancel := context.WithTimeout(request.Context(), defaultDockerImageListTimeout)
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
	job, err := api.submitImagePull(input)
	if err != nil {
		api.writeJobError(response, request, err)
		return
	}
	writeJSON(response, http.StatusAccepted, job)
}

func (api *handler) submitImagePull(input docker.ImagePullRequest) (jobSnapshot, error) {
	return api.jobs.submit("docker-image-pull", input.Reference, input.ExpectedBytes, func(ctx context.Context, id string) { api.runImagePull(ctx, id, input) })
}
func (api *handler) runImagePull(ctx context.Context, jobID string, input docker.ImagePullRequest) {
	requestContext, cancel := context.WithTimeout(ctx, defaultDockerImageTimeout)
	defer cancel()
	var result docker.ImagePullResult
	var err error
	if client, ok := api.dockerImages.(DockerImageProgressAgentClient); ok {
		result, err = client.PullDockerImageWithProgress(requestContext, api.agentSocketPath, input, func(progress docker.ImagePullProgress) {
			api.jobs.updatePullProgress(jobID, jobLayer{ID: progress.LayerID, Status: progress.Status, Current: progress.Current, Total: progress.Total})
		})
	} else {
		result, err = api.dockerImages.PullDockerImage(requestContext, api.agentSocketPath, input)
	}
	if err != nil {
		if requestContext.Err() != nil {
			code := "JOB_EXECUTION_INTERRUPTED"
			if requestContext.Err() == context.DeadlineExceeded {
				code = "JOB_EXECUTION_TIMEOUT"
			}
			api.jobs.interrupt(jobID, code, "本地调用已结束，远端执行结果未确认")
			return
		}
		if jobRPCOutcomeUnknown(err) {
			api.jobs.interrupt(jobID, "JOB_EXECUTION_INTERRUPTED", "连接中断，远端镜像拉取结果未确认")
			return
		}
		api.jobs.update(jobID, "failed", "镜像拉取失败", "请检查镜像引用、仓库连接与 Agent 日志。", 100)
		return
	}
	if !result.Completed || strings.TrimSpace(result.Reference) == "" {
		api.jobs.update(jobID, "interrupted", "未收到可信完成确认，请核对镜像状态", "", 0)
		return
	}
	api.jobs.update(jobID, "completed", "镜像拉取完成", "", 100)
}

// 只有明确的执行失败代码才归档 failed；传输错误不能证明远端操作失败或停止。
func jobRPCOutcomeUnknown(err error) bool {
	code := agentsocket.ErrorCode(err)
	if ncpcompose.IsDeployErrorCode(code) {
		return false
	}
	switch code {
	case "DOCKER_IMAGE_PULL_FAILED", "AGENT_RPC_REQUEST_INVALID":
		return false
	default:
		return true
	}
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
