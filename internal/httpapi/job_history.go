package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
)

const (
	jobHistoryQueryBytes         = 8192
	jobHistoryCursorBytes        = 2048
	jobHistoryCursorDecodedBytes = 1536
	jobHistoryResponseBytes      = 1 << 20
)

var errJobHistoryResponseLarge = errors.New("job history response exceeds budget")

type jobHistoryReader interface {
	JobHistory(context.Context, controlstore.JobHistoryQuery) (controlstore.JobHistoryPage, error)
}

type jobHistoryCursor struct {
	Version       int    `json:"v"`
	Type          string `json:"type"`
	CreatedAtUnix string `json:"createdAtUnix"`
	ID            string `json:"id"`
}

// 游标只描述已授权只读查询的位置，不是权限票据。规范编码限制歧义与长度。
func encodeJobHistoryCursor(kind string, key controlstore.JobHistoryKey) (string, error) {
	if err := controlstore.ValidateJobHistoryQuery(controlstore.JobHistoryQuery{Type: kind, Limit: 1, Before: &key}); err != nil {
		return "", err
	}
	payload := jobHistoryCursor{Version: 1, Type: kind, CreatedAtUnix: strconv.FormatInt(key.CreatedAtUnix, 10), ID: key.ID}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return "", err
	}
	raw := bytes.TrimSuffix(encoded.Bytes(), []byte{'\n'})
	if len(raw) > jobHistoryCursorDecodedBytes {
		return "", controlstore.ErrJobHistoryInvalidQuery
	}
	value := base64.RawURLEncoding.EncodeToString(raw)
	if len(value) > jobHistoryCursorBytes {
		return "", controlstore.ErrJobHistoryInvalidQuery
	}
	return value, nil
}

