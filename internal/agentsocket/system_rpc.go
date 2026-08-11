package agentsocket

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/Kkwans/nas-control-plane/internal/system"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	agentSystemServiceName                         = "ncp.agent.v1.AgentSystemService"
	AgentSystemServiceGetDNSCapabilityFullMethod   = "/ncp.agent.v1.AgentSystemService/GetDNSCapability"
	AgentSystemServicePreviewDNSChangeFullMethod   = "/ncp.agent.v1.AgentSystemService/PreviewDNSChange"
	AgentSystemServiceConfirmDNSChangeFullMethod   = "/ncp.agent.v1.AgentSystemService/ConfirmDNSChange"
	AgentSystemServiceRollbackDNSChangeFullMethod  = "/ncp.agent.v1.AgentSystemService/RollbackDNSChange"
	AgentSystemServiceGetPublicEgressFullMethod    = "/ncp.agent.v1.AgentSystemService/GetPublicEgressCapability"
	AgentSystemServiceDetectPublicEgressFullMethod = "/ncp.agent.v1.AgentSystemService/DetectPublicEgress"
)

type AgentSystemServiceClient interface {
	GetDNSCapability(context.Context, *emptypb.Empty, ...grpc.CallOption) (*structpb.Struct, error)
	PreviewDNSChange(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	ConfirmDNSChange(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	RollbackDNSChange(context.Context, *structpb.Struct, ...grpc.CallOption) (*structpb.Struct, error)
	GetPublicEgressCapability(context.Context, *emptypb.Empty, ...grpc.CallOption) (*structpb.Struct, error)
	DetectPublicEgress(context.Context, *emptypb.Empty, ...grpc.CallOption) (*structpb.Struct, error)
}

type agentSystemServiceClient struct {
	connection grpc.ClientConnInterface
}

func NewAgentSystemServiceClient(connection grpc.ClientConnInterface) AgentSystemServiceClient {
	return &agentSystemServiceClient{connection: connection}
}

func (c *agentSystemServiceClient) GetDNSCapability(ctx context.Context, in *emptypb.Empty, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, AgentSystemServiceGetDNSCapabilityFullMethod, in, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *agentSystemServiceClient) PreviewDNSChange(ctx context.Context, in *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, AgentSystemServicePreviewDNSChangeFullMethod, in, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *agentSystemServiceClient) ConfirmDNSChange(ctx context.Context, in *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, AgentSystemServiceConfirmDNSChangeFullMethod, in, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *agentSystemServiceClient) RollbackDNSChange(ctx context.Context, in *structpb.Struct, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, AgentSystemServiceRollbackDNSChangeFullMethod, in, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *agentSystemServiceClient) GetPublicEgressCapability(ctx context.Context, in *emptypb.Empty, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, AgentSystemServiceGetPublicEgressFullMethod, in, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *agentSystemServiceClient) DetectPublicEgress(ctx context.Context, in *emptypb.Empty, options ...grpc.CallOption) (*structpb.Struct, error) {
	response := new(structpb.Struct)
	if err := c.connection.Invoke(ctx, AgentSystemServiceDetectPublicEgressFullMethod, in, response, options...); err != nil {
		return nil, err
	}
	return response, nil
}

type AgentSystemServiceServer interface {
	GetDNSCapability(context.Context, *emptypb.Empty) (*structpb.Struct, error)
	PreviewDNSChange(context.Context, *structpb.Struct) (*structpb.Struct, error)
	ConfirmDNSChange(context.Context, *structpb.Struct) (*structpb.Struct, error)
	RollbackDNSChange(context.Context, *structpb.Struct) (*structpb.Struct, error)
	GetPublicEgressCapability(context.Context, *emptypb.Empty) (*structpb.Struct, error)
	DetectPublicEgress(context.Context, *emptypb.Empty) (*structpb.Struct, error)
}

func RegisterAgentSystemServiceServer(server grpc.ServiceRegistrar, implementation AgentSystemServiceServer) {
	server.RegisterService(&agentSystemServiceDescription, implementation)
}

type SystemProvider interface {
	CollectDNSCapability(context.Context) (system.DNSCapability, error)
	PreviewDNSChange(context.Context, system.DNSChangeRequest) (system.DNSChangePreview, error)
	ConfirmDNSChange(context.Context, system.DNSChangeConfirmation) (system.DNSChangeResult, error)
	RollbackDNSChange(context.Context, system.DNSRollbackRequest) (system.DNSChangeResult, error)
	GetPublicEgressCapability(context.Context) (system.PublicEgressCapability, error)
	DetectPublicEgress(context.Context) (system.PublicEgressResult, error)
}

// LiveSystemProvider 是 Agent 注册 system RPC 时可直接使用的只读默认实现。
// DNS 写入仍必须由外部注入已审计的 backend adapter；默认值不会修改宿主机文件。
type LiveSystemProvider struct {
	Environment    system.Environment
	DNSController  system.DNSChangeController
	EgressDetector *system.PublicEgressDetector
	EgressEndpoint string

	dnsMu                sync.Mutex
	dnsControllerFactory func() (system.DNSChangeController, error)
}

func NewLiveSystemProvider(environment system.Environment, egressEndpoint string, dnsController system.DNSChangeController) *LiveSystemProvider {
	provider, _ := NewLiveSystemProviderWithProxy(environment, egressEndpoint, "", dnsController)
	return provider
}

func (p *LiveSystemProvider) SetDNSControllerFactory(factory func() (system.DNSChangeController, error)) {
	if p == nil {
		return
	}
	p.dnsMu.Lock()
	p.dnsControllerFactory = factory
	p.dnsMu.Unlock()
}

func NewLiveSystemProviderWithProxy(environment system.Environment, egressEndpoint, outboundProxy string, dnsController system.DNSChangeController) (*LiveSystemProvider, error) {
	if environment == nil {
		environment = system.NewOSEnvironment()
	}
	egressDetector, err := system.NewPublicEgressDetectorWithProxy(egressEndpoint, outboundProxy)
	if err != nil {
		return nil, err
	}
	return &LiveSystemProvider{
		Environment: environment, DNSController: dnsController,
		EgressDetector: egressDetector, EgressEndpoint: egressEndpoint,
	}, nil
}

func (p *LiveSystemProvider) CollectDNSCapability(ctx context.Context) (system.DNSCapability, error) {
	if p == nil || p.Environment == nil {
		return system.DNSCapability{}, errors.New("DNS_SOURCE_UNAVAILABLE")
	}
	capability := system.ProbeDNS(ctx, p.Environment)
	// 探测到管理后端并不等于具备安全写入能力。只有显式注入实现了
	// 预览、确认、回滚和当前配置读取契约的控制器时，才向 Console 开放修改入口。
	controller := p.resolveDNSController()
	reader, readable := controller.(system.DNSStateReader)
	if controller != nil && readable && capability.Detected {
		managedState, err := reader.CurrentDNSState(ctx)
		if err != nil {
			capability.State = system.CapabilityStateDegraded
			capability.ErrorCode = "DNS_MANAGED_STATE_UNAVAILABLE"
			return capability, nil
		}
		capability.ConfiguredNameservers = append([]string(nil), managedState.Nameservers...)
		capability.State = system.CapabilityStateAvailable
		capability.ReadOnly = false
		capability.CanPreview = true
		capability.CanConfirm = true
		capability.CanRollback = true
		capability.ErrorCode = ""
	}
	return capability, nil
}

func (p *LiveSystemProvider) PreviewDNSChange(ctx context.Context, request system.DNSChangeRequest) (system.DNSChangePreview, error) {
	controller := p.dnsController(ctx)
	return controller.Preview(ctx, request)
}

func (p *LiveSystemProvider) ConfirmDNSChange(ctx context.Context, request system.DNSChangeConfirmation) (system.DNSChangeResult, error) {
	controller := p.dnsController(ctx)
	return controller.Confirm(ctx, request)
}

func (p *LiveSystemProvider) RollbackDNSChange(ctx context.Context, request system.DNSRollbackRequest) (system.DNSChangeResult, error) {
	controller := p.dnsController(ctx)
	return controller.Rollback(ctx, request)
}

func (p *LiveSystemProvider) dnsController(ctx context.Context) system.DNSChangeController {
	if controller := p.resolveDNSController(); controller != nil {
		return controller
	}
	capability := system.DNSCapability{Backend: system.DNSBackendUnknown}
	if p != nil && p.Environment != nil {
		capability = system.ProbeDNS(ctx, p.Environment)
	}
	return system.NewReadOnlyDNSController(capability)
}

func (p *LiveSystemProvider) resolveDNSController() system.DNSChangeController {
	if p == nil {
		return nil
	}
	p.dnsMu.Lock()
	defer p.dnsMu.Unlock()
	if p.DNSController != nil {
		return p.DNSController
	}
	if p.dnsControllerFactory == nil {
		return nil
	}
	controller, err := p.dnsControllerFactory()
	if err != nil || controller == nil {
		return nil
	}
	p.DNSController = controller
	return controller
}

func (p *LiveSystemProvider) GetPublicEgressCapability(context.Context) (system.PublicEgressCapability, error) {
	if p == nil {
		return system.PublicEgressCapability{}, errors.New("PUBLIC_EGRESS_ENDPOINT_NOT_CONFIGURED")
	}
	return system.NewPublicEgressCapability(p.EgressEndpoint), nil
}

func (p *LiveSystemProvider) DetectPublicEgress(ctx context.Context) (system.PublicEgressResult, error) {
	if p == nil || p.EgressDetector == nil {
		return system.PublicEgressResult{Status: system.CapabilityStateUnavailable, ErrorCode: "PUBLIC_EGRESS_ENDPOINT_NOT_CONFIGURED"}, nil
	}
	return p.EgressDetector.Detect(ctx), nil
}

type systemService struct {
	provider SystemProvider
}

func NewSystemService(provider SystemProvider) AgentSystemServiceServer {
	return &systemService{provider: provider}
}

func (s *systemService) GetDNSCapability(ctx context.Context, _ *emptypb.Empty) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_DNS_CAPABILITY_UNAVAILABLE")
	}
	value, err := s.provider.CollectDNSCapability(ctx)
	if err != nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_DNS_CAPABILITY_UNAVAILABLE")
	}
	return dashboardStruct(value, "AGENT_DNS_CAPABILITY_RESPONSE_INVALID")
}

