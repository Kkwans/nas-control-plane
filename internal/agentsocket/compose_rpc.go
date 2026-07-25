package agentsocket

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	agentComposeServiceName              = "ncp.agent.v1.AgentComposeService"
	agentComposeReadConfigFullMethod     = "/ncp.agent.v1.AgentComposeService/ReadConfig"
	agentComposeValidateConfigFullMethod = "/ncp.agent.v1.AgentComposeService/ValidateConfig"
	agentComposeDeployConfigFullMethod   = "/ncp.agent.v1.AgentComposeService/DeployConfig"
)

type AgentComposeServiceClient interface {
	ReadConfig(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	ValidateConfig(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	DeployConfig(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
}

type agentComposeServiceClient struct{ connection grpc.ClientConnInterface }

func NewAgentComposeServiceClient(connection grpc.ClientConnInterface) AgentComposeServiceClient {
	return &agentComposeServiceClient{connection: connection}
}

func (client *agentComposeServiceClient) ReadConfig(ctx context.Context, request *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := client.connection.Invoke(ctx, agentComposeReadConfigFullMethod, request, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

func (client *agentComposeServiceClient) ValidateConfig(ctx context.Context, request *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := client.connection.Invoke(ctx, agentComposeValidateConfigFullMethod, request, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

func (client *agentComposeServiceClient) DeployConfig(ctx context.Context, request *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := client.connection.Invoke(ctx, agentComposeDeployConfigFullMethod, request, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

type AgentComposeServiceServer interface {
	ReadConfig(context.Context, *structpb.Struct) (*structpb.Struct, error)
	ValidateConfig(context.Context, *structpb.Struct) (*structpb.Struct, error)
	DeployConfig(context.Context, *structpb.Struct) (*structpb.Struct, error)
}

func RegisterAgentComposeServiceServer(server grpc.ServiceRegistrar, implementation AgentComposeServiceServer) {
	server.RegisterService(&agentComposeServiceDescription, implementation)
}

func agentComposeReadConfigHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(structpb.Struct)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentComposeServiceServer).ReadConfig(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: agentComposeReadConfigFullMethod}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentComposeServiceServer).ReadConfig(ctx, request.(*structpb.Struct))
	}
	return interceptor(ctx, request, info, handler)
}

func agentComposeValidateConfigHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(structpb.Struct)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentComposeServiceServer).ValidateConfig(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: agentComposeValidateConfigFullMethod}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentComposeServiceServer).ValidateConfig(ctx, request.(*structpb.Struct))
	}
	return interceptor(ctx, request, info, handler)
}

func agentComposeDeployConfigHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(structpb.Struct)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentComposeServiceServer).DeployConfig(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: agentComposeDeployConfigFullMethod}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentComposeServiceServer).DeployConfig(ctx, request.(*structpb.Struct))
	}
	return interceptor(ctx, request, info, handler)
}

var agentComposeServiceDescription = grpc.ServiceDesc{
	ServiceName: agentComposeServiceName,
	HandlerType: (*AgentComposeServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "ReadConfig", Handler: agentComposeReadConfigHandler},
		{MethodName: "ValidateConfig", Handler: agentComposeValidateConfigHandler},
		{MethodName: "DeployConfig", Handler: agentComposeDeployConfigHandler},
	},
}
