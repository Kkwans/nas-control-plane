package agentsocket

import (
	"context"
	"encoding/json"
	"errors"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

func DiscoverDatabases(ctx context.Context, socketPath string) (ncpdatabase.Discovery, error) {
	var result ncpdatabase.Discovery
	err := callDatabase(ctx, socketPath, struct{}{}, &result, func(client AgentDatabaseServiceClient, request *structpb.Struct) (*structpb.Struct, error) {
		return client.Discover(ctx, request)
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
	err := callDatabase(ctx, socketPath, request, &result, func(client AgentDatabaseServiceClient, payload *structpb.Struct) (*structpb.Struct, error) {
		return client.Query(ctx, payload)
	})
	return result, err
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
		return contextError(err)
	}
	connection, err := dialSocket(socketPath)
	if err != nil {
		return err
	}
	defer connection.Close()
	encoded, err := json.Marshal(request)
	if err != nil {
		return coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	values := make(map[string]any)
	if err := json.Unmarshal(encoded, &values); err != nil {
		return coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	payload, err := structpb.NewStruct(values)
	if err != nil {
		return coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := call(NewAgentDatabaseServiceClient(connection), payload)
	if err != nil {
		message := grpcstatus.Convert(err).Message()
		if message == "" {
			message = "数据库操作失败"
		}
		return coded("AGENT_DATABASE_UNAVAILABLE", errors.New(message))
	}
	if response == nil {
		return coded("AGENT_RPC_RESPONSE_INVALID", errors.New("数据库响应为空"))
	}
	encoded, err = json.Marshal(response.AsMap())
	if err != nil {
		return coded("AGENT_RPC_RESPONSE_INVALID", err)
	}
	if err := json.Unmarshal(encoded, result); err != nil {
		return coded("AGENT_RPC_RESPONSE_INVALID", err)
	}
	return nil
}