func (s *systemService) PreviewDNSChange(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_DNS_CHANGE_UNAVAILABLE")
	}
	var input system.DNSChangeRequest
	if err := decodeSystemStruct(request, &input); err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "AGENT_DNS_REQUEST_INVALID")
	}
	value, err := s.provider.PreviewDNSChange(ctx, input)
	if err != nil {
		return nil, grpcstatus.Error(codes.FailedPrecondition, systemErrorCode(err, "AGENT_DNS_PREVIEW_FAILED"))
	}
	return dashboardStruct(value, "AGENT_DNS_PREVIEW_RESPONSE_INVALID")
}

func (s *systemService) ConfirmDNSChange(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_DNS_CHANGE_UNAVAILABLE")
	}
	var input system.DNSChangeConfirmation
	if err := decodeSystemStruct(request, &input); err != nil {
		return nil, grpcstatus.Error(codes.InvalidArgument, "AGENT_DNS_REQUEST_INVALID")
	}
	if !input.Confirmed {
		return nil, grpcstatus.Error(codes.FailedPrecondition, "AGENT_DNS_CONFIRMATION_REQUIRED")
	}
	value, err := s.provider.ConfirmDNSChange(ctx, input)
	if err != nil {
		return nil, grpcstatus.Error(codes.FailedPrecondition, systemErrorCode(err, "AGENT_DNS_CONFIRM_FAILED"))
	}
	return dashboardStruct(value, "AGENT_DNS_CONFIRM_RESPONSE_INVALID")
}

