package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	ncpcompose "github.com/Kkwans/nas-control-plane/internal/compose"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/go-chi/chi/v5"
)

// DockerProjectAgentClient is kept separate from AgentClient so the existing
// public handler contract remains source-compatible until the main Agent/API
// integration adds project routes.
type DockerProjectAgentClient interface {
	ControlStandaloneProject(context.Context, string, docker.ProjectActionRequest) (docker.ProjectActionResult, error)
}

type DockerImageBatchAgentClient interface {
	RemoveDockerImages(context.Context, string, docker.ImageRemoveBatchRequest) (docker.ImageRemoveBatchResult, error)
}

type ComposeProjectAgentClient interface {
	ControlComposeProject(context.Context, string, ncpcompose.LifecycleRequest) (ncpcompose.LifecycleResult, error)
}

func (socketAgentClient) ControlStandaloneProject(ctx context.Context, socketPath string, request docker.ProjectActionRequest) (docker.ProjectActionResult, error) {
	return agentsocket.ControlStandaloneProject(ctx, socketPath, request)
}

func (socketAgentClient) RemoveDockerImages(ctx context.Context, socketPath string, request docker.ImageRemoveBatchRequest) (docker.ImageRemoveBatchResult, error) {
	return agentsocket.RemoveDockerImages(ctx, socketPath, request)
}

func (socketAgentClient) ControlComposeProject(ctx context.Context, socketPath string, request ncpcompose.LifecycleRequest) (ncpcompose.LifecycleResult, error) {
	return agentsocket.ControlComposeProject(ctx, socketPath, request)
}

// controlStandaloneProject is intentionally not registered here. The main
// handler owns the public route table; it can register this method once the
// project action contract is added to OpenAPI.
func (api *handler) controlStandaloneProject(response http.ResponseWriter, request *http.Request) {
	var input docker.ProjectActionRequest
	if !api.decodeControlBody(response, request, &input) {
		return
	}
	if projectID := strings.TrimSpace(chi.URLParam(request, "projectID")); projectID != "" {
		input.ProjectID = projectID
	}
	if actionValue := strings.TrimSpace(chi.URLParam(request, "action")); actionValue != "" {
		action, err := docker.ParseContainerAction(actionValue)
		if err != nil {
			api.writeError(response, request, http.StatusBadRequest, "DOCKER_PROJECT_ACTION_INVALID", "Docker 项目操作参数无效。")
			return
		}
		input.Action = action
	}
	input, err := input.Normalize()
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "DOCKER_PROJECT_ACTION_INVALID", "Docker 独立容器项目参数无效。")
		return
	}
	client, ok := api.agent.(DockerProjectAgentClient)
	if !ok {
		api.writeError(response, request, http.StatusServiceUnavailable, "AGENT_DOCKER_CONTROL_UNAVAILABLE", "Root Agent 尚未提供 Docker 项目控制能力。")
		return
	}
	requestContext, cancel := context.WithTimeout(request.Context(), api.agentTimeout)
	defer cancel()
	result, err := client.ControlStandaloneProject(requestContext, api.agentSocketPath, input)
	if err != nil {
		code := agentsocket.ErrorCode(err)
		switch code {
		case "AGENT_PROTOCOL_MISMATCH":
			api.writeError(response, request, http.StatusConflict, code, "Root Agent 与 NCP Server 版本不一致，请同步更新后重试。")
		case "DOCKER_PROJECT_ACTION_INVALID":
			api.writeError(response, request, http.StatusBadRequest, code, "Docker 独立容器项目参数无效。")
		case "DOCKER_PROJECT_ACTION_FAILED":
			api.writeError(response, request, http.StatusConflict, code, "Docker 项目操作未能完成，请查看逐容器结果并刷新状态。")
		default:
			api.writeError(response, request, http.StatusServiceUnavailable, "AGENT_DOCKER_CONTROL_UNAVAILABLE", "Docker 项目操作暂不可用，请确认 Root Agent 与 Docker Engine 已启动。")
		}
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (api *handler) controlComposeProject(response http.ResponseWriter, request *http.Request) {
	var input ncpcompose.LifecycleRequest
	if !api.decodeControlBody(response, request, &input) {
		return
	}
	if projectID := strings.TrimSpace(chi.URLParam(request, "projectID")); projectID != "" {
		input.ProjectID = projectID
	}
	if actionValue := strings.TrimSpace(chi.URLParam(request, "action")); actionValue != "" {
		action, err := ncpcompose.ParseLifecycleAction(actionValue)
		if err != nil {
			api.writeError(response, request, http.StatusBadRequest, "COMPOSE_LIFECYCLE_INVALID", "Compose 项目操作参数无效。")
			return
		}
		input.Action = action
	}
	input, err := input.Normalize()
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "COMPOSE_LIFECYCLE_INVALID", "Compose 项目操作参数无效。")
		return
	}
	client, ok := api.agent.(ComposeProjectAgentClient)
	if !ok {
		api.writeError(response, request, http.StatusServiceUnavailable, "AGENT_DOCKER_CONTROL_UNAVAILABLE", "Root Agent 尚未提供 Compose 项目控制能力。")
		return
	}
	requestContext, cancel := context.WithTimeout(request.Context(), api.agentTimeout)
	defer cancel()
	result, err := client.ControlComposeProject(requestContext, api.agentSocketPath, input)
	if err != nil {
		code := agentsocket.ErrorCode(err)
		switch code {
		case "COMPOSE_LIFECYCLE_INVALID":
			api.writeError(response, request, http.StatusBadRequest, code, "Compose 项目操作参数无效。")
		case "COMPOSE_LIFECYCLE_FAILED", "COMPOSE_LIFECYCLE_VERIFY_FAILED":
			api.writeError(response, request, http.StatusConflict, code, "Compose 项目操作或状态复核未完成，请刷新状态后重试。")
		default:
			api.writeError(response, request, http.StatusServiceUnavailable, "AGENT_DOCKER_CONTROL_UNAVAILABLE", "Compose 项目操作暂不可用，请确认 Root Agent 与 Docker Engine 已启动。")
		}
		return
	}
	writeJSON(response, http.StatusOK, result)
}

