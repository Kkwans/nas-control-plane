package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
)

func TestDatabaseBudgetHTTPStableBoundedError(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/databases/query", nil)
	err := &ncpdatabase.DatabaseError{Code: ncpdatabase.CodeResultTooLarge, Cause: errors.New(strings.Repeat("password=private", ncpdatabase.MaxResultBytes/8))}
	(&handler{}).writeDatabaseError(response, request, err)
	body := response.Body.String()
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(body, `"code":"result_too_large"`) {
		t.Fatalf("budget error response=%d %s", response.Code, body)
	}
	if len(body) > 2048 || strings.Contains(body, "private") || strings.Contains(body, "Root Agent 暂不可用") {
		t.Fatalf("budget error not bounded or redacted: length=%d", len(body))
	}
	if !strings.Contains(body, "SQL 可能已经执行") || !strings.Contains(body, "不要直接重试") {
		t.Fatalf("budget error omitted execution uncertainty: %s", body)
	}
}

func TestDatabaseBudgetHTTPQueryDoesNotReplay(t *testing.T) {
	agent := &queryHTTPAgent{err: &ncpdatabase.DatabaseError{Code: ncpdatabase.CodeResultTooLarge, Cause: errors.New("private SQL details")}}
	api := &handler{databaseAgent: agent}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/databases/query", strings.NewReader(`{"sourceId":"fixture","sql":"UPDATE items SET value=value+1 RETURNING value","resultMode":"rows"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.databaseQuery(response, request)
	if response.Code != http.StatusUnprocessableEntity || agent.queryCalls != 1 || agent.request.ResultMode != ncpdatabase.QueryResultModeRows {
		t.Fatalf("query error replayed or changed: status=%d, calls=%d, mode=%q", response.Code, agent.queryCalls, agent.request.ResultMode)
	}
}

func TestDatabaseBudgetHTTPPreservesExactPrefixAndReason(t *testing.T) {
	for _, reason := range []string{"", "row_limit", "byte_limit"} {
		t.Run(reason, func(t *testing.T) {
			want := ncpdatabase.QueryResult{
				Columns: []string{"id", "decimal"}, Rows: [][]any{{"18446744073709551615", "1.234567890123456789002500"}},
				Truncated: reason != "", TruncationReason: reason,
			}
			agent := &queryHTTPAgent{result: want}
			api := &handler{databaseAgent: agent}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/databases/query", strings.NewReader(`{"sourceId":"fixture","sql":"SELECT id, decimal FROM items"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			api.databaseQuery(response, request)
			var got ncpdatabase.QueryResult
			if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || !reflect.DeepEqual(got, want) || agent.queryCalls != 1 {
				t.Fatalf("response status=%d, result=%#v, calls=%d", response.Code, got, agent.queryCalls)
			}
			if reason == "" && strings.Contains(response.Body.String(), "truncationReason") {
				t.Fatal("empty optional truncation reason was included")
			}
		})
	}
}
