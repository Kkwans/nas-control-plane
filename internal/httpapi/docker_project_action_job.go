package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/go-chi/chi/v5"
)

const projectActionJobPrefix = "docker-project-"
const projectActionPreflightTimeout = 5 * time.Second

func isProjectActionJob(kind string) bool {
	return strings.HasPrefix(kind, projectActionJobPrefix) && validContainerJobAction(docker.ContainerAction(strings.TrimPrefix(kind, projectActionJobPrefix)))
}

// 单字段解码而非宽松 struct 解码：拒绝重复字段、未知字段及尾随 JSON。
func decodeProjectActionIDs(raw []byte) ([]string, bool) {
	if !utf8.Valid(raw) {
		return nil, false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') || !d.More() {
		return nil, false
	}
	key, err := d.Token()
	if err != nil || key != "containerIds" {
		return nil, false
	}
	var ids []string
	if d.Decode(&ids) != nil || d.More() {
		return nil, false
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') {
		return nil, false
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, false
	}
	if len(ids) == 0 || len(ids) > 128 {
		return nil, false
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !validContainerActionJobID(id) || seen[id] {
			return nil, false
		}
		seen[id] = true
	}
	return ids, true
}

func (api *handler) projectActionJob(w http.ResponseWriter, r *http.Request) {
	action := docker.ContainerAction(chi.URLParam(r, "action"))
	if !validContainerJobAction(action) {
		api.writeError(w, r, 400, "DOCKER_PROJECT_ACTION_INVALID", "请选择小写 start、stop 或 restart。")
		return
	}
	if validateJSONContentType(r) != nil {
		api.writeUnsupportedMediaType(w, r)
		return
	}
	raw, err := readBoundedJSONBody(r, 16*1024)
	if isRequestBodyTooLarge(err) {
		api.writeRequestBodyTooLarge(w, r)
		return
	}
	ids, valid := decodeProjectActionIDs(raw)
	if err != nil || !valid {
		api.writeError(w, r, 400, "DOCKER_PROJECT_ACTION_INVALID", "请选择 1 至 128 个不重复的完整容器标识；正文仅接受 containerIds。")
		return
	}
	client, ok := api.agent.(DockerProjectAgentClient)
	if !ok {
		api.writeError(w, r, 503, "AGENT_DOCKER_CONTROL_UNAVAILABLE", "Root Agent 尚未提供 Docker 项目控制能力。")
		return
	}
	input := docker.ProjectActionRequest{ProjectID: "standalone", Kind: docker.ProjectKindStandalone, Action: action, ContainerIDs: append([]string(nil), ids...)}
	job, err := api.submitProjectAction(client, input)
	if err != nil {
		api.writeJobError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (api *handler) submitProjectAction(client DockerProjectAgentClient, input docker.ProjectActionRequest) (jobSnapshot, error) {
	input.ContainerIDs = append([]string(nil), input.ContainerIDs...)
	return api.jobs.submit(projectActionJobPrefix+string(input.Action), "standalone", 0, func(ctx context.Context, id string) { api.runProjectAction(ctx, id, client, input) })
}

// 仅核对冻结子集的唯一归属；不按当前状态重选，不扩展目标，也不承诺原子归属。
func projectActionInventoryMatches(ids []string, inventory docker.Inventory) bool {
	counts := make(map[string]int, len(ids))
	for _, id := range ids {
		counts[id] = 0
	}
	for _, item := range inventory.Containers {
		if _, wanted := counts[item.ID]; wanted {
			if item.ProjectID != "standalone" {
				return false
			}
			counts[item.ID]++
		}
	}
	for _, count := range counts {
		if count != 1 {
			return false
		}
	}
	return len(ids) > 0
}

func (api *handler) runProjectAction(ctx context.Context, id string, client DockerProjectAgentClient, input docker.ProjectActionRequest) {
	preflight, cancel := context.WithTimeout(ctx, projectActionPreflightTimeout)
	inventory, err := api.agent.CollectDockerInventory(preflight, api.agentSocketPath)
	preflightErr := preflight.Err()
	cancel()
	if err != nil || preflightErr != nil || !projectActionInventoryMatches(input.ContainerIDs, inventory) {
		api.jobs.fail(id, "DOCKER_PROJECT_ACTION_PREFLIGHT_FAILED", "项目操作未派发", "无法确认所选容器唯一属于独立容器组，请只读刷新核对。")
		return
	}
	// 保留既有预算与 Agent worker 行为；大集合仍可能超时，不能据此推断未执行。
	mutation, cancel := context.WithTimeout(ctx, defaultDockerContainerActionTimeout)
	defer cancel()
	result, err := client.ControlStandaloneProject(mutation, api.agentSocketPath, input)
	if err != nil || mutation.Err() != nil {
		status, code := projectActionJobFailure(agentsocket.ErrorCode(err), err, mutation.Err())
		if status == "failed" {
			api.jobs.fail(id, code, "项目操作未开始", "派发前校验未通过。")
		} else {
			api.jobs.interrupt(id, code, "项目操作结果未确认，可能已部分执行；请只读刷新核对，勿重复提交")
		}
		return
	}
	safe, status, code := projectActionJobResult(input, result)
	if safe == nil {
		api.jobs.interrupt(id, code, "项目操作回执无效，结果未确认；请只读刷新核对，勿重复提交")
		return
	}
	api.jobs.finishProjectAction(id, status, code, safe)
}

func projectActionJobFailure(code string, err, contextErr error) (string, string) {
	if errors.Is(err, context.DeadlineExceeded) || contextErr == context.DeadlineExceeded || code == "AGENT_RPC_TIMEOUT" {
		return "interrupted", "JOB_EXECUTION_TIMEOUT"
	}
	if contextErr != nil || errors.Is(err, context.Canceled) || code == "AGENT_RPC_CANCELED" {
		return "interrupted", "JOB_EXECUTION_INTERRUPTED"
	}
	if code == "AGENT_RPC_RESPONSE_INVALID" {
		return "interrupted", "DOCKER_PROJECT_ACTION_RECEIPT_INVALID"
	}
	switch code {
	case "DOCKER_PROJECT_ACTION_INVALID", "AGENT_PROTOCOL_MISMATCH", "AGENT_RPC_REQUEST_INVALID":
		return "failed", code
	}
	return "interrupted", "JOB_EXECUTION_INTERRUPTED"
}

// 全回执通过关联、集合、每项与汇总验证后才允许保存任何项。
func projectActionJobResult(input docker.ProjectActionRequest, result docker.ProjectActionResult) (*controlstore.ProjectActionJobResult, string, string) {
	invalid := func() (*controlstore.ProjectActionJobResult, string, string) {
		return nil, "interrupted", "DOCKER_PROJECT_ACTION_RECEIPT_INVALID"
	}
	if input.ProjectID != "standalone" || input.Kind != docker.ProjectKindStandalone || !validContainerJobAction(input.Action) || len(input.ContainerIDs) == 0 || len(input.ContainerIDs) > 128 || result.ProjectID != input.ProjectID || result.Kind != input.Kind || result.Action != input.Action || len(result.Containers) != len(input.ContainerIDs) {
		return invalid()
	}
	desired := "running"
	if input.Action == docker.ContainerActionStop {
		desired = "stopped"
	}
	wanted := make(map[string]bool, len(input.ContainerIDs))
	for _, id := range input.ContainerIDs {
		if !validContainerActionJobID(id) || wanted[id] {
			return invalid()
		}
		wanted[id] = true
	}
	items := make(map[string]controlstore.ProjectActionJobItem, len(wanted))
	allSuccess, unknown, mismatch := true, false, false
	states := map[string]int{}
	for _, item := range result.Containers {
		if !wanted[item.ContainerID] || item.Action != input.Action {
			return invalid()
		}
		if _, exists := items[item.ContainerID]; exists {
			return invalid()
		}
		if item.State != "running" && item.State != "stopped" && item.State != "unknown" {
			return invalid()
		}
		states[item.State]++
		safe := controlstore.ProjectActionJobItem{ContainerID: item.ContainerID, State: item.State}
		if item.Success {
			if strings.TrimSpace(item.Name) == "" || item.State != desired || item.ErrorCode != "" {
				return invalid()
			}
			safe.Outcome = "succeeded"
		} else {
			allSuccess = false
			switch item.ErrorCode {
			case "DOCKER_CONTAINER_STATE_MISMATCH":
				if item.State == desired || item.State == "unknown" || strings.TrimSpace(item.Name) == "" {
					return invalid()
				}
				safe.Outcome = "state-mismatch"
				safe.ErrorCode = item.ErrorCode
				mismatch = true
			case "DOCKER_CONTAINER_ACTION_FAILED", "DOCKER_CONTAINER_INSPECT_FAILED", "DOCKER_CONTAINER_NOT_FOUND":
				safe.Outcome = "unknown"
				safe.State = "unknown"
				safe.ErrorCode = "JOB_EXECUTION_INTERRUPTED"
				unknown = true
			default:
				return invalid()
			}
		}
		items[item.ContainerID] = safe
	}
	summary := "degraded"
	if states["running"] == len(items) {
		summary = "running"
	} else if states["stopped"] == len(items) {
		summary = "stopped"
	}
	if result.Completed != allSuccess || result.State != summary {
		return invalid()
	}
	safe := &controlstore.ProjectActionJobResult{ProjectID: "standalone", Kind: "standalone", Action: string(input.Action), Items: make([]controlstore.ProjectActionJobItem, 0, len(items))}
	for _, id := range input.ContainerIDs {
		safe.Items = append(safe.Items, items[id])
	}
	if unknown {
		return safe, "interrupted", "JOB_EXECUTION_INTERRUPTED"
	}
	if mismatch {
		return safe, "failed", "DOCKER_PROJECT_STATE_MISMATCH"
	}
	return safe, "completed", ""
}

func (r *jobRegistry) finishProjectAction(id, status, code string, result *controlstore.ProjectActionJobResult) {
	e := r.lookup(id)
	if e == nil {
		return
	}
	e.opMu.Lock()
	defer e.opMu.Unlock()
	j := e.snapshot()
	if !isProjectActionJob(j.Type) || j.Status != "running" || e.deleted || e.pending != nil {
		return
	}
	j.Status = status
	j.ErrorCode = code
	j.Error = ""
	j.ProjectActionResult = result
	j.Message = "项目操作已返回且读回状态符合目标；当前状态请只读刷新核对"
	if status == "failed" {
		j.Message = "项目操作已执行，但部分容器读回状态未达到目标；请只读刷新核对，勿重复提交"
	}
	if status == "interrupted" {
		j.Message = "项目操作可能已部分执行，部分结果未确认；请只读刷新核对，勿重复提交"
	}
	if _, err := controlstore.ParseProjectActionJobResult(recordFromSnapshot(j)); err != nil {
		return
	}
	r.finishLocked(e, j)
}