func (s *systemService) RollbackDNSChange(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_DNS_CHANGE_UNAVAILABLE")
	}
	var input system.DNSRollbackRequest
	if err := decodeSystemStruct(request, &input); err != nil || input.ChangeID == "" {
		return nil, grpcstatus.Error(codes.InvalidArgument, "AGENT_DNS_ROLLBACK_REQUEST_INVALID")
	}
	value, err := s.provider.RollbackDNSChange(ctx, input)
	if err != nil {
		return nil, grpcstatus.Error(codes.FailedPrecondition, systemErrorCode(err, "AGENT_DNS_ROLLBACK_FAILED"))
	}
	return dashboardStruct(value, "AGENT_DNS_ROLLBACK_RESPONSE_INVALID")
}

func (s *systemService) GetPublicEgressCapability(ctx context.Context, _ *emptypb.Empty) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_PUBLIC_EGRESS_UNAVAILABLE")
	}
	value, err := s.provider.GetPublicEgressCapability(ctx)
	if err != nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_PUBLIC_EGRESS_UNAVAILABLE")
	}
	return dashboardStruct(value, "AGENT_PUBLIC_EGRESS_RESPONSE_INVALID")
}

func (s *systemService) DetectPublicEgress(ctx context.Context, _ *emptypb.Empty) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, grpcstatus.Error(codes.Canceled, "AGENT_RPC_CANCELED")
	}
	if s.provider == nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_PUBLIC_EGRESS_UNAVAILABLE")
	}
	value, err := s.provider.DetectPublicEgress(ctx)
	if err != nil {
		return nil, grpcstatus.Error(codes.Unavailable, "AGENT_PUBLIC_EGRESS_UNAVAILABLE")
	}
	return dashboardStruct(value, "AGENT_PUBLIC_EGRESS_RESPONSE_INVALID")
}

