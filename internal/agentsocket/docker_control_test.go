package agentsocket

import (
	"context"
	"net"
	"testing"

	ncpcompose "github.com/Kkwans/nas-control-plane/internal/compose"
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

func TestAgentDockerControlServiceReturnsStandaloneProjectItems(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	provider := &fakeDockerControlProvider{projectResult: docker.ProjectActionResult{
		ProjectID: "standalone", Kind: docker.ProjectKindStandalone, Action: docker.ContainerActionStop,
		State: "degraded", Completed: false, Containers: []docker.ProjectContainerActionResult{{
			ContainerID: "abc123", Action: docker.ContainerActionStop, State: "running", Success: false,
			ErrorCode: "DOCKER_CONTAINER_ACTION_FAILED",
		}},
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
		"project_id": "standalone", "kind": "standalone", "action": "stop", "container_ids": []any{"abc123"},
	})
	if err != nil {
		t.Fatalf("NewStruct() error = %v", err)
	}
	response, err := NewAgentDockerControlServiceClient(connection).ControlStandaloneProject(context.Background(), request)
	if err != nil {
		t.Fatalf("ControlStandaloneProject() error = %v", err)
	}
	if provider.projectRequest.ProjectID != "standalone" || len(provider.projectRequest.ContainerIDs) != 1 {
		t.Fatalf("provider request = %#v", provider.projectRequest)
	}
	if response.AsMap()["state"] != "degraded" {
		t.Fatalf("response = %#v", response.AsMap())
	}
}

func TestAgentDockerControlServiceReturnsComposeLifecycleState(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	provider := &fakeComposeLifecycleProvider{result: ncpcompose.LifecycleResult{
		ProjectID: "compose:demo", Action: ncpcompose.LifecycleActionRestart, State: "running",
		Completed: true, Services: []ncpcompose.LifecycleServiceStatus{{Name: "api", State: "running", Running: true}},
	}}
	RegisterAgentDockerControlServiceServer(server, newDockerControlService(&fakeDockerControlProvider{}, provider))
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
		"project_id": "compose:demo", "working_directory": "/volume2/Project/demo",
		"config_files": []any{"compose.yaml"}, "action": "restart",
	})
	if err != nil {
		t.Fatalf("NewStruct() error = %v", err)
	}
	response, err := NewAgentDockerControlServiceClient(connection).ControlComposeProject(context.Background(), request)
	if err != nil {
		t.Fatalf("ControlComposeProject() error = %v", err)
	}
	if provider.request.ProjectID != "compose:demo" || provider.request.Action != ncpcompose.LifecycleActionRestart {
		t.Fatalf("provider request = %#v", provider.request)
	}
	if response.AsMap()["state"] != "running" {
		t.Fatalf("response = %#v", response.AsMap())
	}
}

func TestAgentDockerControlServiceCreatesContainerFromStructuredRequest(t *testing.T) {
	provider := &fakeDockerControlProvider{createResult: docker.ContainerCreateResult{
		ContainerID: "created", Name: "demo", Image: "alpine:3.21", State: "stopped", Created: true,
	}}
	service := newDockerControlService(provider)
	request, err := structpb.NewStruct(map[string]any{
		"image": "alpine:3.21", "name": "demo", "command": []any{"/bin/echo", "ready"},
		"run_container": false,
	})
	if err != nil {
		t.Fatalf("NewStruct() error = %v", err)
	}
	response, err := service.CreateContainer(context.Background(), request)
	if err != nil {
		t.Fatalf("CreateContainer() error = %v", err)
	}
	if provider.createRequest.Image != "alpine:3.21" || provider.createRequest.Name != "demo" || provider.createRequest.RunContainer {
		t.Fatalf("provider request = %#v", provider.createRequest)
	}
	if response.AsMap()["containerId"] != "created" || response.AsMap()["started"] != false {
		t.Fatalf("response = %#v", response.AsMap())
	}
}

