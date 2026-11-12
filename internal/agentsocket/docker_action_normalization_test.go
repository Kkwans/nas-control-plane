package agentsocket

import (
	"context"
	"reflect"
	"testing"

	ncpcompose "github.com/Kkwans/nas-control-plane/internal/compose"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestProjectActionDecoderReturnsNormalizedSnapshot(t *testing.T) {
	request, err := structpb.NewStruct(map[string]any{
		"project_id": " standalone ", "action": " Stop ", "container_ids": []any{" first ", " second "},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := decodeProjectActionRequest(request)
	if err != nil {
		t.Fatalf("decodeProjectActionRequest() error = %v", err)
	}
	if result.ProjectID != "standalone" || result.Kind != docker.ProjectKindStandalone || result.Action != docker.ContainerActionStop || !reflect.DeepEqual(result.ContainerIDs, []string{"first", "second"}) {
		t.Fatalf("decoded result = %#v", result)
	}
	result.ContainerIDs[0] = "changed"
	if !reflect.DeepEqual(request.AsMap()["container_ids"], []any{" first ", " second "}) {
		t.Fatalf("decoder changed wire request: %#v", request.AsMap())
	}
}

func TestProjectActionServicePassesNormalizedSnapshotToCustomProvider(t *testing.T) {
	provider := &fakeDockerControlProvider{}
	service := newDockerControlService(provider)
	request, err := structpb.NewStruct(map[string]any{
		"project_id": " standalone ", "action": " ReStart ", "container_ids": []any{" first ", " second "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ControlStandaloneProject(context.Background(), request); err != nil {
		t.Fatalf("ControlStandaloneProject() error = %v", err)
	}
	got := provider.projectRequest
	if got.ProjectID != "standalone" || got.Kind != docker.ProjectKindStandalone || got.Action != docker.ContainerActionRestart || !reflect.DeepEqual(got.ContainerIDs, []string{"first", "second"}) {
		t.Fatalf("provider request = %#v", got)
	}
}

func TestProjectActionServiceRejectsInvalidSnapshotBeforeCustomProvider(t *testing.T) {
	for _, test := range []struct {
		name   string
		ids    []any
		action string
	}{
		{"duplicates after trim", []any{" first ", "first"}, " START "},
		{"empty after trim", []any{" first ", " "}, " START "},
		{"invalid action", []any{" first "}, " stop;start "},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &fakeDockerControlProvider{}
			request, err := structpb.NewStruct(map[string]any{
				"project_id": "standalone", "action": test.action, "container_ids": test.ids,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = newDockerControlService(provider).ControlStandaloneProject(context.Background(), request)
			if grpcstatus.Code(err) != codes.InvalidArgument || grpcstatus.Convert(err).Message() != "DOCKER_PROJECT_ACTION_INVALID" {
				t.Fatalf("error = %v", err)
			}
			if provider.projectRequest.ProjectID != "" || provider.projectRequest.ContainerIDs != nil {
				t.Fatalf("invalid request reached provider: %#v", provider.projectRequest)
			}
		})
	}
}

func TestComposeActionDecoderReturnsNormalizedSnapshot(t *testing.T) {
	request, err := structpb.NewStruct(map[string]any{
		"project_id": " compose:demo ", "action": " Stop ",
		"working_directory": " /volume2/Project/demo/./ ", "config_files": []any{" ./compose.yaml ", "config/../override.yml"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := decodeComposeProjectRequest(request)
	if err != nil {
		t.Fatalf("decodeComposeProjectRequest() error = %v", err)
	}
	if result.ProjectID != "compose:demo" || result.WorkingDirectory != "/volume2/Project/demo" || result.Action != ncpcompose.LifecycleActionStop || !reflect.DeepEqual(result.ConfigFiles, []string{"/volume2/Project/demo/compose.yaml", "/volume2/Project/demo/override.yml"}) {
		t.Fatalf("decoded result = %#v", result)
	}
	result.ConfigFiles[0] = "changed"
	if !reflect.DeepEqual(request.AsMap()["config_files"], []any{" ./compose.yaml ", "config/../override.yml"}) {
		t.Fatalf("decoder changed wire request: %#v", request.AsMap())
	}
}

func TestComposeActionServicePassesNormalizedSnapshotToCustomProvider(t *testing.T) {
	provider := &fakeComposeLifecycleProvider{}
	service := newDockerControlService(&fakeDockerControlProvider{}, provider)
	request, err := structpb.NewStruct(map[string]any{
		"project_id": " compose:demo ", "action": " ReStart ",
		"working_directory": " /volume2/Project/demo/. ", "config_files": []any{" compose.yaml "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ControlComposeProject(context.Background(), request); err != nil {
		t.Fatalf("ControlComposeProject() error = %v", err)
	}
	got := provider.request
	if got.ProjectID != "compose:demo" || got.WorkingDirectory != "/volume2/Project/demo" || got.Action != ncpcompose.LifecycleActionRestart || !reflect.DeepEqual(got.ConfigFiles, []string{"/volume2/Project/demo/compose.yaml"}) {
		t.Fatalf("provider request = %#v", got)
	}
}

func TestComposeActionServiceRejectsInvalidSnapshotBeforeCustomProvider(t *testing.T) {
	for _, test := range []struct {
		name   string
		paths  []any
		action string
	}{
		{"duplicate paths", []any{"compose.yaml", "./compose.yaml"}, " START "},
		{"outside directory", []any{"compose.yaml", "../outside.yaml"}, " START "},
		{"invalid action", []any{"compose.yaml"}, " stop;start "},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &fakeComposeLifecycleProvider{}
			request, err := structpb.NewStruct(map[string]any{
				"project_id": "compose:demo", "action": test.action,
				"working_directory": "/volume2/Project/demo", "config_files": test.paths,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = newDockerControlService(&fakeDockerControlProvider{}, provider).ControlComposeProject(context.Background(), request)
			if grpcstatus.Code(err) != codes.InvalidArgument || grpcstatus.Convert(err).Message() != "COMPOSE_LIFECYCLE_INVALID" {
				t.Fatalf("error = %v", err)
			}
			if provider.request.ProjectID != "" || provider.request.ConfigFiles != nil {
				t.Fatalf("invalid request reached provider: %#v", provider.request)
			}
		})
	}
}
