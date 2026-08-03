package agentsocket

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

const agentDatabaseServiceName = "ncp.agent.v1.AgentDatabaseService"

const (
	AgentDatabaseDiscoverMethod       = "/ncp.agent.v1.AgentDatabaseService/Discover"
	AgentDatabaseTestConnectionMethod = "/ncp.agent.v1.AgentDatabaseService/TestConnection"
	AgentDatabaseCatalogMethod        = "/ncp.agent.v1.AgentDatabaseService/Catalog"
	AgentDatabaseQueryMethod          = "/ncp.agent.v1.AgentDatabaseService/Query"
	AgentDatabaseRowsMethod           = "/ncp.agent.v1.AgentDatabaseService/Rows"
	AgentDatabaseInsertMethod         = "/ncp.agent.v1.AgentDatabaseService/Insert"
	AgentDatabaseUpdateMethod         = "/ncp.agent.v1.AgentDatabaseService/Update"
	AgentDatabaseDeleteMethod         = "/ncp.agent.v1.AgentDatabaseService/Delete"
)

type AgentDatabaseServiceClient interface {
	Discover(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	Catalog(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	Query(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	Rows(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	Insert(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	Update(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	Delete(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
}

type AgentDatabaseConnectionServiceClient interface {
	AgentDatabaseServiceClient
	TestConnection(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
}

type agentDatabaseServiceClient struct {
	connection grpc.ClientConnInterface
}

func NewAgentDatabaseServiceClient(connection grpc.ClientConnInterface) AgentDatabaseServiceClient {
	return &agentDatabaseServiceClient{connection: connection}
}

func NewAgentDatabaseConnectionServiceClient(connection grpc.ClientConnInterface) AgentDatabaseConnectionServiceClient {
	return &agentDatabaseServiceClient{connection: connection}
}

func (c *agentDatabaseServiceClient) invoke(ctx context.Context, method string, in *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, method, in, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *agentDatabaseServiceClient) Discover(ctx context.Context, in *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	return c.invoke(ctx, AgentDatabaseDiscoverMethod, in, options...)
}
func (c *agentDatabaseServiceClient) TestConnection(ctx context.Context, in *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	return c.invoke(ctx, AgentDatabaseTestConnectionMethod, in, options...)
}
func (c *agentDatabaseServiceClient) Catalog(ctx context.Context, in *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	return c.invoke(ctx, AgentDatabaseCatalogMethod, in, options...)
}
func (c *agentDatabaseServiceClient) Query(ctx context.Context, in *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	return c.invoke(ctx, AgentDatabaseQueryMethod, in, options...)
}
func (c *agentDatabaseServiceClient) Rows(ctx context.Context, in *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	return c.invoke(ctx, AgentDatabaseRowsMethod, in, options...)
}
func (c *agentDatabaseServiceClient) Insert(ctx context.Context, in *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	return c.invoke(ctx, AgentDatabaseInsertMethod, in, options...)
}
func (c *agentDatabaseServiceClient) Update(ctx context.Context, in *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	return c.invoke(ctx, AgentDatabaseUpdateMethod, in, options...)
}
func (c *agentDatabaseServiceClient) Delete(ctx context.Context, in *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	return c.invoke(ctx, AgentDatabaseDeleteMethod, in, options...)
}

type AgentDatabaseServiceServer interface {
	Discover(context.Context, *structpb.Struct) (*structpb.Struct, error)
	Catalog(context.Context, *structpb.Struct) (*structpb.Struct, error)
	Query(context.Context, *structpb.Struct) (*structpb.Struct, error)
	Rows(context.Context, *structpb.Struct) (*structpb.Struct, error)
	Insert(context.Context, *structpb.Struct) (*structpb.Struct, error)
	Update(context.Context, *structpb.Struct) (*structpb.Struct, error)
	Delete(context.Context, *structpb.Struct) (*structpb.Struct, error)
}

type AgentDatabaseConnectionServiceServer interface {
	AgentDatabaseServiceServer
	TestConnection(context.Context, *structpb.Struct) (*structpb.Struct, error)
}

func RegisterAgentDatabaseServiceServer(server grpc.ServiceRegistrar, implementation AgentDatabaseServiceServer) {
	server.RegisterService(&agentDatabaseServiceDescription, implementation)
}

func databaseUnaryHandler(method string, call func(AgentDatabaseServiceServer, context.Context, *structpb.Struct) (*structpb.Struct, error)) func(any, context.Context, func(any) error, grpc.UnaryServerInterceptor) (any, error) {
	return func(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		request := new(structpb.Struct)
		if err := decoder(request); err != nil {
			return nil, err
		}
		if interceptor == nil {
			return call(server.(AgentDatabaseServiceServer), ctx, request)
		}
		info := &grpc.UnaryServerInfo{Server: server, FullMethod: method}
		handler := func(ctx context.Context, request any) (any, error) {
			return call(server.(AgentDatabaseServiceServer), ctx, request.(*structpb.Struct))
		}
		return interceptor(ctx, request, info, handler)
	}
}

var agentDatabaseServiceDescription = grpc.ServiceDesc{
	ServiceName: agentDatabaseServiceName,
	HandlerType: (*AgentDatabaseServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "Discover", Handler: databaseUnaryHandler(AgentDatabaseDiscoverMethod, func(s AgentDatabaseServiceServer, ctx context.Context, r *structpb.Struct) (*structpb.Struct, error) {
			return s.Discover(ctx, r)
		})},
		{MethodName: "TestConnection", Handler: databaseUnaryHandler(AgentDatabaseTestConnectionMethod, func(s AgentDatabaseServiceServer, ctx context.Context, r *structpb.Struct) (*structpb.Struct, error) {
			implementation, ok := s.(AgentDatabaseConnectionServiceServer)
			if !ok {
				return nil, grpcstatus.Error(codes.Unimplemented, "database connection diagnostics are unavailable")
			}
			return implementation.TestConnection(ctx, r)
		})},
		{MethodName: "Catalog", Handler: databaseUnaryHandler(AgentDatabaseCatalogMethod, func(s AgentDatabaseServiceServer, ctx context.Context, r *structpb.Struct) (*structpb.Struct, error) {
			return s.Catalog(ctx, r)
		})},
		{MethodName: "Query", Handler: databaseUnaryHandler(AgentDatabaseQueryMethod, func(s AgentDatabaseServiceServer, ctx context.Context, r *structpb.Struct) (*structpb.Struct, error) {
			return s.Query(ctx, r)
		})},
		{MethodName: "Rows", Handler: databaseUnaryHandler(AgentDatabaseRowsMethod, func(s AgentDatabaseServiceServer, ctx context.Context, r *structpb.Struct) (*structpb.Struct, error) {
			return s.Rows(ctx, r)
		})},
		{MethodName: "Insert", Handler: databaseUnaryHandler(AgentDatabaseInsertMethod, func(s AgentDatabaseServiceServer, ctx context.Context, r *structpb.Struct) (*structpb.Struct, error) {
			return s.Insert(ctx, r)
		})},
		{MethodName: "Update", Handler: databaseUnaryHandler(AgentDatabaseUpdateMethod, func(s AgentDatabaseServiceServer, ctx context.Context, r *structpb.Struct) (*structpb.Struct, error) {
			return s.Update(ctx, r)
		})},
		{MethodName: "Delete", Handler: databaseUnaryHandler(AgentDatabaseDeleteMethod, func(s AgentDatabaseServiceServer, ctx context.Context, r *structpb.Struct) (*structpb.Struct, error) {
			return s.Delete(ctx, r)
		})},
	},
}
