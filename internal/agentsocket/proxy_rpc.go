package agentsocket

import (
	"context"
	"errors"

	"github.com/Kkwans/nas-control-plane/internal/system"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	agentProxyServiceName                          = "ncp.agent.v1.AgentProxyService"
	AgentProxyServiceGetMihomoCapabilityFullMethod = "/ncp.agent.v1.AgentProxyService/GetMihomoCapability"
	AgentProxyServiceGetMihomoCapabilityMethod     = AgentProxyServiceGetMihomoCapabilityFullMethod
	AgentProxyServiceInvokeMihomoFullMethod        = "/ncp.agent.v1.AgentProxyService/InvokeMihomo"
)

type AgentProxyServiceClient interface {
	GetMihomoCapability(context.Context, *emptypb.Empty, ...grpc.CallOption) (*structpb.Struct, error)
	InvokeMihomo(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
}

type agentProxyServiceClient struct {
	connection grpc.ClientConnInterface
}

func NewAgentProxyServiceClient(connection grpc.ClientConnInterface) AgentProxyServiceClient {
	return &agentProxyServiceClient{connection: connection}
}

func (c *agentProxyServiceClient) GetMihomoCapability(ctx context.Context, in *emptypb.Empty, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, AgentProxyServiceGetMihomoCapabilityMethod, in, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *agentProxyServiceClient) InvokeMihomo(ctx context.Context, in *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, AgentProxyServiceInvokeMihomoFullMethod, in, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

type AgentProxyServiceServer interface {
	GetMihomoCapability(context.Context, *emptypb.Empty) (*structpb.Struct, error)
	InvokeMihomo(context.Context, *structpb.Struct) (*structpb.Struct, error)
}

func RegisterAgentProxyServiceServer(server grpc.ServiceRegistrar, implementation AgentProxyServiceServer) {
	server.RegisterService(&agentProxyServiceDescription, implementation)
}

type ProxyProvider interface {
	ProbeMihomo(context.Context) (system.MihomoCapability, error)
	InvokeMihomo(context.Context, system.MihomoInvokeRequest) (system.MihomoInvokeResult, error)
}

// LiveProxyProvider 保持 Mihomo controller endpoint/token 在 Agent 内部；RPC 与 HTTP
// 只接触 capability 和 allowlisted operation，不会把认证信息发送到浏览器。
type LiveProxyProvider struct {
	Environment system.Environment
	Controller  *system.MihomoControllerClient
	Endpoint    string
}

func NewLiveProxyProvider(environment system.Environment, endpoint, token string) (*LiveProxyProvider, error) {
	if environment == nil {
		environment = system.NewOSEnvironment()
	}
	controller, err := system.NewMihomoControllerClient(endpoint, token)
	if err != nil {
		return nil, err
	}
	return &LiveProxyProvider{Environment: environment, Controller: controller, Endpoint: endpoint}, nil
}

func (p *LiveProxyProvider) ProbeMihomo(ctx context.Context) (system.MihomoCapability, error) {
	if p == nil || p.Environment == nil {
		return system.MihomoCapability{}, errors.New("MIHOMO_CONTROLLER_UNAVAILABLE")
	}
	return system.ProbeMihomo(ctx, p.Environment, p.Endpoint), nil
}

func (p *LiveProxyProvider) InvokeMihomo(ctx context.Context, request system.MihomoInvokeRequest) (system.MihomoInvokeResult, error) {
	if p == nil || p.Controller == nil {
		return system.MihomoInvokeResult{}, errors.New("MIHOMO_CONTROLLER_UNAVAILABLE")
	}
	return p.Controller.Invoke(ctx, request)
}

type proxyService struct {
	provider ProxyProvider
}

func NewProxyService(provider ProxyProvider) AgentProxyServiceServer {
	return &proxyService{provider: provider}
}

func (s *proxyService) GetMihomoCapability(ctx context.Context, _ *emptypb.Empty) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_MIHOMO_UNAVAILABLE")
	}
	value, err := s.provider.ProbeMihomo(ctx)
	if err != nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_MIHOMO_UNAVAILABLE")
	}
	return dashboardStruct(value, "AGENT_MIHOMO_RESPONSE_INVALID")
}

