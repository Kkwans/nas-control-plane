//go:build linux

package agentsocket

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Linux 标准 go test ./... 会运行以下集成测试；需要允许 Unix bind 的隔离环境。
// 定向运行：go test ./internal/agentsocket -run 'TestSocketRecoveryIntegration'
// 所有端点均由 t.TempDir 创建，不访问 /run/ncp 或真实 Agent。
func TestSocketRecoveryIntegrationAfterCrash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.sock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestSocketRecoveryIntegrationChild$")
	cmd.Env = append(os.Environ(), "NCP_TEST_CRASH_SOCKET="+path)
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	ready := make(chan bool, 1)
	go func() { scanner := bufio.NewScanner(output); ready <- scanner.Scan() && scanner.Text() == "ready" }()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("child failed before listener was ready")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child startup timed out")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	before, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	listener, cleanup, err := listenUnixSocket(SocketConfig{SocketPath: path})
	if err != nil {
		t.Fatalf("restart after SIGKILL: %v", err)
	}
	defer cleanup()
	if listener == nil {
		t.Fatal("missing listener")
	}
	connection, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("recovered endpoint not reachable by configured path: %v", err)
	}
	_ = connection.Close()
	after, err := os.Stat(filepath.Dir(path))
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("runtime directory inode changed")
	}
	cleanup()
	cleanup()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("cleanup left socket: %v", err)
	}
	// 清理必须释放目录锁，可在同一目录再次启动。
	_, again, err := listenUnixSocket(SocketConfig{SocketPath: path})
	if err != nil {
		t.Fatal(err)
	}
	again()
}

func TestSocketRecoveryIntegrationChild(t *testing.T) {
	path := os.Getenv("NCP_TEST_CRASH_SOCKET")
	if path == "" {
		return
	}
	// 避免误将独立手动执行的 helper 指向生产路径。
	rel, err := filepath.Rel(os.TempDir(), path)
	if err != nil || rel == ".." || filepath.IsAbs(rel) || len(rel) < len("TestSocketRecoveryIntegration") || rel[:len("TestSocketRecoveryIntegration")] != "TestSocketRecoveryIntegration" {
		t.Fatal("helper requires isolated test tempdir")
	}
	_, cleanup, err := listenUnixSocket(SocketConfig{SocketPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	fmt.Println("ready")
	select {}
}

func TestSocketRecoveryIntegrationRetainsLegacyActiveListener(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.sock")
	active, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	_, cleanup, err := listenUnixSocket(SocketConfig{SocketPath: path})
	if cleanup != nil {
		cleanup()
		t.Fatal("unexpected successful replacement")
	}
	if ErrorCode(err) != "AGENT_SOCKET_PATH_OCCUPIED" {
		t.Fatalf("error=%v", err)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("active socket changed")
	}
}

func TestSocketRecoveryIntegrationCleanupPreservesReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.sock")
	_, cleanup, err := listenUnixSocket(SocketConfig{SocketPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	// 模拟受信任管理员在服务存活期间显式替换端点。
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	replacement, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("cleanup removed replacement socket")
	}
}
