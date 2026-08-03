package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
)

const maxDatabaseBodySize = 1024 * 1024

func (api *handler) databaseDiscovery(response http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), defaultDatabaseTimeout)
	defer cancel()
	result, err := api.databaseAgent.DiscoverDatabases(ctx, api.agentSocketPath)
	if err != nil {
		api.writeDatabaseError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (api *handler) databaseCatalog(response http.ResponseWriter, request *http.Request) {
	var input ncpdatabase.CatalogRequest
	if !api.decodeDatabaseBody(response, request, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), defaultDatabaseTimeout)
	defer cancel()
	resolved, err := api.prepareDatabaseConnection(ctx, input.Connection)
	if err != nil {
		api.writeDatabaseError(response, request, err)
		return
	}
	input.Connection = resolved
	result, err := api.databaseAgent.CatalogDatabase(ctx, api.agentSocketPath, input)
	if err != nil {
		api.writeDatabaseError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (api *handler) databaseQuery(response http.ResponseWriter, request *http.Request) {
	var input ncpdatabase.QueryRequest
	if !api.decodeDatabaseBody(response, request, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), defaultDatabaseTimeout)
	defer cancel()
	resolved, err := api.prepareDatabaseConnection(ctx, input.Connection)
	if err != nil {
		api.writeDatabaseError(response, request, err)
		return
	}
	input.Connection = resolved
	result, err := api.databaseAgent.QueryDatabase(ctx, api.agentSocketPath, input)
	if err != nil {
		api.writeDatabaseError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (api *handler) databaseRows(response http.ResponseWriter, request *http.Request) {
	var input ncpdatabase.RowsRequest
	if !api.decodeDatabaseBody(response, request, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), defaultDatabaseTimeout)
	defer cancel()
	resolved, err := api.prepareDatabaseConnection(ctx, input.Connection)
	if err != nil {
		api.writeDatabaseError(response, request, err)
		return
	}
	input.Connection = resolved
	result, err := api.databaseAgent.ReadDatabaseRows(ctx, api.agentSocketPath, input)
	if err != nil {
		api.writeDatabaseError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (api *handler) databaseInsert(response http.ResponseWriter, request *http.Request) {
	var input ncpdatabase.InsertRequest
	if !api.decodeDatabaseBody(response, request, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), defaultDatabaseTimeout)
	defer cancel()
	resolved, err := api.prepareDatabaseConnection(ctx, input.Connection)
	if err != nil {
		api.writeDatabaseError(response, request, err)
		return
	}
	input.Connection = resolved
	result, err := api.databaseAgent.InsertDatabaseRow(ctx, api.agentSocketPath, input)
	if err != nil {
		api.writeDatabaseError(response, request, err)
		return
	}
	writeJSON(response, http.StatusCreated, result)
}

func (api *handler) databaseUpdate(response http.ResponseWriter, request *http.Request) {
	var input ncpdatabase.UpdateRequest
	if !api.decodeDatabaseBody(response, request, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), defaultDatabaseTimeout)
	defer cancel()
	resolved, err := api.prepareDatabaseConnection(ctx, input.Connection)
	if err != nil {
		api.writeDatabaseError(response, request, err)
		return
	}
	input.Connection = resolved
	result, err := api.databaseAgent.UpdateDatabaseRow(ctx, api.agentSocketPath, input)
	if err != nil {
		api.writeDatabaseError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (api *handler) databaseDelete(response http.ResponseWriter, request *http.Request) {
	var input ncpdatabase.DeleteRequest
	if !api.decodeDatabaseBody(response, request, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), defaultDatabaseTimeout)
	defer cancel()
	resolved, err := api.prepareDatabaseConnection(ctx, input.Connection)
	if err != nil {
		api.writeDatabaseError(response, request, err)
		return
	}
	input.Connection = resolved
	result, err := api.databaseAgent.DeleteDatabaseRow(ctx, api.agentSocketPath, input)
	if err != nil {
		api.writeDatabaseError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (api *handler) decodeDatabaseBody(response http.ResponseWriter, request *http.Request, destination any) bool {
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxDatabaseBodySize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		api.writeError(response, request, http.StatusBadRequest, "DATABASE_INPUT_INVALID", "数据库请求参数无效。")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		api.writeError(response, request, http.StatusBadRequest, "DATABASE_INPUT_INVALID", "数据库请求只能包含一个 JSON 对象。")
		return false
	}
	return true
}

func (api *handler) writeDatabaseError(response http.ResponseWriter, request *http.Request, err error) {
	code := ncpdatabase.ErrorCodeOf(err)
	if code == "" {
		code = ncpdatabase.CodeFromString(agentsocket.ErrorCode(err))
	}
	status := http.StatusBadRequest
	message := "数据库操作失败，请检查连接信息、SQL 或数据约束。"
	switch code {
	case ncpdatabase.CodeCredentialsRequired:
		message = "数据库连接需要凭据。"
	case ncpdatabase.CodeAuthFailed:
		status, message = http.StatusUnauthorized, "数据库认证失败。"
	case ncpdatabase.CodeUnreachable:
		status, message = http.StatusServiceUnavailable, "数据库服务不可达。"
	case ncpdatabase.CodeDatabaseNotFound:
		status, message = http.StatusNotFound, "目标数据库不存在。"
	case ncpdatabase.CodePermissionDenied:
		status, message = http.StatusForbidden, "数据库权限不足。"
	case ncpdatabase.CodeSQLInvalid:
		message = "SQL 或数据库请求无效。"
	case ncpdatabase.CodeConstraintFailed:
		status, message = http.StatusConflict, "数据库约束未满足。"
	case ncpdatabase.CodeAgentUnavailable:
		status, message = http.StatusServiceUnavailable, "Root Agent 暂不可用。"
	case ncpdatabase.CodeTimeout:
		status, message = http.StatusGatewayTimeout, "数据库操作超时。"
	case ncpdatabase.CodeCredentialStoreUnavailable, ncpdatabase.CodeCredentialCorrupt,
		ncpdatabase.CodeKeyUnavailable, ncpdatabase.CodeKeyRotationFailed, ncpdatabase.CodeMigrationFailed:
		status, message = http.StatusServiceUnavailable, "数据库凭据服务暂不可用。"
	default:
		code = "DATABASE_OPERATION_FAILED"
	}
	if code == "" {
		code = "DATABASE_OPERATION_FAILED"
	}
	api.writeError(response, request, status, string(code), message)
}
