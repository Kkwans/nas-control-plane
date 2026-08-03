package agentsocket

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/system"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestAgentSystemServiceExposesDNSLifecycleAndPublicEgress(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterAgentSystemServiceServer(server, NewSystemService(fakeSystemProvider{
		dns:              system.DNSCapability{Backend: system.DNSBackendSystemdResolved, CanPreview: true, CanConfirm: true, CanRollback: true, Nameservers: []string{}},
		preview:          system.DNSChangePreview{PreviewID: "preview-1", RequiresConfirm: true, RollbackAvailable: true},
		confirm:          system.DNSChangeResult{ChangeID: "change-1", Applied: true, RollbackAvailable: true},
		rollback:         system.DNSChangeResult{ChangeID: "change-1", Applied: false},
		egressCapability: system.PublicEgressCapability{Configured: true, Status: "not-checked", RequiresUserAction: true},
		egress:           system.PublicEgressResult{Status: system.CapabilityStateAvailable, Address: "1.1.1.1", CheckedAt: time.Now().UTC()},
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

	client := NewAgentSystemServiceClient(connection)
	dns, err := client.GetDNSCapability(context.Background(), &emptypb.Empty{})
	if err != nil || dns.AsMap()["backend"] != system.DNSBackendSystemdResolved {
		t.Fatalf("DNS capability = %#v, error = %v", dns.AsMap(), err)
	}
	payload, _ := structpb.NewStruct(map[string]any{
		"interface": "eth0", "nameservers": []string{"1.1.1.1"}, "searchDomains": []string{},
	})
	preview, err := client.PreviewDNSChange(context.Background(), payload)
	if err != nil || preview.AsMap()["previewId"] != "preview-1" {
		t.Fatalf("DNS preview = %#v, error = %v", preview.AsMap(), err)
	}
	confirmed, _ := structpb.NewStruct(map[string]any{"previewId": "preview-1", "confirmed": true})
	if _, err := client.ConfirmDNSChange(context.Background(), confirmed); err != nil {
		t.Fatalf("DNS confirm error = %v", err)
	}
	rollback, _ := structpb.NewStruct(map[string]any{"changeId": "change-1"})
	if _, err := client.RollbackDNSChange(context.Background(), rollback); err != nil {
		t.Fatalf("DNS rollback error = %v", err)
	}
	capability, err := client.GetPublicEgressCapability(context.Background(), &emptypb.Empty{})
	if err != nil || capability.AsMap()["requiresUserAction"] != true {
		t.Fatalf("egress capability = %#v, error = %v", capability.AsMap(), err)
	}
	egress, err := client.DetectPublicEgress(context.Background(), &emptypb.Empty{})
	if err != nil || egress.AsMap()["address"] != "1.1.1.1" {
		t.Fatalf("egress result = %#v, error = %v", egress.AsMap(), err)
	}
}

func TestAgentSystemServiceRequiresExplicitDNSConfirmation(t *testing.T) {
	service := NewSystemService(fakeSystemProvider{})
	payload, _ := structpb.NewStruct(map[string]any{"previewId": "preview-1", "confirmed": false})
	_, err := service.ConfirmDNSChange(context.Background(), payload)
	if grpcstatus.Code(err) != codes.FailedPrecondition || grpcstatus.Convert(err).Message() != "AGENT_DNS_CONFIRMATION_REQUIRED" {
		t.Fatalf("error = %v", err)
	}
}

type fakeSystemProvider struct {
	dns              system.DNSCapability
	preview          system.DNSChangePreview
	confirm          system.DNSChangeResult
	rollback         system.DNSChangeResult
	egressCapability system.PublicEgressCapability
	egress           system.PublicEgressResult
	err              error
}

func (f fakeSystemProvider) CollectDNSCapability(context.Context) (system.DNSCapability, error) {
	return f.dns, f.err
}
func (f fakeSystemProvider) PreviewDNSChange(context.Context, system.DNSChangeRequest) (system.DNSChangePreview, error) {
	return f.preview, f.err
}
func (f fakeSystemProvider) ConfirmDNSChange(context.Context, system.DNSChangeConfirmation) (system.DNSChangeResult, error) {
	return f.confirm, f.err
}
func (f fakeSystemProvider) RollbackDNSChange(context.Context, system.DNSRollbackRequest) (system.DNSChangeResult, error) {
	return f.rollback, f.err
}
func (f fakeSystemProvider) GetPublicEgressCapability(context.Context) (system.PublicEgressCapability, error) {
	return f.egressCapability, f.err
}
func (f fakeSystemProvider) DetectPublicEgress(context.Context) (system.PublicEgressResult, error) {
	return f.egress, f.err
}
