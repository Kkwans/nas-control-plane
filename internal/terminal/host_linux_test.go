//go:build linux

package terminal

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestHostPTYSupportsResizeCtrlCAndTermination(t *testing.T) {
	manager := NewManager(NewHostStarter(), nil, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := RunPOC(ctx, manager, TargetHost)
	if err != nil {
		t.Fatalf("run host terminal POC: %v", err)
	}
	if !result.PTY || !result.Resize || !result.CtrlC || !result.SessionTerminated {
		t.Fatalf("result = %#v", result)
	}
}

func TestWaitForTerminalReadyReturnsAfterReadySignal(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create ready pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})
	go func() {
		_, _ = writer.Write([]byte("ready\n"))
		_ = writer.Close()
	}()

	if !waitForTerminalReady(reader) {
		t.Fatal("ready signal was not detected")
	}
}
