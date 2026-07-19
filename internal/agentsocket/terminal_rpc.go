package agentsocket

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	agentTerminalPOCServiceName           = "ncp.agent.v1.AgentTerminalPOCService"
	AgentTerminalPOCServiceOpenFullMethod = "/ncp.agent.v1.AgentTerminalPOCService/Open"
)

type AgentTerminalPOCServiceClient interface {
	Open(context.Context, ...grpc.CallOption) (AgentTerminalPOCService_OpenClient, error)
}

type agentTerminalPOCServiceClient struct {
	connection grpc.ClientConnInterface
}

func NewAgentTerminalPOCServiceClient(connection grpc.ClientConnInterface) AgentTerminalPOCServiceClient {
	return &agentTerminalPOCServiceClient{connection: connection}
}

func (c *agentTerminalPOCServiceClient) Open(ctx context.Context, options ...grpc.CallOption) (AgentTerminalPOCService_OpenClient, error) {
	stream, err := c.connection.NewStream(ctx, &agentTerminalPOCServiceDescription.Streams[0], AgentTerminalPOCServiceOpenFullMethod, options...)
	if err != nil {
		return nil, err
	}
	return &agentTerminalPOCServiceOpenClient{ClientStream: stream}, nil
}

type AgentTerminalPOCService_OpenClient interface {
	Send(*structpb.Struct) error
	Recv() (*structpb.Struct, error)
	grpc.ClientStream
}

type agentTerminalPOCServiceOpenClient struct {
	grpc.ClientStream
}

func (c *agentTerminalPOCServiceOpenClient) Send(message *structpb.Struct) error {
	return c.ClientStream.SendMsg(message)
}

func (c *agentTerminalPOCServiceOpenClient) Recv() (*structpb.Struct, error) {
	message := new(structpb.Struct)
	if err := c.ClientStream.RecvMsg(message); err != nil {
		return nil, err
	}
	return message, nil
}

type AgentTerminalPOCServiceServer interface {
	Open(AgentTerminalPOCService_OpenServer) error
}

type AgentTerminalPOCService_OpenServer interface {
	Send(*structpb.Struct) error
	Recv() (*structpb.Struct, error)
	grpc.ServerStream
}

type agentTerminalPOCServiceOpenServer struct {
	grpc.ServerStream
}

func (s *agentTerminalPOCServiceOpenServer) Send(message *structpb.Struct) error {
	return s.ServerStream.SendMsg(message)
}

func (s *agentTerminalPOCServiceOpenServer) Recv() (*structpb.Struct, error) {
	message := new(structpb.Struct)
	if err := s.ServerStream.RecvMsg(message); err != nil {
		return nil, err
	}
	return message, nil
}

func RegisterAgentTerminalPOCServiceServer(server grpc.ServiceRegistrar, implementation AgentTerminalPOCServiceServer) {
	server.RegisterService(&agentTerminalPOCServiceDescription, implementation)
}

func agentTerminalPOCServiceOpenHandler(server any, stream grpc.ServerStream) error {
	return server.(AgentTerminalPOCServiceServer).Open(&agentTerminalPOCServiceOpenServer{ServerStream: stream})
}

var agentTerminalPOCServiceDescription = grpc.ServiceDesc{
	ServiceName: agentTerminalPOCServiceName,
	HandlerType: (*AgentTerminalPOCServiceServer)(nil),
	Streams: []grpc.StreamDesc{{
		StreamName:    "Open",
		Handler:       agentTerminalPOCServiceOpenHandler,
		ServerStreams: true,
		ClientStreams: true,
	}},
}
