package agentsocket

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	agentJournalServiceName        = "ncp.agent.v1.AgentJournalService"
	AgentJournalServiceQueryMethod = "/ncp.agent.v1.AgentJournalService/Query"
)

type AgentJournalServiceClient interface {
	Query(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
}

type agentJournalServiceClient struct{ connection grpc.ClientConnInterface }

func NewAgentJournalServiceClient(connection grpc.ClientConnInterface) AgentJournalServiceClient {
	return &agentJournalServiceClient{connection: connection}
}

func (client *agentJournalServiceClient) Query(ctx context.Context, input *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := client.connection.Invoke(ctx, AgentJournalServiceQueryMethod, input, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

type AgentJournalServiceServer interface {
	Query(context.Context, *structpb.Struct) (*structpb.Struct, error)
}

func RegisterAgentJournalServiceServer(server grpc.ServiceRegistrar, implementation AgentJournalServiceServer) {
	server.RegisterService(&agentJournalServiceDescription, implementation)
}

func agentJournalServiceQueryHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(structpb.Struct)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentJournalServiceServer).Query(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: AgentJournalServiceQueryMethod}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentJournalServiceServer).Query(ctx, request.(*structpb.Struct))
	}
	return interceptor(ctx, request, info, handler)
}

var agentJournalServiceDescription = grpc.ServiceDesc{
	ServiceName: agentJournalServiceName,
	HandlerType: (*AgentJournalServiceServer)(nil),
	Methods:     []grpc.MethodDesc{{MethodName: "Query", Handler: agentJournalServiceQueryHandler}},
}
