package agentsocket

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// 用可观测的自定义编码器证明预算检查发生在 json.Marshal 之前。
type databaseBudgetMarshaler struct{ calls *int }

func (m databaseBudgetMarshaler) MarshalJSON() ([]byte, error) {
	*m.calls = *m.calls + 1
	return []byte(`"private provider payload"`), nil
}

type databaseBudgetTextMarshaler struct{ calls *int }

func (m databaseBudgetTextMarshaler) MarshalText() ([]byte, error) {
	*m.calls = *m.calls + 1
	return []byte("private provider payload"), nil
}

func assertDatabaseBudgetStatus(t *testing.T, response *structpb.Struct, err error) {
	t.Helper()
	status := grpcstatus.Convert(err)
	if response != nil || status.Code() != codes.ResourceExhausted || status.Message() != string(ncpdatabase.CodeResultTooLarge) {
		t.Fatalf("response present=%v, status=%v", response != nil, status)
	}
}

func assertDatabaseBudgetClientError(t *testing.T, err error) {
	t.Helper()
	if err == nil || ErrorCode(err) != string(ncpdatabase.CodeResultTooLarge) || err.Error() != string(ncpdatabase.CodeResultTooLarge) {
		t.Fatalf("result budget error=%v, code=%q", err, ErrorCode(err))
	}
	if cause := errors.Unwrap(err); cause == nil || cause.Error() != string(ncpdatabase.CodeResultTooLarge) {
		t.Fatalf("result budget cause=%v", cause)
	}
}

func TestDatabaseBudgetResponseRejectsBeforeMarshal(t *testing.T) {
	for _, name := range []string{"JSON marshaler", "text marshaler"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			var value any = databaseBudgetMarshaler{calls: &calls}
			if name == "text marshaler" {
				value = databaseBudgetTextMarshaler{calls: &calls}
			}
			response, err := databaseResponse(ncpdatabase.QueryResult{
				Columns: []string{"value"}, Rows: [][]any{{value}},
			}, nil)
			assertDatabaseBudgetStatus(t, response, err)
			if calls != 0 {
				t.Fatalf("untrusted marshaler called %d times", calls)
			}
		})
	}
}

func TestDatabaseBudgetResponseRetainsExistingMetadataTypes(t *testing.T) {
	count := int64(1)
	for _, result := range []any{
		ncpdatabase.Discovery{CollectedAt: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC), Sources: []ncpdatabase.Source{{ID: "fixture", Driver: ncpdatabase.DriverSQLite}}},
		ncpdatabase.Catalog{Tables: []ncpdatabase.Table{{Name: "fixture", RowCount: &count, Columns: []ncpdatabase.Column{{Name: "id", Default: "9007199254740993"}}}}},
		ncpdatabase.ConnectionDiagnostic{Connected: true, Driver: ncpdatabase.DriverSQLite},
		ncpdatabase.MutationResult{RowsAffected: 1},
	} {
		if _, err := databaseResponse(result, nil); err != nil {
			t.Fatalf("existing response type %T rejected: %v", result, err)
		}
	}
}

func TestDatabaseBudgetResponseRejectsOversizedProviderValues(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		result any
	}{
		{"single cell", ncpdatabase.QueryResult{Columns: []string{"value"}, Rows: [][]any{{strings.Repeat("x", ncpdatabase.MaxResultBytes)}}}},
		{"escaped cell", ncpdatabase.QueryResult{Columns: []string{"value"}, Rows: [][]any{{strings.Repeat("\x00", ncpdatabase.MaxResultBytes/5)}}}},
		{"binary expansion", ncpdatabase.QueryResult{Columns: []string{"value"}, Rows: [][]any{{make([]byte, ncpdatabase.MaxResultBytes*3/4)}}}},
		{"large column", ncpdatabase.QueryResult{Columns: []string{strings.Repeat("n", ncpdatabase.MaxResultBytes)}}},
		{"many cells", ncpdatabase.QueryResult{Columns: []string{"value"}, Rows: [][]any{make([]any, ncpdatabase.MaxResultBytes/4)}}},
		{"paged table metadata", ncpdatabase.RowsResult{Table: ncpdatabase.Table{Definition: strings.Repeat("s", ncpdatabase.MaxResultBytes)}}},
		{"catalog metadata", ncpdatabase.Catalog{Tables: []ncpdatabase.Table{{Name: strings.Repeat("t", ncpdatabase.MaxResultBytes)}}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response, err := databaseResponse(testCase.result, nil)
			assertDatabaseBudgetStatus(t, response, err)
		})
	}
}

