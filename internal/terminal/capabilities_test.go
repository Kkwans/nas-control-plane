package terminal

import (
	"strings"
	"testing"
)

func TestParseBashCapabilityProbe(t *testing.T) {
	probe := parseBashCapabilityProbe([]byte("NCP_READLINE=1\nNCP_BLE_SH=1\nset enable-bracketed-paste on\n"))
	if !probe.Readline || !probe.BleSH || !probe.BracketedPaste {
		t.Fatalf("probe = %#v", probe)
	}
}

func TestParseTerminalReadySignalUsesActualPTYCapabilities(t *testing.T) {
	ready, probe := parseTerminalReadySignal([]byte("ready NCP_READLINE=1 NCP_BLE_SH=1 NCP_BRACKETED_PASTE=1\n"))
	if !ready || !probe.Readline || !probe.BleSH || !probe.BracketedPaste {
		t.Fatalf("ready=%t probe=%#v", ready, probe)
	}

	ready, probe = parseTerminalReadySignal([]byte("ready\n"))
	if !ready || probe.Readline || probe.BleSH || probe.BracketedPaste {
		t.Fatalf("legacy ready=%t probe=%#v", ready, probe)
	}
}

func TestSessionCapabilitiesForProbeDoesNotInferPasteFromBashAlone(t *testing.T) {
	capabilities := sessionCapabilitiesForProbe("bash", "native", shellCapabilityProbe{Readline: true})
	if !capabilities.Readline || capabilities.BracketedPaste || capabilities.MultilinePaste {
		t.Fatalf("capabilities = %#v", capabilities)
	}
}

func TestApplyHostBashCapabilitiesRejectsUnloadedBlesh(t *testing.T) {
	metadata := SessionMetadata{Shell: "bash", Enhancement: "blesh"}
	applyHostBashCapabilities(&metadata, shellCapabilityProbe{Readline: true, BracketedPaste: true})
	if metadata.Enhancement != "native" || !metadata.Capabilities.MultilinePaste {
		t.Fatalf("metadata = %#v", metadata)
	}
	if !strings.Contains(metadata.Reason, "ble.sh") {
		t.Fatalf("reason = %q", metadata.Reason)
	}
}
