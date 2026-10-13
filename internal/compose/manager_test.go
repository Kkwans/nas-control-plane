package compose

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type fakeRunner struct {
	output string
	err    error
}

func (runner fakeRunner) Validate(context.Context, string, string) (string, error) {
	return runner.output, runner.err
}

func (runner fakeRunner) Deploy(context.Context, string, []string) (string, error) {
	return runner.output, runner.err
}

func TestValidateExtractsComposeServices(t *testing.T) {
	manager := NewManager(fakeRunner{output: "name: demo\nservices:\n  api:\n    image: api:latest\n  web:\n    image: web:latest\nnetworks:\n  default:\n"})
	result, err := manager.Validate(context.Background(), ValidateRequest{
		Path:    "/volume2/Project/demo/compose.yaml",
		Content: "services:\n  api:\n    image: api:latest\n",
	})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !result.Valid || strings.Join(result.Services, ",") != "api,web" {
		t.Fatalf("result = %#v", result)
	}
}

func TestValidateRejectsConfigOutsideProjectVolumes(t *testing.T) {
	manager := NewManager(fakeRunner{})
	if _, err := manager.Validate(context.Background(), ValidateRequest{Path: "/etc/compose.yaml", Content: "services: {}"}); err == nil {
		t.Fatal("Validate() error = nil, want path rejection")
	}
}

func TestReadRejectsConfigOutsideWorkingDirectory(t *testing.T) {
	manager := NewManager(fakeRunner{})
	_, err := manager.Read(context.Background(), ReadRequest{
		ProjectID: "compose:demo", WorkingDirectory: "/volume2/Project/demo",
		ConfigFiles: []string{"/volume2/Project/other/compose.yaml"},
	})
	if err == nil {
		t.Fatal("Read() error = nil, want path rejection")
	}
}

// 部署测试仅使用临时文件和可控 runner，不创建 NAS 目录或启动 Docker。
type deployTestRunner struct {
	deploy func(context.Context, string, []string) (string, error)
}

func (r deployTestRunner) Validate(_ context.Context, _ string, content string) (string, error) {
	return content, nil
}

func (r deployTestRunner) Deploy(ctx context.Context, directory string, files []string) (string, error) {
	return r.deploy(ctx, directory, files)
}

func deployFixture(t *testing.T) DeployRequest {
	t.Helper()
	directory := t.TempDir()
	target := filepath.Join(directory, "compose.yaml")
	if err := os.WriteFile(target, []byte("services: old\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	return DeployRequest{ProjectID: "demo", WorkingDirectory: directory, TargetPath: target, ConfigFiles: []string{target}, Content: "services: new\n"}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("file %s = %q, %v; want %q", path, got, err, want)
	}
}

func TestDeployCancellationRollbackHasIndependentBoundedContext(t *testing.T) {
	for _, rollbackFails := range []bool{false, true} {
		t.Run(fmt.Sprint(rollbackFails), func(t *testing.T) {
			request := deployFixture(t)
			type contextKey struct{}
			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "trace"))
			deployStarted := make(chan struct{})
			cancellationDone := make(chan struct{})
			go func() {
				select {
				case <-deployStarted:
					cancel()
				case <-ctx.Done():
				}
				close(cancellationDone)
			}()
			defer cancel()
			rollbackErr := errors.New("rollback refused")
			calls := 0
			var rollbackContext context.Context
			manager := NewManager(deployTestRunner{deploy: func(commandCtx context.Context, _ string, _ []string) (string, error) {
				calls++
				if calls == 1 {
					assertFile(t, request.TargetPath, request.Content)
					close(deployStarted)
					<-commandCtx.Done()
					return "", commandCtx.Err()
				}
				rollbackContext = commandCtx
				assertFile(t, request.TargetPath, "services: old\n")
				if commandCtx.Err() != nil || commandCtx.Value(contextKey{}) != "trace" {
					t.Fatalf("rollback context lost values or inherited cancellation: %v", commandCtx.Err())
				}
				deadline, ok := commandCtx.Deadline()
				if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > defaultRollbackTimeout {
					t.Fatalf("rollback deadline = %v, %v", deadline, ok)
				}
				if rollbackFails {
					return "", rollbackErr
				}
				return "restored", nil
			}})
			result, err := manager.deploy(ctx, request)
			cancel()
			<-cancellationDone
			if calls != 2 || result.Completed || !errors.Is(err, context.Canceled) {
				t.Fatalf("calls=%d result=%+v err=%v", calls, result, err)
			}
			if rollbackFails && (!errors.Is(err, rollbackErr) || !strings.Contains(err.Error(), "rollback failed")) {
				t.Fatalf("rollback failure missing: %v", err)
			}
			if !rollbackFails && !strings.Contains(err.Error(), "previous version was restored") {
				t.Fatalf("rollback success missing: %v", err)
			}
			if rollbackContext.Err() != context.Canceled {
				t.Fatalf("rollback context not released: %v", rollbackContext.Err())
			}
			assertFile(t, request.TargetPath, "services: old\n")
			info, err := os.Stat(request.TargetPath)
			if err != nil || info.Mode().Perm() != 0o640 {
				t.Fatalf("restored mode: %v, %v", info, err)
			}
		})
	}
}

