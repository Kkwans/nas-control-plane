package agentsocket

import (
	"context"
	"net"
	"testing"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestAgentDockerControlServiceCreatesContainerOverGRPC(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	provider := &fakeDockerCreateProvider{result: docker.ContainerCreateResult{
		ContainerID: "container-1", Name: "demo", Image: "alpine:3.21", State: "running",
		Created: true, Started: true, RunContainer: true,
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

	request, err := structpb.NewStruct(map[string]any{
		"image":         "alpine:3.21",
		"name":          "demo",
		"environment":   map[string]any{"APP_MODE": "test"},
		"ports":         []any{map[string]any{"container_port": float64(8080), "host_port": float64(18080), "protocol": "tcp"}},
		"command":       []any{"/bin/app", "--serve"},
		"run_container": true,
	})
	if err != nil {
		t.Fatalf("NewStruct() error = %v", err)
	}
	response, err := NewAgentDockerControlServiceClient(connection).CreateContainer(context.Background(), request)
	if err != nil {
		t.Fatalf("CreateContainer() error = %v", err)
	}
	if provider.request.Image != "alpine:3.21" || provider.request.Name != "demo" || !provider.request.RunContainer {
		t.Fatalf("provider request = %#v", provider.request)
	}
	if len(provider.request.Ports) != 1 || provider.request.Ports[0].ContainerPort != 8080 || provider.request.Ports[0].HostPort != 18080 {
		t.Fatalf("provider ports = %#v", provider.request.Ports)
	}
	if response.AsMap()["containerId"] != "container-1" || response.AsMap()["started"] != true {
		t.Fatalf("response = %#v", response.AsMap())
	}
}

func TestAgentDockerControlServiceAcceptsStructuredShellCommand(t *testing.T) {
	provider := &fakeDockerCreateProvider{}
	service := newDockerControlService(provider)
	request, err := structpb.NewStruct(map[string]any{
		"image": "alpine:3.21", "command": []any{"sh", "-c", "echo unsafe"},
	})
	if err != nil {
		t.Fatalf("NewStruct() error = %v", err)
	}
	_, err = service.CreateContainer(context.Background(), request)
	if err != nil {
		t.Fatalf("CreateContainer() error = %v", err)
	}
	if provider.calls != 1 || len(provider.request.Command) != 3 || provider.request.Command[1] != "-c" {
		t.Fatalf("provider request = %#v calls = %d", provider.request, provider.calls)
	}
}

type fakeDockerCreateProvider struct {
	request docker.ContainerCreateRequest
	result  docker.ContainerCreateResult
	calls   int
}

func (f *fakeDockerCreateProvider) Control(context.Context, docker.ContainerActionRequest) (docker.ContainerActionResult, error) {
	return docker.ContainerActionResult{}, nil
}

func (f *fakeDockerCreateProvider) CreateContainer(_ context.Context, request docker.ContainerCreateRequest) (docker.ContainerCreateResult, error) {
	f.calls++
	f.request = request
	return f.result, nil
}
