//go:build linux

package terminal

import (
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

// 仅在隔离 Linux CI 执行；本地 fake 选择器明确排除此真实直属 child 测试。
func TestHostDirectChildReaperLinuxCI(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"zero", "nonzero", "signal"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command(executable, "-test.run=^TestHostDirectChildHelperLinuxCI$")
			cmd.Env = append(os.Environ(), "NCP_HOST_REAPER_HELPER="+mode)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			receipt := make(chan hostWaitResult, 1)
			go func() { receipt <- waitHostCommand(cmd) }()
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			if mode == "signal" {
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case result := <-receipt:
				if !result.reaped {
					t.Fatalf("direct child not reaped: %v", result.err)
				}
				if mode == "zero" && result.err != nil {
					t.Fatal(result.err)
				}
				if mode != "zero" {
					var exitErr *exec.ExitError
					if !errors.As(result.err, &exitErr) {
						t.Fatalf("expected exit error, got %v", result.err)
					}
				}
			case <-time.After(10 * time.Second):
				_ = cmd.Process.Kill()
				select {
				case <-receipt:
					t.Fatal("direct child wait exceeded CI tolerance")
				case <-time.After(2 * time.Second):
					t.Fatal("direct child exit still unconfirmed; reaper remains responsible")
				}
			}
		})
	}
}

func TestHostDirectChildHelperLinuxCI(t *testing.T) {
	switch os.Getenv("NCP_HOST_REAPER_HELPER") {
	case "zero":
		os.Exit(0)
	case "nonzero":
		os.Exit(7)
	case "signal":
		time.Sleep(time.Minute)
		os.Exit(8)
	}
}
