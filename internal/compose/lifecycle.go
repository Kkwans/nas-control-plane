package compose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

type LifecycleAction string

const (
	LifecycleActionStart   LifecycleAction = "start"
	LifecycleActionStop    LifecycleAction = "stop"
	LifecycleActionRestart LifecycleAction = "restart"
)

func ParseLifecycleAction(value string) (LifecycleAction, error) {
	action := LifecycleAction(strings.ToLower(strings.TrimSpace(value)))
	switch action {
	case LifecycleActionStart, LifecycleActionStop, LifecycleActionRestart:
		return action, nil
	default:
		return "", coded("COMPOSE_LIFECYCLE_INVALID", errors.New("unsupported compose lifecycle action"))
	}
}

type LifecycleRequest struct {
	ProjectID        string          `json:"projectId"`
	WorkingDirectory string          `json:"workingDirectory"`
	ConfigFiles      []string        `json:"configFiles"`
	Action           LifecycleAction `json:"action"`
}

func (r LifecycleRequest) Normalize() (LifecycleRequest, error) {
	r.ProjectID = strings.TrimSpace(r.ProjectID)
	if r.ProjectID == "" || len(r.ConfigFiles) == 0 {
		return LifecycleRequest{}, coded("COMPOSE_LIFECYCLE_INVALID", errors.New("compose project and config files are required"))
	}
	workingDirectory, err := validateWorkingDirectory(r.WorkingDirectory)
	if err != nil {
		return LifecycleRequest{}, coded("COMPOSE_LIFECYCLE_INVALID", err)
	}
	r.WorkingDirectory = workingDirectory
	seen := make(map[string]struct{}, len(r.ConfigFiles))
	for index, value := range r.ConfigFiles {
		configPath, err := validateConfigPath(workingDirectory, value)
		if err != nil {
			return LifecycleRequest{}, coded("COMPOSE_LIFECYCLE_INVALID", err)
		}
		if _, exists := seen[configPath]; exists {
			return LifecycleRequest{}, coded("COMPOSE_LIFECYCLE_INVALID", errors.New("compose config files contain duplicates"))
		}
		seen[configPath] = struct{}{}
		r.ConfigFiles[index] = configPath
	}
	if _, err := ParseLifecycleAction(string(r.Action)); err != nil {
		return LifecycleRequest{}, err
	}
	return r, nil
}

func (r LifecycleRequest) Validate() error {
	_, err := r.Normalize()
	return err
}

type LifecycleServiceStatus struct {
	Name        string `json:"name"`
	ContainerID string `json:"containerId,omitempty"`
	State       string `json:"state"`
	Running     bool   `json:"running"`
}

type LifecycleResult struct {
	ProjectID string                   `json:"projectId"`
	Action    LifecycleAction          `json:"action"`
	State     string                   `json:"state"`
	Services  []LifecycleServiceStatus `json:"services"`
	Output    string                   `json:"output"`
	Completed bool                     `json:"completed"`
}

// LifecycleRunner is intentionally separate from Runner so existing read,
// validation and deployment test doubles remain source-compatible. A live
// OSRunner implements both interfaces.
type LifecycleRunner interface {
	Start(context.Context, string, []string) (string, error)
	Stop(context.Context, string, []string) (string, error)
	Restart(context.Context, string, []string) (string, error)
	Inspect(context.Context, string, []string) ([]LifecycleServiceStatus, error)
}

func (manager *Manager) Lifecycle(ctx context.Context, request LifecycleRequest) (LifecycleResult, error) {
	request, err := request.Normalize()
	if err != nil {
		return LifecycleResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return LifecycleResult{}, err
	}
	runner, ok := manager.runner.(LifecycleRunner)
	if !ok {
		return LifecycleResult{}, coded("COMPOSE_LIFECYCLE_UNAVAILABLE", errors.New("compose lifecycle runner is not configured"))
	}

	result := LifecycleResult{
		ProjectID: request.ProjectID,
		Action:    request.Action,
		State:     "unknown",
		Services:  make([]LifecycleServiceStatus, 0),
	}
	var output string
	switch request.Action {
	case LifecycleActionStart:
		output, err = runner.Start(ctx, request.WorkingDirectory, request.ConfigFiles)
	case LifecycleActionStop:
		output, err = runner.Stop(ctx, request.WorkingDirectory, request.ConfigFiles)
	case LifecycleActionRestart:
		output, err = runner.Restart(ctx, request.WorkingDirectory, request.ConfigFiles)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			return result, err
		}
		return result, coded("COMPOSE_LIFECYCLE_FAILED", err)
	}
	result.Output = strings.TrimSpace(output)

	services, err := runner.Inspect(ctx, request.WorkingDirectory, request.ConfigFiles)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, coded("COMPOSE_LIFECYCLE_VERIFY_FAILED", err)
	}
	result.Services = normalizeServiceStatuses(services)
	result.State = lifecycleState(result.Services)
	if !lifecycleMatchesAction(request.Action, result.Services) {
		return result, coded("COMPOSE_LIFECYCLE_VERIFY_FAILED", errors.New("compose state does not match requested action"))
	}
	result.Completed = true
	return result, nil
}