func TestAgentDockerControlServiceDeletesProjectFromTrustedIdentity(t *testing.T) {
	provider := &fakeDockerControlProvider{deleteResult: docker.ProjectDeleteResult{
		ProjectID: "compose:demo", Kind: docker.ProjectKindCompose, Completed: true, RegistryDeleted: true,
		Containers: []docker.ProjectDeleteContainerResult{{ContainerID: "one", Deleted: true, Success: true}},
	}}
	service := newDockerControlService(provider)
	request, err := structpb.NewStruct(map[string]any{
		"project_id": "compose:demo", "kind": "compose", "registry_name": "demo",
	})
	if err != nil {
		t.Fatalf("NewStruct() error = %v", err)
	}
	response, err := service.DeleteProject(context.Background(), request)
	if err != nil {
		t.Fatalf("DeleteProject() error = %v", err)
	}
	if provider.deleteRequest.RegistryName != "demo" || provider.deleteRequest.ProjectID != "compose:demo" {
		t.Fatalf("provider request = %#v", provider.deleteRequest)
	}
	if response.AsMap()["registryDeleted"] != true || response.AsMap()["completed"] != true {
		t.Fatalf("response = %#v", response.AsMap())
	}
}

type fakeDockerControlProvider struct {
	request        docker.ContainerActionRequest
	result         docker.ContainerActionResult
	controlErr     error
	projectRequest docker.ProjectActionRequest
	projectResult  docker.ProjectActionResult
	projectErr     error
	createRequest  docker.ContainerCreateRequest
	createResult   docker.ContainerCreateResult
	createErr      error
	deleteRequest  docker.ProjectDeleteRequest
	deleteResult   docker.ProjectDeleteResult
	deleteErr      error
}

func (f *fakeDockerControlProvider) Control(_ context.Context, request docker.ContainerActionRequest) (docker.ContainerActionResult, error) {
	f.request = request
	if f.controlErr != nil {
		return docker.ContainerActionResult{}, f.controlErr
	}
	return f.result, nil
}

func (f *fakeDockerControlProvider) ControlStandaloneProject(_ context.Context, request docker.ProjectActionRequest) (docker.ProjectActionResult, error) {
	f.projectRequest = request
	if f.projectErr != nil {
		return docker.ProjectActionResult{}, f.projectErr
	}
	return f.projectResult, nil
}

func (f *fakeDockerControlProvider) CreateContainer(_ context.Context, request docker.ContainerCreateRequest) (docker.ContainerCreateResult, error) {
	f.createRequest = request
	if f.createErr != nil {
		return docker.ContainerCreateResult{}, f.createErr
	}
	return f.createResult, nil
}

func (f *fakeDockerControlProvider) DeleteProject(_ context.Context, request docker.ProjectDeleteRequest) (docker.ProjectDeleteResult, error) {
	f.deleteRequest = request
	if f.deleteErr != nil {
		return docker.ProjectDeleteResult{}, f.deleteErr
	}
	return f.deleteResult, nil
}

type fakeComposeLifecycleProvider struct {
	request composeLifecycleRequest
	result  ncpcompose.LifecycleResult
}

type composeLifecycleRequest = ncpcompose.LifecycleRequest

func (f *fakeComposeLifecycleProvider) ControlComposeProject(_ context.Context, request ncpcompose.LifecycleRequest) (ncpcompose.LifecycleResult, error) {
	f.request = request
	return f.result, nil
}

func (f *fakeComposeLifecycleProvider) Read(context.Context, ncpcompose.ReadRequest) (ncpcompose.ProjectConfig, error) {
	return ncpcompose.ProjectConfig{}, nil
}

func (f *fakeComposeLifecycleProvider) Validate(context.Context, ncpcompose.ValidateRequest) (ncpcompose.ValidationResult, error) {
	return ncpcompose.ValidationResult{}, nil
}

func (f *fakeComposeLifecycleProvider) Deploy(context.Context, ncpcompose.DeployRequest) (ncpcompose.DeployResult, error) {
	return ncpcompose.DeployResult{}, nil
}
