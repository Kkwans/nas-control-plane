package compose

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	configFiles := make([]string, len(r.ConfigFiles))
	for index, value := range r.ConfigFiles {
		configPath, err := validateConfigPath(workingDirectory, value)
		if err != nil {
			return LifecycleRequest{}, coded("COMPOSE_LIFECYCLE_INVALID", err)
		}
		if _, exists := seen[configPath]; exists {
			return LifecycleRequest{}, coded("COMPOSE_LIFECYCLE_INVALID", errors.New("compose config files contain duplicates"))
		}
		seen[configPath] = struct{}{}
		configFiles[index] = configPath
	}
	action, err := ParseLifecycleAction(string(r.Action))
	if err != nil {
		return LifecycleRequest{}, err
	}
	r.ConfigFiles = configFiles
	r.Action = action
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
		// Running 是公开投影，不作为未知或缺失 State 的完成证据。
		service.Running = service.State == "running"
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

// 明确的非运行观察不等同于健康停止；保留既有 stopped 兼容值。
func lifecycleStoppedState(state string) bool {
	switch state {
	case "stopped", "exited", "created", "dead":
		return true
	default:
		return false
	}
}

func lifecycleMatchesAction(action LifecycleAction, services []LifecycleServiceStatus) bool {
	if len(services) == 0 {
		return false
	}
	for _, service := range services {
		switch action {
		case LifecycleActionStart, LifecycleActionRestart:
			if service.State != "running" {
				return false
			}
		case LifecycleActionStop:
			if !lifecycleStoppedState(service.State) {
				return false
			}
		default:
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
		if service.State == "running" {
			running++
		} else if !lifecycleStoppedState(service.State) {
			return "unknown"
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

// 已知字段使用精确名称，拒绝重复键与大小写别名；CLI 的其他字段保持兼容。
// 不使用 struct 的零值解码作为证据，否则 null/缺省会被误判为停止。
func (record *composePSRecord) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("compose ps record is not an object")
	}
	var decoded composePSRecord
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return errors.New("compose ps record key is invalid")
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("compose ps record key is invalid")
		}
		canonical := ""
		for _, known := range []string{"ID", "Name", "Service", "State", "Running"} {
			if strings.EqualFold(key, known) {
				canonical = known
				break
			}
		}
		if canonical != "" && (key != canonical || seen[canonical]) {
			return errors.New("compose ps record has ambiguous fields")
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return errors.New("compose ps record value is invalid")
		}
		if canonical == "" {
			continue
		}
		seen[canonical] = true
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return errors.New("compose ps record field is null")
		}
		var target any
		switch canonical {
		case "ID":
			target = &decoded.ID
		case "Name":
			target = &decoded.Name
		case "Service":
			target = &decoded.Service
		case "State":
			target = &decoded.State
		case "Running":
			target = &decoded.Running
		}
		if err := json.Unmarshal(raw, target); err != nil {
			return errors.New("compose ps record field type is invalid")
		}
	}
	if _, err := decoder.Token(); err != nil {
		return errors.New("compose ps record is incomplete")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("compose ps record has trailing data")
	}
	decoded.State = strings.ToLower(strings.TrimSpace(decoded.State))
	if !seen["State"] || decoded.State == "" {
		return errors.New("compose ps record state is missing")
	}
	if decoded.Running != nil && ((decoded.State == "running" && !*decoded.Running) ||
		(lifecycleStoppedState(decoded.State) && *decoded.Running)) {
		return errors.New("compose ps record state is contradictory")
	}
	*record = decoded
	return nil
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
	if records == nil {
		return nil, errors.New("compose ps output is not a record collection")
	}
	result := make([]LifecycleServiceStatus, 0, len(records))
	seenIDs := make(map[string]bool)
	for _, record := range records {
		state := strings.ToLower(strings.TrimSpace(record.State))
		running := state == "running"
		name := strings.TrimSpace(record.Service)
		if name == "" {
			name = strings.TrimPrefix(strings.TrimSpace(record.Name), "/")
		}
		id := strings.TrimSpace(record.ID)
		if id == "" && name == "" {
			return nil, errors.New("compose ps record identity is missing")
		}
		if id != "" {
			if seenIDs[id] {
				return nil, errors.New("compose ps record identity is repeated")
			}
			seenIDs[id] = true
		}
		result = append(result, LifecycleServiceStatus{
			Name: name, ContainerID: id, State: state, Running: running,
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
