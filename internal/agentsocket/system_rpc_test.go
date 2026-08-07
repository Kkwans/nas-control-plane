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
		egress:           system.PublicEgressResult{Status: system.CapabilityStateAvailable, Address: "1.1.1.1", Country: "CN", ISP: "Example ISP", ASN: "4809", CheckedAt: time.Now().UTC()},
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
	if err != nil || egress.AsMap()["address"] != "1.1.1.1" || egress.AsMap()["asn"] != "4809" {
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

func TestLiveSystemProviderAdvertisesDNSWritesOnlyWithInjectedController(t *testing.T) {
	environment := &dnsCapabilityEnvironment{
		files:    map[string][]byte{"/etc/resolv.conf": []byte("nameserver 1.1.1.1\n")},
		commands: map[string][]byte{"resolvectl status": []byte("Global\n")},
	}
	provider := NewLiveSystemProvider(environment, "", nil)
	capability, err := provider.CollectDNSCapability(context.Background())
	if err != nil {
		t.Fatalf("CollectDNSCapability() error = %v", err)
	}
	if !capability.ReadOnly || capability.CanPreview || capability.ErrorCode != "DNS_WRITE_ADAPTER_UNAVAILABLE" {
		t.Fatalf("capability without controller = %#v", capability)
	}

	provider.DNSController = fakeDNSController{}
	capability, err = provider.CollectDNSCapability(context.Background())
	if err != nil {
		t.Fatalf("CollectDNSCapability() with controller error = %v", err)
	}
	if capability.ReadOnly || !capability.CanPreview || !capability.CanConfirm || !capability.CanRollback || capability.ErrorCode != "" {
		t.Fatalf("capability with controller = %#v", capability)
	}
}

type fakeDNSController struct{}

func (fakeDNSController) Preview(context.Context, system.DNSChangeRequest) (system.DNSChangePreview, error) {
	return system.DNSChangePreview{}, nil
}
func (fakeDNSController) Confirm(context.Context, system.DNSChangeConfirmation) (system.DNSChangeResult, error) {
	return system.DNSChangeResult{}, nil
}
func (fakeDNSController) Rollback(context.Context, system.DNSRollbackRequest) (system.DNSChangeResult, error) {
	return system.DNSChangeResult{}, nil
}

type dnsCapabilityEnvironment struct {
	files    map[string][]byte
	commands map[string][]byte
}

func (f *dnsCapabilityEnvironment) Architecture() string      { return "arm64" }
func (f *dnsCapabilityEnvironment) Hostname() (string, error) { return "nas", nil }
func (f *dnsCapabilityEnvironment) ReadFile(name string) ([]byte, error) {
	if value, ok := f.files[name]; ok {
		return append([]byte{}, value...), nil
	}
	return nil, context.Canceled
}
func (f *dnsCapabilityEnvironment) PathExists(string) bool { return false }
func (f *dnsCapabilityEnvironment) LookPath(name string) (string, error) {
	if name == "resolvectl" {
		return name, nil
	}
	return "", context.Canceled
}
func (f *dnsCapabilityEnvironment) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name
	for _, arg := range args {
		key += " " + arg
	}
	if value, ok := f.commands[key]; ok {
		return append([]byte{}, value...), nil
	}
	return nil, context.Canceled
}
func (f *dnsCapabilityEnvironment) Glob(string) ([]string, error)        { return nil, nil }
func (f *dnsCapabilityEnvironment) NetworkInterfaces() ([]string, error) { return nil, nil }
func (f *dnsCapabilityEnvironment) EffectiveUID() int                    { return 0 }

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
