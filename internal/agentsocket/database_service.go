package agentsocket

import (
	"context"
	"encoding/json"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

type DatabaseProvider interface {
	Discover(context.Context) (ncpdatabase.Discovery, error)
	Catalog(context.Context, ncpdatabase.CatalogRequest) (ncpdatabase.Catalog, error)
	Query(context.Context, ncpdatabase.QueryRequest) (ncpdatabase.QueryResult, error)
	Rows(context.Context, ncpdatabase.RowsRequest) (ncpdatabase.RowsResult, error)
	Insert(context.Context, ncpdatabase.InsertRequest) (ncpdatabase.MutationResult, error)
	Update(context.Context, ncpdatabase.UpdateRequest) (ncpdatabase.MutationResult, error)
	Delete(context.Context, ncpdatabase.DeleteRequest) (ncpdatabase.MutationResult, error)
}

type databaseService struct {
	provider DatabaseProvider
}

func newDatabaseService(provider DatabaseProvider) *databaseService {
	return &databaseService{provider: provider}
}

func (s *databaseService) Discover(ctx context.Context, _ *structpb.Struct) (*structpb.Struct, error) {
	result, err := s.provider.Discover(ctx)
	return databaseResponse(result, err)
}

func (s *databaseService) Catalog(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	var decoded ncpdatabase.CatalogRequest
	if err := decodeDatabaseRequest(request, &decoded); err != nil {
		return nil, err
	}
	result, err := s.provider.Catalog(ctx, decoded)
	return databaseResponse(result, err)
}

func (s *databaseService) Query(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	var decoded ncpdatabase.QueryRequest
	if err := decodeDatabaseRequest(request, &decoded); err != nil {
		return nil, err
	}
	result, err := s.provider.Query(ctx, decoded)
	return databaseResponse(result, err)
}

func (s *databaseService) Rows(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	var decoded ncpdatabase.RowsRequest
	if err := decodeDatabaseRequest(request, &decoded); err != nil {
		return nil, err
	}
	result, err := s.provider.Rows(ctx, decoded)
	return databaseResponse(result, err)
}

func (s *databaseService) Insert(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	var decoded ncpdatabase.InsertRequest
	if err := decodeDatabaseRequest(request, &decoded); err != nil {
		return nil, err
	}
	result, err := s.provider.Insert(ctx, decoded)
	return databaseResponse(result, err)
}

func (s *databaseService) Update(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	var decoded ncpdatabase.UpdateRequest
	if err := decodeDatabaseRequest(request, &decoded); err != nil {
		return nil, err
	}
	result, err := s.provider.Update(ctx, decoded)
	return databaseResponse(result, err)
}

func (s *databaseService) Delete(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	var decoded ncpdatabase.DeleteRequest
	if err := decodeDatabaseRequest(request, &decoded); err != nil {
		return nil, err
	}
	result, err := s.provider.Delete(ctx, decoded)
	return databaseResponse(result, err)
}

func decodeDatabaseRequest(request *structpb.Struct, destination any) error {
	if request == nil {
		return grpcstatus.Error(codes.InvalidArgument, "数据库请求不能为空")
	}
	encoded, err := json.Marshal(request.AsMap())
	if err != nil {
		return grpcstatus.Error(codes.InvalidArgument, "数据库请求格式无效")
	}
	if err := json.Unmarshal(encoded, destination); err != nil {
		return grpcstatus.Error(codes.InvalidArgument, "数据库请求格式无效")
	}
	return nil
}

func databaseResponse(result any, err error) (*structpb.Struct, error) {
	if err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, err.Error())
	}
	return dashboardStruct(result, "AGENT_DATABASE_RESPONSE_INVALID")
}
