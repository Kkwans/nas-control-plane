package agentsocket

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestDockerRealtimeInventoryTransportBudget(t *testing.T) {
	fixture := docker.Inventory{CollectedAt: time.Now(), Containers: make([]docker.InventoryContainer, 1000)}
	for i := range fixture.Containers {
		fixture.Containers[i] = docker.InventoryContainer{Name: "容器", Ports: []docker.PortMapping{{Protocol: "tcp"}}, State: "running"}
	}
	encoded, _ := json.Marshal(fixture)
	response, err := dashboardStruct(fixture, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) >= 1<<20 || proto.Size(response) <= len(encoded) || proto.Size(response) >= 3<<20 {
		t.Fatalf("fixture JSON=%d proto=%d", len(encoded), proto.Size(response))
	}
	for _, test := range []struct {
		name      string
		inventory docker.Inventory
		code      codes.Code
	}{
		{"small fields and unicode", fixture, codes.OK},
		{"too large", docker.Inventory{CollectedAt: time.Now(), Engine: docker.EngineInfo{ServerVersion: strings.Repeat("x", 1<<20)}}, codes.ResourceExhausted},
	} {
		t.Run(test.name, func(t *testing.T) {
			listener := bufconn.Listen(1 << 20)
			server := grpc.NewServer()
			RegisterAgentDashboardServiceServer(server, newDashboardService(fakeDashboardProvider{inventory: test.inventory}))
			go func() { _ = server.Serve(listener) }()
			defer server.Stop()
			defer listener.Close()
			conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			received, err := NewAgentDashboardServiceClient(conn).GetDockerInventory(ctx, &emptypb.Empty{})
			if grpcstatus.Code(err) != test.code {
				t.Fatal(err)
			}
			if test.code == codes.OK && !proto.Equal(received, response) {
				t.Fatal("transport truncated inventory")
			}
			if test.code != codes.OK && grpcstatus.Convert(err).Message() != "AGENT_DOCKER_INVENTORY_RESPONSE_LIMIT_EXCEEDED" {
				t.Fatal(err)
			}
		})
	}
}
