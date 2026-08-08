package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/Kkwans/nas-control-plane/internal/journal"
	"github.com/Kkwans/nas-control-plane/internal/logformat"
)

type logEntry struct {
	ID         string    `json:"id"`
	Timestamp  time.Time `json:"timestamp"`
	Source     string    `json:"source"`
	Unit       string    `json:"unit"`
	Level      string    `json:"level"`
	Stream     string    `json:"stream,omitempty"`
	Message    string    `json:"message"`
	RawMessage string    `json:"rawMessage,omitempty"`
}

type logResponse struct {
	CollectedAt time.Time  `json:"collectedAt"`
	Entries     []logEntry `json:"entries"`
	NextCursor  string     `json:"nextCursor"`
}

func (api *handler) logs(response http.ResponseWriter, request *http.Request) {
	result, status, err := api.collectLogs(request)
	if err != nil {
		api.writeError(response, request, status, "LOG_QUERY_FAILED", err.Error())
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (api *handler) collectLogs(request *http.Request) (logResponse, int, error) {
	source := request.URL.Query().Get("source")
	if source == "" {
		source = "system"
	}
	limit := boundedQueryInt(request, "limit", 150, 1, 200)
	timeoutContext, cancel := context.WithTimeout(request.Context(), api.agentTimeout)
	defer cancel()

	var result logResponse
	var err error
	switch source {
	case "system", "agent":
		result, err = api.readJournalLogs(timeoutContext, source, limit, request)
	case "container":
		result, err = api.readContainerLogCenter(timeoutContext, limit, request.URL.Query().Get("containerId"), request)
	default:
		return logResponse{}, http.StatusBadRequest, fmt.Errorf("日志来源无效。")
	}
	if err != nil {
		return logResponse{}, http.StatusServiceUnavailable, fmt.Errorf("日志读取失败，请确认 Root Agent 和目标服务正在运行。")
	}
	result.Entries = filterLogEntries(result.Entries, request.URL.Query().Get("level"), request.URL.Query().Get("query"))
	return result, http.StatusOK, nil
}

func (api *handler) logEvents(response http.ResponseWriter, request *http.Request) {
	flusher, ok := response.(http.Flusher)
	if !ok {
		api.writeError(response, request, http.StatusInternalServerError, "LOG_STREAM_UNSUPPORTED", "当前服务不支持日志实时流。")
		return
	}
	response.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	response.Header().Set("Cache-Control", "no-cache, no-transform")
	response.Header().Set("Connection", "keep-alive")
	response.Header().Set("X-Accel-Buffering", "no")
	response.WriteHeader(http.StatusOK)
	interval := realtimeInterval(request)
	_, _ = fmt.Fprintf(response, "retry: %d\n\n", interval.Milliseconds())
	flusher.Flush()

	streamRequest := request.Clone(request.Context())
	streamQuery := streamRequest.URL.Query()
	streamQuery.Set("since", time.Now().UTC().Format(time.RFC3339Nano))
	streamRequest.URL.RawQuery = streamQuery.Encode()
	seen := make(map[string]struct{})
	send := func() bool {
		result, _, err := api.collectLogs(streamRequest)
		if err != nil {
			_, err = fmt.Fprintf(response, "event: unavailable\ndata: {}\n\n")
		} else {
			incremental := make([]logEntry, 0, len(result.Entries))
			var latest time.Time
			for _, entry := range result.Entries {
				if _, exists := seen[entry.ID]; exists {
					continue
				}
				seen[entry.ID] = struct{}{}
				incremental = append(incremental, entry)
				if entry.Timestamp.After(latest) {
					latest = entry.Timestamp
				}
			}
			if len(incremental) == 0 {
				_, err = fmt.Fprint(response, ": heartbeat\n\n")
				flusher.Flush()
				return err == nil
			}
			result.Entries = incremental
			var payload []byte
			payload, err = json.Marshal(result)
			if err == nil {
				_, err = fmt.Fprintf(response, "event: logs\ndata: %s\n\n", payload)
			}
			query := streamRequest.URL.Query()
			if result.NextCursor != "" {
				query.Set("cursor", result.NextCursor)
			}
			if !latest.IsZero() {
				query.Set("since", latest.Add(time.Nanosecond).Format(time.RFC3339Nano))
			}
			streamRequest.URL.RawQuery = query.Encode()
		}
		if err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if !send() {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case <-ticker.C:
			if !send() {
				return
			}
		}
	}
}

func (api *handler) readJournalLogs(ctx context.Context, source string, limit int, request *http.Request) (logResponse, error) {
	query := journal.Query{Limit: limit, Cursor: request.URL.Query().Get("cursor")}
	if source == "agent" {
		query.Unit = "ncp-agent.service"
	}
	if since, err := time.Parse(time.RFC3339Nano, request.URL.Query().Get("since")); err == nil {
		query.Since = &since
	} else if hours, err := strconv.Atoi(request.URL.Query().Get("hours")); err == nil && hours > 0 && hours <= 168 {
		since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
		query.Since = &since
	}
	page, err := api.agent.QueryJournal(ctx, api.agentSocketPath, query)
	if err != nil {
		return logResponse{}, err
	}
	entries := make([]logEntry, 0, len(page.Entries))
	for _, item := range page.Entries {
		normalized := logformat.NormalizeMessage(item.Message)
		entries = append(entries, logEntry{
			ID: item.Cursor, Timestamp: item.Timestamp, Source: source, Unit: firstNonEmpty(item.Unit, item.Identifier),
			Level: journalLevel(item.Priority), Message: normalized.Text, RawMessage: normalized.RawMessage,
		})
	}
	return logResponse{CollectedAt: time.Now().UTC(), Entries: entries, NextCursor: page.NextCursor}, nil
}

func (api *handler) readContainerLogCenter(ctx context.Context, limit int, containerID string, request *http.Request) (logResponse, error) {
	since := request.URL.Query().Get("since")
	if since == "" {
		if hours, err := strconv.Atoi(request.URL.Query().Get("hours")); err == nil && hours > 0 && hours <= 168 {
			since = time.Now().UTC().Add(-time.Duration(hours) * time.Hour).Format(time.RFC3339Nano)
		}
	}
	result, err := api.agent.ReadContainerLogs(ctx, api.agentSocketPath, docker.ContainerLogsRequest{ContainerID: containerID, Tail: limit, Since: since})
	if err != nil {
		return logResponse{}, err
	}
	entries := make([]logEntry, 0, len(result.Entries))
	for _, item := range result.Entries {
		original := item.RawMessage
		if original == "" {
			original = item.Message
		}
		normalized := logformat.NormalizeMessage(original)
		level := docker.ResolveContainerLogLevel(item.Level, original)
		identifier := fmt.Sprintf("%x", sha256.Sum256([]byte(containerID+"\x00"+item.Stream+"\x00"+item.Timestamp.Format(time.RFC3339Nano)+"\x00"+original)))
		entries = append(entries, logEntry{
			ID: identifier[:20], Timestamp: item.Timestamp, Source: "container",
			Unit: containerID, Level: level, Stream: item.Stream, Message: normalized.Text, RawMessage: normalized.RawMessage,
		})
	}
	return logResponse{CollectedAt: result.CollectedAt, Entries: entries, NextCursor: ""}, nil
}

func filterLogEntries(entries []logEntry, level, keyword string) []logEntry {
	level = strings.TrimSpace(strings.ToLower(level))
	keyword = strings.TrimSpace(strings.ToLower(keyword))
	filtered := make([]logEntry, 0, len(entries))
	for _, entry := range entries {
		if level != "" && level != "all" && entry.Level != level {
			continue
		}
		if keyword != "" && !strings.Contains(strings.ToLower(entry.Message+" "+entry.RawMessage+" "+entry.Unit), keyword) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func journalLevel(priority int) string {
	switch {
	case priority <= 3:
		return "error"
	case priority == 4:
		return "warning"
	case priority >= 7:
		return "debug"
	default:
		return "info"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "system"
}
