package agentsocket

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	agentDockerLogsServiceName           = "ncp.agent.v1.AgentDockerLogsService"
	AgentDockerLogsServiceReadLogsMethod = "/ncp.agent.v1.AgentDockerLogsService/ReadLogs"
)

type AgentDockerLogsServiceClient interface {
	ReadLogs(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
}

type agentDockerLogsServiceClient struct {
	connection grpc.ClientConnInterface
}

func NewAgentDockerLogsServiceClient(connection grpc.ClientConnInterface) AgentDockerLogsServiceClient {
	return &agentDockerLogsServiceClient{connection: connection}
}

func (c *agentDockerLogsServiceClient) ReadLogs(ctx context.Context, in *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, AgentDockerLogsServiceReadLogsMethod, in, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

type AgentDockerLogsServiceServer interface {
	ReadLogs(context.Context, *structpb.Struct) (*structpb.Struct, error)
}

func RegisterAgentDockerLogsServiceServer(server grpc.ServiceRegistrar, implementation AgentDockerLogsServiceServer) {
	server.RegisterService(&agentDockerLogsServiceDescription, implementation)
}

func agentDockerLogsServiceReadLogsHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(structpb.Struct)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentDockerLogsServiceServer).ReadLogs(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: AgentDockerLogsServiceReadLogsMethod}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentDockerLogsServiceServer).ReadLogs(ctx, request.(*structpb.Struct))
	}
	return interceptor(ctx, request, info, handler)
}

var agentDockerLogsServiceDescription = grpc.ServiceDesc{
	ServiceName: agentDockerLogsServiceName,
	HandlerType: (*AgentDockerLogsServiceServer)(nil),
	Methods: []grpc.MethodDesc{{
		MethodName: "ReadLogs",
		Handler:    agentDockerLogsServiceReadLogsHandler,
	}},
}
