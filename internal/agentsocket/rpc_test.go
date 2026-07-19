package agentsocket

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestAgentProbeServiceUsesGRPC(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterAgentProbeServiceServer(server, newStatusService())
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

	response, err := NewAgentProbeServiceClient(connection).GetStatus(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}
	if response.GetFields()["transport"].GetStringValue() != "unix" {
		t.Fatalf("transport = %q，期望 unix", response.GetFields()["transport"].GetStringValue())
	}
}
