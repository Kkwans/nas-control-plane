package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
)

type tlsHTTPAgent struct {
	connectHTTPAgent
	operationCalls int
	connection     ncpdatabase.Connection
	insert         ncpdatabase.InsertRequest
	update         ncpdatabase.UpdateRequest
	delete         ncpdatabase.DeleteRequest
}

func (a *tlsHTTPAgent) CatalogDatabase(_ context.Context, _ string, input ncpdatabase.CatalogRequest) (ncpdatabase.Catalog, error) {
	a.operationCalls++
	a.connection = input.Connection
	return ncpdatabase.Catalog{}, nil
}
func (a *tlsHTTPAgent) QueryDatabase(_ context.Context, _ string, input ncpdatabase.QueryRequest) (ncpdatabase.QueryResult, error) {
	a.operationCalls++
	a.connection = input.Connection
	return ncpdatabase.QueryResult{}, nil
}
func (a *tlsHTTPAgent) ReadDatabaseRows(_ context.Context, _ string, input ncpdatabase.RowsRequest) (ncpdatabase.RowsResult, error) {
	a.operationCalls++
	a.connection = input.Connection
	return ncpdatabase.RowsResult{}, nil
}
func (a *tlsHTTPAgent) InsertDatabaseRow(_ context.Context, _ string, input ncpdatabase.InsertRequest) (ncpdatabase.MutationResult, error) {
	a.operationCalls++
	a.connection, a.insert = input.Connection, input
	return ncpdatabase.MutationResult{}, nil
}
func (a *tlsHTTPAgent) UpdateDatabaseRow(_ context.Context, _ string, input ncpdatabase.UpdateRequest) (ncpdatabase.MutationResult, error) {
	a.operationCalls++
	a.connection, a.update = input.Connection, input
	return ncpdatabase.MutationResult{}, nil
}
func (a *tlsHTTPAgent) DeleteDatabaseRow(_ context.Context, _ string, input ncpdatabase.DeleteRequest) (ncpdatabase.MutationResult, error) {
	a.operationCalls++
	a.connection, a.delete = input.Connection, input
	return ncpdatabase.MutationResult{}, nil
}

type tlsHTTPEndpoint struct {
	path   string
	handle func(*handler, http.ResponseWriter, *http.Request)
	extra  string
}

func tlsHTTPEndpoints() []tlsHTTPEndpoint {
	return []tlsHTTPEndpoint{
		{"connect", (*handler).databaseConnect, ""},
		{"test-connection", (*handler).databaseTestConnection, ""},
		{"catalog", (*handler).databaseCatalog, ""},
		{"query", (*handler).databaseQuery, `,"sql":"SELECT 'credentials: {tls: null}'"`},
		{"rows", (*handler).databaseRows, `,"table":"tls"`},
		{"rows/insert", (*handler).databaseInsert, `,"table":"tls","values":{"tls":null,"credentials":{"tls":true},"id":9007199254740993}`},
		{"rows/update", (*handler).databaseUpdate, `,"table":"tls","values":{"tls":"tls"},"keys":{"tls":null,"credentials":{"tls":true},"id":9007199254740993}`},
		{"rows/delete", (*handler).databaseDelete, `,"table":"tls","keys":{"tls":null,"credentials":{"tls":true},"id":9007199254740993}`},
	}
}

func tlsHTTPRecorder(api *handler, endpoint tlsHTTPEndpoint, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/databases/"+endpoint.path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	endpoint.handle(api, response, request)
	return response
}

// 八个入口共享同一个 bounded raw gate；所有用例只用 Recorder/fake。
func TestDatabaseTLSHTTPRolloutGateAllEndpoints(t *testing.T) {
	for _, endpoint := range tlsHTTPEndpoints() {
		t.Run(endpoint.path, func(t *testing.T) {
			for _, field := range []string{
				`"credentials":{"tls":null}`,
				`"credentials":{"tls":{}}`,
				`"credentials":{"tls":{"mode":"verify-full"}}`,
				`"Credentials":{"TLS":null}`,
				`"cReDeNtIaLs":{"TlS":false}`,
				`"credentialſ":{"tlſ":null}`,
				`"\u0063redentials":{"\u0074\u006c\u0073":null}`,
				`"credentials":{"tls":null},"credentials":{"username":"fixture"}`,
				`"credentials":{"tls":{"mode":"verify-full"}},"credentials":null`,
				`"credentials":null,"CREDENTIALS":{"TLS":null}`,
				`"credentials":{"tls":{"mode":"verify-full"},"tls":null}`,
				`"credentials":{"username":"fixture","TLS":null,"username":"replacement"}`,
			} {
				t.Run(field, func(t *testing.T) {
					agent := &tlsHTTPAgent{}
					coordinator := &connectHTTPCoordinator{}
					api := &handler{databaseAgent: agent, databaseConnections: coordinator}
					response := tlsHTTPRecorder(api, endpoint, `{"sourceId":"fixture",`+field+endpoint.extra+`}`)
					var envelope ErrorResponse
					if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
						t.Fatal(err)
					}
					if response.Code != http.StatusBadRequest || envelope.Code != "DATABASE_INPUT_INVALID" {
						t.Fatalf("gate response = %d %s", response.Code, response.Body)
					}
					if agent.discoverCalls != 0 || agent.testCalls != 0 || agent.operationCalls != 0 || coordinator.calls != 0 {
						t.Fatal("TLS input reached discovery, coordinator or Agent")
					}
				})
			}
		})
	}
}