func TestDatabaseBudgetResponseKeepsStableBoundedErrors(t *testing.T) {
	response, err := databaseResponse(nil, &ncpdatabase.DatabaseError{
		Code: ncpdatabase.CodeResultTooLarge, Cause: errors.New(strings.Repeat("password=private;", ncpdatabase.MaxResultBytes/8)),
	})
	assertDatabaseBudgetStatus(t, response, err)
	assertDatabaseBudgetClientError(t, databaseRPCError(err))
	for _, message := range []string{
		string(ncpdatabase.CodeResultTooLarge),
		"grpc: received message larger than max (4194305 vs. 4194304)",
		"private credentials and query text",
	} {
		assertDatabaseBudgetClientError(t, databaseRPCError(grpcstatus.Error(codes.ResourceExhausted, message)))
	}
}

func TestDatabaseBudgetRPCPreservesWholeExactStrings(t *testing.T) {
	for _, reason := range []string{"", "row_limit", "byte_limit"} {
		t.Run(reason, func(t *testing.T) {
			want := ncpdatabase.QueryResult{
				Columns:   []string{"id", "decimal", "payload"},
				Rows:      [][]any{{"18446744073709551615", "1.234567890123456789002500", strings.Repeat("精确\x00<>&", 4096)}},
				Truncated: reason != "", TruncationReason: reason,
			}
			response, err := databaseResponse(want, nil)
			if err != nil {
				t.Fatal(err)
			}
			if size := proto.Size(response); size > ncpdatabase.MaxResultBytes {
				t.Fatalf("successful response wire size=%d", size)
			}
			provider := &optionsQueryProvider{legacyQueryProvider: &legacyQueryProvider{result: want}}
			client := newQueryTestClient(t, newDatabaseService(provider), false)
			got, err := callQueryTestClient(t, client, ncpdatabase.QueryRequest{SQL: "UPDATE fixture SET value=value+1 RETURNING id", ResultMode: ncpdatabase.QueryResultModeRows})
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("round trip changed result, error=%v", err)
			}
			if provider.calls != 0 || provider.optionsCalls != 1 {
				t.Fatalf("provider legacy/options calls=%d/%d", provider.calls, provider.optionsCalls)
			}
		})
	}
}

func TestDatabaseBudgetRPCRejectsProviderBypassWithoutReplay(t *testing.T) {
	for _, mode := range []ncpdatabase.QueryResultMode{"", ncpdatabase.QueryResultModeRows} {
		t.Run(string(mode), func(t *testing.T) {
			provider := &optionsQueryProvider{legacyQueryProvider: &legacyQueryProvider{result: ncpdatabase.QueryResult{
				Columns: []string{"value"}, Rows: [][]any{{strings.Repeat("private row payload", ncpdatabase.MaxResultBytes)}},
			}}}
			client := newQueryTestClient(t, newDatabaseService(provider), false)
			_, err := callQueryTestClient(t, client, ncpdatabase.QueryRequest{SQL: "UPDATE fixture SET value=value+1 RETURNING value", ResultMode: mode})
			assertDatabaseBudgetClientError(t, err)
			if provider.calls+provider.optionsCalls != 1 {
				t.Fatalf("oversized write was replayed: %d/%d", provider.calls, provider.optionsCalls)
			}
		})
	}
}

type databaseBudgetRowsProvider struct {
	DatabaseProvider
	result ncpdatabase.RowsResult
	calls  atomic.Int32
}

func (p *databaseBudgetRowsProvider) Rows(context.Context, ncpdatabase.RowsRequest) (ncpdatabase.RowsResult, error) {
	p.calls.Add(1)
	return p.result, nil
}

func TestDatabaseBudgetRPCKeepsPagedResultsWhole(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		name := "complete page"
		if oversized {
			name = "oversized page"
		}
		t.Run(name, func(t *testing.T) {
			want := ncpdatabase.RowsResult{
				Table: ncpdatabase.Table{Name: "fixture", Columns: []ncpdatabase.Column{{Name: "id", PrimaryKey: true}}},
				Rows:  []ncpdatabase.Row{{"id": "9007199254740993"}, {"id": "18446744073709551615"}},
				Limit: 2, Offset: 4, HasMore: true, Ordering: ncpdatabase.Ordering{Stable: true, Columns: []string{"id"}},
			}
			if oversized {
				want.Rows[1]["payload"] = strings.Repeat("x", ncpdatabase.MaxResultBytes)
			}
			provider := &databaseBudgetRowsProvider{result: want}
			client := newQueryTestClient(t, newDatabaseService(provider), false)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var got ncpdatabase.RowsResult
			err := callDatabaseClient(client, ncpdatabase.RowsRequest{Table: "fixture", Limit: 2, Offset: 4}, &got, func(client AgentDatabaseServiceClient, request *structpb.Struct) (*structpb.Struct, error) {
				return client.Rows(ctx, request)
			})
			if oversized {
				assertDatabaseBudgetClientError(t, err)
				if len(got.Rows) != 0 {
					t.Fatal("oversized page returned an incomplete prefix")
				}
			} else if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("paged result changed: result=%#v, err=%v", got, err)
			}
			if calls := provider.calls.Load(); calls != 1 {
				t.Fatalf("paged result provider called %d times", calls)
			}
		})
	}
}

type databaseBudgetLegacyServer struct {
	AgentDatabaseServiceServer
	response *structpb.Struct
	calls    atomic.Int32
}

