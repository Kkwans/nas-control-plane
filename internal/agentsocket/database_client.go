package agentsocket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

func DiscoverDatabases(ctx context.Context, socketPath string) (ncpdatabase.Discovery, error) {
	return discoverDatabases(ctx, socketPath, false)
}

func RefreshDatabases(ctx context.Context, socketPath string) (ncpdatabase.Discovery, error) {
	return discoverDatabases(ctx, socketPath, true)
}

func discoverDatabases(ctx context.Context, socketPath string, force bool) (ncpdatabase.Discovery, error) {
	var result ncpdatabase.Discovery
	err := callDatabase(ctx, socketPath, map[string]any{"refresh": force}, &result, func(client AgentDatabaseServiceClient, request *structpb.Struct) (*structpb.Struct, error) {
		return client.Discover(ctx, request)
	})
	return result, err
}

func TestDatabaseConnection(ctx context.Context, socketPath string, request ncpdatabase.Connection) (ncpdatabase.ConnectionDiagnostic, error) {
	var result ncpdatabase.ConnectionDiagnostic
	err := callDatabase(ctx, socketPath, request, &result, func(client AgentDatabaseServiceClient, payload *structpb.Struct) (*structpb.Struct, error) {
		connectionClient, ok := client.(AgentDatabaseConnectionServiceClient)
		if !ok {
			return nil, grpcstatus.Error(codes.Unimplemented, "database connection diagnostics are unavailable")
		}
		return connectionClient.TestConnection(ctx, payload)
	})
	return result, err
}

func CatalogDatabase(ctx context.Context, socketPath string, request ncpdatabase.CatalogRequest) (ncpdatabase.Catalog, error) {
	var result ncpdatabase.Catalog
	err := callDatabase(ctx, socketPath, request, &result, func(client AgentDatabaseServiceClient, payload *structpb.Struct) (*structpb.Struct, error) {
		return client.Catalog(ctx, payload)
	})
	return result, err
}

func QueryDatabase(ctx context.Context, socketPath string, request ncpdatabase.QueryRequest) (ncpdatabase.QueryResult, error) {
	var result ncpdatabase.QueryResult
	if err := ncpdatabase.ValidateQueryResultMode(request.ResultMode); err != nil {
		return result, coded(string(ncpdatabase.CodeSQLInvalid), err)
	}
	err := callDatabase(ctx, socketPath, request, &result, func(client AgentDatabaseServiceClient, payload *structpb.Struct) (*structpb.Struct, error) {
		return queryDatabaseRPC(ctx, client, payload, request.ResultMode)
	})
	return result, err
}

func queryDatabaseRPC(ctx context.Context, client AgentDatabaseServiceClient, payload *structpb.Struct, mode ncpdatabase.QueryResultMode) (*structpb.Struct, error) {
	if mode == "" {
		return client.Query(ctx, payload)
	}
	optionsClient, ok := client.(AgentDatabaseQueryOptionsServiceClient)
	if !ok {
		return nil, grpcstatus.Error(codes.InvalidArgument, string(ncpdatabase.CodeResultModeUnsupported))
	}
	response, err := optionsClient.QueryWithOptions(ctx, payload)
	// 只在显式模式调用上识别旧 Agent；绝不移除模式或重放 SQL。
	if grpcstatus.Code(err) == codes.Unimplemented {
		return nil, grpcstatus.Error(codes.InvalidArgument, string(ncpdatabase.CodeResultModeUnsupported))
	}
	return response, err
}

func ReadDatabaseRows(ctx context.Context, socketPath string, request ncpdatabase.RowsRequest) (ncpdatabase.RowsResult, error) {
	var result ncpdatabase.RowsResult
	err := callDatabase(ctx, socketPath, request, &result, func(client AgentDatabaseServiceClient, payload *structpb.Struct) (*structpb.Struct, error) {
		return client.Rows(ctx, payload)
	})
	return result, err
}

