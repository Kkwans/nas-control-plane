package agentsocket

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"
)

// 只覆盖数据库 provider 和内存 gRPC，不打开宿主机套接字或真实数据库。
type legacyQueryProvider struct {
	DatabaseProvider
	calls   int
	request ncpdatabase.QueryRequest
	result  ncpdatabase.QueryResult
}

func (p *legacyQueryProvider) Query(_ context.Context, request ncpdatabase.QueryRequest) (ncpdatabase.QueryResult, error) {
	p.calls++
	p.request = request
	return p.result, nil
}

type optionsQueryProvider struct {
	*legacyQueryProvider
	optionsCalls int
	err          error
}

func (p *optionsQueryProvider) QueryWithOptions(_ context.Context, request ncpdatabase.QueryRequest) (ncpdatabase.QueryResult, error) {
	p.optionsCalls++
	p.request = request
	return p.result, p.err
}

type legacyQueryServer struct {
	AgentDatabaseServiceServer
	calls int
}

func (s *legacyQueryServer) Query(context.Context, *structpb.Struct) (*structpb.Struct, error) {
	s.calls++
	// 模拟旧 Agent 的真实响应：没有 rowsAffectedKnown 属性。
	return structpb.NewStruct(map[string]any{
		"columns": []any{}, "rows": []any{}, "rowsAffected": 7,
		"truncated": false, "durationMs": 1,
	})
}

func newQueryTestClient(t *testing.T, implementation AgentDatabaseServiceServer, legacyDescription bool) AgentDatabaseServiceClient {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	if legacyDescription {
		description := agentDatabaseServiceDescription
		description.Methods = nil
		for _, method := range agentDatabaseServiceDescription.Methods {
			if method.MethodName != "QueryWithOptions" {
				description.Methods = append(description.Methods, method)
			}
		}
		server.RegisterService(&description, implementation)
	} else {
		RegisterAgentDatabaseServiceServer(server, implementation)
	}
	go func() { _ = server.Serve(listener) }()
	connection, err := grpc.NewClient("passthrough:///database-query-test",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
	)
	if err != nil {
		server.Stop()
		_ = listener.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close(); server.Stop(); _ = listener.Close() })
	return NewAgentDatabaseServiceClient(connection)
}

func callQueryTestClient(t *testing.T, client AgentDatabaseServiceClient, request ncpdatabase.QueryRequest) (ncpdatabase.QueryResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var result ncpdatabase.QueryResult
	err := callDatabaseClient(client, request, &result, func(client AgentDatabaseServiceClient, payload *structpb.Struct) (*structpb.Struct, error) {
		return queryDatabaseRPC(ctx, client, payload, request.ResultMode)
	})
	return result, err
}