func TestDeployRollbackTimeoutIsReported(t *testing.T) {
	request := deployFixture(t)
	deployErr := errors.New("deploy failed")
	calls := 0
	manager := NewManager(deployTestRunner{deploy: func(ctx context.Context, _ string, _ []string) (string, error) {
		calls++
		if calls == 1 {
			return "", deployErr
		}
		<-ctx.Done()
		return "", ctx.Err()
	}})
	manager.rollbackTimeout = 10 * time.Millisecond
	_, err := manager.deploy(context.Background(), request)
	if !errors.Is(err, deployErr) || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("error = %v", err)
	}
}

func TestDeployBackupsNeverOverwriteWithinSameSecond(t *testing.T) {
	request := deployFixture(t)
	manager := NewManager(deployTestRunner{deploy: func(context.Context, string, []string) (string, error) { return "ok", nil }})
	manager.now = func() time.Time { return time.Unix(1, 0) }
	first, err := manager.deploy(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.Content = "services: newest\n"
	second, err := manager.deploy(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.BackupPath == second.BackupPath || !first.Completed || !second.Completed {
		t.Fatalf("backups/results = %+v / %+v", first, second)
	}
	assertFile(t, first.BackupPath, "services: old\n")
	assertFile(t, second.BackupPath, "services: new\n")
	for _, path := range []string{first.BackupPath, second.BackupPath} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("backup mode: %v, %v", info, err)
		}
	}
}

func TestTargetLockCancellationAndIndependentTargets(t *testing.T) {
	manager := NewManager(fakeRunner{})
	unlock, err := manager.lockTarget(context.Background(), "first")
	if err != nil {
		t.Fatal(err)
	}
	otherUnlock, err := manager.lockTarget(context.Background(), "other")
	if err != nil {
		t.Fatal(err)
	}
	otherUnlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.lockTarget(ctx, "first"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled lock error = %v", err)
	}
	unlock()
	if len(manager.targetLocks) != 0 {
		t.Fatal("idle target locks leaked")
	}
}

