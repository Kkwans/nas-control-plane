package agentsocket

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/Kkwans/nas-control-plane/internal/system"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestAgentDashboardServiceReturnsHostAndDockerDataOverGRPC(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterAgentDashboardServiceServer(server, newDashboardService(fakeDashboardProvider{
		summary: system.Summary{
			CollectedAt: time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC),
			Host:        system.HostSnapshot{Hostname: "DH4300-PLUS"},
		},
		details: system.Details{
			CollectedAt: time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC),
			Device:      system.DeviceDetails{Hostname: "DH4300-PLUS", Architecture: "arm64"},
			Control: system.ControlDetails{Nodes: []system.ControlNode{{
				ID: "agent", Name: "Root Agent", Status: "ready",
			}}},
		},
		inventory: docker.Inventory{
			CollectedAt: time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC),
			Engine:      docker.EngineInfo{ContainersRunning: 3},
		},
	}))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	connection, err := grpc.DialContext(context.Background(), "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	client := NewAgentDashboardServiceClient(connection)
	summary, err := client.GetSystemSummary(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatalf("GetSystemSummary() error = %v", err)
	}
	if summary.GetFields()["host"].GetStructValue().GetFields()["hostname"].GetStringValue() != "DH4300-PLUS" {
		t.Fatalf("hostname = %#v", summary.AsMap())
	}
	details, err := client.GetSystemDetails(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatalf("GetSystemDetails() error = %v", err)
	}
	if details.GetFields()["device"].GetStructValue().GetFields()["architecture"].GetStringValue() != "arm64" {
		t.Fatalf("details = %#v", details.AsMap())
	}
	nodes := details.GetFields()["control"].GetStructValue().GetFields()["nodes"].GetListValue().GetValues()
	if len(nodes) != 1 || nodes[0].GetStructValue().GetFields()["version"].GetStringValue() != BuildVersion {
		t.Fatalf("control nodes = %#v", details.AsMap())
	}
	inventory, err := client.GetDockerInventory(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatalf("GetDockerInventory() error = %v", err)
	}
	if inventory.GetFields()["engine"].GetStructValue().GetFields()["containersRunning"].GetNumberValue() != 3 {
		t.Fatalf("inventory = %#v", inventory.AsMap())
	}
}

func TestAgentDashboardServiceUsesStableUnavailableCode(t *testing.T) {
	service := newDashboardService(fakeDashboardProvider{summaryErr: errors.New("proc unavailable")})
	_, err := service.GetSystemSummary(context.Background(), &emptypb.Empty{})
	if grpcstatus.Code(err) != codes.Unavailable || grpcstatus.Convert(err).Message() != "AGENT_SYSTEM_SUMMARY_UNAVAILABLE" {
		t.Fatalf("error = %v", err)
	}
}

type fakeDashboardProvider struct {
	summary      system.Summary
	summaryErr   error
	details      system.Details
	detailsErr   error
	inventory    docker.Inventory
	inventoryErr error
}

func (f fakeDashboardProvider) CollectSystemSummary(context.Context) (system.Summary, error) {
	return f.summary, f.summaryErr
}

func (f fakeDashboardProvider) CollectSystemDetails(context.Context) (system.Details, error) {
	return f.details, f.detailsErr
}

func (f fakeDashboardProvider) CollectDockerInventory(context.Context) (docker.Inventory, error) {
	return f.inventory, f.inventoryErr
}
