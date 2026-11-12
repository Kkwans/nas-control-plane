package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	ncpcompose "github.com/Kkwans/nas-control-plane/internal/compose"
	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/go-chi/chi/v5"
)

func isComposeActionJob(kind string) bool {
	return kind == "compose-start" || kind == "compose-stop" || kind == "compose-restart"
}

func boundedComposeText(s string, limit int, empty bool) bool {
	if len(s) > limit || !utf8.ValidString(s) || (!empty && strings.TrimSpace(s) == "") {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

// 新入口只接受路径信息；动作和项目身份只能来自 URL。
func decodeComposeActionJob(raw []byte, projectID, action string) (ncpcompose.LifecycleRequest, bool) {
	var input ncpcompose.LifecycleRequest
	if len(raw) > 65536 || !utf8.Valid(raw) || !boundedComposeText(projectID, 256, false) || !isComposeActionJob("compose-"+action) {
		return input, false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return input, false
	}
	seen := map[string]bool{}
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return input, false
		}
		key, ok := token.(string)
		if !ok || seen[key] || (key != "workingDirectory" && key != "configFiles") {
			return input, false
		}
		seen[key] = true
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return input, false
		}
		if key == "workingDirectory" {
			if json.Unmarshal(value, &input.WorkingDirectory) != nil {
				return input, false
			}
		} else {
			if json.Unmarshal(value, &input.ConfigFiles) != nil {
				return input, false
			}
		}
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') || len(seen) != 2 {
		return input, false
	}
	if _, err = d.Token(); err != io.EOF {
		return input, false
	}
	input.ProjectID = projectID
	input.Action = ncpcompose.LifecycleAction(action)
	if !boundedComposeText(input.WorkingDirectory, 4096, false) || len(input.ConfigFiles) < 1 || len(input.ConfigFiles) > 32 {
		return input, false
	}
	for _, p := range input.ConfigFiles {
		if !boundedComposeText(p, 4096, false) {
			return input, false
		}
	}
	input, err = input.Normalize()
	if err != nil {
		return input, false
	}
	if !boundedComposeText(input.ProjectID, 256, false) || !boundedComposeText(input.WorkingDirectory, 4096, false) {
		return input, false
	}
	for _, p := range input.ConfigFiles {
		if !boundedComposeText(p, 4096, false) {
			return input, false
		}
	}
	return input, true
}

