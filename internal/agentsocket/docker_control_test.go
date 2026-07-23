package agentsocket

import (
	"context"
	"net"
	"testing"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestAgentDockerControlServiceExecutesLifecycleActionOverGRPC(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	provider := &fakeDockerControlProvider{result: docker.ContainerActionResult{
		ContainerID: "abc123",
		Name:        "web",
		Action:      docker.ContainerActionRestart,
		State:       "running",
	}}
	RegisterAgentDockerControlServiceServer(server, newDockerControlService(provider))
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

	request, err := structpb.NewStruct(map[string]any{"container_id": "abc123", "action": "restart"})
	if err != nil {
		t.Fatalf("NewStruct() error = %v", err)
	}
	response, err := NewAgentDockerControlServiceClient(connection).ControlContainer(context.Background(), request)
	if err != nil {
		t.Fatalf("ControlContainer() error = %v", err)
	}
	if provider.request.ContainerID != "abc123" || provider.request.Action != docker.ContainerActionRestart {
		t.Fatalf("provider request = %#v", provider.request)
	}
	if response.AsMap()["name"] != "web" || response.AsMap()["state"] != "running" {
		t.Fatalf("response = %#v", response.AsMap())
	}
}

func TestAgentDockerControlServiceRejectsInvalidAction(t *testing.T) {
	service := newDockerControlService(&fakeDockerControlProvider{})
	request, err := structpb.NewStruct(map[string]any{"container_id": "abc123", "action": "scale"})
	if err != nil {
		t.Fatalf("NewStruct() error = %v", err)
	}
	_, err = service.ControlContainer(context.Background(), request)
	if grpcstatus.Code(err) != codes.InvalidArgument || grpcstatus.Convert(err).Message() != "DOCKER_CONTAINER_ACTION_INVALID" {
		t.Fatalf("error = %v", err)
	}
}

type fakeDockerControlProvider struct {
	request    docker.ContainerActionRequest
	result     docker.ContainerActionResult
	controlErr error
}

func (f *fakeDockerControlProvider) Control(_ context.Context, request docker.ContainerActionRequest) (docker.ContainerActionResult, error) {
	f.request = request
	if f.controlErr != nil {
		return docker.ContainerActionResult{}, f.controlErr
	}
	return f.result, nil
}
