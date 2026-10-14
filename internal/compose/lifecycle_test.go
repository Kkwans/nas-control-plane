package compose

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestLifecycleRequestNormalizePreservesCallerOwnership(t *testing.T) {
	for _, test := range []struct {
		name        string
		configFiles []string
		action      LifecycleAction
		wantCode    string
	}{
		{name: "success", configFiles: []string{" ./compose.yaml ", "config/../override.yml"}, action: " ReStArT\t"},
		{name: "invalid later path", configFiles: []string{" ./compose.yaml ", "../outside.yaml"}, action: LifecycleActionStart, wantCode: "COMPOSE_LIFECYCLE_INVALID"},
		{name: "duplicate normalized path", configFiles: []string{" ./compose.yaml ", "compose.yaml"}, action: LifecycleActionStop, wantCode: "COMPOSE_LIFECYCLE_INVALID"},
		{name: "invalid action", configFiles: []string{" ./compose.yaml "}, action: "launch", wantCode: "COMPOSE_LIFECYCLE_INVALID"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := LifecycleRequest{
				ProjectID: " compose:demo ", WorkingDirectory: " /volume2/Project/demo/./ ",
				ConfigFiles: append([]string(nil), test.configFiles...), Action: test.action,
			}
			original := request
			original.ConfigFiles = append([]string(nil), request.ConfigFiles...)
			normalized, err := request.Normalize()
			if (err == nil) != (test.wantCode == "") || ErrorCode(err) != test.wantCode {
				t.Fatalf("Normalize() error code = %q, want %q: %v", ErrorCode(err), test.wantCode, err)
			}
			if !reflect.DeepEqual(request, original) {
				t.Fatalf("Normalize() changed caller request: got %#v, want %#v", request, original)
			}
			if err == nil {
				want := LifecycleRequest{
					ProjectID: "compose:demo", WorkingDirectory: "/volume2/Project/demo",
					ConfigFiles: []string{"/volume2/Project/demo/compose.yaml", "/volume2/Project/demo/override.yml"}, Action: LifecycleActionRestart,
				}
				if !reflect.DeepEqual(normalized, want) {
					t.Fatalf("Normalize() = %#v, want %#v", normalized, want)
				}
				normalized.ConfigFiles[0] = "changed.yaml"
				if !reflect.DeepEqual(request, original) {
					t.Fatalf("normalized result aliases caller config files: %#v", request)
				}
			} else if !reflect.DeepEqual(normalized, LifecycleRequest{}) {
				t.Fatalf("Normalize() on error = %#v, want zero request", normalized)
			}
			if err := request.Validate(); (err == nil) != (test.wantCode == "") || ErrorCode(err) != test.wantCode {
				t.Fatalf("Validate() error code = %q, want %q: %v", ErrorCode(err), test.wantCode, err)
			}
			if !reflect.DeepEqual(request, original) {
				t.Fatalf("Validate() changed caller request: got %#v, want %#v", request, original)
			}
		})
	}
}

func TestManagerLifecycleDispatchesNormalizedActionExactlyOnce(t *testing.T) {
	for _, test := range []struct {
		action LifecycleAction
		want   LifecycleAction
	}{
		{LifecycleActionStart, LifecycleActionStart},
		{" \tStArT\n", LifecycleActionStart},
		{LifecycleActionStop, LifecycleActionStop},
		{" \tStOp\n", LifecycleActionStop},
		{LifecycleActionRestart, LifecycleActionRestart},
		{" \tReStArT\n", LifecycleActionRestart},
	} {
		t.Run(string(test.action), func(t *testing.T) {
			state := "running"
			if test.want == LifecycleActionStop {
				state = "exited"
			}
			runner := &lifecycleRunnerStub{services: []LifecycleServiceStatus{{Name: "api", State: state}}}
			request := LifecycleRequest{
				ProjectID: " compose:demo ", WorkingDirectory: "/volume2/Project/demo", ConfigFiles: []string{"compose.yaml"}, Action: test.action,
			}
			result, err := NewManager(runner).Lifecycle(context.Background(), request)
			if err != nil {
				t.Fatalf("Lifecycle() error = %v", err)
			}
			wantState := "running"
			if test.want == LifecycleActionStop {
				wantState = "stopped"
			}
			if !result.Completed || result.ProjectID != "compose:demo" || result.Action != test.want || result.State != wantState || result.Output != "done" || len(result.Services) != 1 {
				t.Fatalf("result = %#v", result)
			}
			if wantCalls := []string{string(test.want), "inspect"}; !reflect.DeepEqual(runner.calls, wantCalls) {
				t.Fatalf("runner calls = %v, want %v", runner.calls, wantCalls)
			}
			if runner.workingDirectory != "/volume2/Project/demo" || !reflect.DeepEqual(runner.configFiles, []string{"/volume2/Project/demo/compose.yaml"}) {
				t.Fatalf("runner arguments = %#v", runner)
			}
			if request.Action != test.action || request.ProjectID != " compose:demo " || !reflect.DeepEqual(request.ConfigFiles, []string{"compose.yaml"}) {
				t.Fatalf("Lifecycle() changed caller request: %#v", request)
			}
		})
	}
}

