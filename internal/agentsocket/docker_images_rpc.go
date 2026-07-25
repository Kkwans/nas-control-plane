package agentsocket

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	agentDockerImagesServiceName            = "ncp.agent.v1.AgentDockerImagesService"
	agentDockerImagesListImagesFullMethod   = "/ncp.agent.v1.AgentDockerImagesService/ListImages"
	agentDockerImagesSearchImagesFullMethod = "/ncp.agent.v1.AgentDockerImagesService/SearchImages"
	agentDockerImagesListTagsFullMethod     = "/ncp.agent.v1.AgentDockerImagesService/ListTags"
	agentDockerImagesPullImageFullMethod    = "/ncp.agent.v1.AgentDockerImagesService/PullImage"
	agentDockerImagesRemoveImageFullMethod  = "/ncp.agent.v1.AgentDockerImagesService/RemoveImage"
)

type AgentDockerImagesServiceClient interface {
	ListImages(context.Context, *emptypb.Empty, ...grpc.CallOption) (*structpb.Struct, error)
	SearchImages(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	ListTags(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	PullImage(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	RemoveImage(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
}

type agentDockerImagesServiceClient struct {
	connection grpc.ClientConnInterface
}

func NewAgentDockerImagesServiceClient(connection grpc.ClientConnInterface) AgentDockerImagesServiceClient {
	return &agentDockerImagesServiceClient{connection: connection}
}

func (c *agentDockerImagesServiceClient) ListImages(ctx context.Context, request *emptypb.Empty, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, agentDockerImagesListImagesFullMethod, request, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *agentDockerImagesServiceClient) SearchImages(ctx context.Context, request *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, agentDockerImagesSearchImagesFullMethod, request, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *agentDockerImagesServiceClient) ListTags(ctx context.Context, request *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, agentDockerImagesListTagsFullMethod, request, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *agentDockerImagesServiceClient) PullImage(ctx context.Context, request *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, agentDockerImagesPullImageFullMethod, request, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *agentDockerImagesServiceClient) RemoveImage(ctx context.Context, request *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, agentDockerImagesRemoveImageFullMethod, request, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

type AgentDockerImagesServiceServer interface {
	ListImages(context.Context, *emptypb.Empty) (*structpb.Struct, error)
	SearchImages(context.Context, *structpb.Struct) (*structpb.Struct, error)
	ListTags(context.Context, *structpb.Struct) (*structpb.Struct, error)
	PullImage(context.Context, *structpb.Struct) (*structpb.Struct, error)
	RemoveImage(context.Context, *structpb.Struct) (*structpb.Struct, error)
}

func RegisterAgentDockerImagesServiceServer(server grpc.ServiceRegistrar, implementation AgentDockerImagesServiceServer) {
	server.RegisterService(&agentDockerImagesServiceDescription, implementation)
}

func agentDockerImagesListImagesHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(emptypb.Empty)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentDockerImagesServiceServer).ListImages(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: agentDockerImagesListImagesFullMethod}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentDockerImagesServiceServer).ListImages(ctx, request.(*emptypb.Empty))
	}
	return interceptor(ctx, request, info, handler)
}

func agentDockerImagesSearchImagesHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(structpb.Struct)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentDockerImagesServiceServer).SearchImages(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: agentDockerImagesSearchImagesFullMethod}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentDockerImagesServiceServer).SearchImages(ctx, request.(*structpb.Struct))
	}
	return interceptor(ctx, request, info, handler)
}

func agentDockerImagesListTagsHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(structpb.Struct)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentDockerImagesServiceServer).ListTags(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: agentDockerImagesListTagsFullMethod}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentDockerImagesServiceServer).ListTags(ctx, request.(*structpb.Struct))
	}
	return interceptor(ctx, request, info, handler)
}

func agentDockerImagesPullImageHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(structpb.Struct)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentDockerImagesServiceServer).PullImage(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: agentDockerImagesPullImageFullMethod}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentDockerImagesServiceServer).PullImage(ctx, request.(*structpb.Struct))
	}
	return interceptor(ctx, request, info, handler)
}

func agentDockerImagesRemoveImageHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(structpb.Struct)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentDockerImagesServiceServer).RemoveImage(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: agentDockerImagesRemoveImageFullMethod}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentDockerImagesServiceServer).RemoveImage(ctx, request.(*structpb.Struct))
	}
	return interceptor(ctx, request, info, handler)
}

var agentDockerImagesServiceDescription = grpc.ServiceDesc{
	ServiceName: agentDockerImagesServiceName,
	HandlerType: (*AgentDockerImagesServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "ListImages", Handler: agentDockerImagesListImagesHandler},
		{MethodName: "SearchImages", Handler: agentDockerImagesSearchImagesHandler},
		{MethodName: "ListTags", Handler: agentDockerImagesListTagsHandler},
		{MethodName: "PullImage", Handler: agentDockerImagesPullImageHandler},
		{MethodName: "RemoveImage", Handler: agentDockerImagesRemoveImageHandler},
	},
}
