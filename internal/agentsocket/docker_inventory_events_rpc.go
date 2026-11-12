package agentsocket

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	agentDockerInventoryEventsServiceName            = "ncp.agent.v1.AgentDockerInventoryEventsService"
	AgentDockerInventoryEventsServiceWatchFullMethod = "/ncp.agent.v1.AgentDockerInventoryEventsService/Watch"
)

type AgentDockerInventoryEventsServiceClient interface {
	Watch(context.Context, *emptypb.Empty, ...grpc.CallOption) (AgentDockerInventoryEventsService_WatchClient, error)
}

type agentDockerInventoryEventsServiceClient struct {
	connection grpc.ClientConnInterface
}

func NewAgentDockerInventoryEventsServiceClient(connection grpc.ClientConnInterface) AgentDockerInventoryEventsServiceClient {
	return &agentDockerInventoryEventsServiceClient{connection: connection}
}

func (c *agentDockerInventoryEventsServiceClient) Watch(ctx context.Context, request *emptypb.Empty, options ...grpc.CallOption) (AgentDockerInventoryEventsService_WatchClient, error) {
	stream, err := c.connection.NewStream(ctx, &agentDockerInventoryEventsServiceDescription.Streams[0], AgentDockerInventoryEventsServiceWatchFullMethod, options...)
	if err != nil {
		return nil, err
	}
	if err := stream.SendMsg(request); err != nil {
		return nil, err
	}
	if err := stream.CloseSend(); err != nil {
		return nil, err
	}
	return &agentDockerInventoryEventsServiceWatchClient{ClientStream: stream}, nil
}

type AgentDockerInventoryEventsService_WatchClient interface {
	Recv() (*structpb.Struct, error)
	grpc.ClientStream
}

type agentDockerInventoryEventsServiceWatchClient struct {
	grpc.ClientStream
}

func (s *agentDockerInventoryEventsServiceWatchClient) Recv() (*structpb.Struct, error) {
	message := new(structpb.Struct)
	if err := s.ClientStream.RecvMsg(message); err != nil {
		return nil, err
	}
	return message, nil
}

type AgentDockerInventoryEventsServiceServer interface {
	Watch(*emptypb.Empty, AgentDockerInventoryEventsService_WatchServer) error
}

type AgentDockerInventoryEventsService_WatchServer interface {
	Send(*structpb.Struct) error
	grpc.ServerStream
}

type agentDockerInventoryEventsServiceWatchServer struct {
	grpc.ServerStream
}

func (s *agentDockerInventoryEventsServiceWatchServer) Send(message *structpb.Struct) error {
	return s.ServerStream.SendMsg(message)
}

func RegisterAgentDockerInventoryEventsServiceServer(server grpc.ServiceRegistrar, implementation AgentDockerInventoryEventsServiceServer) {
	server.RegisterService(&agentDockerInventoryEventsServiceDescription, implementation)
}

func agentDockerInventoryEventsServiceWatchHandler(server any, stream grpc.ServerStream) error {
	request := new(emptypb.Empty)
	if err := stream.RecvMsg(request); err != nil {
		return err
	}
	return server.(AgentDockerInventoryEventsServiceServer).Watch(request, &agentDockerInventoryEventsServiceWatchServer{ServerStream: stream})
}

var agentDockerInventoryEventsServiceDescription = grpc.ServiceDesc{
	ServiceName: agentDockerInventoryEventsServiceName,
	HandlerType: (*AgentDockerInventoryEventsServiceServer)(nil),
	Streams: []grpc.StreamDesc{{
		StreamName:    "Watch",
		Handler:       agentDockerInventoryEventsServiceWatchHandler,
		ServerStreams: true,
	}},
}
