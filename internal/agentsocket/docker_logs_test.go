package agentsocket

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestAgentDockerLogsServiceReturnsTailEntriesOverGRPC(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	provider := &fakeDockerLogsProvider{result: docker.ContainerLogsResult{
		ContainerID: "abc123",
		Tail:        20,
		CollectedAt: time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC),
		Entries:     []docker.ContainerLogEntry{{Stream: "stdout", Message: "ready"}},
	}}
	RegisterAgentDockerLogsServiceServer(server, newDockerLogsService(provider))
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

	request, err := structpb.NewStruct(map[string]any{"container_id": "abc123", "tail": 20})
	if err != nil {
		t.Fatalf("NewStruct() error = %v", err)
	}
	response, err := NewAgentDockerLogsServiceClient(connection).ReadLogs(context.Background(), request)
	if err != nil {
		t.Fatalf("ReadLogs() error = %v", err)
	}
	if provider.request.ContainerID != "abc123" || provider.request.Tail != 20 {
		t.Fatalf("provider request = %#v", provider.request)
	}
	if response.AsMap()["containerId"] != "abc123" || len(response.AsMap()["entries"].([]any)) != 1 {
		t.Fatalf("response = %#v", response.AsMap())
	}
}

func TestAgentDockerLogsServiceRejectsInvalidTail(t *testing.T) {
	service := newDockerLogsService(&fakeDockerLogsProvider{})
	request, err := structpb.NewStruct(map[string]any{"container_id": "abc123", "tail": 0})
	if err != nil {
		t.Fatalf("NewStruct() error = %v", err)
	}
	_, err = service.ReadLogs(context.Background(), request)
	if grpcstatus.Code(err) != codes.InvalidArgument || grpcstatus.Convert(err).Message() != "DOCKER_LOGS_INPUT_INVALID" {
		t.Fatalf("error = %v", err)
	}
}

type fakeDockerLogsProvider struct {
	request docker.ContainerLogsRequest
	result  docker.ContainerLogsResult
}

func (f *fakeDockerLogsProvider) Read(_ context.Context, request docker.ContainerLogsRequest) (docker.ContainerLogsResult, error) {
	f.request = request
	return f.result, nil
}
