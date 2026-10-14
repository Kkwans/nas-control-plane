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

func TestContainerControllerNormalizesActionBeforeDispatch(t *testing.T) {
	for _, test := range []struct {
		input ContainerAction
		want  ContainerAction
	}{
		{ContainerActionStart, ContainerActionStart},
		{ContainerActionStop, ContainerActionStop},
		{ContainerActionRestart, ContainerActionRestart},
		{" START ", ContainerActionStart},
		{"\tStop\n", ContainerActionStop},
		{" ReStart ", ContainerActionRestart},
	} {
		t.Run(string(test.input), func(t *testing.T) {
			request := ContainerActionRequest{ContainerID: "abc123", Action: test.input}
			gateway := &fakeContainerControlGateway{snapshot: ContainerSnapshot{ID: "abc123", Running: test.want != ContainerActionStop}}
			result, err := NewContainerController(gateway).Control(context.Background(), request)
			if err != nil {
				t.Fatalf("Control() error = %v", err)
			}
			if len(gateway.actions) != 1 || gateway.actions[0] != string(test.want) || gateway.inspectCalls != 1 {
				t.Fatalf("actions = %#v, inspect calls = %d", gateway.actions, gateway.inspectCalls)
			}
			if result.Action != test.want || request.Action != test.input {
				t.Fatalf("result action = %q, original action = %q", result.Action, request.Action)
			}
		})
	}
}

func TestContainerControllerRejectsInvalidOrCancelledActionWithoutGatewayCall(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, test := range []struct {
		name    string
		ctx     context.Context
		request ContainerActionRequest
		code    string
	}{
		{"unsupported", context.Background(), ContainerActionRequest{ContainerID: "abc123", Action: " stop;start "}, "DOCKER_CONTAINER_ACTION_INVALID"},
		{"empty action", context.Background(), ContainerActionRequest{ContainerID: "abc123", Action: "\t"}, "DOCKER_CONTAINER_ACTION_INVALID"},
		{"empty container", context.Background(), ContainerActionRequest{ContainerID: " ", Action: " START "}, "DOCKER_CONTAINER_ACTION_INVALID"},
		{"cancelled", cancelled, ContainerActionRequest{ContainerID: "abc123", Action: " START "}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			gateway := &fakeContainerControlGateway{}
			_, err := NewContainerController(gateway).Control(test.ctx, test.request)
			if test.code == "" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error = %v, want context cancellation", err)
				}
			} else if ErrorCode(err) != test.code {
				t.Fatalf("error = %v, want %s", err, test.code)
			}
			if len(gateway.actions) != 0 || gateway.inspectCalls != 0 {
				t.Fatalf("actions = %#v, inspect calls = %d, want no calls", gateway.actions, gateway.inspectCalls)
			}
		})
	}
}

type fakeContainerControlGateway struct {
	actions      []string
	actionErr    error
	snapshot     ContainerSnapshot
	inspectErr   error
	inspectCalls int
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
	f.inspectCalls++
	return f.snapshot, f.inspectErr
}
