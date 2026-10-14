package docker

import (
	"context"
	"errors"
	"reflect"
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
	firstActionStarted := make(chan struct{})
	containerIDs := []string{"first", "second", "third", "fourth", "fifth"}
	gateway := &projectControlGateway{
		snapshots: map[string]ContainerSnapshot{"first": {ID: "first", Name: "/first", Running: true}},
		beforeAction: func(id string) {
			if id != "first" {
				<-firstActionStarted
			}
		},
		onAction: func(id string) {
			if id == "first" {
				cancel()
				close(firstActionStarted)
			}
		},
	}
	result, err := NewProjectController(NewContainerController(gateway)).Control(ctx, ProjectActionRequest{
		ProjectID: "standalone", ContainerIDs: containerIDs, Action: ContainerActionRestart,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	if len(result.Containers) == 0 || len(result.Containers) >= len(containerIDs) {
		t.Fatalf("cancellation should preserve a partial result, result = %#v", result)
	}
	if actions := gateway.recordedActions(); len(actions) > maxStandaloneProjectConcurrency {
		t.Fatalf("actions after cancellation exceeded concurrency = %#v", actions)
	} else {
		for _, id := range actions {
			if id == "fifth" {
				t.Fatalf("cancellation should stop queued fifth action, actions = %#v", actions)
			}
		}
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

func TestProjectControllerNormalizesActionBeforeDispatch(t *testing.T) {
	for _, test := range []struct {
		input ContainerAction
		want  ContainerAction
	}{
		{" START ", ContainerActionStart},
		{"\tStop\n", ContainerActionStop},
		{" ReStart ", ContainerActionRestart},
	} {
		t.Run(string(test.input), func(t *testing.T) {
			request := ProjectActionRequest{ProjectID: "standalone", ContainerIDs: []string{" abc123 "}, Action: test.input}
			gateway := &fakeContainerControlGateway{snapshot: ContainerSnapshot{ID: "abc123", Running: test.want != ContainerActionStop}}
			result, err := NewProjectController(NewContainerController(gateway)).Control(context.Background(), request)
			if err != nil {
				t.Fatalf("Control() error = %v", err)
			}
			if len(gateway.actions) != 1 || gateway.actions[0] != string(test.want) || gateway.inspectCalls != 1 {
				t.Fatalf("actions = %#v, inspect calls = %d", gateway.actions, gateway.inspectCalls)
			}
			if !result.Completed || result.Action != test.want || len(result.Containers) != 1 || result.Containers[0].Action != test.want || !result.Containers[0].Success {
				t.Fatalf("result = %#v", result)
			}
			if request.Action != test.input || request.ContainerIDs[0] != " abc123 " {
				t.Fatalf("caller request changed: %#v", request)
			}
		})
	}
}

func TestProjectActionNormalizeOwnsContainerIDsOnSuccessAndFailure(t *testing.T) {
	for _, test := range []struct {
		name   string
		ids    []string
		action ContainerAction
		valid  bool
	}{
		{"success", []string{" first ", " second "}, " START ", true},
		{"empty item", []string{" first ", " "}, ContainerActionStart, false},
		{"duplicate item", []string{" first ", "first"}, ContainerActionStart, false},
		{"invalid action", []string{" first ", " second "}, " stop;start ", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := append([]string(nil), test.ids...)
			request := ProjectActionRequest{ProjectID: "standalone", ContainerIDs: test.ids, Action: test.action}
			normalized, err := request.Normalize()
			if (err == nil) != test.valid {
				t.Fatalf("Normalize() error = %v, want valid = %t", err, test.valid)
			}
			if !reflect.DeepEqual(test.ids, original) {
				t.Fatalf("Normalize() changed caller IDs: %#v, want %#v", test.ids, original)
			}
			if err != nil {
				if ErrorCode(err) != "DOCKER_PROJECT_ACTION_INVALID" {
					t.Fatalf("error code = %q", ErrorCode(err))
				}
				return
			}
			if normalized.Action != ContainerActionStart || !reflect.DeepEqual(normalized.ContainerIDs, []string{"first", "second"}) {
				t.Fatalf("normalized request = %#v", normalized)
			}
			normalized.ContainerIDs[0] = "changed normalized"
			test.ids[1] = "changed original"
			if test.ids[0] != original[0] || normalized.ContainerIDs[1] != "second" {
				t.Fatalf("normalized and caller IDs still alias: %#v / %#v", normalized.ContainerIDs, test.ids)
			}
		})
	}
}

func TestProjectActionNormalizeCanShareReadOnlyRequest(t *testing.T) {
	request := ProjectActionRequest{ProjectID: "standalone", ContainerIDs: []string{" first ", " second "}, Action: " START "}
	var workers sync.WaitGroup
	for range 16 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			normalized, err := request.Normalize()
			if err != nil || normalized.Action != ContainerActionStart || !reflect.DeepEqual(normalized.ContainerIDs, []string{"first", "second"}) {
				t.Errorf("Normalize() = %#v, error = %v", normalized, err)
			}
		}()
	}
	workers.Wait()
	if !reflect.DeepEqual(request.ContainerIDs, []string{" first ", " second "}) {
		t.Fatalf("shared caller request changed: %#v", request)
	}
}

type projectControlGateway struct {
	mu           sync.Mutex
	actions      []string
	actionErrors map[string]error
	snapshots    map[string]ContainerSnapshot
	beforeAction func(string)
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
	beforeAction := gateway.beforeAction
	onAction := gateway.onAction
	gateway.mu.Unlock()

	if beforeAction != nil {
		beforeAction(id)
	}
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