func (s *databaseBudgetLegacyServer) Query(context.Context, *structpb.Struct) (*structpb.Struct, error) {
	s.calls.Add(1)
	return s.response, nil
}

func TestDatabaseBudgetClientRejectsLegacyResults(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		text     string
		tooLarge bool
	}{
		{"application wire budget", strings.Repeat("x", ncpdatabase.MaxResultBytes), true},
		{"JSON escaping budget", strings.Repeat("\x00", ncpdatabase.MaxResultBytes/5), false},
		{"default gRPC transport budget", strings.Repeat("x", 4*1024*1024+1), true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			payload, err := structpb.NewStruct(map[string]any{"columns": []any{"value"}, "rows": []any{[]any{testCase.text}}})
			if err != nil {
				t.Fatal(err)
			}
			if (proto.Size(payload) > ncpdatabase.MaxResultBytes) != testCase.tooLarge {
				t.Fatalf("unexpected fixture protobuf size=%d", proto.Size(payload))
			}
			server := &databaseBudgetLegacyServer{response: payload}
			// 此内存传输保持 gRPC 默认限制，不能靠增大 MaxCallRecvMsgSize 隐藏问题。
			client := newQueryTestClient(t, server, true)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var result ncpdatabase.QueryResult
			var rpcErr error
			err = callDatabaseClient(client, ncpdatabase.QueryRequest{SQL: "UPDATE fixture SET value=value+1 RETURNING value"}, &result, func(client AgentDatabaseServiceClient, request *structpb.Struct) (*structpb.Struct, error) {
				response, err := client.Query(ctx, request)
				rpcErr = err
				return response, err
			})
			assertDatabaseBudgetClientError(t, err)
			if testCase.name == "default gRPC transport budget" {
				if grpcstatus.Code(rpcErr) != codes.ResourceExhausted || !strings.Contains(grpcstatus.Convert(rpcErr).Message(), "larger than max") {
					t.Fatalf("default transport size error=%v", rpcErr)
				}
			} else if rpcErr != nil {
				t.Fatalf("fixture should reach application budget defense: %v", rpcErr)
			}
			if calls := server.calls.Load(); calls != 1 {
				t.Fatalf("legacy query called %d times", calls)
			}
		})
	}
}

func TestDatabaseBudgetClientRejectsBeforeDecodingDestination(t *testing.T) {
	payload, err := structpb.NewStruct(map[string]any{"rows": []any{[]any{strings.Repeat("x", 2*ncpdatabase.MaxResultBytes)}}})
	if err != nil {
		t.Fatal(err)
	}
	result := ncpdatabase.QueryResult{Rows: [][]any{{"unchanged"}}}
	calls := 0
	err = callDatabaseClient(nil, ncpdatabase.QueryRequest{}, &result, func(AgentDatabaseServiceClient, *structpb.Struct) (*structpb.Struct, error) {
		calls++
		return payload, nil
	})
	assertDatabaseBudgetClientError(t, err)
	if calls != 1 || !reflect.DeepEqual(result.Rows, [][]any{{"unchanged"}}) {
		t.Fatalf("oversized result changed destination or replayed: calls=%d", calls)
	}
}

func TestDatabaseBudgetClientPreflightsDenseProtoBeforeAsMap(t *testing.T) {
	values := make([]*structpb.Value, 250000)
	for index := range values {
		values[index] = structpb.NewNullValue()
	}
	response := &structpb.Struct{Fields: map[string]*structpb.Value{"rows": structpb.NewListValue(&structpb.ListValue{Values: values})}}
	if proto.Size(response) > ncpdatabase.MaxResultBytes {
		t.Fatal("fixture must pass wire-only guard")
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	err := validateDatabaseProtoBudget(response)
	runtime.ReadMemStats(&after)
	if err == nil {
		t.Fatal("dense protobuf escaped preflight")
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 64<<10 {
		t.Fatalf("preflight allocated %d bytes", allocated)
	}
	var result ncpdatabase.QueryResult
	calls := 0
	err = callDatabaseClient(nil, map[string]any{}, &result, func(AgentDatabaseServiceClient, *structpb.Struct) (*structpb.Struct, error) {
		calls++
		return response, nil
	})
	assertDatabaseBudgetClientError(t, err)
	if calls != 1 || result.Rows != nil {
		t.Fatal("replayed or retained dense response")
	}
}

func TestDatabaseBudgetClientRejectsProtoCyclesBeforeSizeOrAsMap(t *testing.T) {
	response := &structpb.Struct{Fields: make(map[string]*structpb.Value)}
	response.Fields["cycle"] = structpb.NewStructValue(response)
	var result ncpdatabase.QueryResult
	err := callDatabaseClient(nil, map[string]any{}, &result, func(AgentDatabaseServiceClient, *structpb.Struct) (*structpb.Struct, error) { return response, nil })
	assertDatabaseBudgetClientError(t, err)
}