func (api *handler) composeActionJob(w http.ResponseWriter, r *http.Request) {
	if validateJSONContentType(r) != nil {
		api.writeUnsupportedMediaType(w, r)
		return
	}
	raw, err := readBoundedJSONBody(r, 65536)
	if isRequestBodyTooLarge(err) {
		api.writeRequestBodyTooLarge(w, r)
		return
	}
	projectID := chi.URLParam(r, "projectID")
	// chi 优先使用 RawPath；Path 分支已经解码，不能重复解码字面百分号。
	if r.URL.RawPath != "" {
		var decodeErr error
		projectID, decodeErr = url.PathUnescape(projectID)
		if decodeErr != nil {
			api.writeError(w, r, 400, "COMPOSE_LIFECYCLE_INVALID", "Compose 项目标识无效。")
			return
		}
	}
	input, valid := decodeComposeActionJob(raw, projectID, chi.URLParam(r, "action"))
	if err != nil || !valid {
		api.writeError(w, r, 400, "COMPOSE_LIFECYCLE_INVALID", "Compose 项目操作参数无效；正文仅接受 workingDirectory 与 configFiles。")
		return
	}
	client, ok := api.agent.(ComposeProjectAgentClient)
	if !ok {
		api.writeError(w, r, 503, "AGENT_DOCKER_CONTROL_UNAVAILABLE", "Root Agent 尚未提供 Compose 项目控制能力。")
		return
	}
	job, err := api.submitComposeAction(client, input)
	if err != nil {
		api.writeJobError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (api *handler) submitComposeAction(client ComposeProjectAgentClient, input ncpcompose.LifecycleRequest) (jobSnapshot, error) {
	input.ConfigFiles = append([]string(nil), input.ConfigFiles...)
	return api.jobs.submit("compose-"+string(input.Action), input.ProjectID, 0, func(ctx context.Context, id string) { api.runComposeAction(ctx, id, client, input) })
}

func composeActionJobFailure(code string, err, contextErr error) (string, string) {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(contextErr, context.DeadlineExceeded) || code == "AGENT_RPC_TIMEOUT" {
		return "interrupted", "JOB_EXECUTION_TIMEOUT"
	}
	if contextErr != nil || errors.Is(err, context.Canceled) || code == "AGENT_RPC_CANCELED" {
		return "interrupted", "JOB_EXECUTION_INTERRUPTED"
	}
	if code == "AGENT_RPC_RESPONSE_INVALID" {
		return "interrupted", "COMPOSE_LIFECYCLE_RECEIPT_INVALID"
	}
	switch code {
	case "COMPOSE_LIFECYCLE_INVALID", "AGENT_PROTOCOL_MISMATCH", "AGENT_RPC_REQUEST_INVALID":
		return "failed", code
	}
	return "interrupted", "JOB_EXECUTION_INTERRUPTED"
}

// 全回执先核对，随后仅投影聚合证据，不保存服务身份、配置路径或输出。
func composeActionJobResult(input ncpcompose.LifecycleRequest, result ncpcompose.LifecycleResult) *controlstore.ComposeActionJobResult {
	if !isComposeActionJob("compose-"+string(input.Action)) || result.ProjectID != input.ProjectID || result.Action != input.Action || !result.Completed || len(result.Services) < 1 || len(result.Services) > 1024 {
		return nil
	}
	desired := "running"
	if input.Action == ncpcompose.LifecycleActionStop {
		desired = "stopped"
	}
	if result.State != desired {
		return nil
	}
	ids := map[string]bool{}
	for _, s := range result.Services {
		if !boundedComposeText(s.Name, 4096, true) || !boundedComposeText(s.ContainerID, 4096, true) || strings.TrimSpace(s.Name) != s.Name || strings.TrimSpace(s.ContainerID) != s.ContainerID || (s.Name == "" && s.ContainerID == "") {
			return nil
		}
		if s.ContainerID != "" {
			if ids[s.ContainerID] {
				return nil
			}
			ids[s.ContainerID] = true
		}
		if s.Running != (s.State == "running") {
			return nil
		}
		if desired == "running" {
			if s.State != "running" {
				return nil
			}
		} else {
			switch s.State {
			case "stopped", "exited", "created", "dead":
			default:
				return nil
			}
		}
	}
	return &controlstore.ComposeActionJobResult{ProjectID: input.ProjectID, Action: string(input.Action), State: desired, ServiceCount: len(result.Services)}
}

func (api *handler) runComposeAction(ctx context.Context, id string, client ComposeProjectAgentClient, input ncpcompose.LifecycleRequest) {
	mutation, cancel := context.WithTimeout(ctx, defaultComposeLifecycleTimeout)
	defer cancel()
	result, err := client.ControlComposeProject(mutation, api.agentSocketPath, input)
	if err != nil || mutation.Err() != nil {
		status, code := composeActionJobFailure(agentsocket.ErrorCode(err), err, mutation.Err())
		if status == "failed" {
			api.jobs.fail(id, code, "Compose 操作未开始", "派发前校验未通过。")
		} else {
			api.jobs.interrupt(id, code, "Compose 操作结果未确认，可能已执行；请只读刷新核对，勿重复提交")
		}
		return
	}
	safe := composeActionJobResult(input, result)
	if safe == nil {
		api.jobs.interrupt(id, "COMPOSE_LIFECYCLE_RECEIPT_INVALID", "Compose 操作回执无效，结果未确认；请只读刷新核对，勿重复提交")
		return
	}
	api.jobs.finishComposeAction(id, safe)
}

func (r *jobRegistry) finishComposeAction(id string, result *controlstore.ComposeActionJobResult) {
	e := r.lookup(id)
	if e == nil {
		return
	}
	e.opMu.Lock()
	defer e.opMu.Unlock()
	j := e.snapshot()
	if !isComposeActionJob(j.Type) || j.Status != "running" || e.deleted || e.pending != nil {
		return
	}
	j.Status = "completed"
	j.ErrorCode = ""
	j.Error = ""
	j.ComposeActionResult = result
	j.Message = "Compose 命令已返回且读回状态符合目标；当前状态请只读刷新核对"
	if _, err := controlstore.ParseComposeActionJobResult(recordFromSnapshot(j)); err != nil {
		j.Status = "interrupted"
		j.ComposeActionResult = nil
		j.ErrorCode = "COMPOSE_LIFECYCLE_RECEIPT_INVALID"
		j.Message = "Compose 操作回执无效，结果未确认；请只读刷新核对，勿重复提交"
	}
	r.finishLocked(e, j)
}
