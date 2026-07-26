package agentsocket

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestServeAndProbeOverUnixSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上不校验 Linux Unix Socket 文件权限")
	}

	socketPath := filepath.Join(t.TempDir(), "agent.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- Serve(ctx, SocketConfig{SocketPath: socketPath, SocketMode: 0o660})
	}()
	waitForSocket(t, socketPath)

	status, err := Probe(context.Background(), socketPath)
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if status.ProtocolVersion != ProtocolVersion {
		t.Fatalf("协议版本 = %q，期望 %q", status.ProtocolVersion, ProtocolVersion)
	}
	if status.AgentEUID != os.Geteuid() {
		t.Fatalf("Agent EUID = %d，期望 %d", status.AgentEUID, os.Geteuid())
	}
	if status.Transport != "unix" {
		t.Fatalf("传输类型 = %q，期望 unix", status.Transport)
	}

	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("Stat(socket) error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o660 {
		t.Fatalf("Socket mode = %o，期望 660", got)
	}

	cancel()
	select {
	case err := <-serveErrors:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve() 未在 Context 取消后退出")
	}
	if _, err := os.Lstat(socketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Socket 应在服务退出后清理，Lstat error = %v", err)
	}
}

func TestListenUnixSocketRejectsExistingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上不运行 Linux Unix Socket 文件测试")
	}

	path := filepath.Join(t.TempDir(), "agent.sock")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, cleanup, err := listenUnixSocket(SocketConfig{SocketPath: path, SocketMode: 0o660})
	if cleanup != nil {
		t.Fatal("已有文件时不应返回清理函数")
	}
	if ErrorCode(err) != "AGENT_SOCKET_PATH_OCCUPIED" {
		t.Fatalf("错误码 = %q，期望 AGENT_SOCKET_PATH_OCCUPIED", ErrorCode(err))
	}
	content, readErr := os.ReadFile(path)
	if readErr != nil || string(content) != "keep" {
		t.Fatalf("已有文件不应被删除或覆盖，content = %q, err = %v", content, readErr)
	}
}

func TestProbeHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Probe(ctx, "/run/ncp/agent.sock")
	if ErrorCode(err) != "AGENT_RPC_CANCELED" {
		t.Fatalf("错误码 = %q，期望 AGENT_RPC_CANCELED", ErrorCode(err))
	}
}

func TestStatusServiceReturnsOnlyAllowedFields(t *testing.T) {
	response, err := newStatusService().GetStatus(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}
	fields := response.GetFields()
	if len(fields) != 4 {
		t.Fatalf("状态字段数量 = %d，期望 3", len(fields))
	}
	for _, field := range []string{"protocol_version", "build_version", "agent_euid", "transport"} {
		if _, ok := fields[field]; !ok {
			t.Fatalf("缺少允许字段 %q", field)
		}
	}
}

func waitForSocket(t *testing.T, socketPath string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Lstat(socketPath); err == nil && info.Mode()&os.ModeSocket != 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Socket %q 未在超时内创建", socketPath)
}
