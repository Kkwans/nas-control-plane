//go:build linux

package terminal

import (
	"context"
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
