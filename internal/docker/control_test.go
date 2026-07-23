package docker

import (
	"context"
	"errors"
	"testing"
)

func TestContainerControllerExecutesExplicitLifecycleActionAndReadsBackState(t *testing.T) {
	tests := []struct {
		name   string
		action ContainerAction
		want   string
	}{
		{name: "start", action: ContainerActionStart, want: "start"},
		{name: "stop", action: ContainerActionStop, want: "stop"},
		{name: "restart", action: ContainerActionRestart, want: "restart"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gateway := &fakeContainerControlGateway{snapshot: ContainerSnapshot{ID: "abc123", Name: "/web", Running: true}}
			controller := NewContainerController(gateway)

			result, err := controller.Control(context.Background(), ContainerActionRequest{ContainerID: "abc123", Action: test.action})
			if err != nil {
				t.Fatalf("Control() error = %v", err)
			}
			if result.ContainerID != "abc123" || result.Name != "web" || result.Action != test.action || result.State != "running" {
				t.Fatalf("result = %#v", result)
			}
			if len(gateway.actions) != 1 || gateway.actions[0] != test.want {
				t.Fatalf("actions = %#v", gateway.actions)
			}
		})
	}
}

func TestContainerControllerRejectsInvalidActionBeforeGatewayCall(t *testing.T) {
	gateway := &fakeContainerControlGateway{}
	controller := NewContainerController(gateway)

	_, err := controller.Control(context.Background(), ContainerActionRequest{ContainerID: "abc123", Action: "scale"})
	if ErrorCode(err) != "DOCKER_CONTAINER_ACTION_INVALID" {
		t.Fatalf("error code = %q, want DOCKER_CONTAINER_ACTION_INVALID", ErrorCode(err))
	}
	if len(gateway.actions) != 0 {
		t.Fatalf("gateway actions = %#v, want none", gateway.actions)
	}
}

func TestContainerControllerReturnsStableFailureWhenActionFails(t *testing.T) {
	gateway := &fakeContainerControlGateway{actionErr: errors.New("docker rejected action")}
	controller := NewContainerController(gateway)

	_, err := controller.Control(context.Background(), ContainerActionRequest{ContainerID: "abc123", Action: ContainerActionRestart})
	if ErrorCode(err) != "DOCKER_CONTAINER_ACTION_FAILED" {
		t.Fatalf("error code = %q, want DOCKER_CONTAINER_ACTION_FAILED", ErrorCode(err))
	}
}

type fakeContainerControlGateway struct {
	actions    []string
	actionErr  error
	snapshot   ContainerSnapshot
	inspectErr error
}

func (f *fakeContainerControlGateway) StartContainer(context.Context, string) error {
	f.actions = append(f.actions, "start")
	return f.actionErr
}

func (f *fakeContainerControlGateway) StopContainer(context.Context, string) error {
	f.actions = append(f.actions, "stop")
	return f.actionErr
}

func (f *fakeContainerControlGateway) RestartContainer(context.Context, string) error {
	f.actions = append(f.actions, "restart")
	return f.actionErr
}

func (f *fakeContainerControlGateway) InspectContainer(context.Context, string) (ContainerSnapshot, error) {
	return f.snapshot, f.inspectErr
}