func TestManagerLifecycleNormalizedActionPreservesFailures(t *testing.T) {
	for _, action := range []LifecycleAction{LifecycleActionStart, LifecycleActionStop, LifecycleActionRestart} {
		for _, test := range []struct {
			name          string
			actionErr     error
			inspectErr    error
			wantCode      string
			wantErr       error
			wantInspect   bool
			mismatchState bool
		}{
			{name: "action failure", actionErr: errors.New("command failed"), wantCode: "COMPOSE_LIFECYCLE_FAILED"},
			{name: "inspect failure", inspectErr: errors.New("inspect failed"), wantCode: "COMPOSE_LIFECYCLE_VERIFY_FAILED", wantInspect: true},
			{name: "state mismatch", wantCode: "COMPOSE_LIFECYCLE_VERIFY_FAILED", wantInspect: true, mismatchState: true},
			{name: "empty services", wantCode: "COMPOSE_LIFECYCLE_VERIFY_FAILED", wantInspect: true},
			{name: "action canceled", actionErr: context.Canceled, wantErr: context.Canceled},
			{name: "action deadline exceeded", actionErr: context.DeadlineExceeded, wantErr: context.DeadlineExceeded},
		} {
			t.Run(string(action)+"/"+test.name, func(t *testing.T) {
				runner := &lifecycleRunnerStub{actionErr: test.actionErr, inspectErr: test.inspectErr}
				wantState := "unknown"
				if test.mismatchState {
					state := "exited"
					wantState = "stopped"
					if action == LifecycleActionStop {
						state = "running"
						wantState = "running"
					}
					runner.services = []LifecycleServiceStatus{{Name: "api", State: state}}
				}
				request := LifecycleRequest{
					ProjectID: "compose:demo", WorkingDirectory: "/volume2/Project/demo", ConfigFiles: []string{"compose.yaml"}, Action: LifecycleAction(" " + string(action) + "\t"),
				}
				result, err := NewManager(runner).Lifecycle(context.Background(), request)
				if test.wantErr != nil {
					if err != test.wantErr {
						t.Fatalf("Lifecycle() error = %v, want %v", err, test.wantErr)
					}
				} else if ErrorCode(err) != test.wantCode {
					t.Fatalf("Lifecycle() error code = %q, want %q: %v", ErrorCode(err), test.wantCode, err)
				}
				wantCalls := []string{string(action)}
				wantOutput := ""
				if test.wantInspect {
					wantCalls = append(wantCalls, "inspect")
					wantOutput = "done"
				}
				if !reflect.DeepEqual(runner.calls, wantCalls) {
					t.Fatalf("runner calls = %v, want %v", runner.calls, wantCalls)
				}
				if result.Completed || result.Action != action || result.ProjectID != request.ProjectID || result.State != wantState || result.Output != wantOutput {
					t.Fatalf("result = %#v", result)
				}
				if !reflect.DeepEqual(request.ConfigFiles, []string{"compose.yaml"}) {
					t.Fatalf("Lifecycle() changed caller config files: %v", request.ConfigFiles)
				}
			})
		}
	}
}