func decodeJobHistoryCursor(value, kind string) (*controlstore.JobHistoryKey, error) {
	invalid := controlstore.ErrJobHistoryInvalidQuery
	if value == "" || len(value) > jobHistoryCursorBytes {
		return nil, invalid
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || len(raw) > jobHistoryCursorDecodedBytes || !utf8.Valid(raw) {
		return nil, invalid
	}
	var payload jobHistoryCursor
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || payload.Version != 1 || payload.Type != kind {
		return nil, invalid
	}
	seconds, err := strconv.ParseInt(payload.CreatedAtUnix, 10, 64)
	if err != nil || strconv.FormatInt(seconds, 10) != payload.CreatedAtUnix {
		return nil, invalid
	}
	key := controlstore.JobHistoryKey{CreatedAtUnix: seconds, ID: payload.ID}
	canonical, err := encodeJobHistoryCursor(kind, key)
	// 全文规范比对也拒绝重复键、尾随JSON、空白、字段重排与非规范base64。
	if err != nil || canonical != value {
		return nil, invalid
	}
	return &key, nil
}

func parseJobHistoryQuery(raw string) (controlstore.JobHistoryQuery, error) {
	query := controlstore.JobHistoryQuery{Limit: 50}
	invalid := controlstore.ErrJobHistoryInvalidQuery
	if len(raw) > jobHistoryQueryBytes {
		return query, invalid
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return query, invalid
	}
	for key, entries := range values {
		if (key != "type" && key != "limit" && key != "cursor") || len(entries) != 1 {
			return query, invalid
		}
	}
	query.Type = values.Get("type")
	if entries, ok := values["limit"]; ok {
		value := entries[0]
		if len(value) == 0 || len(value) > 3 {
			return query, invalid
		}
		for _, digit := range value {
			if digit < '0' || digit > '9' {
				return query, invalid
			}
		}
		query.Limit, err = strconv.Atoi(value)
		if err != nil {
			return query, invalid
		}
	}
	if entries, ok := values["cursor"]; ok {
		query.Before, err = decodeJobHistoryCursor(entries[0], query.Type)
		if err != nil {
			return query, invalid
		}
	}
	if err := controlstore.ValidateJobHistoryQuery(query); err != nil {
		return query, invalid
	}
	return query, nil
}

func historyKeyBefore(left, right controlstore.JobHistoryKey) bool {
	return left.CreatedAtUnix < right.CreatedAtUnix || (left.CreatedAtUnix == right.CreatedAtUnix && left.ID < right.ID)
}

// 逐条编码并限制缓冲，绝不在验证完整响应前发送200或写入部分JSON。
func encodeJobHistoryPage(query controlstore.JobHistoryQuery, page controlstore.JobHistoryPage) ([]byte, error) {
	corrupt := controlstore.ErrJobHistoryCorrupt
	if len(page.Items) > query.Limit {
		return nil, corrupt
	}
	next := ""
	var previous *controlstore.JobHistoryKey
	for _, entry := range page.Items {
		if err := controlstore.ValidateJobHistoryEntry(entry); err != nil {
			return nil, corrupt
		}
		key := controlstore.JobHistoryKey{CreatedAtUnix: entry.CreatedAtUnix, ID: entry.ID}
		if query.Type != "" && entry.Type != query.Type {
			return nil, corrupt
		}
		if query.Before != nil && !historyKeyBefore(key, *query.Before) {
			return nil, corrupt
		}
		if previous != nil && !historyKeyBefore(key, *previous) {
			return nil, corrupt
		}
		previous = &key
	}
	if page.Next != nil {
		if len(page.Items) != query.Limit || previous == nil || *page.Next != *previous {
			return nil, corrupt
		}
		value, err := encodeJobHistoryCursor(query.Type, *page.Next)
		if err != nil {
			return nil, corrupt
		}
		next = value
	}
	var output bytes.Buffer
	output.WriteString(`{"source":"persisted","items":[`)
	for i, entry := range page.Items {
		encoded, err := json.Marshal(entry)
		if err != nil {
			return nil, corrupt
		}
		needed := len(encoded)
		if i > 0 {
			needed++
		}
		if output.Len()+needed > jobHistoryResponseBytes {
			return nil, errJobHistoryResponseLarge
		}
		if i > 0 {
			output.WriteByte(',')
		}
		output.Write(encoded)
	}
	tail := "]}"
	if next != "" {
		encoded, _ := json.Marshal(next) // 仅由受限base64url编码器产生。
		tail = `],"nextCursor":` + string(encoded) + "}"
	}
	if output.Len()+len(tail) > jobHistoryResponseBytes {
		return nil, errJobHistoryResponseLarge
	}
	output.WriteString(tail)
	return output.Bytes(), nil
}

func (api *handler) listJobHistory(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	query, err := parseJobHistoryQuery(request.URL.RawQuery)
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "JOB_HISTORY_INVALID_QUERY", "任务历史查询参数无效。")
		return
	}
	// 自定义JobEngine可以使用与ControlStore不同的持久层，必须跟随实际执行器。
	var reader jobHistoryReader
	if api.jobs != nil {
		reader, _ = api.jobs.store.(jobHistoryReader)
	}
	if reader == nil {
		api.writeError(response, request, http.StatusNotImplemented, "JOB_HISTORY_UNSUPPORTED", "此服务暂不能提供已保存任务历史。")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), jobStoreTimeout)
	defer cancel()
	page, err := reader.JobHistory(ctx, query)
	if err != nil {
		if errors.Is(err, controlstore.ErrJobHistoryCorrupt) {
			api.writeError(response, request, http.StatusServiceUnavailable, "JOB_HISTORY_CORRUPT", "已保存任务历史含无法读取的数据。")
		} else {
			api.writeError(response, request, http.StatusServiceUnavailable, "JOB_HISTORY_UNAVAILABLE", "已保存任务历史暂时无法读取，请重试。")
		}
		return
	}
	body, err := encodeJobHistoryPage(query, page)
	if err != nil {
		code, message := "JOB_HISTORY_CORRUPT", "已保存任务历史含无法读取的数据。"
		if errors.Is(err, errJobHistoryResponseLarge) {
			code, message = "JOB_HISTORY_TOO_LARGE", "本页任务历史过大，请减少每页条数。"
		}
		api.writeError(response, request, http.StatusServiceUnavailable, code, message)
		return
	}
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(body)
}
