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

type mutationReceiptHTTPAgent struct {
	queryHTTPAgent
	calls  int
	result ncpdatabase.MutationResult
	err    error
}

func (a *mutationReceiptHTTPAgent) InsertDatabaseRow(context.Context, string, ncpdatabase.InsertRequest) (ncpdatabase.MutationResult, error) {
	a.calls++
	return a.result, a.err
}
func (a *mutationReceiptHTTPAgent) UpdateDatabaseRow(context.Context, string, ncpdatabase.UpdateRequest) (ncpdatabase.MutationResult, error) {
	a.calls++
	return a.result, a.err
}
func (a *mutationReceiptHTTPAgent) DeleteDatabaseRow(context.Context, string, ncpdatabase.DeleteRequest) (ncpdatabase.MutationResult, error) {
	a.calls++
	return a.result, a.err
}

func TestDatabaseMutationReceiptHTTP(t *testing.T) {
	for _, op := range []struct {
		name, body string
		handle     func(*handler, http.ResponseWriter, *http.Request)
		success    int
	}{
		{"insert", `{"sourceId":"fixture","table":"items","values":{"value":1}}`, (*handler).databaseInsert, http.StatusCreated},
		{"update", `{"sourceId":"fixture","table":"items","values":{"value":1},"keys":{"id":1}}`, (*handler).databaseUpdate, http.StatusOK},
		{"delete", `{"sourceId":"fixture","table":"items","keys":{"id":1}}`, (*handler).databaseDelete, http.StatusOK},
	} {
		t.Run(op.name, func(t *testing.T) {
			for _, invalid := range []bool{false, true} {
				agent := &mutationReceiptHTTPAgent{}
				if invalid {
					agent.err = &ncpdatabase.DatabaseError{Code: ncpdatabase.CodeMutationReceiptInvalid, Cause: errors.New("password=private; SQL=private")}
				}
				resolver := &queryHTTPResolver{}
				api := &handler{databaseAgent: agent, databaseConnections: resolver}
				request := httptest.NewRequest(http.MethodPost, "/api/v1/databases/rows/"+op.name, strings.NewReader(op.body))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				op.handle(api, response, request)
				if agent.calls != 1 || resolver.calls != 1 || agent.discoverCalls != 1 {
					t.Fatalf("dispatch/resolve/discover=%d/%d/%d", agent.calls, resolver.calls, agent.discoverCalls)
				}
				if !invalid {
					var got map[string]any
					if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
						t.Fatal(err)
					}
					if response.Code != op.success || len(got) != 1 || got["rowsAffected"] != float64(0) {
						t.Fatalf("zero success changed: %d %s", response.Code, response.Body)
					}
					continue
				}
				body := response.Body.String()
				if response.Code != http.StatusBadGateway || !strings.Contains(body, `"code":"mutation_receipt_invalid"`) {
					t.Fatalf("invalid receipt=%d %s", response.Code, body)
				}
				if len(body) > 2048 || strings.Contains(body, "private") || strings.Contains(body, "rowsAffected") {
					t.Fatalf("unsafe error: %s", body)
				}
				if !strings.Contains(body, "可能已生效") || !strings.Contains(body, "只读核对") || !strings.Contains(body, "勿直接重试") {
					t.Fatalf("missing uncertainty: %s", body)
				}
			}
		})
	}
}
