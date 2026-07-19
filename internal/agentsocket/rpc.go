package agentsocket

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	agentProbeServiceName                = "ncp.agent.v1.AgentProbeService"
	AgentProbeServiceGetStatusFullMethod = "/ncp.agent.v1.AgentProbeService/GetStatus"
)

type AgentProbeServiceClient interface {
	GetStatus(ctx context.Context, in *emptypb.Empty, options ...grpc.CallOption) (*structpb.Struct, error)
}

type agentProbeServiceClient struct {
	connection grpc.ClientConnInterface
}

func NewAgentProbeServiceClient(connection grpc.ClientConnInterface) AgentProbeServiceClient {
	return &agentProbeServiceClient{connection: connection}
}

func (c *agentProbeServiceClient) GetStatus(ctx context.Context, in *emptypb.Empty, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, AgentProbeServiceGetStatusFullMethod, in, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

type AgentProbeServiceServer interface {
	GetStatus(context.Context, *emptypb.Empty) (*structpb.Struct, error)
}

func RegisterAgentProbeServiceServer(server grpc.ServiceRegistrar, implementation AgentProbeServiceServer) {
	server.RegisterService(&agentProbeServiceDescription, implementation)
}

func agentProbeServiceGetStatusHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(emptypb.Empty)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentProbeServiceServer).GetStatus(ctx, request)
	}
	info := &grpc.UnaryServerInfo{
		Server:     server,
		FullMethod: AgentProbeServiceGetStatusFullMethod,
	}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentProbeServiceServer).GetStatus(ctx, request.(*emptypb.Empty))
	}
	return interceptor(ctx, request, info, handler)
}

var agentProbeServiceDescription = grpc.ServiceDesc{
	ServiceName: agentProbeServiceName,
	HandlerType: (*AgentProbeServiceServer)(nil),
	Methods: []grpc.MethodDesc{{
		MethodName: "GetStatus",
		Handler:    agentProbeServiceGetStatusHandler,
	}},
}
