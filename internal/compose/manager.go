package compose

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	pathpkg "path"
	"strings"
	"time"
)

const maxConfigBytes = 2 << 20

type FileSnapshot struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Size    int64  `json:"size"`
}

type ProjectConfig struct {
	ProjectID        string         `json:"projectId"`
	WorkingDirectory string         `json:"workingDirectory"`
	Files            []FileSnapshot `json:"files"`
	CollectedAt      time.Time      `json:"collectedAt"`
}

type ReadRequest struct {
	ProjectID        string   `json:"projectId"`
	WorkingDirectory string   `json:"workingDirectory"`
	ConfigFiles      []string `json:"configFiles"`
}

type ValidateRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type ValidationResult struct {
	Valid      bool     `json:"valid"`
	Services   []string `json:"services"`
	Normalized string   `json:"normalized"`
}

type Runner interface {
	Validate(context.Context, string, string) (string, error)
}

type Manager struct {
	runner Runner
	now    func() time.Time
}

func NewManager(runner Runner) *Manager {
	if runner == nil {
		runner = OSRunner{}
	}
	return &Manager{runner: runner, now: time.Now}
}

func (manager *Manager) Read(ctx context.Context, request ReadRequest) (ProjectConfig, error) {
	workingDirectory, err := validateWorkingDirectory(request.WorkingDirectory)
	if err != nil {
		return ProjectConfig{}, err
	}
	if strings.TrimSpace(request.ProjectID) == "" || len(request.ConfigFiles) == 0 {
		return ProjectConfig{}, errors.New("compose project or config files are missing")
	}
	files := make([]FileSnapshot, 0, len(request.ConfigFiles))
	for _, value := range request.ConfigFiles {
		path, err := validateConfigPath(workingDirectory, value)
		if err != nil {
			return ProjectConfig{}, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return ProjectConfig{}, err
		}
		if info.Size() > maxConfigBytes {
			return ProjectConfig{}, errors.New("compose config is too large")
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return ProjectConfig{}, err
		}
		files = append(files, FileSnapshot{Path: path, Name: pathpkg.Base(path), Content: string(content), Size: info.Size()})
	}
	return ProjectConfig{ProjectID: request.ProjectID, WorkingDirectory: workingDirectory, Files: files, CollectedAt: manager.now().UTC()}, nil
}

func (manager *Manager) Validate(ctx context.Context, request ValidateRequest) (ValidationResult, error) {
	configPath, err := validateConfigPath(pathpkg.Dir(request.Path), request.Path)
	if err != nil {
		return ValidationResult{}, err
	}
	if request.Content == "" || len(request.Content) > maxConfigBytes {
		return ValidationResult{}, errors.New("compose content is empty or too large")
	}
	normalized, err := manager.runner.Validate(ctx, pathpkg.Dir(configPath), request.Content)
	if err != nil {
		return ValidationResult{Valid: false, Services: make([]string, 0)}, err
	}
	return ValidationResult{
		Valid:      true,
		Services:   extractServiceNames(normalized),
		Normalized: normalized,
	}, nil
}

type OSRunner struct{}

func (OSRunner) Validate(ctx context.Context, workingDirectory, content string) (string, error) {
	command := exec.CommandContext(ctx, "docker", "compose", "-f", "-", "config")
	command.Dir = workingDirectory
	command.Stdin = strings.NewReader(content)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("compose validation failed: %s", strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func validateWorkingDirectory(value string) (string, error) {
	cleaned := pathpkg.Clean(strings.TrimSpace(value))
	if !pathpkg.IsAbs(cleaned) || (!withinRoot(cleaned, "/volume1") && !withinRoot(cleaned, "/volume2")) {
		return "", errors.New("compose working directory is outside NAS data volumes")
	}
	return cleaned, nil
}

func validateConfigPath(workingDirectory, value string) (string, error) {
	directory, err := validateWorkingDirectory(workingDirectory)
	if err != nil {
		return "", err
	}
	configPath := pathpkg.Clean(strings.TrimSpace(value))
	if !pathpkg.IsAbs(configPath) {
		configPath = pathpkg.Join(directory, configPath)
	}
	if !withinRoot(configPath, directory) {
		return "", errors.New("compose config is outside project directory")
	}
	extension := strings.ToLower(pathpkg.Ext(configPath))
	if extension != ".yml" && extension != ".yaml" {
		return "", errors.New("compose config must be yaml")
	}
	return configPath, nil
}

func withinRoot(path, root string) bool {
	cleanRoot := strings.TrimSuffix(pathpkg.Clean(root), "/")
	cleanPath := pathpkg.Clean(path)
	return cleanPath == cleanRoot || strings.HasPrefix(cleanPath, cleanRoot+"/")
}

func extractServiceNames(normalized string) []string {
	lines := strings.Split(normalized, "\n")
	services := make([]string, 0)
	inServices := false
	for _, line := range lines {
		if line == "services:" {
			inServices = true
			continue
		}
		if !inServices {
			continue
		}
		if line != "" && line[0] != ' ' {
			break
		}
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") && strings.HasSuffix(strings.TrimSpace(line), ":") {
			services = append(services, strings.TrimSuffix(strings.TrimSpace(line), ":"))
		}
	}
	return services
}