// removeDockerImages is likewise a Docker-specific handler ready for public
// route registration by the main Agent. Individual in-use/removal failures
// are returned in the 200 response as item-level error codes.
func (api *handler) removeDockerImages(response http.ResponseWriter, request *http.Request) {
	var input docker.ImageRemoveBatchRequest
	if !api.decodeControlBody(response, request, &input) {
		return
	}
	input, err := input.Normalize()
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "DOCKER_IMAGE_REMOVE_BATCH_INVALID", "镜像批量删除参数无效，最多只能选择 50 个镜像。")
		return
	}
	client, ok := api.dockerImages.(DockerImageBatchAgentClient)
	if !ok {
		api.writeError(response, request, http.StatusServiceUnavailable, "AGENT_DOCKER_IMAGES_UNAVAILABLE", "Root Agent 尚未提供镜像批量删除能力。")
		return
	}
	requestContext, cancel := context.WithTimeout(request.Context(), defaultDockerImageTimeout)
	defer cancel()
	result, err := client.RemoveDockerImages(requestContext, api.agentSocketPath, input)
	if err != nil {
		code := agentsocket.ErrorCode(err)
		switch code {
		case "DOCKER_IMAGE_REMOVE_BATCH_INVALID":
			api.writeError(response, request, http.StatusBadRequest, code, "镜像批量删除参数无效，最多只能选择 50 个镜像。")
		case "DOCKER_IMAGE_REMOVE_BATCH_LIST_FAILED":
			api.writeError(response, request, http.StatusServiceUnavailable, code, "删除前无法读取 Docker 镜像状态，请稍后重试。")
		default:
			api.writeError(response, request, http.StatusServiceUnavailable, "DOCKER_IMAGE_REMOVE_BATCH_FAILED", "镜像批量删除暂不可用，请确认 Root Agent 与 Docker Engine 已启动。")
		}
		return
	}
	writeJSON(response, http.StatusOK, result)
}
