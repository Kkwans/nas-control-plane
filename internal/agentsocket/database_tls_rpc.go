package agentsocket

import (
	"context"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

const agentDatabaseTLSServiceName = "ncp.agent.v1.AgentDatabaseTLSService"

const (
	AgentDatabaseTLSTestConnectionMethod   = "/ncp.agent.v1.AgentDatabaseTLSService/TestConnection"
	AgentDatabaseTLSCatalogMethod          = "/ncp.agent.v1.AgentDatabaseTLSService/Catalog"
	AgentDatabaseTLSQueryMethod            = "/ncp.agent.v1.AgentDatabaseTLSService/Query"
	AgentDatabaseTLSQueryWithOptionsMethod = "/ncp.agent.v1.AgentDatabaseTLSService/QueryWithOptions"
	AgentDatabaseTLSRowsMethod             = "/ncp.agent.v1.AgentDatabaseTLSService/Rows"
	AgentDatabaseTLSInsertMethod           = "/ncp.agent.v1.AgentDatabaseTLSService/Insert"
	AgentDatabaseTLSUpdateMethod           = "/ncp.agent.v1.AgentDatabaseTLSService/Update"
	AgentDatabaseTLSDeleteMethod           = "/ncp.agent.v1.AgentDatabaseTLSService/Delete"
)

// AgentDatabaseTLSServiceClient 的八个建连方法均为必需，不提供旧路径回退。
type AgentDatabaseTLSServiceClient interface {
	TestConnection(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	Catalog(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	Query(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	QueryWithOptions(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	Rows(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	Insert(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	Update(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	Delete(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
}

type agentDatabaseTLSServiceClient struct{ connection grpc.ClientConnInterface }

func NewAgentDatabaseTLSServiceClient(connection grpc.ClientConnInterface) AgentDatabaseTLSServiceClient {
	return &agentDatabaseTLSServiceClient{connection: connection}
}

func (c *agentDatabaseTLSServiceClient) invoke(ctx context.Context, method string, request *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, method, request, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}
func (c *agentDatabaseTLSServiceClient) TestConnection(ctx context.Context, request *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	return c.invoke(ctx, AgentDatabaseTLSTestConnectionMethod, request, options...)
}
func (c *agentDatabaseTLSServiceClient) Catalog(ctx context.Context, request *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	return c.invoke(ctx, AgentDatabaseTLSCatalogMethod, request, options...)
}
func (c *agentDatabaseTLSServiceClient) Query(ctx context.Context, request *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	return c.invoke(ctx, AgentDatabaseTLSQueryMethod, request, options...)
}
func (c *agentDatabaseTLSServiceClient) QueryWithOptions(ctx context.Context, request *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	return c.invoke(ctx, AgentDatabaseTLSQueryWithOptionsMethod, request, options...)
}
func (c *agentDatabaseTLSServiceClient) Rows(ctx context.Context, request *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	return c.invoke(ctx, AgentDatabaseTLSRowsMethod, request, options...)
}
func (c *agentDatabaseTLSServiceClient) Insert(ctx context.Context, request *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	return c.invoke(ctx, AgentDatabaseTLSInsertMethod, request, options...)
}
func (c *agentDatabaseTLSServiceClient) Update(ctx context.Context, request *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	return c.invoke(ctx, AgentDatabaseTLSUpdateMethod, request, options...)
}
func (c *agentDatabaseTLSServiceClient) Delete(ctx context.Context, request *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	return c.invoke(ctx, AgentDatabaseTLSDeleteMethod, request, options...)
}

// 专用 marker 防止把普通 databaseService 注册到严格 namespace。
type AgentDatabaseTLSServiceServer interface {
	TestConnection(context.Context, *structpb.Struct) (*structpb.Struct, error)
	Catalog(context.Context, *structpb.Struct) (*structpb.Struct, error)
	Query(context.Context, *structpb.Struct) (*structpb.Struct, error)
	QueryWithOptions(context.Context, *structpb.Struct) (*structpb.Struct, error)
	Rows(context.Context, *structpb.Struct) (*structpb.Struct, error)
	Insert(context.Context, *structpb.Struct) (*structpb.Struct, error)
	Update(context.Context, *structpb.Struct) (*structpb.Struct, error)
	Delete(context.Context, *structpb.Struct) (*structpb.Struct, error)
	databaseTLSServiceMarker()
}

func RegisterAgentDatabaseTLSServiceServer(server grpc.ServiceRegistrar, implementation AgentDatabaseTLSServiceServer) {
	server.RegisterService(&agentDatabaseTLSServiceDescription, implementation)
}

func registerDatabaseServices(server grpc.ServiceRegistrar, provider DatabaseProvider) {
	RegisterAgentDatabaseServiceServer(server, newDatabaseService(provider))
	if strict, ok := provider.(DatabaseTLSPolicyProvider); ok && strict.SupportsDatabaseTLSPolicy() {
		implementation, err := newDatabaseTLSService(strict)
		if err == nil {
			RegisterAgentDatabaseTLSServiceServer(server, implementation)
		}
	}
}

func databaseTLSUnaryHandler(method string, call func(AgentDatabaseTLSServiceServer, context.Context, *structpb.Struct) (*structpb.Struct, error)) func(any, context.Context, func(any) error, grpc.UnaryServerInterceptor) (any, error) {
	return func(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		request := new(structpb.Struct)
		if err := decoder(request); err != nil {
			return nil, err
		}
		if interceptor == nil {
			return call(server.(AgentDatabaseTLSServiceServer), ctx, request)
		}
		info := &grpc.UnaryServerInfo{Server: server, FullMethod: method}
		handler := func(ctx context.Context, request any) (any, error) {
			return call(server.(AgentDatabaseTLSServiceServer), ctx, request.(*structpb.Struct))
		}
		return interceptor(ctx, request, info, handler)
	}
}

var agentDatabaseTLSServiceDescription = grpc.ServiceDesc{
	ServiceName: agentDatabaseTLSServiceName,
	HandlerType: (*AgentDatabaseTLSServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "TestConnection", Handler: databaseTLSUnaryHandler(AgentDatabaseTLSTestConnectionMethod, func(s AgentDatabaseTLSServiceServer, ctx context.Context, r *structpb.Struct) (*structpb.Struct, error) {
			return s.TestConnection(ctx, r)
		})},
		{MethodName: "Catalog", Handler: databaseTLSUnaryHandler(AgentDatabaseTLSCatalogMethod, func(s AgentDatabaseTLSServiceServer, ctx context.Context, r *structpb.Struct) (*structpb.Struct, error) {
			return s.Catalog(ctx, r)
		})},
		{MethodName: "Query", Handler: databaseTLSUnaryHandler(AgentDatabaseTLSQueryMethod, func(s AgentDatabaseTLSServiceServer, ctx context.Context, r *structpb.Struct) (*structpb.Struct, error) {
			return s.Query(ctx, r)
		})},
		{MethodName: "QueryWithOptions", Handler: databaseTLSUnaryHandler(AgentDatabaseTLSQueryWithOptionsMethod, func(s AgentDatabaseTLSServiceServer, ctx context.Context, r *structpb.Struct) (*structpb.Struct, error) {
			return s.QueryWithOptions(ctx, r)
		})},
		{MethodName: "Rows", Handler: databaseTLSUnaryHandler(AgentDatabaseTLSRowsMethod, func(s AgentDatabaseTLSServiceServer, ctx context.Context, r *structpb.Struct) (*structpb.Struct, error) {
			return s.Rows(ctx, r)
		})},
		{MethodName: "Insert", Handler: databaseTLSUnaryHandler(AgentDatabaseTLSInsertMethod, func(s AgentDatabaseTLSServiceServer, ctx context.Context, r *structpb.Struct) (*structpb.Struct, error) {
			return s.Insert(ctx, r)
		})},
		{MethodName: "Update", Handler: databaseTLSUnaryHandler(AgentDatabaseTLSUpdateMethod, func(s AgentDatabaseTLSServiceServer, ctx context.Context, r *structpb.Struct) (*structpb.Struct, error) {
			return s.Update(ctx, r)
		})},
		{MethodName: "Delete", Handler: databaseTLSUnaryHandler(AgentDatabaseTLSDeleteMethod, func(s AgentDatabaseTLSServiceServer, ctx context.Context, r *structpb.Struct) (*structpb.Struct, error) {
			return s.Delete(ctx, r)
		})},
	},
}
