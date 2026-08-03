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

// DatabaseConnectionResolver is optional so handlers remain source-compatible
// with existing test doubles. The live coordinator resolves encrypted saved
// credentials for catalog, query and mutation requests after a connection has
// been diagnosed successfully.
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
	client, ok := api.databaseAgent.(DatabaseConnectionAgentClient)
	if !ok {
		api.writeError(response, request, http.StatusServiceUnavailable, string(ncpdatabase.CodeAgentUnavailable), "Root Agent 尚未提供数据库连接诊断能力。")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), defaultDatabaseTimeout)
	defer cancel()
	result, err := client.TestDatabaseConnection(ctx, api.agentSocketPath, input)
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
		api.writeDatabaseError(response, request, err)
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
			credentials = *input.Credentials
		}
		result, err = client.TestDatabaseConnection(ctx, api.agentSocketPath, ncpdatabase.Connection{SourceID: source.ID, Credentials: credentials})
	}
	if err != nil {
		api.writeDatabaseError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (api *handler) prepareDatabaseConnection(ctx context.Context, connection ncpdatabase.Connection) (ncpdatabase.Connection, error) {
	resolver, ok := api.databaseConnections.(DatabaseConnectionResolver)
	if !ok {
		return connection, nil
	}
	source, err := api.discoverDatabaseSource(ctx, connection.SourceID)
	if err != nil {
		return ncpdatabase.Connection{}, err
	}
	var manual *ncpdatabase.Credentials
	if hasDatabaseCredentials(connection.Credentials) {
		credentials := connection.Credentials
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
		strings.TrimSpace(credentials.Database) != ""
}
