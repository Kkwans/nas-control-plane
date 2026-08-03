package docker

import (
	"context"
	"errors"
	"testing"
)

func TestProjectControllerReturnsPerContainerResultsForPartialFailure(t *testing.T) {
	gateway := &projectControlGateway{
		snapshots: map[string]ContainerSnapshot{
			"first":  {ID: "first", Name: "/first", Running: true},
			"second": {ID: "second", Name: "/second", Running: true},
			"third":  {ID: "third", Name: "/third", Running: true},
		},
		actionErrors: map[string]error{"second": errors.New("engine rejected stop")},
	}
	result, err := NewProjectController(NewContainerController(gateway)).Control(context.Background(), ProjectActionRequest{
		ProjectID: "standalone", Kind: ProjectKindStandalone, ContainerIDs: []string{"first", "second", "third"}, Action: ContainerActionStop,
	})
	if err != nil {
		t.Fatalf("Control() error = %v", err)
	}
	if result.Completed || result.State != "degraded" || len(result.Containers) != 3 {
		t.Fatalf("result = %#v", result)
	}
	if !result.Containers[0].Success || result.Containers[0].State != "stopped" {
		t.Fatalf("first result = %#v", result.Containers[0])
	}
	if result.Containers[1].Success || result.Containers[1].ErrorCode != "DOCKER_CONTAINER_ACTION_FAILED" || result.Containers[1].State != "running" {
		t.Fatalf("second result = %#v", result.Containers[1])
	}
	if !result.Containers[2].Success || result.Containers[2].State != "stopped" {
		t.Fatalf("third result = %#v", result.Containers[2])
	}
	if len(gateway.actions) != 3 || gateway.actions[0] != "first" || gateway.actions[1] != "second" || gateway.actions[2] != "third" {
		t.Fatalf("actions = %#v", gateway.actions)
	}
}

func TestProjectControllerPreservesCancellationAfterPartialExecution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	gateway := &projectControlGateway{
		snapshots: map[string]ContainerSnapshot{"first": {ID: "first", Name: "/first", Running: true}},
		onAction: func(id string) {
			if id == "first" {
				cancel()
			}
		},
	}
	result, err := NewProjectController(NewContainerController(gateway)).Control(ctx, ProjectActionRequest{
		ProjectID: "standalone", ContainerIDs: []string{"first", "second"}, Action: ContainerActionRestart,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	if len(result.Containers) != 1 || !result.Containers[0].Success {
		t.Fatalf("partial result = %#v", result)
	}
}

func TestProjectActionRequestRejectsComposeKindAndDuplicateIDs(t *testing.T) {
	for _, request := range []ProjectActionRequest{
		{ProjectID: "compose:demo", Kind: ProjectKindCompose, ContainerIDs: []string{"one"}, Action: ContainerActionStart},
		{ProjectID: "standalone", ContainerIDs: []string{"one", "one"}, Action: ContainerActionStart},
	} {
		if ErrorCode(request.Validate()) != "DOCKER_PROJECT_ACTION_INVALID" {
			t.Fatalf("request %#v was accepted", request)
		}
	}
}

type projectControlGateway struct {
	actions      []string
	actionErrors map[string]error
	snapshots    map[string]ContainerSnapshot
	onAction     func(string)
}

func (gateway *projectControlGateway) StartContainer(_ context.Context, id string) error {
	return gateway.action(id, true)
}

func (gateway *projectControlGateway) StopContainer(_ context.Context, id string) error {
	return gateway.action(id, false)
}

func (gateway *projectControlGateway) RestartContainer(_ context.Context, id string) error {
	return gateway.action(id, true)
}

func (gateway *projectControlGateway) InspectContainer(_ context.Context, id string) (ContainerSnapshot, error) {
	snapshot, ok := gateway.snapshots[id]
	if !ok {
		return ContainerSnapshot{}, errors.New("container not found")
	}
	return snapshot, nil
}

func (gateway *projectControlGateway) action(id string, running bool) error {
	gateway.actions = append(gateway.actions, id)
	if gateway.onAction != nil {
		gateway.onAction(id)
	}
	if err := gateway.actionErrors[id]; err != nil {
		return err
	}
	snapshot, ok := gateway.snapshots[id]
	if !ok {
		return errors.New("container not found")
	}
	snapshot.Running = running
	gateway.snapshots[id] = snapshot
	return nil
}