func TestManagerLifecycleNormalizedActionHonorsContextCancellation(t *testing.T) {
	for _, stage := range []string{"before action", "during action", "during inspect"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runner := &lifecycleRunnerStub{}
			var wantCalls []string
			wantResult := LifecycleResult{}
			switch stage {
			case "before action":
				cancel()
			case "during action":
				runner.onAction = cancel
				runner.actionErr = errors.New("command interrupted")
				wantCalls = []string{"restart"}
				wantResult = LifecycleResult{ProjectID: "compose:demo", Action: LifecycleActionRestart, State: "unknown", Services: []LifecycleServiceStatus{}}
			case "during inspect":
				runner.onInspect = cancel
				runner.inspectErr = errors.New("inspection interrupted")
				wantCalls = []string{"restart", "inspect"}
				wantResult = LifecycleResult{ProjectID: "compose:demo", Action: LifecycleActionRestart, State: "unknown", Services: []LifecycleServiceStatus{}, Output: "done"}
			}
			request := LifecycleRequest{
				ProjectID: "compose:demo", WorkingDirectory: "/volume2/Project/demo", ConfigFiles: []string{"compose.yaml"}, Action: " ReStArT ",
			}
			result, err := NewManager(runner).Lifecycle(ctx, request)
			if err != context.Canceled {
				t.Fatalf("Lifecycle() error = %v, want context.Canceled", err)
			}
			if !reflect.DeepEqual(result, wantResult) {
				t.Fatalf("result = %#v, want %#v", result, wantResult)
			}
			if !reflect.DeepEqual(runner.calls, wantCalls) {
				t.Fatalf("runner calls = %v, want %v", runner.calls, wantCalls)
			}
			if !reflect.DeepEqual(request.ConfigFiles, []string{"compose.yaml"}) {
				t.Fatalf("Lifecycle() changed caller config files: %v", request.ConfigFiles)
			}
		})
	}
}

func TestManagerLifecycleRunsActionAndVerifiesRealServiceState(t *testing.T) {
	runner := &lifecycleRunnerStub{services: []LifecycleServiceStatus{{Name: "api", State: "running", Running: true}}}
	result, err := NewManager(runner).Lifecycle(context.Background(), LifecycleRequest{
		ProjectID: "compose:demo", WorkingDirectory: "/volume2/Project/demo", ConfigFiles: []string{"compose.yaml"}, Action: LifecycleActionStart,
	})
	if err != nil {
		t.Fatalf("Lifecycle() error = %v", err)
	}
	if !result.Completed || result.State != "running" || result.Action != LifecycleActionStart || len(result.Services) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if runner.action != "start" || runner.workingDirectory != "/volume2/Project/demo" || len(runner.configFiles) != 1 || runner.configFiles[0] != "/volume2/Project/demo/compose.yaml" {
		t.Fatalf("runner call = %#v", runner)
	}
}

func TestManagerLifecycleRejectsStateMismatch(t *testing.T) {
	runner := &lifecycleRunnerStub{services: []LifecycleServiceStatus{{Name: "api", State: "exited", Running: false}}}
	result, err := NewManager(runner).Lifecycle(context.Background(), LifecycleRequest{
		ProjectID: "compose:demo", WorkingDirectory: "/volume2/Project/demo", ConfigFiles: []string{"compose.yaml"}, Action: LifecycleActionRestart,
	})
	if ErrorCode(err) != "COMPOSE_LIFECYCLE_VERIFY_FAILED" {
		t.Fatalf("error code = %q", ErrorCode(err))
	}
	if result.Completed || result.State != "stopped" {
		t.Fatalf("result = %#v", result)
	}
}

func TestManagerLifecycleRejectsUnsafeConfigPathBeforeRunner(t *testing.T) {
	runner := &lifecycleRunnerStub{}
	request := LifecycleRequest{
		ProjectID: "compose:demo", WorkingDirectory: "/volume2/Project/demo", ConfigFiles: []string{"compose.yaml", "/etc/compose.yaml"}, Action: LifecycleActionStop,
	}
	result, err := NewManager(runner).Lifecycle(context.Background(), request)
	if ErrorCode(err) != "COMPOSE_LIFECYCLE_INVALID" {
		t.Fatalf("error code = %q", ErrorCode(err))
	}
	if len(runner.calls) != 0 || !reflect.DeepEqual(result, LifecycleResult{}) {
		t.Fatalf("runner calls = %v, result = %#v; want no calls and zero result", runner.calls, result)
	}
	if !reflect.DeepEqual(request.ConfigFiles, []string{"compose.yaml", "/etc/compose.yaml"}) {
		t.Fatalf("Lifecycle() changed caller config files: %v", request.ConfigFiles)
	}
}