func InsertDatabaseRow(ctx context.Context, socketPath string, request ncpdatabase.InsertRequest) (ncpdatabase.MutationResult, error) {
	return mutateDatabase(ctx, socketPath, request, func(client AgentDatabaseServiceClient, payload *structpb.Struct) (*structpb.Struct, error) {
		return client.Insert(ctx, payload)
	})
}

func UpdateDatabaseRow(ctx context.Context, socketPath string, request ncpdatabase.UpdateRequest) (ncpdatabase.MutationResult, error) {
	return mutateDatabase(ctx, socketPath, request, func(client AgentDatabaseServiceClient, payload *structpb.Struct) (*structpb.Struct, error) {
		return client.Update(ctx, payload)
	})
}

func DeleteDatabaseRow(ctx context.Context, socketPath string, request ncpdatabase.DeleteRequest) (ncpdatabase.MutationResult, error) {
	return mutateDatabase(ctx, socketPath, request, func(client AgentDatabaseServiceClient, payload *structpb.Struct) (*structpb.Struct, error) {
		return client.Delete(ctx, payload)
	})
}

func mutateDatabase(ctx context.Context, socketPath string, request any, call func(AgentDatabaseServiceClient, *structpb.Struct) (*structpb.Struct, error)) (ncpdatabase.MutationResult, error) {
	var result ncpdatabase.MutationResult
	err := callDatabase(ctx, socketPath, request, &result, call)
	return result, err
}

func callDatabase(ctx context.Context, socketPath string, request, result any, call func(AgentDatabaseServiceClient, *structpb.Struct) (*structpb.Struct, error)) error {
	if err := ctx.Err(); err != nil {
		return databaseRPCError(err)
	}
	connection, err := dialSocket(socketPath)
	if err != nil {
		return databaseCodedError(ncpdatabase.CodeAgentUnavailable, nil)
	}
	defer connection.Close()
	return callDatabaseClient(NewAgentDatabaseServiceClient(connection), request, result, call)
}

