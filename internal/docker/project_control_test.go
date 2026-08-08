package docker

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"
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
	actions := gateway.recordedActions()
	sort.Strings(actions)
	if len(actions) != 3 || actions[0] != "first" || actions[1] != "second" || actions[2] != "third" {
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
	if len(result.Containers) >= 2 {
		t.Fatalf("cancellation should stop queued work, partial result = %#v", result)
	}
	if actions := gateway.recordedActions(); len(actions) > maxStandaloneProjectConcurrency {
		t.Fatalf("actions after cancellation = %#v", actions)
	}
}

func TestProjectControllerLimitsConcurrentDockerActions(t *testing.T) {
	gateway := &projectControlGateway{
		snapshots:   map[string]ContainerSnapshot{},
		actionDelay: 20 * time.Millisecond,
	}
	containerIDs := make([]string, 12)
	for index := range containerIDs {
		containerIDs[index] = string(rune('a' + index))
		gateway.snapshots[containerIDs[index]] = ContainerSnapshot{ID: containerIDs[index], Name: "/" + containerIDs[index]}
	}
	result, err := NewProjectController(NewContainerController(gateway)).Control(context.Background(), ProjectActionRequest{
		ProjectID: "standalone", ContainerIDs: containerIDs, Action: ContainerActionStart,
	})
	if err != nil {
		t.Fatalf("Control() error = %v", err)
	}
	if !result.Completed || len(result.Containers) != len(containerIDs) {
		t.Fatalf("result = %#v", result)
	}
	if gateway.maxConcurrent() != maxStandaloneProjectConcurrency {
		t.Fatalf("max concurrent actions = %d, want %d", gateway.maxConcurrent(), maxStandaloneProjectConcurrency)
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
	mu           sync.Mutex
	actions      []string
	actionErrors map[string]error
	snapshots    map[string]ContainerSnapshot
	onAction     func(string)
	actionDelay  time.Duration
	active       int
	maxActive    int
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
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	snapshot, ok := gateway.snapshots[id]
	if !ok {
		return ContainerSnapshot{}, errors.New("container not found")
	}
	return snapshot, nil
}

func (gateway *projectControlGateway) action(id string, running bool) error {
	gateway.mu.Lock()
	gateway.actions = append(gateway.actions, id)
	gateway.active++
	if gateway.active > gateway.maxActive {
		gateway.maxActive = gateway.active
	}
	err := gateway.actionErrors[id]
	snapshot, ok := gateway.snapshots[id]
	if ok && err == nil {
		snapshot.Running = running
		gateway.snapshots[id] = snapshot
	}
	delay := gateway.actionDelay
	onAction := gateway.onAction
	gateway.mu.Unlock()

	if onAction != nil {
		onAction(id)
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	gateway.mu.Lock()
	gateway.active--
	gateway.mu.Unlock()
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("container not found")
	}
	return nil
}

func (gateway *projectControlGateway) recordedActions() []string {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	return append([]string(nil), gateway.actions...)
}

func (gateway *projectControlGateway) maxConcurrent() int {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	return gateway.maxActive
}
