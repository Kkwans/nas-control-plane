package agentsocket

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	agentWebProbeServiceName                            = "ncp.agent.v1.AgentWebProbeService"
	AgentWebProbeServiceProbeFullMethodName             = "/ncp.agent.v1.AgentWebProbeService/Probe"
	AgentWebProbeServiceDiscoverHostSitesFullMethodName = "/ncp.agent.v1.AgentWebProbeService/DiscoverHostSites"
)

type AgentWebProbeServiceClient interface {
	Probe(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	DiscoverHostSites(context.Context, *emptypb.Empty, ...grpc.CallOption) (*structpb.Struct, error)
}

func (client *agentWebProbeServiceClient) DiscoverHostSites(ctx context.Context, input *emptypb.Empty, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := client.connection.Invoke(ctx, AgentWebProbeServiceDiscoverHostSitesFullMethodName, input, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

type agentWebProbeServiceClient struct {
	connection grpc.ClientConnInterface
}

func NewAgentWebProbeServiceClient(connection grpc.ClientConnInterface) AgentWebProbeServiceClient {
	return &agentWebProbeServiceClient{connection: connection}
}

func (client *agentWebProbeServiceClient) Probe(ctx context.Context, input *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := client.connection.Invoke(ctx, AgentWebProbeServiceProbeFullMethodName, input, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

type AgentWebProbeServiceServer interface {
	Probe(context.Context, *structpb.Struct) (*structpb.Struct, error)
	DiscoverHostSites(context.Context, *emptypb.Empty) (*structpb.Struct, error)
}

func agentWebProbeDiscoverHostSitesHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(emptypb.Empty)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentWebProbeServiceServer).DiscoverHostSites(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: AgentWebProbeServiceDiscoverHostSitesFullMethodName}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentWebProbeServiceServer).DiscoverHostSites(ctx, request.(*emptypb.Empty))
	}
	return interceptor(ctx, request, info, handler)
}

func RegisterAgentWebProbeServiceServer(server grpc.ServiceRegistrar, implementation AgentWebProbeServiceServer) {
	server.RegisterService(&agentWebProbeServiceDescription, implementation)
}

func agentWebProbeHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(structpb.Struct)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentWebProbeServiceServer).Probe(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: AgentWebProbeServiceProbeFullMethodName}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentWebProbeServiceServer).Probe(ctx, request.(*structpb.Struct))
	}
	return interceptor(ctx, request, info, handler)
}

var agentWebProbeServiceDescription = grpc.ServiceDesc{
	ServiceName: agentWebProbeServiceName,
	HandlerType: (*AgentWebProbeServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "Probe", Handler: agentWebProbeHandler},
		{MethodName: "DiscoverHostSites", Handler: agentWebProbeDiscoverHostSitesHandler},
	},
}