func callDatabaseClient(client AgentDatabaseServiceClient, request, result any, call func(AgentDatabaseServiceClient, *structpb.Struct) (*structpb.Struct, error)) error {
	encoded, err := json.Marshal(request)
	if err != nil {
		return coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	values := make(map[string]any)
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&values); err != nil {
		return coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	// Struct 的数值会转换为 float64；数据库字段按原始数字文本传输，避免主键和小数被舍入。
	// 仅转换行数据，保留 limit、offset 等协议字段的数值类型。
	for _, field := range []string{"values", "keys"} {
		if row, ok := values[field].(map[string]any); ok {
			for name, value := range row {
				if number, ok := value.(json.Number); ok {
					row[name] = number.String()
				}
			}
		}
	}
	payload, err := structpb.NewStruct(values)
	if err != nil {
		return coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := call(client, payload)
	if err != nil {
		return databaseRPCError(err)
	}
	if response == nil {
		return coded("AGENT_RPC_RESPONSE_INVALID", errors.New("数据库响应为空"))
	}
	// 兼容旧 Agent 或自定义 provider：先检查线格式大小，再创建第二份结果树。
	if err := validateDatabaseProtoBudget(response); err != nil {
		return databaseRPCError(databaseResultTooLargeStatus())
	}
	if proto.Size(response) > ncpdatabase.MaxResultBytes {
		return databaseRPCError(databaseResultTooLargeStatus())
	}
	responseValues := response.AsMap()
	if err := ncpdatabase.ValidateResultBudget(responseValues); err != nil {
		return databaseRPCError(databaseResultTooLargeStatus())
	}
	encoded, err = json.Marshal(responseValues)
	if err != nil {
		return coded("AGENT_RPC_RESPONSE_INVALID", err)
	}
	if len(encoded) > ncpdatabase.MaxResultBytes {
		return databaseRPCError(databaseResultTooLargeStatus())
	}
	if err := json.Unmarshal(encoded, result); err != nil {
		return coded("AGENT_RPC_RESPONSE_INVALID", err)
	}
	return nil
}

func databaseRPCError(err error) error {
	if err == nil {
		return nil
	}
	status := grpcstatus.Convert(err)
	// 仅保留安全的 Context 哨兵，不能将原始 gRPC 消息或详情留在错误链中。
	if errors.Is(err, context.DeadlineExceeded) {
		return databaseCodedError(ncpdatabase.CodeTimeout, context.DeadlineExceeded)
	}
	if errors.Is(err, context.Canceled) {
		return databaseCodedError(ncpdatabase.CodeTimeout, context.Canceled)
	}
	if status.Code() == codes.DeadlineExceeded {
		return databaseCodedError(ncpdatabase.CodeTimeout, context.DeadlineExceeded)
	}
	if status.Code() == codes.Canceled {
		return databaseCodedError(ncpdatabase.CodeTimeout, context.Canceled)
	}
	// 旧 Agent 的默认 gRPC 4 MiB 接收限制同样表示结果过大，不能误报 Agent 断连。
	if status.Code() == codes.ResourceExhausted {
		return databaseCodedError(ncpdatabase.CodeResultTooLarge, nil)
	}
	if code := ncpdatabase.CodeFromString(status.Message()); code != "" {
		return databaseCodedError(code, nil)
	}
	return databaseCodedError(ncpdatabase.CodeAgentUnavailable, nil)
}

func databaseCodedError(code ncpdatabase.ErrorCode, cause error) error {
	// 兼容 agentsocket.ErrorCode，并让连接协调器通过类型保留稳定码，无需重判文本。
	return coded(string(code), &ncpdatabase.DatabaseError{Code: code, Cause: cause})
}

// 默认传输上限只约束线格式；密集小值在 AsMap 中会膨胀为接口/容器树。
// 先直接遍历 protobuf，限制节点、文本转义和深度，再进行转换或递归 Size。
func validateDatabaseProtoBudget(response *structpb.Struct) error {
	return chargeDatabaseProtoStruct(ncpdatabase.NewResultBudget(), response, 0)
}

func chargeDatabaseProtoStruct(budget *ncpdatabase.ResultBudget, object *structpb.Struct, depth int) error {
	if depth > 32 {
		return databaseResultTooLargeStatus()
	}
	if err := budget.AddValue(nil); err != nil {
		return err
	}
	for key, value := range object.GetFields() {
		if err := budget.AddValue(key); err != nil {
			return err
		}
		if err := chargeDatabaseProtoValue(budget, value, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func chargeDatabaseProtoValue(budget *ncpdatabase.ResultBudget, value *structpb.Value, depth int) error {
	if depth > 32 {
		return databaseResultTooLargeStatus()
	}
	if value == nil {
		return budget.AddValue(nil)
	}
	switch typed := value.Kind.(type) {
	case *structpb.Value_StringValue:
		if typed != nil {
			return budget.AddValue(typed.StringValue)
		}
	case *structpb.Value_NumberValue:
		if typed != nil {
			switch {
			case math.IsNaN(typed.NumberValue):
				return budget.AddValue("NaN")
			case math.IsInf(typed.NumberValue, 1):
				return budget.AddValue("Infinity")
			case math.IsInf(typed.NumberValue, -1):
				return budget.AddValue("-Infinity")
			}
		}
	case *structpb.Value_StructValue:
		if typed != nil {
			return chargeDatabaseProtoStruct(budget, typed.StructValue, depth+1)
		}
	case *structpb.Value_ListValue:
		if typed != nil {
			if err := budget.AddValue(nil); err != nil {
				return err
			}
			for _, item := range typed.ListValue.GetValues() {
				if err := chargeDatabaseProtoValue(budget, item, depth+1); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return budget.AddValue(nil)
}
