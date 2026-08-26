package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/go-chi/chi/v5"
)

func (api *handler) dockerInventory(response http.ResponseWriter, request *http.Request) {
	inventory, ok := api.collectDockerInventory(response, request)
	if !ok {
		return
	}
	writeJSON(response, http.StatusOK, inventory)
}

func (api *handler) services(response http.ResponseWriter, request *http.Request) {
	inventory, ok := api.collectDockerInventory(response, request)
	if !ok {
		return
	}
	writeJSON(response, http.StatusOK, ServiceListResponse{
		CollectedAt: inventory.CollectedAt,
		Services:    inventory.Projects,
	})
}

func (api *handler) containerAction(response http.ResponseWriter, request *http.Request) {
	containerRequest := docker.ContainerActionRequest{
		ContainerID: chi.URLParam(request, "containerID"),
	}
	action, err := docker.ParseContainerAction(chi.URLParam(request, "action"))
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "DOCKER_CONTAINER_ACTION_INVALID", "容器操作参数无效。")
		return
	}
	containerRequest.Action = action
	if err := containerRequest.Validate(); err != nil {
		api.writeError(response, request, http.StatusBadRequest, "DOCKER_CONTAINER_ACTION_INVALID", "容器操作参数无效。")
		return
	}

	requestContext, cancel := context.WithTimeout(request.Context(), defaultDockerContainerActionTimeout)
	defer cancel()
	result, err := api.agent.ControlContainer(requestContext, api.agentSocketPath, containerRequest)
	if err != nil {
		code := agentsocket.ErrorCode(err)
		if errors.Is(err, context.DeadlineExceeded) {
			code = "AGENT_RPC_TIMEOUT"
		}
		switch code {
		case "AGENT_RPC_TIMEOUT":
			api.writeError(response, request, http.StatusGatewayTimeout, code, "容器操作超时，Docker 可能仍在处理；请刷新状态后再重试。")
		case "AGENT_PROTOCOL_MISMATCH":
			api.writeError(response, request, http.StatusConflict, code, "Root Agent 与 NCP Server 版本不一致，请同步更新后重试。")
		case "DOCKER_CONTAINER_NOT_FOUND":
			api.writeError(response, request, http.StatusNotFound, code, "目标容器不存在或已被移除。")
		case "DOCKER_CONTAINER_ACTION_FAILED", "DOCKER_CONTAINER_INSPECT_FAILED":
			api.writeError(response, request, http.StatusConflict, code, "Docker Engine 未能完成容器操作，请刷新状态后重试。")
		default:
			api.writeError(response, request, http.StatusServiceUnavailable, "DOCKER_CONTAINER_ACTION_UNAVAILABLE", "Docker 控制通道暂不可用，请检查 Root Agent 与 Docker Engine 状态。")
		}
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (api *handler) containerLogs(response http.ResponseWriter, request *http.Request) {
	containerRequest := docker.ContainerLogsRequest{
		ContainerID: chi.URLParam(request, "containerID"),
	}
	if rawTail := request.URL.Query().Get("tail"); rawTail != "" {
		tail, err := strconv.Atoi(rawTail)
		if err != nil {
			api.writeError(response, request, http.StatusBadRequest, "DOCKER_LOGS_INPUT_INVALID", "日志条数参数无效。")
			return
		}
		containerRequest.Tail = tail
	}
	normalized, err := containerRequest.Normalize()
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "DOCKER_LOGS_INPUT_INVALID", "日志条数或容器标识无效。")
		return
	}

	requestContext, cancel := context.WithTimeout(request.Context(), api.agentTimeout)
	defer cancel()
	result, err := api.agent.ReadContainerLogs(requestContext, api.agentSocketPath, normalized)
	if err != nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "DOCKER_LOGS_UNAVAILABLE", "容器日志暂不可用，请确认 Root Agent 与 Docker Engine 已启动。")
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (api *handler) collectDockerInventory(response http.ResponseWriter, request *http.Request) (docker.Inventory, bool) {
	inventory, err := api.collectDockerInventoryContext(request.Context())
	if err != nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "DOCKER_INVENTORY_UNAVAILABLE", "Docker 实时清单暂不可用，请确认 Root Agent 与 Docker Engine 已启动。")
		return docker.Inventory{}, false
	}
	return inventory, true
}
