package agentsocket

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	agentDashboardServiceName                         = "ncp.agent.v1.AgentDashboardService"
	AgentDashboardServiceGetSystemSummaryFullMethod   = "/ncp.agent.v1.AgentDashboardService/GetSystemSummary"
	AgentDashboardServiceGetSystemDetailsFullMethod   = "/ncp.agent.v1.AgentDashboardService/GetSystemDetails"
	AgentDashboardServiceGetDockerInventoryFullMethod = "/ncp.agent.v1.AgentDashboardService/GetDockerInventory"
)

type AgentDashboardServiceClient interface {
	GetSystemSummary(context.Context, *emptypb.Empty, ...grpc.CallOption) (*structpb.Struct, error)
	GetSystemDetails(context.Context, *emptypb.Empty, ...grpc.CallOption) (*structpb.Struct, error)
	GetDockerInventory(context.Context, *emptypb.Empty, ...grpc.CallOption) (*structpb.Struct, error)
}

type agentDashboardServiceClient struct {
	connection grpc.ClientConnInterface
}

func NewAgentDashboardServiceClient(connection grpc.ClientConnInterface) AgentDashboardServiceClient {
	return &agentDashboardServiceClient{connection: connection}
}

func (c *agentDashboardServiceClient) GetSystemSummary(ctx context.Context, in *emptypb.Empty, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, AgentDashboardServiceGetSystemSummaryFullMethod, in, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *agentDashboardServiceClient) GetSystemDetails(ctx context.Context, in *emptypb.Empty, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, AgentDashboardServiceGetSystemDetailsFullMethod, in, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *agentDashboardServiceClient) GetDockerInventory(ctx context.Context, in *emptypb.Empty, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, AgentDashboardServiceGetDockerInventoryFullMethod, in, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

type AgentDashboardServiceServer interface {
	GetSystemSummary(context.Context, *emptypb.Empty) (*structpb.Struct, error)
	GetSystemDetails(context.Context, *emptypb.Empty) (*structpb.Struct, error)
	GetDockerInventory(context.Context, *emptypb.Empty) (*structpb.Struct, error)
}

func RegisterAgentDashboardServiceServer(server grpc.ServiceRegistrar, implementation AgentDashboardServiceServer) {
	server.RegisterService(&agentDashboardServiceDescription, implementation)
}

func agentDashboardServiceGetSystemSummaryHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(emptypb.Empty)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentDashboardServiceServer).GetSystemSummary(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: AgentDashboardServiceGetSystemSummaryFullMethod}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentDashboardServiceServer).GetSystemSummary(ctx, request.(*emptypb.Empty))
	}
	return interceptor(ctx, request, info, handler)
}

func agentDashboardServiceGetSystemDetailsHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(emptypb.Empty)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentDashboardServiceServer).GetSystemDetails(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: AgentDashboardServiceGetSystemDetailsFullMethod}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentDashboardServiceServer).GetSystemDetails(ctx, request.(*emptypb.Empty))
	}
	return interceptor(ctx, request, info, handler)
}

func agentDashboardServiceGetDockerInventoryHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(emptypb.Empty)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentDashboardServiceServer).GetDockerInventory(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: AgentDashboardServiceGetDockerInventoryFullMethod}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentDashboardServiceServer).GetDockerInventory(ctx, request.(*emptypb.Empty))
	}
	return interceptor(ctx, request, info, handler)
}

var agentDashboardServiceDescription = grpc.ServiceDesc{
	ServiceName: agentDashboardServiceName,
	HandlerType: (*AgentDashboardServiceServer)(nil),
	Methods: []grpc.MethodDesc{{
		MethodName: "GetSystemSummary",
		Handler:    agentDashboardServiceGetSystemSummaryHandler,
	}, {
		MethodName: "GetSystemDetails",
		Handler:    agentDashboardServiceGetSystemDetailsHandler,
	}, {
		MethodName: "GetDockerInventory",
		Handler:    agentDashboardServiceGetDockerInventoryHandler,
	}},
}
