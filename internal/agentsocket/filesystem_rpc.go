package agentsocket

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	agentFilesystemServiceName           = "ncp.agent.v1.AgentFilesystemService"
	AgentFilesystemServiceListPathMethod = "/ncp.agent.v1.AgentFilesystemService/ListPath"
)

type AgentFilesystemServiceClient interface {
	ListPath(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
}

type agentFilesystemServiceClient struct{ connection grpc.ClientConnInterface }

func NewAgentFilesystemServiceClient(connection grpc.ClientConnInterface) AgentFilesystemServiceClient {
	return &agentFilesystemServiceClient{connection: connection}
}

func (c *agentFilesystemServiceClient) ListPath(ctx context.Context, in *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, AgentFilesystemServiceListPathMethod, in, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

type AgentFilesystemServiceServer interface {
	ListPath(context.Context, *structpb.Struct) (*structpb.Struct, error)
}

func RegisterAgentFilesystemServiceServer(server grpc.ServiceRegistrar, implementation AgentFilesystemServiceServer) {
	server.RegisterService(&agentFilesystemServiceDescription, implementation)
}

func agentFilesystemServiceListPathHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(structpb.Struct)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentFilesystemServiceServer).ListPath(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: AgentFilesystemServiceListPathMethod}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentFilesystemServiceServer).ListPath(ctx, request.(*structpb.Struct))
	}
	return interceptor(ctx, request, info, handler)
}

var agentFilesystemServiceDescription = grpc.ServiceDesc{
	ServiceName: agentFilesystemServiceName,
	HandlerType: (*AgentFilesystemServiceServer)(nil),
	Methods:     []grpc.MethodDesc{{MethodName: "ListPath", Handler: agentFilesystemServiceListPathHandler}},
}
