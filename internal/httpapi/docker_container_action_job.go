package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/go-chi/chi/v5"
)

const containerActionJobPrefix = "docker-container-"

func validContainerActionJobID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func validContainerJobAction(action docker.ContainerAction) bool {
	return action == docker.ContainerActionStart || action == docker.ContainerActionStop || action == docker.ContainerActionRestart
}
func isContainerActionJob(kind string) bool {
	return strings.HasPrefix(kind, containerActionJobPrefix) && validContainerJobAction(docker.ContainerAction(strings.TrimPrefix(kind, containerActionJobPrefix)))
}

func (api *handler) containerActionJob(response http.ResponseWriter, request *http.Request) {
	input := docker.ContainerActionRequest{ContainerID: chi.URLParam(request, "containerID"), Action: docker.ContainerAction(chi.URLParam(request, "action"))}
	// 新入口不接受名字、短 ID、归一化动作或请求体，避免排队目标漂移。
	hasBody, err := requestHasBody(request)
	if !validContainerActionJobID(input.ContainerID) || !validContainerJobAction(input.Action) || err != nil || hasBody {
		api.writeError(response, request, http.StatusBadRequest, "DOCKER_CONTAINER_ACTION_INVALID", "请选择完整容器标识及小写 start、stop 或 restart；请求不接受正文。")
		return
	}
	// 请求值冻结于闭包；执行与 HTTP 取消无关，只使用 engine 的 context。
	job, err := api.jobs.submit(containerActionJobPrefix+string(input.Action), input.ContainerID, 0, func(ctx context.Context, jobID string) {
		api.runContainerAction(ctx, jobID, input)
	})
	if err != nil {
		api.writeJobError(response, request, err)
		return
	}
	writeJSON(response, http.StatusAccepted, job)
}

func (api *handler) runContainerAction(ctx context.Context, jobID string, input docker.ContainerActionRequest) {
	requestContext, cancel := context.WithTimeout(ctx, defaultDockerContainerActionTimeout)
	defer cancel()
	result, err := api.agent.ControlContainer(requestContext, api.agentSocketPath, input)
	if err != nil {
		status, code := containerActionJobFailure(agentsocket.ErrorCode(err), err, requestContext.Err())
		if status == "failed" {
			api.jobs.fail(jobID, code, "容器操作未开始", "派发前校验未通过，请检查控制通道与请求。")
		} else {
			api.jobs.interrupt(jobID, code, "容器操作结果未确认；请只读刷新核对，勿重复提交")
		}
		return
	}
	status, code := containerActionJobOutcome(input, result)
	switch status {
	case "completed":
		api.jobs.update(jobID, status, "本次容器操作已返回且读回状态符合目标；当前状态请刷新核对", "", 100)
	case "failed":
		api.jobs.fail(jobID, code, "容器操作已返回，但读回状态未达到目标", "命令可能已执行，请只读刷新核对，勿重复提交。")
	default:
		api.jobs.interrupt(jobID, code, "容器操作回执无效，结果未确认；请只读刷新核对，勿重复提交")
	}
}

// 仅固定白名单证明派发前拒绝；NOT_FOUND 也可能来自执行后的 inspect。
func containerActionRejectedBeforeMutation(code string) bool {
	switch code {
	case "DOCKER_CONTAINER_ACTION_INVALID", "AGENT_PROTOCOL_MISMATCH", "AGENT_RPC_REQUEST_INVALID":
		return true
	default:
		return false
	}
}

// 不调用宽松 Normalize，回执必须精确归属于冻结的完整身份和动作。
func containerActionJobOutcome(input docker.ContainerActionRequest, result docker.ContainerActionResult) (string, string) {
	if !validContainerActionJobID(input.ContainerID) || !validContainerJobAction(input.Action) || result.ContainerID != input.ContainerID || result.Action != input.Action || strings.TrimSpace(result.Name) == "" || (result.State != "running" && result.State != "stopped") {
		return "interrupted", "DOCKER_CONTAINER_ACTION_RECEIPT_INVALID"
	}
	desired := "running"
	if input.Action == docker.ContainerActionStop {
		desired = "stopped"
	}
	if result.State != desired {
		return "failed", "DOCKER_CONTAINER_STATE_MISMATCH"
	}
	return "completed", ""
}

func containerActionJobFailure(code string, err, contextErr error) (string, string) {
	if errors.Is(err, context.DeadlineExceeded) || contextErr == context.DeadlineExceeded || code == "AGENT_RPC_TIMEOUT" {
		return "interrupted", "JOB_EXECUTION_TIMEOUT"
	}
	if contextErr != nil || errors.Is(err, context.Canceled) || code == "AGENT_RPC_CANCELED" {
		return "interrupted", "JOB_EXECUTION_INTERRUPTED"
	}
	if code == "AGENT_RPC_RESPONSE_INVALID" {
		return "interrupted", "DOCKER_CONTAINER_ACTION_RECEIPT_INVALID"
	}
	if containerActionRejectedBeforeMutation(code) {
		return "failed", code
	}
	return "interrupted", "JOB_EXECUTION_INTERRUPTED"
}
