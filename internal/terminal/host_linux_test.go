//go:build linux

package terminal

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestHostEnhancementRequiresBothNCPFiles(t *testing.T) {
	directory := t.TempDir()
	rcFile := directory + "/terminal.bashrc"
	bleFile := directory + "/ble.sh"
	if got := hostEnhancement(rcFile, bleFile); got != "native" {
		t.Fatalf("missing files enhancement = %q", got)
	}
	if err := os.WriteFile(rcFile, []byte("# test\n"), 0o600); err != nil {
		t.Fatalf("write rc file: %v", err)
	}
	if got := hostEnhancement(rcFile, bleFile); got != "native" {
		t.Fatalf("missing ble.sh enhancement = %q", got)
	}
	if err := os.WriteFile(bleFile, []byte("# test\n"), 0o600); err != nil {
		t.Fatalf("write ble.sh file: %v", err)
	}
	if got := hostEnhancement(rcFile, bleFile); got != "blesh" {
		t.Fatalf("complete host enhancement = %q", got)
	}
}

func TestHostSessionMetadataReportsBleshCapabilities(t *testing.T) {
	metadata := hostSessionMetadata("bash", true, true)
	applyHostBashCapabilities(&metadata, shellCapabilityProbe{Readline: true, BracketedPaste: true, BleSH: true})
	if metadata.Shell != "bash" || metadata.Enhancement != "blesh" || metadata.Reason != "" {
		t.Fatalf("ble.sh metadata = %#v", metadata)
	}
	if !metadata.Capabilities.Readline || !metadata.Capabilities.BracketedPaste || !metadata.Capabilities.MultilinePaste {
		t.Fatalf("ble.sh capabilities = %#v", metadata.Capabilities)
	}
}

func TestHostSessionMetadataReportsBashWithoutEnhancement(t *testing.T) {
	metadata := hostSessionMetadata("bash", false, false)
	if metadata.Shell != "bash" || metadata.Enhancement != "native" || metadata.Reason == "" {
		t.Fatalf("Bash fallback metadata = %#v", metadata)
	}
	if metadata.Capabilities.Readline || metadata.Capabilities.BracketedPaste || metadata.Capabilities.MultilinePaste {
		t.Fatalf("Bash fallback capabilities = %#v", metadata.Capabilities)
	}
}

func TestApplyHostBashCapabilitiesOnlyReportsProbedFeatures(t *testing.T) {
	metadata := hostSessionMetadata("bash", false, false)
	applyHostBashCapabilities(&metadata, shellCapabilityProbe{Readline: true})
	if !metadata.Capabilities.Readline || metadata.Capabilities.BracketedPaste || metadata.Capabilities.MultilinePaste {
		t.Fatalf("Bash probed capabilities = %#v", metadata.Capabilities)
	}
	if metadata.Enhancement != "native" || metadata.Reason == "" {
		t.Fatalf("Bash probed metadata = %#v", metadata)
	}
}

func TestHostSessionMetadataReportsNoEnhancementForSh(t *testing.T) {
	metadata := hostSessionMetadata("sh", false, false)
	if metadata.Shell != "sh" || metadata.Enhancement != "native" || metadata.Reason == "" {
		t.Fatalf("sh metadata = %#v", metadata)
	}
	if metadata.Capabilities.Readline || metadata.Capabilities.BracketedPaste || metadata.Capabilities.MultilinePaste {
		t.Fatalf("sh capabilities = %#v", metadata.Capabilities)
	}
}

func TestHostPTYSupportsResizeCtrlCAndTermination(t *testing.T) {
	manager := NewManager(NewHostStarter(), nil, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
		_, _ = writer.Write([]byte("ready NCP_READLINE=1 NCP_BLE_SH=1 NCP_BRACKETED_PASTE=1\n"))
		_ = writer.Close()
	}()

	ready, probe := waitForTerminalReady(reader)
	if !ready || !probe.Readline || !probe.BleSH || !probe.BracketedPaste {
		t.Fatal("ready signal was not detected")
	}
}

func TestWaitForTerminalReadyHonorsContextCancellation(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create ready pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ready, _ := waitForTerminalReady(reader, ctx)
	if ready {
		t.Fatal("canceled readiness wait must not report ready")
	}
}
