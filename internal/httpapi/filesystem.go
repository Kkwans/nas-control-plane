package httpapi

import (
	"context"
	"net/http"
	"strconv"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/filesystem"
	grpcstatus "google.golang.org/grpc/status"
)

func (socketAgentClient) ListPath(ctx context.Context, socketPath string, request filesystem.Request) (filesystem.Page, error) {
	return agentsocket.ListPath(ctx, socketPath, request)
}

func (api *handler) fileEntries(response http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	limit := 0
	if value := query.Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			api.writeError(response, request, http.StatusBadRequest, "FILES_LIMIT_INVALID", "目录分页参数无效。")
			return
		}
		limit = parsed
	}
	input := filesystem.Request{Path: query.Get("path"), Cursor: query.Get("cursor"), Limit: limit}
	if _, err := input.Normalize(); err != nil {
		api.writeError(response, request, http.StatusBadRequest, filesystem.ErrorCode(err), "NAS 路径或分页参数无效。")
		return
	}
	requestContext, cancel := context.WithTimeout(request.Context(), api.agentTimeout)
	defer cancel()
	result, err := api.filesystem.ListPath(requestContext, api.agentSocketPath, input)
	if err != nil {
		code := agentsocket.ErrorCode(err)
		if code == "" {
			code = grpcstatus.Convert(err).Message()
		}
		status := http.StatusServiceUnavailable
		message := "NAS 目录暂不可读取，请确认 Root Agent 正常运行。"
		switch code {
		case "FILES_PATH_INVALID", "FILES_CURSOR_INVALID", "FILES_LIMIT_INVALID":
			status, message = http.StatusBadRequest, "NAS 路径或分页参数无效。"
		case "FILES_PATH_NOT_FOUND":
			status, message = http.StatusNotFound, "NAS 路径不存在。"
		case "FILES_PATH_UNREADABLE", "FILES_DIRECTORY_READ_FAILED":
			status, message = http.StatusConflict, "NAS 目录暂不可读取，请检查路径状态后重试。"
		}
		api.writeError(response, request, status, codeOrFallback(code, "FILES_DIRECTORY_READ_FAILED"), message)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func codeOrFallback(code, fallback string) string {
	if code == "" {
		return fallback
	}
	return code
}
