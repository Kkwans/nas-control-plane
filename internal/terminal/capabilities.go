package terminal

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

const (
	bashCapabilityProbeTimeout = 750 * time.Millisecond
	maxCapabilityProbeOutput   = 32 * 1024
	bashCapabilityProbeScript  = `if bind -v >/dev/null 2>&1; then printf 'NCP_READLINE=1\n'; fi
if type -t ble-attach >/dev/null 2>&1; then printf 'NCP_BLE_SH=1\n'; fi
bind -v 2>/dev/null`
)

// shellCapabilityProbe contains only capabilities that can be verified from
// the running shell. The caller must treat false as unsupported, not unknown:
// a failed probe is deliberately conservative so the browser does not send
// bracketed paste markers to a shell that cannot consume them.
type shellCapabilityProbe struct {
	Readline       bool
	BracketedPaste bool
	BleSH          bool
	Err            error
}

func sessionCapabilitiesFor(shell, enhancement string) SessionCapabilities {
	return SessionCapabilities{
		Resize:     true,
		ANSIColors: true,
	}
}

func sessionCapabilitiesForProbe(shell, enhancement string, probe shellCapabilityProbe) SessionCapabilities {
	capabilities := sessionCapabilitiesFor(shell, enhancement)
	if shell != "bash" {
		return capabilities
	}
	capabilities.Readline = probe.Readline
	capabilities.BracketedPaste = probe.Readline && probe.BracketedPaste
	capabilities.MultilinePaste = capabilities.BracketedPaste
	return capabilities
}

func probeBashCapabilities(ctx context.Context, shell, rcFile string) shellCapabilityProbe {
	if shell == "" {
		return shellCapabilityProbe{Err: context.Canceled}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	probeContext, cancel := context.WithTimeout(ctx, bashCapabilityProbeTimeout)
	defer cancel()

	arguments := []string{"--noprofile"}
	if rcFile != "" {
		arguments = append(arguments, "--rcfile", rcFile)
	} else {
		arguments = append(arguments, "--norc")
	}
	arguments = append(arguments, "-i", "-c", bashCapabilityProbeScript)
	command := exec.CommandContext(probeContext, shell, arguments...)
	command.Dir = "/root"
	command.Env = []string{
		"HOME=/root",
		"USER=root",
		"LOGNAME=root",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"LANG=C",
		"LC_ALL=C",
	}
	output, err := command.CombinedOutput()
	probe := parseBashCapabilityProbe(output)
	if err != nil {
		probe.Err = err
	}
	return probe
}

func parseBashCapabilityProbe(output []byte) shellCapabilityProbe {
	probe := shellCapabilityProbe{}
	for _, line := range strings.Split(string(output), "\n") {
		switch strings.TrimSpace(line) {
		case "NCP_READLINE=1":
			probe.Readline = true
		case "NCP_BLE_SH=1":
			probe.BleSH = true
		case "set enable-bracketed-paste on":
			probe.BracketedPaste = true
		}
	}
	return probe
}

func parseTerminalReadySignal(output []byte) (bool, shellCapabilityProbe) {
	probe := shellCapabilityProbe{}
	ready := false
	for _, field := range strings.Fields(string(output)) {
		switch strings.TrimSpace(field) {
		case "ready":
			ready = true
		case "NCP_READLINE=1":
			probe.Readline = true
		case "NCP_BLE_SH=1":
			probe.BleSH = true
		case "NCP_BRACKETED_PASTE=1":
			probe.BracketedPaste = true
		}
	}
	return ready, probe
}
