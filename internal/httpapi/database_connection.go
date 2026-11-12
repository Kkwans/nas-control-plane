package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
)

// DatabaseConnectionAgentClient is optional to keep existing test doubles and
// embedders source-compatible while the connection diagnostic endpoint rolls out.
type DatabaseConnectionAgentClient interface {
	TestDatabaseConnection(context.Context, string, ncpdatabase.Connection) (ncpdatabase.ConnectionDiagnostic, error)
}

// DatabaseConnectionTester 在不保存凭据的前提下准备并测试策略。
// 已配置协调器时必须实现此扩展，不能绕过 vault 直接调用 Agent。
type DatabaseConnectionTester interface {
	TestConnection(context.Context, ncpdatabase.Source, ncpdatabase.Credentials) (ncpdatabase.ConnectionDiagnostic, error)
}

// DatabaseConnectionResolver 保留可选接口的源码兼容性；已配置协调器
// 却缺少此扩展时必须拒绝请求，不能跳过已保存的策略。
type DatabaseConnectionResolver interface {
	ResolveConnection(context.Context, ncpdatabase.Source, *ncpdatabase.Credentials) (ncpdatabase.Connection, error)
}

type databaseConnectInput struct {
	SourceID    string                   `json:"sourceId"`
	Credentials *ncpdatabase.Credentials `json:"credentials,omitempty"`
}

func (socketAgentClient) TestDatabaseConnection(ctx context.Context, socketPath string, request ncpdatabase.Connection) (ncpdatabase.ConnectionDiagnostic, error) {
	return agentsocket.TestDatabaseConnection(ctx, socketPath, request)
}

func (api *handler) databaseTestConnection(response http.ResponseWriter, request *http.Request) {
	var input ncpdatabase.Connection
	if !api.decodeDatabaseBody(response, request, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), defaultDatabaseTimeout)
	defer cancel()
	var result ncpdatabase.ConnectionDiagnostic
	var err error
	if api.databaseConnections != nil {
		tester, ok := api.databaseConnections.(DatabaseConnectionTester)
		if !ok {
			api.writeDatabaseError(response, request, &ncpdatabase.DatabaseError{Code: ncpdatabase.CodeCredentialStoreUnavailable, Operation: "connection_test"})
			return
		}
		source, discoverErr := api.discoverDatabaseSource(ctx, input.SourceID)
		if discoverErr != nil {
			api.writeDatabaseError(response, request, discoverErr)
			return
		}
		result, err = tester.TestConnection(ctx, source, ncpdatabase.CloneCredentials(input.Credentials))
	} else {
		client, ok := api.databaseAgent.(DatabaseConnectionAgentClient)
		if !ok {
			api.writeError(response, request, http.StatusServiceUnavailable, string(ncpdatabase.CodeAgentUnavailable), "Root Agent 尚未提供数据库连接诊断能力。")
			return
		}
		result, err = client.TestDatabaseConnection(ctx, api.agentSocketPath, input)
	}
	if err != nil {
		api.writeDatabaseError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (api *handler) databaseConnect(response http.ResponseWriter, request *http.Request) {
	var input databaseConnectInput
	if !api.decodeDatabaseBody(response, request, &input) {
		return
	}
	if strings.TrimSpace(input.SourceID) == "" {
		api.writeError(response, request, http.StatusBadRequest, string(ncpdatabase.CodeDatabaseNotFound), "必须指定要连接的数据库。")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), defaultDatabaseTimeout)
	defer cancel()
	discovery, err := api.databaseAgent.DiscoverDatabases(ctx, api.agentSocketPath)
	if err != nil {
		api.writeDatabaseConnectError(response, request, err)
		return
	}
	var source ncpdatabase.Source
	for _, candidate := range discovery.Sources {
		if candidate.ID == input.SourceID {
			source = candidate
			break
		}
	}
	if source.ID == "" {
		api.writeError(response, request, http.StatusNotFound, string(ncpdatabase.CodeDatabaseNotFound), "未找到目标数据库。")
		return
	}

	var result ncpdatabase.ConnectionDiagnostic
	if api.databaseConnections != nil {
		result, err = api.databaseConnections.Connect(ctx, source, input.Credentials)
	} else {
		client, ok := api.databaseAgent.(DatabaseConnectionAgentClient)
		if !ok {
			api.writeError(response, request, http.StatusServiceUnavailable, string(ncpdatabase.CodeAgentUnavailable), "Root Agent 尚未提供数据库连接能力。")
			return
		}
		credentials := ncpdatabase.Credentials{}
		if input.Credentials != nil {
			credentials = ncpdatabase.CloneCredentials(*input.Credentials)
		}
		result, err = client.TestDatabaseConnection(ctx, api.agentSocketPath, ncpdatabase.Connection{SourceID: source.ID, Credentials: credentials})
	}
	if err != nil {
		api.writeDatabaseConnectError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

// /connect 的冲突仅表示本次尝试未保存凭据，不代表测试已成功；
// Agent 测试阶段的同名错误也会在保存前中止，其他请求仍可能已保存新凭据。
func (api *handler) writeDatabaseConnectError(response http.ResponseWriter, request *http.Request, err error) {
	if databaseErrorCode(err) == ncpdatabase.CodeCredentialConflict {
		api.writeError(response, request, http.StatusConflict, string(ncpdatabase.CodeCredentialConflict),
			"凭据已被其他请求更改，本次凭据未保存，请重新确认后手动重试。")
		return
	}
	api.writeDatabaseError(response, request, err)
}

func (api *handler) prepareDatabaseConnection(ctx context.Context, connection ncpdatabase.Connection) (ncpdatabase.Connection, error) {
	if api.databaseConnections == nil {
		return connection, nil
	}
	resolver, ok := api.databaseConnections.(DatabaseConnectionResolver)
	if !ok {
		return ncpdatabase.Connection{}, &ncpdatabase.DatabaseError{Code: ncpdatabase.CodeCredentialStoreUnavailable, Operation: "connection_resolve"}
	}
	source, err := api.discoverDatabaseSource(ctx, connection.SourceID)
	if err != nil {
		return ncpdatabase.Connection{}, err
	}
	var manual *ncpdatabase.Credentials
	if hasDatabaseCredentials(connection.Credentials) {
		credentials := ncpdatabase.CloneCredentials(connection.Credentials)
		manual = &credentials
	}
	return resolver.ResolveConnection(ctx, source, manual)
}

func (api *handler) discoverDatabaseSource(ctx context.Context, sourceID string) (ncpdatabase.Source, error) {
	discovery, err := api.databaseAgent.DiscoverDatabases(ctx, api.agentSocketPath)
	if err != nil {
		return ncpdatabase.Source{}, err
	}
	for _, source := range discovery.Sources {
		if source.ID == sourceID {
			return source, nil
		}
	}
	return ncpdatabase.Source{}, &ncpdatabase.DatabaseError{Code: ncpdatabase.CodeDatabaseNotFound, Operation: "source_discovery"}
}

func hasDatabaseCredentials(credentials ncpdatabase.Credentials) bool {
	return strings.TrimSpace(credentials.Username) != "" ||
		strings.TrimSpace(credentials.Password) != "" ||
		strings.TrimSpace(credentials.Token) != "" ||
		strings.TrimSpace(credentials.Database) != "" ||
		credentials.TLS != nil
}