func TestDeploySameTargetWaitsThroughRollback(t *testing.T) {
	request := deployFixture(t)
	rollbackStarted, releaseRollback := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-releaseRollback:
		default:
			close(releaseRollback)
		}
	}()
	var calls atomic.Int32
	manager := NewManager(deployTestRunner{deploy: func(_ context.Context, _ string, _ []string) (string, error) {
		switch calls.Add(1) {
		case 1:
			return "", errors.New("deploy failed")
		case 2:
			close(rollbackStarted)
			<-releaseRollback
		}
		return "ok", nil
	}})
	firstDone := make(chan error, 1)
	go func() { _, err := manager.deploy(context.Background(), request); firstDone <- err }()
	select {
	case <-rollbackStarted:
	case <-time.After(time.Second):
		t.Fatal("rollback not started")
	}
	second := request
	second.Content = "services: second\n"
	secondDone := make(chan error, 1)
	go func() { _, err := manager.deploy(context.Background(), second); secondDone <- err }()
	// 等待第二个事务实际登记为等待者，避免仅凭调度延时判断串行化。
	deadline := time.Now().Add(time.Second)
	for {
		manager.locksMu.Lock()
		users := manager.targetLocks[request.TargetPath].users
		manager.locksMu.Unlock()
		if users == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second deploy did not wait")
		}
		time.Sleep(time.Millisecond)
	}
	assertFile(t, request.TargetPath, "services: old\n")
	if calls.Load() != 2 {
		t.Fatal("second deploy entered before rollback completed")
	}
	close(releaseRollback)
	if err := <-firstDone; err == nil {
		t.Fatal("first deploy should fail")
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	assertFile(t, request.TargetPath, second.Content)
	if calls.Load() != 3 {
		t.Fatalf("calls = %d", calls.Load())
	}
	if len(manager.targetLocks) != 0 {
		t.Fatal("idle target locks leaked")
	}
}

func TestDeployReportsConfigRestoreFailureWithoutRunningRollback(t *testing.T) {
	request := deployFixture(t)
	deployErr := errors.New("deploy failed")
	calls := 0
	manager := NewManager(deployTestRunner{deploy: func(context.Context, string, []string) (string, error) {
		calls++
		if err := os.Remove(request.TargetPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(request.TargetPath, 0o700); err != nil {
			t.Fatal(err)
		}
		return "", deployErr
	}})
	result, err := manager.deploy(context.Background(), request)
	if result.Completed || calls != 1 || !errors.Is(err, deployErr) || !strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("calls=%d result=%+v error=%v", calls, result, err)
	}
}

func TestDeployCancelledWhileWaitingDoesNotModifyFiles(t *testing.T) {
	request := deployFixture(t)
	manager := NewManager(deployTestRunner{deploy: func(context.Context, string, []string) (string, error) {
		t.Fatal("cancelled waiter invoked runner")
		return "", nil
	}})
	unlock, err := manager.lockTarget(context.Background(), request.TargetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := manager.deploy(ctx, request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	assertFile(t, request.TargetPath, "services: old\n")
	if _, err := os.Stat(filepath.Join(request.WorkingDirectory, ".ncp-backups")); !os.IsNotExist(err) {
		t.Fatalf("cancelled waiter created backups: %v", err)
	}
}

func TestTargetLockAlreadyCancelledWithAvailableToken(t *testing.T) {
	manager := NewManager(fakeRunner{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// 同时可读的取消信号和空闲 token 不能随机放行已取消请求。
	for range 100 {
		if unlock, err := manager.lockTarget(ctx, "available"); !errors.Is(err, context.Canceled) || unlock != nil {
			t.Fatalf("cancelled lock = %v, unlock present = %v", err, unlock != nil)
		}
		if len(manager.targetLocks) != 0 {
			t.Fatal("cancelled target lock leaked")
		}
	}
}

func TestDeployDeadlineRollbackUsesFreshContext(t *testing.T) {
	request := deployFixture(t)
	// 虚拟时间仅在所有 goroutine 持久阻塞时推进，不把慢盘耗时算入原请求预算。
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		calls := 0
		manager := NewManager(deployTestRunner{deploy: func(commandCtx context.Context, _ string, _ []string) (string, error) {
			calls++
			if calls == 1 {
				<-commandCtx.Done()
				return "", commandCtx.Err()
			}
			if commandCtx.Err() != nil {
				t.Fatalf("rollback inherited deadline: %v", commandCtx.Err())
			}
			return "restored", nil
		}})
		_, err := manager.deploy(ctx, request)
		if calls != 2 || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "previous version was restored") {
			t.Fatalf("calls=%d error=%v", calls, err)
		}
	})
}