func TestDatabaseQueryRPCModesAndStructRoundTrip(t *testing.T) {
	for _, mode := range []ncpdatabase.QueryResultMode{"", ncpdatabase.QueryResultModeAuto, ncpdatabase.QueryResultModeRows, ncpdatabase.QueryResultModeExec} {
		t.Run(string(mode), func(t *testing.T) {
			for _, known := range []bool{false, true} {
				provider := &optionsQueryProvider{legacyQueryProvider: &legacyQueryProvider{result: ncpdatabase.QueryResult{
					Columns: []string{"id"}, Rows: [][]any{{"9007199254740993"}}, RowsAffectedKnown: known,
				}}}
				client := newQueryTestClient(t, newDatabaseService(provider), false)
				request := ncpdatabase.QueryRequest{
					Connection: ncpdatabase.Connection{SourceID: "fixture"},
					SQL:        " \n/* prefix */ UPDATE items SET value=value+1 RETURNING id; \n", ResultMode: mode,
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				var result ncpdatabase.QueryResult
				err := callDatabaseClient(client, request, &result, func(client AgentDatabaseServiceClient, payload *structpb.Struct) (*structpb.Struct, error) {
					_, present := payload.Fields["resultMode"]
					if present != (mode != "") {
						t.Errorf("mode presence = %v for %q", present, mode)
					}
					response, err := queryDatabaseRPC(ctx, client, payload, mode)
					if err == nil {
						value, present := response.AsMap()["rowsAffectedKnown"]
						if !present || value != known {
							t.Errorf("known flag on wire = %v, present %v", value, present)
						}
					}
					return response, err
				})
				cancel()
				if err != nil || !reflect.DeepEqual(result, provider.result) || !reflect.DeepEqual(provider.request, request) {
					t.Fatalf("result=%#v, request=%#v, err=%v", result, provider.request, err)
				}
				if mode == "" && (provider.calls != 1 || provider.optionsCalls != 0) || mode != "" && (provider.calls != 0 || provider.optionsCalls != 1) {
					t.Fatalf("legacy/options calls = %d/%d", provider.calls, provider.optionsCalls)
				}
			}
		})
	}
}

func TestDatabaseQueryRPCLegacyAgentNeverReplaysExplicitModes(t *testing.T) {
	for _, oldDescription := range []bool{true, false} {
		name := "missing optional server interface"
		if oldDescription {
			name = "old service description"
		}
		t.Run(name, func(t *testing.T) {
			server := &legacyQueryServer{}
			client := newQueryTestClient(t, server, oldDescription)
			for _, mode := range []ncpdatabase.QueryResultMode{ncpdatabase.QueryResultModeAuto, ncpdatabase.QueryResultModeRows, ncpdatabase.QueryResultModeExec} {
				_, err := callQueryTestClient(t, client, ncpdatabase.QueryRequest{SQL: "UPDATE items SET value=value+1", ResultMode: mode})
				if ErrorCode(err) != string(ncpdatabase.CodeResultModeUnsupported) || server.calls != 0 {
					t.Fatalf("mode=%s, error=%v, SQL calls=%d", mode, err, server.calls)
				}
			}
			result, err := callQueryTestClient(t, client, ncpdatabase.QueryRequest{SQL: "UPDATE items SET value=value+1"})
			if err != nil || server.calls != 1 || result.RowsAffectedKnown || result.RowsAffected != 7 {
				t.Fatalf("legacy query result=%#v, error=%v, calls=%d", result, err, server.calls)
			}
		})
	}
}

func TestDatabaseQueryRPCMissingProviderNeverReplaysExplicitModes(t *testing.T) {
	provider := &legacyQueryProvider{result: ncpdatabase.QueryResult{Columns: []string{}, Rows: [][]any{}}}
	client := newQueryTestClient(t, newDatabaseService(provider), false)
	for _, mode := range []ncpdatabase.QueryResultMode{ncpdatabase.QueryResultModeAuto, ncpdatabase.QueryResultModeRows, ncpdatabase.QueryResultModeExec} {
		_, err := callQueryTestClient(t, client, ncpdatabase.QueryRequest{SQL: "UPDATE items SET value=value+1", ResultMode: mode})
		if ErrorCode(err) != string(ncpdatabase.CodeResultModeUnsupported) || provider.calls != 0 {
			t.Fatalf("mode=%s, error=%v, SQL calls=%d", mode, err, provider.calls)
		}
	}
	if _, err := callQueryTestClient(t, client, ncpdatabase.QueryRequest{SQL: "SELECT 1"}); err != nil || provider.calls != 1 {
		t.Fatalf("omitted mode error=%v, calls=%d", err, provider.calls)
	}
}

func TestDatabaseQueryRPCInvalidModesRejectBeforeProvider(t *testing.T) {
	for _, optional := range []bool{false, true} {
		legacy := &legacyQueryProvider{}
		options := &optionsQueryProvider{legacyQueryProvider: legacy}
		var provider DatabaseProvider = legacy
		if optional {
			provider = options
		}
		client := newQueryTestClient(t, newDatabaseService(provider), false)
		for _, mode := range []ncpdatabase.QueryResultMode{"ROWS", " rows", "exec ", "bogus"} {
			_, err := callQueryTestClient(t, client, ncpdatabase.QueryRequest{SQL: "UPDATE items SET value=value+1", ResultMode: mode})
			if ErrorCode(err) != string(ncpdatabase.CodeSQLInvalid) || legacy.calls != 0 || options.optionsCalls != 0 {
				t.Fatalf("optional=%v, mode=%q, error=%v, calls=%d/%d", optional, mode, err, legacy.calls, options.optionsCalls)
			}
		}
	}
	_, err := QueryDatabase(context.Background(), "", ncpdatabase.QueryRequest{SQL: "SELECT 1", ResultMode: "ROWS"})
	if ErrorCode(err) != string(ncpdatabase.CodeSQLInvalid) {
		t.Fatalf("invalid mode should be rejected before dialing: %v", err)
	}
}

func TestDatabaseQueryRPCErrorDoesNotReplay(t *testing.T) {
	for _, code := range []ncpdatabase.ErrorCode{ncpdatabase.CodeSQLInvalid, ncpdatabase.CodeConstraintFailed, ncpdatabase.CodeTimeout} {
		provider := &optionsQueryProvider{legacyQueryProvider: &legacyQueryProvider{}, err: &ncpdatabase.DatabaseError{Code: code, Cause: errors.New("private SQL details")}}
		client := newQueryTestClient(t, newDatabaseService(provider), false)
		_, err := callQueryTestClient(t, client, ncpdatabase.QueryRequest{SQL: "UPDATE items SET value=value+1", ResultMode: ncpdatabase.QueryResultModeRows})
		if ErrorCode(err) != string(code) || provider.calls != 0 || provider.optionsCalls != 1 {
			t.Fatalf("code=%s, error=%v, calls=%d/%d", code, err, provider.calls, provider.optionsCalls)
		}
	}
}

func TestDatabaseQueryRPCUnimplementedMappingIsNarrow(t *testing.T) {
	// 将可选接口隐藏，模拟旧客户端；显式模式不能转调 Query。
	server := &legacyQueryServer{}
	client := newQueryTestClient(t, server, false)
	legacyClient := struct{ AgentDatabaseServiceClient }{client}
	_, err := callQueryTestClient(t, legacyClient, ncpdatabase.QueryRequest{ResultMode: ncpdatabase.QueryResultModeRows})
	if ErrorCode(err) != string(ncpdatabase.CodeResultModeUnsupported) || server.calls != 0 {
		t.Fatalf("missing client interface error=%v, SQL calls=%d", err, server.calls)
	}
	if got := ErrorCode(databaseRPCError(grpcstatus.Error(codes.Unimplemented, "other unsupported method"))); got != string(ncpdatabase.CodeAgentUnavailable) {
		t.Fatalf("generic Unimplemented was remapped to %q", got)
	}
}
