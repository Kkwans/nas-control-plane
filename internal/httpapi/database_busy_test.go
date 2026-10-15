package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
)

func TestDatabaseBusyHTTPStableBoundedError(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/databases/query", nil)
	err := &ncpdatabase.DatabaseError{Code: ncpdatabase.CodeOperationBusy, Operation: "private operation", Endpoint: "private endpoint", Cause: errors.New(strings.Repeat("password=private", 8192))}
	(&handler{}).writeDatabaseError(response, request, err)
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusServiceUnavailable || body.Code != "operation_busy" || body.Message != "数据库操作繁忙，请稍后核对状态再操作。" {
		t.Fatalf("busy response = %d %s", response.Code, response.Body)
	}
	if response.Body.Len() > 2048 || strings.Contains(response.Body.String(), "private") || response.Header().Get("Retry-After") != "" {
		t.Fatal("busy response leaked details or suggested automatic retry")
	}
}

// 来源发现与凭据解析在 Agent 准入之前；繁忙不代表整条 HTTP 请求无副作用。
func TestDatabaseBusyHTTPQueryDoesNotReplayAfterPreparation(t *testing.T) {
	agent := &queryHTTPAgent{err: &ncpdatabase.DatabaseError{Code: ncpdatabase.CodeOperationBusy}}
	resolver := &queryHTTPResolver{}
	api := &handler{databaseAgent: agent, databaseConnections: resolver}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/databases/query", strings.NewReader(`{"sourceId":"fixture","sql":"UPDATE items SET value=value+1 RETURNING value","resultMode":"rows"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.databaseQuery(response, request)
	if response.Code != http.StatusServiceUnavailable || agent.queryCalls != 1 || agent.discoverCalls != 1 || resolver.calls != 1 || agent.request.ResultMode != ncpdatabase.QueryResultModeRows {
		t.Fatalf("busy query changed or replayed: status=%d query=%d discover=%d resolve=%d mode=%q", response.Code, agent.queryCalls, agent.discoverCalls, resolver.calls, agent.request.ResultMode)
	}
}
