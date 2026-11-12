package agentsocket

import (
	"context"
	"errors"
	"github.com/Kkwans/nas-control-plane/internal/terminal"
	"os"
	"path/filepath"
	"testing"
)

func TestTerminalCapacityConfigFactory(t *testing.T) {
	for _, capacity := range []int{0, 1, 2, 3, 4} {
		calls := 0
		expected := terminal.NewManager(nil, nil, capacity)
		got, err := configuredTerminalManager(SocketConfig{TerminalMaxSessions: capacity}, func(n int) (*terminal.Manager, error) {
			calls++
			want := capacity
			if want == 0 {
				want = 1
			}
			if n != want {
				t.Fatalf("factory received %d, want %d", n, want)
			}
			return expected, nil
		})
		if err != nil || got != expected || calls != 1 {
			t.Fatalf("factory=%v %v calls=%d", got, err, calls)
		}
	}
	injected := terminal.NewManager(nil, nil, 3)
	got, err := configuredTerminalManager(SocketConfig{TerminalManager: injected}, func(int) (*terminal.Manager, error) { t.Fatal("injected manager replaced"); return nil, nil })
	if got != injected || err != nil {
		t.Fatal("injected manager not preserved")
	}
	cause := errors.New("fake factory failure")
	_, err = configuredTerminalManager(SocketConfig{}, func(int) (*terminal.Manager, error) { return nil, cause })
	if err != cause {
		t.Fatal("factory error not preserved")
	}
}

func TestTerminalCapacityInvalidBeforeServeSideEffects(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, config := range []SocketConfig{
			{TerminalMaxSessions: -1}, {TerminalMaxSessions: 5},
			{TerminalMaxSessions: 1, TerminalManager: terminal.NewManager(nil, nil, 1)},
			{TerminalMaxSessions: 4, TerminalManager: terminal.NewManager(nil, nil, 4)},
		} {
			dir := filepath.Join(t.TempDir(), "must-not-exist")
			config.SocketPath = filepath.Join(dir, "agent.sock")
			config.EnableTerminalPOC = enabled
			_, err := configuredTerminalManager(config, func(int) (*terminal.Manager, error) { t.Fatal("factory invoked for invalid config"); return nil, nil })
			if err == nil {
				t.Fatal("invalid config accepted")
			}
			// Serve 必须在任何目录、listener 或 live provider 初始化之前返回。
			err = Serve(context.Background(), config)
			code := ErrorCode(err)
			if code != "TERMINAL_MAX_SESSIONS_INVALID" && code != "TERMINAL_CAPACITY_CONFIG_CONFLICT" {
				t.Fatalf("unexpected early error: %v", err)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("Serve changed filesystem: %v", err)
			}
		}
	}
}
