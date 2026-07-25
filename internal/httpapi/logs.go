package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/Kkwans/nas-control-plane/internal/journal"
)

type logEntry struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"`
	Unit      string    `json:"unit"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
}

type logResponse struct {
	CollectedAt time.Time  `json:"collectedAt"`
	Entries     []logEntry `json:"entries"`
	NextCursor  string     `json:"nextCursor"`
}

func (api *handler) logs(response http.ResponseWriter, request *http.Request) {
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
		result, err = api.readContainerLogCenter(timeoutContext, limit, request.URL.Query().Get("containerId"))
	default:
		api.writeError(response, request, http.StatusBadRequest, "LOG_SOURCE_INVALID", "日志来源无效。")
		return
	}
	if err != nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "LOG_QUERY_FAILED", "日志读取失败，请确认 Root Agent 和目标服务正在运行。")
		return
	}
	result.Entries = filterLogEntries(result.Entries, request.URL.Query().Get("level"), request.URL.Query().Get("query"))
	writeJSON(response, http.StatusOK, result)
}

func (api *handler) readJournalLogs(ctx context.Context, source string, limit int, request *http.Request) (logResponse, error) {
	query := journal.Query{Limit: limit, Cursor: request.URL.Query().Get("cursor")}
	if source == "agent" {
		query.Unit = "ncp-agent.service"
	}
	if hours, err := strconv.Atoi(request.URL.Query().Get("hours")); err == nil && hours > 0 && hours <= 168 {
		since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
		query.Since = &since
	}
	page, err := api.agent.QueryJournal(ctx, api.agentSocketPath, query)
	if err != nil {
		return logResponse{}, err
	}
	entries := make([]logEntry, 0, len(page.Entries))
	for _, item := range page.Entries {
		entries = append(entries, logEntry{
			ID: item.Cursor, Timestamp: item.Timestamp, Source: source, Unit: firstNonEmpty(item.Unit, item.Identifier),
			Level: journalLevel(item.Priority), Message: item.Message,
		})
	}
	return logResponse{CollectedAt: time.Now().UTC(), Entries: entries, NextCursor: page.NextCursor}, nil
}

func (api *handler) readContainerLogCenter(ctx context.Context, limit int, containerID string) (logResponse, error) {
	result, err := api.agent.ReadContainerLogs(ctx, api.agentSocketPath, docker.ContainerLogsRequest{ContainerID: containerID, Tail: limit})
	if err != nil {
		return logResponse{}, err
	}
	entries := make([]logEntry, 0, len(result.Entries))
	for index, item := range result.Entries {
		level := "info"
		if item.Stream == "stderr" {
			level = "error"
		}
		entries = append(entries, logEntry{
			ID: containerID + "-" + strconv.Itoa(index), Timestamp: result.CollectedAt, Source: "container",
			Unit: containerID, Level: level, Message: item.Message,
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
		if keyword != "" && !strings.Contains(strings.ToLower(entry.Message+" "+entry.Unit), keyword) {
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
