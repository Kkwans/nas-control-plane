package agentsocket

import (
	"context"
	"net"
	"testing"

	"github.com/Kkwans/nas-control-plane/internal/system"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestAgentProxyServiceAllowsOnlyCapabilityAndAllowlistedOperation(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterAgentProxyServiceServer(server, NewProxyService(fakeProxyProvider{
		capability: system.MihomoCapability{
			Detected: true,
			State:    system.CapabilityStateAvailable,
			Controller: system.MihomoControllerCapability{
				Detected: true, Reachable: true, Operations: []string{string(system.MihomoOperationVersion)},
			},
			Evidence: []system.CapabilityEvidence{}, Warnings: []system.ProbeWarning{},
		},
		result:     system.MihomoInvokeResult{Operation: system.MihomoOperationVersion, StatusCode: 200, Data: []byte(`{"version":"1.0.0"}`)},
		inspection: system.MihomoInspection{Status: system.CapabilityStateAvailable, LocalProxy: system.MihomoLocalProxy{Address: "http://127.0.0.1:7890", Mode: "rule"}},
	}))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })

	connection, err := grpc.DialContext(context.Background(), "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client := NewAgentProxyServiceClient(connection)
	capability, err := client.GetMihomoCapability(context.Background(), &emptypb.Empty{})
	if err != nil || capability.AsMap()["detected"] != true {
		t.Fatalf("capability = %#v, error = %v", capability.AsMap(), err)
	}
	payload, _ := structpb.NewStruct(map[string]any{"operation": "version", "group": "", "proxy": ""})
	result, err := client.InvokeMihomo(context.Background(), payload)
	if err != nil || result.AsMap()["statusCode"] != float64(200) {
		t.Fatalf("invoke result = %#v, error = %v", result.AsMap(), err)
	}
	inspectionRequest, _ := structpb.NewStruct(map[string]any{"force": true})
	inspection, err := client.InspectMihomo(context.Background(), inspectionRequest)
	if err != nil || inspection.AsMap()["status"] != system.CapabilityStateAvailable {
		t.Fatalf("inspection = %#v, error = %v", inspection.AsMap(), err)
	}
	invalid, _ := structpb.NewStruct(map[string]any{"operation": "read-config", "group": "", "proxy": ""})
	if _, err := client.InvokeMihomo(context.Background(), invalid); err == nil {
		t.Fatal("unsupported operation must fail")
	}
}

type fakeProxyProvider struct {
	capability system.MihomoCapability
	result     system.MihomoInvokeResult
	inspection system.MihomoInspection
	err        error
}

func (f fakeProxyProvider) ProbeMihomo(context.Context) (system.MihomoCapability, error) {
	return f.capability, f.err
}
func (f fakeProxyProvider) InvokeMihomo(context.Context, system.MihomoInvokeRequest) (system.MihomoInvokeResult, error) {
	return f.result, f.err
}
func (f fakeProxyProvider) InspectMihomo(context.Context, bool) (system.MihomoInspection, error) {
	return f.inspection, f.err
}
