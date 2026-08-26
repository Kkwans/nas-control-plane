package agentsocket

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	agentDockerResourcesServiceName       = "ncp.agent.v1.AgentDockerResourcesService"
	AgentDockerResourcesServiceListMethod = "/ncp.agent.v1.AgentDockerResourcesService/ListResources"
)

type AgentDockerResourcesServiceClient interface {
	ListResources(context.Context, *emptypb.Empty, ...grpc.CallOption) (*structpb.Struct, error)
}

type agentDockerResourcesServiceClient struct{ connection grpc.ClientConnInterface }

func NewAgentDockerResourcesServiceClient(connection grpc.ClientConnInterface) AgentDockerResourcesServiceClient {
	return &agentDockerResourcesServiceClient{connection: connection}
}

func (c *agentDockerResourcesServiceClient) ListResources(ctx context.Context, in *emptypb.Empty, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, AgentDockerResourcesServiceListMethod, in, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

type AgentDockerResourcesServiceServer interface {
	ListResources(context.Context, *emptypb.Empty) (*structpb.Struct, error)
}

func RegisterAgentDockerResourcesServiceServer(server grpc.ServiceRegistrar, implementation AgentDockerResourcesServiceServer) {
	server.RegisterService(&agentDockerResourcesServiceDescription, implementation)
}

func agentDockerResourcesServiceListHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(emptypb.Empty)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentDockerResourcesServiceServer).ListResources(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: AgentDockerResourcesServiceListMethod}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentDockerResourcesServiceServer).ListResources(ctx, request.(*emptypb.Empty))
	}
	return interceptor(ctx, request, info, handler)
}

var agentDockerResourcesServiceDescription = grpc.ServiceDesc{
	ServiceName: agentDockerResourcesServiceName,
	HandlerType: (*AgentDockerResourcesServiceServer)(nil),
	Methods:     []grpc.MethodDesc{{MethodName: "ListResources", Handler: agentDockerResourcesServiceListHandler}},
}
