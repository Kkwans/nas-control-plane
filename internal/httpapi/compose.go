package httpapi

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

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
	job := api.jobs.create("compose-deploy", input.ProjectID)
	go func() {
		api.jobs.update(job.ID, "running", "正在校验、备份并部署 Compose 项目", "", 15)
		requestContext, cancel := context.WithTimeout(context.Background(), composeDeployTimeout)
		defer cancel()
		result, err := api.compose.DeployComposeConfig(requestContext, api.agentSocketPath, input)
		if err != nil {
			api.jobs.update(job.ID, "failed", "Compose 部署失败，已尝试恢复上一版本", "请查看项目日志并核对 Compose 配置。", 100)
			return
		}
		message := "Compose 项目部署完成并已保存版本"
		if api.controlStore != nil {
			if _, err := api.controlStore.RecordComposeRevision(context.Background(), controlstore.ComposeRevision{
				ProjectID: input.ProjectID, ConfigPath: input.TargetPath, Content: input.Content, BackupPath: result.BackupPath,
			}); err != nil {
				message = "Compose 项目部署完成，但版本记录保存失败"
			}
		}
		api.jobs.update(job.ID, "completed", message, "", 100)
	}()
	writeJSON(response, http.StatusAccepted, job)
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
