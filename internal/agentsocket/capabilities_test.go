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
)

func TestCapabilityServiceReturnsReadOnlyProbeResult(t *testing.T) {
	service := newStatusServiceWithCollector(fakeCapabilityCollector{capabilities: system.Capabilities{
		Hostname:     "DH4300-PLUS",
		Architecture: "arm64",
		Docker:       true,
		DataVolumes:  []string{"/volume1", "/volume2"},
	}})

	response, err := service.GetCapabilities(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatalf("GetCapabilities() error = %v", err)
	}
	if response.GetFields()["hostname"].GetStringValue() != "DH4300-PLUS" {
		t.Fatalf("hostname = %q", response.GetFields()["hostname"].GetStringValue())
	}
	if !response.GetFields()["docker"].GetBoolValue() {
		t.Fatal("docker should be true")
	}
}

func TestAgentProbeServiceExposesCapabilitiesOverGRPC(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterAgentProbeServiceServer(server, newStatusServiceWithCollector(fakeCapabilityCollector{capabilities: system.Capabilities{
		Hostname:     "DH4300-PLUS",
		Architecture: "arm64",
	}}))
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	connection, err := grpc.DialContext(context.Background(), "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	response, err := NewAgentProbeServiceClient(connection).GetCapabilities(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatalf("GetCapabilities() error = %v", err)
	}
	if response.GetFields()["architecture"].GetStringValue() != "arm64" {
		t.Fatalf("architecture = %q", response.GetFields()["architecture"].GetStringValue())
	}
}

type fakeCapabilityCollector struct {
	capabilities system.Capabilities
	err          error
}

func (f fakeCapabilityCollector) Collect(context.Context) (system.Capabilities, error) {
	return f.capabilities, f.err
}
