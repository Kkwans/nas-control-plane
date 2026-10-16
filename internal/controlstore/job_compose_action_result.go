package controlstore

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrJobComposeActionResultInvalid = errors.New("JOB_COMPOSE_ACTION_RESULT_INVALID")

const maxComposeActionResultBytes = 4096

// ComposeActionJobResult 仅保存已确认完成的 Compose 操作摘要，不保存配置或运行时详情。
type ComposeActionJobResult struct {
	ProjectID    string `json:"projectId"`
	Action       string `json:"action"`
	State        string `json:"state"`
	ServiceCount int    `json:"serviceCount"`
}

func composeActionForJobType(kind string) string {
	switch kind {
	case "compose-start":
		return "start"
	case "compose-stop":
		return "stop"
	case "compose-restart":
		return "restart"
	default:
		return ""
	}
}

// ParseComposeActionJobResult 严格验证结果及不可变任务元数据，拒绝未确认成功及混用结果。
func ParseComposeActionJobResult(job JobRecord) (*ComposeActionJobResult, error) {
	raw := job.ComposeActionResultJSON
	action := composeActionForJobType(job.Type)
	eligible := action != "" && job.Status == "completed"
	if raw == "" {
		if eligible {
			return nil, ErrJobComposeActionResultInvalid
		}
		return nil, nil
	}
	if !eligible || len(raw) > maxComposeActionResultBytes || !utf8.ValidString(raw) ||
		job.CreateResultJSON != "" || job.ProjectActionResultJSON != "" {
		return nil, ErrJobComposeActionResultInvalid
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var result ComposeActionJobResult
	seen, err := readProjectActionObject(decoder, func(key string) error {
		switch key {
		case "projectId":
			return readProjectActionString(decoder, &result.ProjectID)
		case "action":
			return readProjectActionString(decoder, &result.Action)
		case "state":
			return readProjectActionString(decoder, &result.State)
		case "serviceCount":
			token, err := decoder.Token()
			number, ok := token.(json.Number)
			if err != nil || !ok {
				return ErrJobComposeActionResultInvalid
			}
			count, err := number.Int64()
			if err != nil || count < 1 || count > 1024 {
				return ErrJobComposeActionResultInvalid
			}
			result.ServiceCount = int(count)
			return nil
		default:
			return ErrJobComposeActionResultInvalid
		}
	})
	if err != nil || len(seen) != 4 || result.Action != action ||
		!validComposeResultProjectID(result.ProjectID) || result.ProjectID != job.Reference {
		return nil, ErrJobComposeActionResultInvalid
	}
	desired := "running"
	if action == "stop" {
		desired = "stopped"
	}
	if result.State != desired {
		return nil, ErrJobComposeActionResultInvalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, ErrJobComposeActionResultInvalid
	}
	return &result, nil
}

func validComposeResultProjectID(id string) bool {
	if id == "" || len(id) > 256 || !utf8.ValidString(id) || strings.TrimSpace(id) != id {
		return false
	}
	for _, char := range id {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}
