package agentsocket

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	agentDockerControlServiceName                           = "ncp.agent.v1.AgentDockerControlService"
	AgentDockerControlServiceControlContainerMethod         = "/ncp.agent.v1.AgentDockerControlService/ControlContainer"
	AgentDockerControlServiceControlStandaloneProjectMethod = "/ncp.agent.v1.AgentDockerControlService/ControlStandaloneProject"
	AgentDockerControlServiceControlComposeProjectMethod    = "/ncp.agent.v1.AgentDockerControlService/ControlComposeProject"
	AgentDockerControlServiceCreateContainerMethod          = "/ncp.agent.v1.AgentDockerControlService/CreateContainer"
	AgentDockerControlServiceDeleteProjectMethod            = "/ncp.agent.v1.AgentDockerControlService/DeleteProject"
)

type AgentDockerControlServiceClient interface {
	ControlContainer(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	ControlStandaloneProject(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	ControlComposeProject(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
}

type AgentDockerControlServiceExtendedClient interface {
	AgentDockerControlServiceClient
	CreateContainer(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	DeleteProject(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
}

type agentDockerControlServiceClient struct {
	connection grpc.ClientConnInterface
}

func NewAgentDockerControlServiceClient(connection grpc.ClientConnInterface) AgentDockerControlServiceExtendedClient {
	return &agentDockerControlServiceClient{connection: connection}
}

func (c *agentDockerControlServiceClient) ControlContainer(ctx context.Context, in *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, AgentDockerControlServiceControlContainerMethod, in, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *agentDockerControlServiceClient) ControlStandaloneProject(ctx context.Context, in *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, AgentDockerControlServiceControlStandaloneProjectMethod, in, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *agentDockerControlServiceClient) ControlComposeProject(ctx context.Context, in *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, AgentDockerControlServiceControlComposeProjectMethod, in, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *agentDockerControlServiceClient) CreateContainer(ctx context.Context, in *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, AgentDockerControlServiceCreateContainerMethod, in, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *agentDockerControlServiceClient) DeleteProject(ctx context.Context, in *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, AgentDockerControlServiceDeleteProjectMethod, in, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

type AgentDockerControlServiceServer interface {
	ControlContainer(context.Context, *structpb.Struct) (*structpb.Struct, error)
	ControlStandaloneProject(context.Context, *structpb.Struct) (*structpb.Struct, error)
	ControlComposeProject(context.Context, *structpb.Struct) (*structpb.Struct, error)
}

// Optional service capabilities keep existing Agent Docker fakes source
// compatible while allowing the concrete service to expose newer operations.
type AgentDockerContainerCreateServiceServer interface {
	CreateContainer(context.Context, *structpb.Struct) (*structpb.Struct, error)
}

type AgentDockerProjectDeleteServiceServer interface {
	DeleteProject(context.Context, *structpb.Struct) (*structpb.Struct, error)
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

func agentDockerControlServiceControlStandaloneProjectHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(structpb.Struct)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentDockerControlServiceServer).ControlStandaloneProject(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: AgentDockerControlServiceControlStandaloneProjectMethod}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentDockerControlServiceServer).ControlStandaloneProject(ctx, request.(*structpb.Struct))
	}
	return interceptor(ctx, request, info, handler)
}

func agentDockerControlServiceControlComposeProjectHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(structpb.Struct)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentDockerControlServiceServer).ControlComposeProject(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: AgentDockerControlServiceControlComposeProjectMethod}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentDockerControlServiceServer).ControlComposeProject(ctx, request.(*structpb.Struct))
	}
	return interceptor(ctx, request, info, handler)
}

func agentDockerControlServiceCreateContainerHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(structpb.Struct)
	if err := decoder(request); err != nil {
		return nil, err
	}
	implementation, ok := server.(AgentDockerContainerCreateServiceServer)
	if !ok {
		return nil, grpcstatus.Error(codes.Unimplemented, "AGENT_DOCKER_CONTROL_CREATE_UNAVAILABLE")
	}
	if interceptor == nil {
		return implementation.CreateContainer(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: AgentDockerControlServiceCreateContainerMethod}
	handler := func(ctx context.Context, request any) (any, error) {
		return implementation.CreateContainer(ctx, request.(*structpb.Struct))
	}
	return interceptor(ctx, request, info, handler)
}

func agentDockerControlServiceDeleteProjectHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(structpb.Struct)
	if err := decoder(request); err != nil {
		return nil, err
	}
	implementation, ok := server.(AgentDockerProjectDeleteServiceServer)
	if !ok {
		return nil, grpcstatus.Error(codes.Unimplemented, "AGENT_DOCKER_CONTROL_DELETE_UNAVAILABLE")
	}
	if interceptor == nil {
		return implementation.DeleteProject(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: AgentDockerControlServiceDeleteProjectMethod}
	handler := func(ctx context.Context, request any) (any, error) {
		return implementation.DeleteProject(ctx, request.(*structpb.Struct))
	}
	return interceptor(ctx, request, info, handler)
}

var agentDockerControlServiceDescription = grpc.ServiceDesc{
	ServiceName: agentDockerControlServiceName,
	HandlerType: (*AgentDockerControlServiceServer)(nil),
	Methods: []grpc.MethodDesc{{
		MethodName: "ControlContainer",
		Handler:    agentDockerControlServiceControlContainerHandler,
	}, {
		MethodName: "ControlStandaloneProject",
		Handler:    agentDockerControlServiceControlStandaloneProjectHandler,
	}, {
		MethodName: "ControlComposeProject",
		Handler:    agentDockerControlServiceControlComposeProjectHandler,
	}, {
		MethodName: "CreateContainer",
		Handler:    agentDockerControlServiceCreateContainerHandler,
	}, {
		MethodName: "DeleteProject",
		Handler:    agentDockerControlServiceDeleteProjectHandler,
	}},
}
