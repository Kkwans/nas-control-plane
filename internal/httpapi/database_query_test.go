package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
)

type queryHTTPAgent struct {
	DatabaseAgentClient
	discoverCalls int
	queryCalls    int
	request       ncpdatabase.QueryRequest
	result        ncpdatabase.QueryResult
	err           error
}

func (a *queryHTTPAgent) DiscoverDatabases(context.Context, string) (ncpdatabase.Discovery, error) {
	a.discoverCalls++
	return ncpdatabase.Discovery{Sources: []ncpdatabase.Source{{ID: "fixture"}}}, nil
}

func (a *queryHTTPAgent) QueryDatabase(_ context.Context, _ string, request ncpdatabase.QueryRequest) (ncpdatabase.QueryResult, error) {
	a.queryCalls++
	a.request = request
	return a.result, a.err
}

type queryHTTPResolver struct {
	DatabaseConnectionCoordinator
	calls int
}

func (r *queryHTTPResolver) ResolveConnection(_ context.Context, source ncpdatabase.Source, _ *ncpdatabase.Credentials) (ncpdatabase.Connection, error) {
	r.calls++
	return ncpdatabase.Connection{SourceID: source.ID}, nil
}

func TestDatabaseQueryHTTPModes(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		field string
		want  ncpdatabase.QueryResultMode
	}{
		{name: "omitted"}, {name: "empty", field: `,"resultMode":""`}, {name: "null", field: `,"resultMode":null`},
		{name: "auto", field: `,"resultMode":"auto"`, want: ncpdatabase.QueryResultModeAuto},
		{name: "rows", field: `,"resultMode":"rows"`, want: ncpdatabase.QueryResultModeRows},
		{name: "exec", field: `,"resultMode":"exec"`, want: ncpdatabase.QueryResultModeExec},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			for _, known := range []bool{false, true} {
				agent := &queryHTTPAgent{result: ncpdatabase.QueryResult{Columns: []string{}, Rows: [][]any{}, RowsAffectedKnown: known}}
				resolver := &queryHTTPResolver{}
				api := &handler{databaseAgent: agent, databaseConnections: resolver}
				request := httptest.NewRequest(http.MethodPost, "/api/v1/databases/query", strings.NewReader(`{"sourceId":"fixture","sql":"  /* prefix */ UPDATE items SET value=value+1;\n"`+testCase.field+`}`))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				api.databaseQuery(response, request)
				if response.Code != http.StatusOK || agent.queryCalls != 1 || agent.discoverCalls != 1 || resolver.calls != 1 {
					t.Fatalf("response=%d %s, query/discover/resolve=%d/%d/%d", response.Code, response.Body, agent.queryCalls, agent.discoverCalls, resolver.calls)
				}
				if agent.request.ResultMode != testCase.want || agent.request.SQL != "  /* prefix */ UPDATE items SET value=value+1;\n" {
					t.Fatalf("request changed: %#v", agent.request)
				}
				var result map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result["rowsAffectedKnown"] != known || result["rowsAffected"] != float64(0) || result["rows"] == nil || result["columns"] == nil {
					t.Fatalf("response contract changed: %#v", result)
				}
			}
		})
	}
}

func TestDatabaseQueryHTTPRejectsInvalidBeforeConnectionPreparation(t *testing.T) {
	for _, testCase := range []struct{ name, field, code string }{
		{"unknown mode", `,"resultMode":"bogus"`, "sql_invalid"},
		{"uppercase", `,"resultMode":"ROWS"`, "sql_invalid"},
		{"leading space", `,"resultMode":" rows"`, "sql_invalid"},
		{"trailing space", `,"resultMode":"exec "`, "sql_invalid"},
		{"number", `,"resultMode":1`, "DATABASE_INPUT_INVALID"},
		{"boolean", `,"resultMode":false`, "DATABASE_INPUT_INVALID"},
		{"array", `,"resultMode":[]`, "DATABASE_INPUT_INVALID"},
		{"object", `,"resultMode":{}`, "DATABASE_INPUT_INVALID"},
		{"unknown field", `,"unexpected":true`, "DATABASE_INPUT_INVALID"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			agent := &queryHTTPAgent{}
			resolver := &queryHTTPResolver{}
			api := &handler{databaseAgent: agent, databaseConnections: resolver}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/databases/query", strings.NewReader(`{"sourceId":"fixture","sql":"UPDATE items SET value=value+1"`+testCase.field+`}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			api.databaseQuery(response, request)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"`+testCase.code+`"`) {
				t.Fatalf("response=%d %s", response.Code, response.Body)
			}
			if agent.queryCalls != 0 || agent.discoverCalls != 0 || resolver.calls != 0 {
				t.Fatalf("invalid request reached query/discover/resolve=%d/%d/%d", agent.queryCalls, agent.discoverCalls, resolver.calls)
			}
		})
	}
}

func TestDatabaseQueryHTTPUnsupportedDoesNotReplay(t *testing.T) {
	agent := &queryHTTPAgent{err: &ncpdatabase.DatabaseError{Code: ncpdatabase.CodeResultModeUnsupported, Cause: errors.New("private SQL details")}}
	api := &handler{databaseAgent: agent}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/databases/query", strings.NewReader(`{"sourceId":"fixture","sql":"UPDATE items SET value=value+1","resultMode":"rows"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.databaseQuery(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"result_mode_unsupported"`) || !strings.Contains(response.Body.String(), "请先升级 Agent") || strings.Contains(response.Body.String(), "private") {
		t.Fatalf("response=%d %s", response.Code, response.Body)
	}
	if agent.queryCalls != 1 || agent.request.ResultMode != ncpdatabase.QueryResultModeRows {
		t.Fatalf("explicit mode was replayed or changed: calls=%d, request=%#v", agent.queryCalls, agent.request)
	}
}