func TestManagerLifecycleRejectsInvalidActionBeforeRunner(t *testing.T) {
	for _, action := range []LifecycleAction{"", " \t", "launch", "start stop"} {
		t.Run(string(action), func(t *testing.T) {
			runner := &lifecycleRunnerStub{}
			request := LifecycleRequest{
				ProjectID: "compose:demo", WorkingDirectory: "/volume2/Project/demo", ConfigFiles: []string{"compose.yaml"}, Action: action,
			}
			result, err := NewManager(runner).Lifecycle(context.Background(), request)
			if ErrorCode(err) != "COMPOSE_LIFECYCLE_INVALID" {
				t.Fatalf("error code = %q", ErrorCode(err))
			}
			if len(runner.calls) != 0 || !reflect.DeepEqual(result, LifecycleResult{}) {
				t.Fatalf("runner calls = %v, result = %#v; want no calls and zero result", runner.calls, result)
			}
			if !reflect.DeepEqual(request.ConfigFiles, []string{"compose.yaml"}) {
				t.Fatalf("Lifecycle() changed caller config files: %v", request.ConfigFiles)
			}
		})
	}
}

func TestParseComposePSSupportsJSONLinesAndArrays(t *testing.T) {
	for _, output := range []string{
		`[{"ID":"a","Name":"demo-api-1","Service":"api","State":"running"},{"ID":"b","Name":"demo-db-1","Service":"db","State":"exited"}]`,
		`{"ID":"a","Name":"demo-api-1","Service":"api","State":"running"}
{"ID":"b","Name":"demo-db-1","Service":"db","State":"exited"}`,
	} {
		services, err := parseComposePS(output)
		if err != nil || len(services) != 2 {
			t.Fatalf("parse %q: services=%#v err=%v", output, services, err)
		}
		if services[0].Name != "api" || !services[0].Running || services[1].Name != "db" || services[1].Running {
			t.Fatalf("services = %#v", services)
		}
	}
}

type lifecycleRunnerStub struct {
	action           string
	workingDirectory string
	configFiles      []string
	services         []LifecycleServiceStatus
	calls            []string
	actionErr        error
	inspectErr       error
	onAction         func()
	onInspect        func()
}

func (runner *lifecycleRunnerStub) Validate(context.Context, string, string) (string, error) {
	return "", nil
}

func (runner *lifecycleRunnerStub) Deploy(context.Context, string, []string) (string, error) {
	return "", nil
}

func (runner *lifecycleRunnerStub) Start(ctx context.Context, workingDirectory string, configFiles []string) (string, error) {
	return runner.run(ctx, "start", workingDirectory, configFiles)
}

func (runner *lifecycleRunnerStub) Stop(ctx context.Context, workingDirectory string, configFiles []string) (string, error) {
	return runner.run(ctx, "stop", workingDirectory, configFiles)
}

func (runner *lifecycleRunnerStub) Restart(ctx context.Context, workingDirectory string, configFiles []string) (string, error) {
	return runner.run(ctx, "restart", workingDirectory, configFiles)
}

func (runner *lifecycleRunnerStub) Inspect(context.Context, string, []string) ([]LifecycleServiceStatus, error) {
	runner.calls = append(runner.calls, "inspect")
	if runner.onInspect != nil {
		runner.onInspect()
	}
	return runner.services, runner.inspectErr
}

func (runner *lifecycleRunnerStub) run(_ context.Context, action, workingDirectory string, configFiles []string) (string, error) {
	runner.calls = append(runner.calls, action)
	runner.action = action
	runner.workingDirectory = workingDirectory
	runner.configFiles = append([]string(nil), configFiles...)
	if runner.onAction != nil {
		runner.onAction()
	}
	return "done", runner.actionErr
}
