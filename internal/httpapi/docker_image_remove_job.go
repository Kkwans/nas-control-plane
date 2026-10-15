package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/docker"
)

const imageRemoveJobType = "docker-image-remove"

// 删除任务只接受清单中的完整内容身份，避免排队期间标签被重新指向其他镜像。
func validImageRemoveJobID(id string) bool {
	if len(id) != 71 || !strings.HasPrefix(id, "sha256:") {
		return false
	}
	for _, c := range id[7:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (api *handler) removeDockerImageJob(response http.ResponseWriter, request *http.Request) {
	var input docker.ImageRemoveRequest
	if !api.decodeControlBody(response, request, &input) {
		return
	}
	if !validImageRemoveJobID(input.ImageID) {
		api.writeError(response, request, http.StatusBadRequest, "DOCKER_IMAGE_REMOVE_INVALID", "请选择清单中具有完整 sha256 标识的镜像。")
		return
	}
	client, ok := api.dockerImages.(DockerImageBatchAgentClient)
	if !ok {
		api.writeError(response, request, http.StatusServiceUnavailable, "AGENT_DOCKER_IMAGES_UNAVAILABLE", "Root Agent 尚未提供镜像批量删除能力。")
		return
	}
	// 接收后的任务使用 engine 自有 context，不把 HTTP 断开解释成取消删除。
	id := input.ImageID
	job, err := api.jobs.submit(imageRemoveJobType, id, 0, func(ctx context.Context, jobID string) {
		api.runImageRemove(ctx, jobID, id, client)
	})
	if err != nil {
		api.writeJobError(response, request, err)
		return
	}
	writeJSON(response, http.StatusAccepted, job)
}

func (api *handler) runImageRemove(ctx context.Context, jobID, imageID string, client DockerImageBatchAgentClient) {
	requestContext, cancel := context.WithTimeout(ctx, defaultDockerImageTimeout)
	defer cancel()
	// 保留批量实现的容器引用预检与 Force=false；只创建本次调用拥有的单元素切片。
	result, err := client.RemoveDockerImages(requestContext, api.agentSocketPath, docker.ImageRemoveBatchRequest{ImageIDs: []string{imageID}})
	if err != nil {
		code := agentsocket.ErrorCode(err)
		if requestContext.Err() == nil && imageRemoveRejectedBeforeMutation(code) {
			api.jobs.fail(jobID, code, "镜像删除未开始", "删除前校验或清单读取未通过，请只读刷新后检查。")
			return
		}
		code = "JOB_EXECUTION_INTERRUPTED"
		if requestContext.Err() == context.DeadlineExceeded {
			code = "JOB_EXECUTION_TIMEOUT"
		}
		api.jobs.interrupt(jobID, code, "镜像删除结果未确认，请只读核对镜像与最近任务，勿重复提交")
		return
	}
	status, code := imageRemoveJobOutcome(imageID, result)
	switch status {
	case "completed":
		api.jobs.update(jobID, status, "本次镜像删除调用已确认完成；当前镜像状态请刷新核对", "", 100)
	case "failed":
		api.jobs.fail(jobID, code, "镜像正在被容器引用，未执行删除", "不会强制删除，请先核对容器引用。")
	default:
		api.jobs.interrupt(jobID, code, "镜像删除结果未确认，请只读核对镜像与最近任务，勿重复提交")
	}
}

func imageRemoveRejectedBeforeMutation(code string) bool {
	switch code {
	case "DOCKER_IMAGE_REMOVE_BATCH_INVALID", "DOCKER_IMAGE_REMOVE_BATCH_LIST_FAILED", "AGENT_DOCKER_IMAGES_UNAVAILABLE", "AGENT_RPC_REQUEST_INVALID":
		return true
	default:
		return false
	}
}

// 结果必须完整归属于本次唯一目标；一般 Docker 错误不能证明没有副作用。
func imageRemoveJobOutcome(imageID string, result docker.ImageRemoveBatchResult) (string, string) {
	invalid := func() (string, string) { return "interrupted", "DOCKER_IMAGE_REMOVE_RECEIPT_INVALID" }
	if len(result.Items) != 1 || result.Items[0].ImageID != imageID {
		return invalid()
	}
	item := result.Items[0]
	if item.Removed {
		if item.ErrorCode != "" || result.RemovedCount != 1 || result.FailedCount != 0 || !result.Completed {
			return invalid()
		}
		return "completed", ""
	}
	if result.RemovedCount != 0 || result.FailedCount != 1 || result.Completed || item.ErrorCode == "" {
		return invalid()
	}
	if item.ErrorCode == "DOCKER_IMAGE_IN_USE" {
		return "failed", item.ErrorCode
	}
	return "interrupted", "JOB_EXECUTION_INTERRUPTED"
}
