package httpapi

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	ncpcompose "github.com/Kkwans/nas-control-plane/internal/compose"
	"github.com/Kkwans/nas-control-plane/internal/controlstore"
)

const (
	composeRequestTimeout = 20 * time.Second
	composeDeployTimeout  = 10 * time.Minute
)

func (api *handler) readComposeConfig(response http.ResponseWriter, request *http.Request) {
	var input ncpcompose.ReadRequest
	if !api.decodeControlBody(response, request, &input) {
		return
	}
	requestContext, cancel := context.WithTimeout(request.Context(), composeRequestTimeout)
	defer cancel()
	result, err := api.compose.ReadComposeConfig(requestContext, api.agentSocketPath, input)
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "COMPOSE_CONFIG_READ_FAILED", "Compose 配置读取失败，请确认项目配置文件仍然存在。")
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (api *handler) composeDraft(response http.ResponseWriter, request *http.Request) {
	if api.controlStore == nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "COMPOSE_DRAFT_STORE_UNAVAILABLE", "Compose 草稿存储暂不可用。")
		return
	}
	projectID := strings.TrimSpace(request.URL.Query().Get("projectId"))
	configPath := strings.TrimSpace(request.URL.Query().Get("configPath"))
	draft, err := api.controlStore.ComposeDraft(request.Context(), projectID, configPath)
	if err != nil {
		status := http.StatusInternalServerError
		code, message := "COMPOSE_DRAFT_READ_FAILED", "Compose 草稿读取失败。"
		if err == sql.ErrNoRows {
			status, code, message = http.StatusNotFound, "COMPOSE_DRAFT_NOT_FOUND", "当前配置还没有保存草稿。"
		}
		api.writeError(response, request, status, code, message)
		return
	}
	writeJSON(response, http.StatusOK, draft)
}

func (api *handler) saveComposeDraft(response http.ResponseWriter, request *http.Request) {
	if api.controlStore == nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "COMPOSE_DRAFT_STORE_UNAVAILABLE", "Compose 草稿存储暂不可用。")
		return
	}
	var input controlstore.ComposeDraft
	if !api.decodeControlBody(response, request, &input) {
		return
	}
	draft, err := api.controlStore.SaveComposeDraft(request.Context(), input)
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "COMPOSE_DRAFT_INVALID", "Compose 草稿内容或项目标识无效。")
		return
	}
	writeJSON(response, http.StatusOK, draft)
}

func (api *handler) validateComposeConfig(response http.ResponseWriter, request *http.Request) {
	var input ncpcompose.ValidateRequest
	if !api.decodeControlBody(response, request, &input) {
		return
	}
	requestContext, cancel := context.WithTimeout(request.Context(), composeRequestTimeout)
	defer cancel()
	result, err := api.compose.ValidateComposeConfig(requestContext, api.agentSocketPath, input)
	if err != nil {
		api.writeError(response, request, http.StatusUnprocessableEntity, "COMPOSE_CONFIG_INVALID", "Compose 配置校验失败，请检查 YAML 结构、服务引用和环境变量。")
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (api *handler) deployComposeConfig(response http.ResponseWriter, request *http.Request) {
	var input ncpcompose.DeployRequest
	if !api.decodeControlBody(response, request, &input) {
		return
	}
	job, submitErr := api.jobs.submit("compose-deploy", input.ProjectID, 0, func(ctx context.Context, jobID string) {
		requestContext, cancel := context.WithTimeout(ctx, composeDeployTimeout)
		defer cancel()
		result, err := api.compose.DeployComposeConfig(requestContext, api.agentSocketPath, input)
		if err != nil {
			if requestContext.Err() != nil {
				code := "JOB_EXECUTION_INTERRUPTED"
				if requestContext.Err() == context.DeadlineExceeded {
					code = "JOB_EXECUTION_TIMEOUT"
				}
				api.jobs.interrupt(jobID, code, "本地调用已结束，远端部署结果未确认")
				return
			}
			if jobRPCOutcomeUnknown(err) {
				api.jobs.interrupt(jobID, "JOB_EXECUTION_INTERRUPTED", "连接中断，远端部署结果未确认")
				return
			}
			code, message := composeDeployFailure(agentsocket.ErrorCode(err))
			api.jobs.fail(jobID, code, message, message)
			return
		}
		if !result.Completed {
			api.jobs.interrupt(jobID, "JOB_EXECUTION_INTERRUPTED", "未收到可信完成确认，请核对 Compose 配置和容器状态")
			return
		}
		message := "Compose 项目部署完成并已保存版本"
		if api.controlStore != nil {
			if _, err := api.controlStore.RecordComposeRevision(requestContext, controlstore.ComposeRevision{
				ProjectID: input.ProjectID, ConfigPath: input.TargetPath, Content: input.Content, BackupPath: result.BackupPath,
			}); err != nil {
				message = "Compose 项目部署完成，但版本记录保存失败"
			}
		}
		api.jobs.update(jobID, "completed", message, "", 100)
	})
	if submitErr != nil {
		api.writeJobError(response, request, submitErr)
		return
	}
	writeJSON(response, http.StatusAccepted, job)
}

// 只使用稳定代码生成公开文案，避免命令输出泄露配置，也不把命令成功视为运行健康。
func composeDeployFailure(code string) (string, string) {
	switch code {
	case "COMPOSE_DEPLOY_ROLLBACK_CONFLICT":
		return code, "Compose 部署失败；检测到配置已被外部修改，未覆盖当前配置，已跳过自动恢复；容器状态未确认。"
	case "COMPOSE_DEPLOY_ROLLBACK_UNVERIFIED":
		return code, "Compose 部署失败；无法确认当前配置，已跳过自动恢复；容器状态未确认。"
	case "COMPOSE_DEPLOY_CONFIG_RESTORE_FAILED":
		return code, "Compose 部署失败；旧配置未能写回，未重新应用；容器状态未确认。"
	case "COMPOSE_DEPLOY_RUNTIME_REAPPLY_FAILED":
		return code, "Compose 部署失败；已写回旧配置，但重新应用命令失败；容器状态未确认。"
	case "COMPOSE_DEPLOY_RUNTIME_REAPPLY_SUCCEEDED":
		return code, "Compose 部署失败；已写回旧配置，重新应用命令成功；运行健康未验证。"
	case "COMPOSE_CONFIG_INVALID":
		return code, "Compose 配置校验失败，请检查 YAML 结构、服务引用和环境变量。"
	case "COMPOSE_REQUEST_INVALID", "AGENT_RPC_REQUEST_INVALID":
		return code, "Compose 部署请求无效，请检查项目标识、配置路径和提交内容。"
	default:
		return "COMPOSE_DEPLOY_FAILED", "Compose 部署失败，恢复结果未确认；请核对配置和容器状态。"
	}
}

func (api *handler) composeRevisions(response http.ResponseWriter, request *http.Request) {
	if api.controlStore == nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "COMPOSE_REVISION_STORE_UNAVAILABLE", "Compose 版本记录暂不可用。")
		return
	}
	revisions, err := api.controlStore.ComposeRevisions(request.Context(), request.URL.Query().Get("projectId"), 30)
	if err != nil {
		api.writeError(response, request, http.StatusInternalServerError, "COMPOSE_REVISION_READ_FAILED", "Compose 版本记录读取失败。")
		return
	}
	writeJSON(response, http.StatusOK, revisions)
}