func CollectDNSCapability(ctx context.Context, socketPath string) (system.DNSCapability, error) {
	connection, err := dialSocket(socketPath)
	if err != nil {
		return system.DNSCapability{}, err
	}
	defer connection.Close()
	response, err := NewAgentSystemServiceClient(connection).GetDNSCapability(ctx, &emptypb.Empty{})
	if err != nil {
		return system.DNSCapability{}, rpcError(err)
	}
	var value system.DNSCapability
	if err := decodeDashboardResponse(response, &value); err != nil {
		return system.DNSCapability{}, err
	}
	if value.Nameservers == nil {
		value.Nameservers = []string{}
	}
	if value.ConfiguredNameservers == nil {
		value.ConfiguredNameservers = []string{}
	}
	return value, nil
}

func PreviewDNSChange(ctx context.Context, socketPath string, request system.DNSChangeRequest) (system.DNSChangePreview, error) {
	connection, err := dialSocket(socketPath)
	if err != nil {
		return system.DNSChangePreview{}, err
	}
	defer connection.Close()
	payload, err := dnsChangeRequestStruct(request)
	if err != nil {
		return system.DNSChangePreview{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := NewAgentSystemServiceClient(connection).PreviewDNSChange(ctx, payload)
	if err != nil {
		return system.DNSChangePreview{}, systemRPCError(err)
	}
	var value system.DNSChangePreview
	if err := decodeDashboardResponse(response, &value); err != nil {
		return system.DNSChangePreview{}, err
	}
	return value, nil
}

func dnsChangeRequestStruct(request system.DNSChangeRequest) (*structpb.Struct, error) {
	return structpb.NewStruct(map[string]any{
		"interface": request.Interface, "connectionId": request.ConnectionID,
		"nameservers": stringSliceValues(request.Nameservers), "searchDomains": stringSliceValues(request.SearchDomains),
	})
}

func stringSliceValues(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

func ConfirmDNSChange(ctx context.Context, socketPath string, request system.DNSChangeConfirmation) (system.DNSChangeResult, error) {
	connection, err := dialSocket(socketPath)
	if err != nil {
		return system.DNSChangeResult{}, err
	}
	defer connection.Close()
	payload, err := structpb.NewStruct(map[string]any{"previewId": request.PreviewID, "confirmed": request.Confirmed})
	if err != nil {
		return system.DNSChangeResult{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := NewAgentSystemServiceClient(connection).ConfirmDNSChange(ctx, payload)
	if err != nil {
		return system.DNSChangeResult{}, systemRPCError(err)
	}
	var value system.DNSChangeResult
	if err := decodeDashboardResponse(response, &value); err != nil {
		return system.DNSChangeResult{}, err
	}
	return value, nil
}

func RollbackDNSChange(ctx context.Context, socketPath string, request system.DNSRollbackRequest) (system.DNSChangeResult, error) {
	connection, err := dialSocket(socketPath)
	if err != nil {
		return system.DNSChangeResult{}, err
	}
	defer connection.Close()
	payload, err := structpb.NewStruct(map[string]any{"changeId": request.ChangeID})
	if err != nil {
		return system.DNSChangeResult{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := NewAgentSystemServiceClient(connection).RollbackDNSChange(ctx, payload)
	if err != nil {
		return system.DNSChangeResult{}, systemRPCError(err)
	}
	var value system.DNSChangeResult
	if err := decodeDashboardResponse(response, &value); err != nil {
		return system.DNSChangeResult{}, err
	}
	return value, nil
}

func GetPublicEgressCapability(ctx context.Context, socketPath string) (system.PublicEgressCapability, error) {
	connection, err := dialSocket(socketPath)
	if err != nil {
		return system.PublicEgressCapability{}, err
	}
	defer connection.Close()
	response, err := NewAgentSystemServiceClient(connection).GetPublicEgressCapability(ctx, &emptypb.Empty{})
	if err != nil {
		return system.PublicEgressCapability{}, rpcError(err)
	}
	var value system.PublicEgressCapability
	if err := decodeDashboardResponse(response, &value); err != nil {
		return system.PublicEgressCapability{}, err
	}
	return value, nil
}

func DetectPublicEgress(ctx context.Context, socketPath string) (system.PublicEgressResult, error) {
	connection, err := dialSocket(socketPath)
	if err != nil {
		return system.PublicEgressResult{}, err
	}
	defer connection.Close()
	response, err := NewAgentSystemServiceClient(connection).DetectPublicEgress(ctx, &emptypb.Empty{})
	if err != nil {
		return system.PublicEgressResult{}, rpcError(err)
	}
	var value system.PublicEgressResult
	if err := decodeDashboardResponse(response, &value); err != nil {
		return system.PublicEgressResult{}, err
	}
	return value, nil
}

func decodeSystemStruct(response *structpb.Struct, destination any) error {
	if response == nil {
		return errors.New("system request is required")
	}
	return decodeDashboardResponse(response, destination)
}

func systemErrorCode(err error, fallback string) string {
	if err == nil {
		return fallback
	}
	if value := err.Error(); value != "" && len(value) < 128 && !containsWhitespace(value) {
		return value
	}
	return fallback
}

func systemRPCError(err error) error {
	if err == nil {
		return nil
	}
	message := grpcstatus.Convert(err).Message()
	if strings.HasPrefix(message, "DNS_") || strings.HasPrefix(message, "UGOS_DNS_") {
		return coded(message, err)
	}
	return rpcError(err)
}

func containsWhitespace(value string) bool {
	for _, character := range value {
		if character == ' ' || character == '\n' || character == '\t' || character == '\r' {
			return true
		}
	}
	return false
}

func agentSystemServiceGetDNSCapabilityHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(emptypb.Empty)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentSystemServiceServer).GetDNSCapability(ctx, request)
	}
	return systemUnaryInterceptor(ctx, request, server, AgentSystemServiceGetDNSCapabilityFullMethod, interceptor, func() (any, error) {
		return server.(AgentSystemServiceServer).GetDNSCapability(ctx, request)
	})
}

func agentSystemServicePreviewDNSChangeHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(structpb.Struct)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentSystemServiceServer).PreviewDNSChange(ctx, request)
	}
	return systemUnaryInterceptor(ctx, request, server, AgentSystemServicePreviewDNSChangeFullMethod, interceptor, func() (any, error) {
		return server.(AgentSystemServiceServer).PreviewDNSChange(ctx, request)
	})
}

func agentSystemServiceConfirmDNSChangeHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(structpb.Struct)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentSystemServiceServer).ConfirmDNSChange(ctx, request)
	}
	return systemUnaryInterceptor(ctx, request, server, AgentSystemServiceConfirmDNSChangeFullMethod, interceptor, func() (any, error) {
		return server.(AgentSystemServiceServer).ConfirmDNSChange(ctx, request)
	})
}

func agentSystemServiceRollbackDNSChangeHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(structpb.Struct)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentSystemServiceServer).RollbackDNSChange(ctx, request)
	}
	return systemUnaryInterceptor(ctx, request, server, AgentSystemServiceRollbackDNSChangeFullMethod, interceptor, func() (any, error) {
		return server.(AgentSystemServiceServer).RollbackDNSChange(ctx, request)
	})
}

func agentSystemServiceGetPublicEgressHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(emptypb.Empty)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentSystemServiceServer).GetPublicEgressCapability(ctx, request)
	}
	return systemUnaryInterceptor(ctx, request, server, AgentSystemServiceGetPublicEgressFullMethod, interceptor, func() (any, error) {
		return server.(AgentSystemServiceServer).GetPublicEgressCapability(ctx, request)
	})
}

func agentSystemServiceDetectPublicEgressHandler(server any, ctx context.Context, decoder func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(emptypb.Empty)
	if err := decoder(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(AgentSystemServiceServer).DetectPublicEgress(ctx, request)
	}
	return systemUnaryInterceptor(ctx, request, server, AgentSystemServiceDetectPublicEgressFullMethod, interceptor, func() (any, error) {
		return server.(AgentSystemServiceServer).DetectPublicEgress(ctx, request)
	})
}

func systemUnaryInterceptor(ctx context.Context, request any, server any, method string, interceptor grpc.UnaryServerInterceptor, invoke func() (any, error)) (any, error) {
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: method}
	return interceptor(ctx, request, info, func(context.Context, any) (any, error) { return invoke() })
}

var agentSystemServiceDescription = grpc.ServiceDesc{
	ServiceName: agentSystemServiceName,
	HandlerType: (*AgentSystemServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "GetDNSCapability", Handler: agentSystemServiceGetDNSCapabilityHandler},
		{MethodName: "PreviewDNSChange", Handler: agentSystemServicePreviewDNSChangeHandler},
		{MethodName: "ConfirmDNSChange", Handler: agentSystemServiceConfirmDNSChangeHandler},
		{MethodName: "RollbackDNSChange", Handler: agentSystemServiceRollbackDNSChangeHandler},
		{MethodName: "GetPublicEgressCapability", Handler: agentSystemServiceGetPublicEgressHandler},
		{MethodName: "DetectPublicEgress", Handler: agentSystemServiceDetectPublicEgressHandler},
	},
}
