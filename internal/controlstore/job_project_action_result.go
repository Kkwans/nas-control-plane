package controlstore

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

var ErrJobProjectActionResultInvalid = errors.New("JOB_PROJECT_ACTION_RESULT_INVALID")

const maxProjectActionResultBytes = 65536

// ProjectActionJobResult 只保存独立容器操作的有界、已确认结果，不包含请求或运行时详情。
type ProjectActionJobResult struct {
	ProjectID string                 `json:"projectId"`
	Kind      string                 `json:"kind"`
	Action    string                 `json:"action"`
	Items     []ProjectActionJobItem `json:"items"`
}

type ProjectActionJobItem struct {
	ContainerID string `json:"containerId"`
	Outcome     string `json:"outcome"`
	State       string `json:"state"`
	ErrorCode   string `json:"errorCode,omitempty"`
}

func projectActionForJobType(kind string) string {
	switch kind {
	case "docker-project-start":
		return "start"
	case "docker-project-stop":
		return "stop"
	case "docker-project-restart":
		return "restart"
	default:
		return ""
	}
}

// ParseProjectActionJobResult 严格拒绝重复字段、未知字段、错误元数据及未确认的成功结果。
// 成功终态必须携带结果；缺失或损坏的历史结果不能被解释为成功。
func ParseProjectActionJobResult(job JobRecord) (*ProjectActionJobResult, error) {
	raw := job.ProjectActionResultJSON
	action := projectActionForJobType(job.Type)
	if raw == "" {
		if action != "" && job.Status == "completed" {
			return nil, ErrJobProjectActionResultInvalid
		}
		return nil, nil
	}
	if action == "" || job.Reference != "standalone" || len(raw) > maxProjectActionResultBytes || !utf8.ValidString(raw) {
		return nil, ErrJobProjectActionResultInvalid
	}
	if job.Status != "completed" && job.Status != "failed" && job.Status != "interrupted" {
		return nil, ErrJobProjectActionResultInvalid
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	var result ProjectActionJobResult
	seen, err := readProjectActionObject(decoder, func(key string) error {
		switch key {
		case "projectId":
			return readProjectActionString(decoder, &result.ProjectID)
		case "kind":
			return readProjectActionString(decoder, &result.Kind)
		case "action":
			return readProjectActionString(decoder, &result.Action)
		case "items":
			var err error
			result.Items, err = readProjectActionItems(decoder)
			return err
		default:
			return ErrJobProjectActionResultInvalid
		}
	})
	if err != nil || len(seen) != 4 || result.ProjectID != "standalone" || result.Kind != "standalone" || result.Action != action || len(result.Items) == 0 {
		return nil, ErrJobProjectActionResultInvalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, ErrJobProjectActionResultInvalid
	}
	desired, opposite := "running", "stopped"
	if action == "stop" {
		desired, opposite = opposite, desired
	}
	ids := make(map[string]bool, len(result.Items))
	mismatches, unknown := 0, 0
	for _, item := range result.Items {
		if !validProjectActionContainerID(item.ContainerID) || ids[item.ContainerID] {
			return nil, ErrJobProjectActionResultInvalid
		}
		ids[item.ContainerID] = true
		switch item.Outcome {
		case "succeeded":
			if item.State != desired || item.ErrorCode != "" {
				return nil, ErrJobProjectActionResultInvalid
			}
		case "state-mismatch":
			if item.State != opposite || item.ErrorCode != "DOCKER_CONTAINER_STATE_MISMATCH" {
				return nil, ErrJobProjectActionResultInvalid
			}
			mismatches++
		case "unknown":
			if item.State != "unknown" || item.ErrorCode != "JOB_EXECUTION_INTERRUPTED" {
				return nil, ErrJobProjectActionResultInvalid
			}
			unknown++
		default:
			return nil, ErrJobProjectActionResultInvalid
		}
	}
	if job.Status == "completed" && (mismatches != 0 || unknown != 0) ||
		job.Status == "failed" && (mismatches == 0 || unknown != 0) ||
		job.Status == "interrupted" && unknown == 0 {
		return nil, ErrJobProjectActionResultInvalid
	}
	return &result, nil
}

func validProjectActionContainerID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, character := range id {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func readProjectActionString(decoder *json.Decoder, target *string) error {
	token, err := decoder.Token()
	value, ok := token.(string)
	if err != nil || !ok {
		return ErrJobProjectActionResultInvalid
	}
	*target = value
	return nil
}

func readProjectActionObject(decoder *json.Decoder, readField func(string) error) (map[string]bool, error) {
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, ErrJobProjectActionResultInvalid
	}
	seen := make(map[string]bool, 4)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return nil, ErrJobProjectActionResultInvalid
		}
		seen[key] = true
		if err := readField(key); err != nil {
			return nil, ErrJobProjectActionResultInvalid
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') {
		return nil, ErrJobProjectActionResultInvalid
	}
	return seen, nil
}

func readProjectActionItems(decoder *json.Decoder) ([]ProjectActionJobItem, error) {
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return nil, ErrJobProjectActionResultInvalid
	}
	items := make([]ProjectActionJobItem, 0)
	for decoder.More() {
		if len(items) == 128 {
			return nil, ErrJobProjectActionResultInvalid
		}
		var item ProjectActionJobItem
		seen, err := readProjectActionObject(decoder, func(key string) error {
			switch key {
			case "containerId":
				return readProjectActionString(decoder, &item.ContainerID)
			case "outcome":
				return readProjectActionString(decoder, &item.Outcome)
			case "state":
				return readProjectActionString(decoder, &item.State)
			case "errorCode":
				return readProjectActionString(decoder, &item.ErrorCode)
			default:
				return ErrJobProjectActionResultInvalid
			}
		})
		if err != nil || !seen["containerId"] || !seen["outcome"] || !seen["state"] {
			return nil, ErrJobProjectActionResultInvalid
		}
		items = append(items, item)
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim(']') {
		return nil, ErrJobProjectActionResultInvalid
	}
	return items, nil
}
