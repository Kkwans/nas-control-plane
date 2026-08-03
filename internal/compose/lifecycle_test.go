package compose

import (
	"context"
	"testing"
)

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
	_, err := NewManager(runner).Lifecycle(context.Background(), LifecycleRequest{
		ProjectID: "compose:demo", WorkingDirectory: "/volume2/Project/demo", ConfigFiles: []string{"/etc/compose.yaml"}, Action: LifecycleActionStop,
	})
	if ErrorCode(err) != "COMPOSE_LIFECYCLE_INVALID" {
		t.Fatalf("error code = %q", ErrorCode(err))
	}
	if runner.action != "" {
		t.Fatalf("runner action = %q, want none", runner.action)
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
	return runner.services, nil
}

func (runner *lifecycleRunnerStub) run(_ context.Context, action, workingDirectory string, configFiles []string) (string, error) {
	runner.action = action
	runner.workingDirectory = workingDirectory
	runner.configFiles = append([]string(nil), configFiles...)
	return "done", nil
}