func TestDatabaseTLSHTTPRolloutGatePreservesDataAndLegacyPaths(t *testing.T) {
	for _, endpoint := range tlsHTTPEndpoints() {
		t.Run(endpoint.path, func(t *testing.T) {
			agent := &tlsHTTPAgent{}
			api := &handler{databaseAgent: agent}
			response := tlsHTTPRecorder(api, endpoint, `{"sourceId":"fixture","credentials":{"username":"{\"credentials\":{\"tls\":null}}","password":"tls"}`+endpoint.extra+`}`)
			wantStatus := http.StatusOK
			if endpoint.path == "rows/insert" {
				wantStatus = http.StatusCreated
			}
			if response.Code != wantStatus {
				t.Fatalf("row data or string rejected: %d %s", response.Code, response.Body)
			}
			var numbers map[string]any
			switch endpoint.path {
			case "rows/insert":
				numbers = agent.insert.Values
			case "rows/update":
				numbers = agent.update.Keys
			case "rows/delete":
				numbers = agent.delete.Keys
			}
			if numbers != nil {
				if number, ok := numbers["id"].(json.Number); !ok || string(number) != "9007199254740993" {
					t.Fatal("raw gate lost exact UseNumber decoding")
				}
			}
			if endpoint.path == "connect" {
				if agent.discoverCalls != 1 || agent.testCalls != 1 || agent.operationCalls != 0 {
					t.Fatal("nil coordinator changed legacy connect route")
				}
			} else if endpoint.path == "test-connection" {
				if agent.discoverCalls != 0 || agent.testCalls != 1 || agent.operationCalls != 0 {
					t.Fatal("nil coordinator changed legacy diagnostic route")
				}
			} else if agent.discoverCalls != 0 || agent.testCalls != 0 || agent.operationCalls != 1 {
				t.Fatal("nil coordinator changed legacy operation route")
			}
		})
	}
}

func TestDatabaseTLSHTTPDecoderPreservesGuards(t *testing.T) {
	valid := `{"sourceId":"fixture"}`
	for _, testCase := range []struct {
		name, body string
		headers    []string
		cancelled  bool
		status     int
	}{
		{"exact limit", valid + strings.Repeat(" ", maxDatabaseBodySize-len(valid)), []string{"application/json; charset=utf-8"}, false, http.StatusOK},
		{"over limit", valid + strings.Repeat(" ", maxDatabaseBodySize-len(valid)+1), []string{"application/json"}, false, http.StatusRequestEntityTooLarge},
		{"missing type", valid, nil, false, http.StatusUnsupportedMediaType},
		{"duplicate type", valid, []string{"application/json", "application/json"}, false, http.StatusUnsupportedMediaType},
		{"wrong type", valid, []string{"text/plain"}, false, http.StatusUnsupportedMediaType},
		{"unknown field", `{"sourceId":"fixture","other":true}`, []string{"application/json"}, false, http.StatusBadRequest},
		{"unknown credential", `{"credentials":{"other":true}}`, []string{"application/json"}, false, http.StatusBadRequest},
		{"trailing object", valid + `{}`, []string{"application/json"}, false, http.StatusBadRequest},
		{"trailing bytes", valid + `invalid`, []string{"application/json"}, false, http.StatusBadRequest},
		{"invalid JSON", `{"credentials":`, []string{"application/json"}, false, http.StatusBadRequest},
		{"cancelled", valid, []string{"application/json"}, true, http.StatusBadRequest},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/databases/query", strings.NewReader(testCase.body))
			for _, contentType := range testCase.headers {
				request.Header.Add("Content-Type", contentType)
			}
			if testCase.cancelled {
				ctx, cancel := context.WithCancel(request.Context())
				cancel()
				request = request.WithContext(ctx)
			}
			response := httptest.NewRecorder()
			var input ncpdatabase.Connection
			ok := (&handler{}).decodeDatabaseBody(response, request, &input)
			if ok != (testCase.status == http.StatusOK) || response.Code != testCase.status {
				t.Fatalf("decode = %t, %d %s", ok, response.Code, response.Body)
			}
		})
	}
}