func (s *proxyService) InvokeMihomo(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_MIHOMO_UNAVAILABLE")
	}
	var input system.MihomoInvokeRequest
	if request == nil || decodeDashboardResponse(request, &input) != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "AGENT_MIHOMO_REQUEST_INVALID")
	}
	if err := system.ValidateMihomoInvokeRequest(input); err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, err.Error())
	}
	value, err := s.provider.InvokeMihomo(ctx, input)
	if err != nil {
		return nil, grpcstatus.Error(codes.FailedPrecondition, proxyErrorCode(err))
	}
	return dashboardStruct(value, "AGENT_MIHOMO_INVOKE_RESPONSE_INVALID")
}

func ProbeMihomo(ctx context.Context, socketPath string) (system.MihomoCapability, error) {
	connection, err := dialSocket(socketPath)
	if err != nil {
		return system.MihomoCapability{}, err
	}
	defer connection.Close()
	response, err := NewAgentProxyServiceClient(connection).GetMihomoCapability(ctx, &emptypb.Empty{})
	if err != nil {
		return system.MihomoCapability{}, rpcError(err)
	}
	var value system.MihomoCapability
	if err := decodeDashboardResponse(response, &value); err != nil {
		return system.MihomoCapability{}, err
	}
	if value.Evidence == nil {
		value.Evidence = []system.CapabilityEvidence{}
	}
	if value.Warnings == nil {
		value.Warnings = []system.ProbeWarning{}
	}
	if value.Controller.Operations == nil {
		value.Controller.Operations = []string{}
	}
	return value, nil
}

func InvokeMihomo(ctx context.Context, socketPath string, request system.MihomoInvokeRequest) (system.MihomoInvokeResult, error) {
	if err := system.ValidateMihomoInvokeRequest(request); err != nil {
		return system.MihomoInvokeResult{}, coded("AGENT_MIHOMO_REQUEST_INVALID", err)
	}
	connection, err := dialSocket(socketPath)
	if err != nil {
		return system.MihomoInvokeResult{}, err
	}
	defer connection.Close()
	payload, err := structpb.NewStruct(map[string]any{
		"operation": string(request.Operation),
		"group":     request.Group,
		"proxy":     request.Proxy,
	})
	if err != nil {
		return system.MihomoInvokeResult{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := NewAgentProxyServiceClient(connection).InvokeMihomo(ctx, payload)
	if err != nil {
		return system.MihomoInvokeResult{}, rpcError(err)
	}
	var value system.MihomoInvokeResult
	if err := decodeDashboardResponse(response, &value); err != nil {
		return system.MihomoInvokeResult{}, err
	}
	if value.Data == nil {
		value.Data = []byte("null")
	}
	return value, nil
}

func proxyErrorCode(err error) string {
	if err == nil {
		return "AGENT_MIHOMO_INVOKE_FAILED"
	}
	if value := err.Error(); value != "" && len(value) < 128 && !containsWhitespace(value) {
		return value
	}
	return "AGENT_MIHOMO_INVOKE_FAILED"
}

func agentProxyServiceGetMihomoCapabilityHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(emptypb.Empty)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentProxyServiceServer).GetMihomoCapability(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: AgentProxyServiceGetMihomoCapabilityMethod}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentProxyServiceServer).GetMihomoCapability(ctx, request.(*emptypb.Empty))
	}
	return interceptor(ctx, request, info, handler)
}

func agentProxyServiceInvokeMihomoHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(structpb.Struct)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentProxyServiceServer).InvokeMihomo(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: AgentProxyServiceInvokeMihomoFullMethod}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(AgentProxyServiceServer).InvokeMihomo(ctx, request.(*structpb.Struct))
	}
	return interceptor(ctx, request, info, handler)
}

var agentProxyServiceDescription = grpc.ServiceDesc{
	ServiceName: agentProxyServiceName,
	HandlerType: (*AgentProxyServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "GetMihomoCapability", Handler: agentProxyServiceGetMihomoCapabilityHandler},
		{MethodName: "InvokeMihomo", Handler: agentProxyServiceInvokeMihomoHandler},
	},
}