func (manager *Manager) ControlComposeProject(ctx context.Context, request LifecycleRequest) (LifecycleResult, error) {
	return manager.Lifecycle(ctx, request)
}

func normalizeServiceStatuses(services []LifecycleServiceStatus) []LifecycleServiceStatus {
	result := make([]LifecycleServiceStatus, 0, len(services))
	for _, service := range services {
		service.Name = strings.TrimSpace(service.Name)
		service.ContainerID = strings.TrimSpace(service.ContainerID)
		service.State = strings.ToLower(strings.TrimSpace(service.State))
		if service.State == "running" {
			service.Running = true
		} else if service.State == "stopped" || service.State == "exited" || service.State == "created" || service.State == "dead" {
			service.Running = false
		}
		result = append(result, service)
	}
	sort.SliceStable(result, func(left, right int) bool {
		if result[left].Name != result[right].Name {
			return result[left].Name < result[right].Name
		}
		return result[left].ContainerID < result[right].ContainerID
	})
	return result
}

func lifecycleMatchesAction(action LifecycleAction, services []LifecycleServiceStatus) bool {
	if len(services) == 0 {
		return false
	}
	wantRunning := action != LifecycleActionStop
	for _, service := range services {
		if service.Running != wantRunning {
			return false
		}
	}
	return true
}

func lifecycleState(services []LifecycleServiceStatus) string {
	if len(services) == 0 {
		return "unknown"
	}
	running := 0
	for _, service := range services {
		if service.Running {
			running++
		}
	}
	if running == 0 {
		return "stopped"
	}
	if running == len(services) {
		return "running"
	}
	return "degraded"
}

func (runner OSRunner) Start(ctx context.Context, workingDirectory string, configFiles []string) (string, error) {
	return runner.runLifecycle(ctx, workingDirectory, configFiles, string(LifecycleActionStart))
}

func (runner OSRunner) Stop(ctx context.Context, workingDirectory string, configFiles []string) (string, error) {
	return runner.runLifecycle(ctx, workingDirectory, configFiles, string(LifecycleActionStop))
}

func (runner OSRunner) Restart(ctx context.Context, workingDirectory string, configFiles []string) (string, error) {
	return runner.runLifecycle(ctx, workingDirectory, configFiles, string(LifecycleActionRestart))
}

func (runner OSRunner) Inspect(ctx context.Context, workingDirectory string, configFiles []string) ([]LifecycleServiceStatus, error) {
	output, err := runner.runComposeCommand(ctx, workingDirectory, configFiles, "ps", "--all", "--format", "json")
	if err != nil {
		return nil, err
	}
	return parseComposePS(output)
}

func (runner OSRunner) runLifecycle(ctx context.Context, workingDirectory string, configFiles []string, action string) (string, error) {
	return runner.runComposeCommand(ctx, workingDirectory, configFiles, action)
}

func (runner OSRunner) runComposeCommand(ctx context.Context, workingDirectory string, configFiles []string, arguments ...string) (string, error) {
	commandArguments := []string{"compose"}
	for _, configFile := range configFiles {
		commandArguments = append(commandArguments, "-f", configFile)
	}
	commandArguments = append(commandArguments, arguments...)
	command := exec.CommandContext(ctx, "docker", commandArguments...)
	command.Dir = workingDirectory
	output, err := command.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("docker compose command failed: %s", strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

type composePSRecord struct {
	ID      string `json:"ID"`
	Name    string `json:"Name"`
	Service string `json:"Service"`
	State   string `json:"State"`
	Running *bool  `json:"Running"`
}

func parseComposePS(output string) ([]LifecycleServiceStatus, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return []LifecycleServiceStatus{}, nil
	}
	var records []composePSRecord
	if err := json.Unmarshal([]byte(output), &records); err != nil {
		records = make([]composePSRecord, 0)
		for _, line := range strings.Split(output, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var record composePSRecord
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				return nil, fmt.Errorf("compose ps output is invalid: %w", err)
			}
			records = append(records, record)
		}
	}
	result := make([]LifecycleServiceStatus, 0, len(records))
	for _, record := range records {
		state := strings.ToLower(strings.TrimSpace(record.State))
		running := state == "running"
		if record.Running != nil {
			running = *record.Running
		}
		name := strings.TrimSpace(record.Service)
		if name == "" {
			name = strings.TrimPrefix(strings.TrimSpace(record.Name), "/")
		}
		if state == "" {
			if running {
				state = "running"
			} else {
				state = "stopped"
			}
		}
		result = append(result, LifecycleServiceStatus{
			Name: name, ContainerID: strings.TrimSpace(record.ID), State: state, Running: running,
		})
	}
	return result, nil
}

type codedError struct {
	code string
	err  error
}

func (e *codedError) Error() string { return e.code }

func (e *codedError) Unwrap() error { return e.err }

func coded(code string, err error) error {
	return &codedError{code: code, err: err}
}

func ErrorCode(err error) string {
	var coded *codedError
	if errors.As(err, &coded) {
		return coded.code
	}
	return ""
}
