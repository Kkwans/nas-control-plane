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
	"sync"
	"time"
)

const maxConfigBytes = 2 << 20

const defaultRollbackTimeout = 2 * time.Minute

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

type DeployRequest struct {
	ProjectID        string   `json:"projectId"`
	WorkingDirectory string   `json:"workingDirectory"`
	ConfigFiles      []string `json:"configFiles"`
	TargetPath       string   `json:"targetPath"`
	Content          string   `json:"content"`
}

type DeployResult struct {
	ProjectID  string   `json:"projectId"`
	TargetPath string   `json:"targetPath"`
	BackupPath string   `json:"backupPath"`
	Services   []string `json:"services"`
	Output     string   `json:"output"`
	Completed  bool     `json:"completed"`
}

type Runner interface {
	Validate(context.Context, string, string) (string, error)
	Deploy(context.Context, string, []string) (string, error)
}

func (manager *Manager) Deploy(ctx context.Context, request DeployRequest) (DeployResult, error) {
	workingDirectory, err := validateWorkingDirectory(request.WorkingDirectory)
	if err != nil {
		return DeployResult{}, err
	}
	targetPath, err := validateConfigPath(workingDirectory, request.TargetPath)
	if err != nil {
		return DeployResult{}, err
	}
	configFiles := make([]string, 0, len(request.ConfigFiles))
	targetKnown := false
	for _, value := range request.ConfigFiles {
		configPath, err := validateConfigPath(workingDirectory, value)
		if err != nil {
			return DeployResult{}, err
		}
		configFiles = append(configFiles, configPath)
		targetKnown = targetKnown || configPath == targetPath
	}
	if !targetKnown {
		return DeployResult{}, errors.New("compose target is not part of project config")
	}
	request.WorkingDirectory = workingDirectory
	request.TargetPath = targetPath
	request.ConfigFiles = configFiles
	return manager.deploy(ctx, request)
}

// deploy 只接收已通过路径校验和规范化的请求；将事务单独拆出便于隔离文件系统测试。
func (manager *Manager) deploy(ctx context.Context, request DeployRequest) (DeployResult, error) {
	unlock, err := manager.lockTarget(ctx, request.TargetPath)
	if err != nil {
		return DeployResult{}, err
	}
	defer unlock()
	workingDirectory, targetPath, configFiles := request.WorkingDirectory, request.TargetPath, request.ConfigFiles
	validation, err := manager.validateContent(ctx, targetPath, request.Content)
	if err != nil {
		return DeployResult{}, err
	}
	original, err := os.ReadFile(targetPath)
	if err != nil {
		return DeployResult{}, err
	}
	info, err := os.Stat(targetPath)
	if err != nil {
		return DeployResult{}, err
	}
	backupDirectory := pathpkg.Join(workingDirectory, ".ncp-backups")
	if err := os.MkdirAll(backupDirectory, 0o700); err != nil {
		return DeployResult{}, err
	}
	backupPath, err := writeBackup(backupDirectory, manager.now().UTC().Format("20060102T150405Z")+"-"+pathpkg.Base(targetPath)+"-*", original)
	if err != nil {
		return DeployResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return DeployResult{}, err
	}
	if err := atomicWrite(targetPath, []byte(request.Content), info.Mode().Perm()); err != nil {
		return DeployResult{}, err
	}
	output, deployErr := manager.runner.Deploy(ctx, workingDirectory, configFiles)
	if deployErr != nil {
		// 原部署可能已超时或取消；回滚 context 保留追踪信息并设置独立时间预算。
		// 此预算不保证同步磁盘操作或不响应取消的 runner 在截止时硬退出。
		timeout := manager.rollbackTimeout
		if timeout <= 0 {
			timeout = defaultRollbackTimeout
		}
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cancel()
		restoreErr := atomicWrite(targetPath, original, info.Mode().Perm())
		if restoreErr == nil {
			_, restoreErr = manager.runner.Deploy(rollbackCtx, workingDirectory, configFiles)
		}
		if restoreErr != nil {
			return DeployResult{}, fmt.Errorf("compose deploy failed and rollback failed: %w; rollback: %w", deployErr, restoreErr)
		}
		return DeployResult{}, fmt.Errorf("compose deploy failed and previous version was restored: %w", deployErr)
	}
	return DeployResult{
		ProjectID: request.ProjectID, TargetPath: targetPath, BackupPath: backupPath,
		Services: validation.Services, Output: output, Completed: true,
	}, nil
}

type Manager struct {
	runner          Runner
	now             func() time.Time
	rollbackTimeout time.Duration
	locksMu         sync.Mutex
	targetLocks     map[string]*targetLock
}

// 锁覆盖校验、备份、部署和回滚；同一 Manager 的不同目标仍可并行。
type targetLock struct {
	token chan struct{}
	users int
}

func (manager *Manager) lockTarget(ctx context.Context, target string) (func(), error) {
	manager.locksMu.Lock()
	if manager.targetLocks == nil {
		manager.targetLocks = make(map[string]*targetLock)
	}
	lock := manager.targetLocks[target]
	if lock == nil {
		lock = &targetLock{token: make(chan struct{}, 1)}
		lock.token <- struct{}{}
		manager.targetLocks[target] = lock
	}
	lock.users++
	manager.locksMu.Unlock()
	releaseReference := func() {
		manager.locksMu.Lock()
		defer manager.locksMu.Unlock()
		lock.users--
		if lock.users == 0 {
			delete(manager.targetLocks, target)
		}
	}
	select {
	case <-ctx.Done():
		releaseReference()
		return nil, ctx.Err()
	case <-lock.token:
		if err := ctx.Err(); err != nil {
			lock.token <- struct{}{}
			releaseReference()
			return nil, err
		}
		return func() {
			lock.token <- struct{}{}
			releaseReference()
		}, nil
	}
}

func NewManager(runner Runner) *Manager {
	if runner == nil {
		runner = OSRunner{}
	}
	return &Manager{runner: runner, now: time.Now, rollbackTimeout: defaultRollbackTimeout}
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
	return manager.validateContent(ctx, configPath, request.Content)
}

func (manager *Manager) validateContent(ctx context.Context, configPath, content string) (ValidationResult, error) {
	if content == "" || len(content) > maxConfigBytes {
		return ValidationResult{}, errors.New("compose content is empty or too large")
	}
	normalized, err := manager.runner.Validate(ctx, pathpkg.Dir(configPath), content)
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
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("compose validation failed: %s", strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (OSRunner) Deploy(ctx context.Context, workingDirectory string, configFiles []string) (string, error) {
	arguments := []string{"compose"}
	for _, configFile := range configFiles {
		arguments = append(arguments, "-f", configFile)
	}
	arguments = append(arguments, "up", "-d")
	command := exec.CommandContext(ctx, "docker", arguments...)
	command.Dir = workingDirectory
	output, err := command.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("compose deploy failed: %s", strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
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

func atomicWrite(target string, content []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(pathpkg.Dir(target), ".ncp-compose-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, target)
}

// CreateTemp 使用独占创建，固定时钟或同秒部署也不会覆盖已有备份。
func writeBackup(directory, pattern string, content []byte) (string, error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if _, err := file.Write(content); err != nil {
		file.Close()
		os.Remove(path)
		return "", err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}
