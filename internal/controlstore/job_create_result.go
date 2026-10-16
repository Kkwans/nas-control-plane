package controlstore

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
)

var ErrJobCreateResultInvalid = errors.New("JOB_CREATE_RESULT_INVALID")

// ContainerCreateJobResult 只保存已确认完成的创建结果，不包含请求或运行时详情。
type ContainerCreateJobResult struct {
	ContainerID string `json:"containerId"`
	Created     bool   `json:"created"`
	Started     bool   `json:"started"`
}

// ParseContainerCreateJobResult 严格验证三个必填字段，拒绝重复字段与额外 JSON。
// 缺失、损坏或未确认完成的创建结果不能成为可用的成功结果。
func ParseContainerCreateJobResult(job JobRecord) (*ContainerCreateJobResult, error) {
	raw := job.CreateResultJSON
	eligible := job.Type == "docker-container-create" && job.Status == "completed"
	if raw == "" {
		if eligible {
			return nil, ErrJobCreateResultInvalid
		}
		return nil, nil
	}
	if !eligible || len(raw) > 256 {
		return nil, ErrJobCreateResultInvalid
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, ErrJobCreateResultInvalid
	}
	var result ContainerCreateJobResult
	seen := make(map[string]bool, 3)
	for decoder.More() {
		token, err = decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return nil, ErrJobCreateResultInvalid
		}
		seen[key] = true
		switch key {
		case "containerId":
			token, err = decoder.Token()
			result.ContainerID, ok = token.(string)
		case "created":
			token, err = decoder.Token()
			result.Created, ok = token.(bool)
		case "started":
			token, err = decoder.Token()
			result.Started, ok = token.(bool)
		default:
			return nil, ErrJobCreateResultInvalid
		}
		if err != nil || !ok {
			return nil, ErrJobCreateResultInvalid
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') || len(seen) != 3 || !result.Created || len(result.ContainerID) != 64 {
		return nil, ErrJobCreateResultInvalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, ErrJobCreateResultInvalid
	}
	for _, character := range result.ContainerID {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return nil, ErrJobCreateResultInvalid
		}
	}
	return &result, nil
}
