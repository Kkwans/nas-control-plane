package agentsocket

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	agentDockerControlServiceName                   = "ncp.agent.v1.AgentDockerControlService"
	AgentDockerControlServiceControlContainerMethod = "/ncp.agent.v1.AgentDockerControlService/ControlContainer"
)

type AgentDockerControlServiceClient interface {
	ControlContainer(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
}

type agentDockerControlServiceClient struct {
	connection grpc.ClientConnInterface
}

func NewAgentDockerControlServiceClient(connection grpc.ClientConnInterface) AgentDockerControlServiceClient {
	return &agentDockerControlServiceClient{connection: connection}
}

func (c *agentDockerControlServiceClient) ControlContainer(ctx context.Context, in *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, AgentDockerControlServiceControlContainerMethod, in, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

type AgentDockerControlServiceServer interface {
	ControlContainer(context.Context, *structpb.Struct) (*structpb.Struct, error)
}

func RegisterAgentDockerControlServiceServer(server grpc.ServiceRegistrar, implementation AgentDockerControlServiceServer) {
	server.RegisterService(&agentDockerControlServiceDescription, implementation)
}

func agentDockerControlServiceControlContainerHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(structpb.Struct)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentDockerControlServiceServer).ControlContainer(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: AgentDockerControlServiceControlContainerMethod}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentDockerControlServiceServer).ControlContainer(ctx, request.(*structpb.Struct))
	}
	return interceptor(ctx, request, info, handler)
}

var agentDockerControlServiceDescription = grpc.ServiceDesc{
	ServiceName: agentDockerControlServiceName,
	HandlerType: (*AgentDockerControlServiceServer)(nil),
	Methods: []grpc.MethodDesc{{
		MethodName: "ControlContainer",
		Handler:    agentDockerControlServiceControlContainerHandler,
	}},
}
