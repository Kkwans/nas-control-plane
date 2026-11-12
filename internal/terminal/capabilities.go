package terminal

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"time"
)

const (
	bashCapabilityProbeTimeout   = 750 * time.Millisecond
	bashCapabilityProbeWaitDelay = 100 * time.Millisecond
	maxCapabilityProbeOutput     = 32 * 1024
	bashCapabilityProbeScript    = `if bind -v >/dev/null 2>&1; then printf 'NCP_READLINE=1\n'; fi
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

var errBashCapabilityOutputLimit = errors.New("terminal bash capability output limit exceeded")

// bashCapabilityOutput 固定容量合并捕获；超限后不再接受数据。
// 同一可比较指针直接交给 Stdout/Stderr，os/exec 保证串行调用 Write。
// 只在同步 Run 返回后读取内容；不同探测不共享状态。
type bashCapabilityOutput struct {
	data     [maxCapabilityProbeOutput]byte
	n        int
	overflow bool
}

func (output *bashCapabilityOutput) Write(p []byte) (int, error) {
	if output.overflow {
		return 0, errBashCapabilityOutputLimit
	}
	n := copy(output.data[output.n:], p)
	output.n += n
	if n != len(p) {
		output.overflow = true
		return n, errBashCapabilityOutputLimit
	}
	return n, nil
}

func probeBashCapabilities(ctx context.Context, shell, rcFile string) shellCapabilityProbe {
	return probeBashCapabilitiesWithRunner(ctx, shell, rcFile, func(ctx context.Context, output io.Writer) error {
		// Run 同步且独占直属 child 的 Wait，不在超时后遗弃回收责任。
		return newBashCapabilityCommand(ctx, shell, rcFile, output).Run()
	})
}

// seam 仅用于主机预探测；fake runner 也必须同步完成写入后才返回。
func probeBashCapabilitiesWithRunner(ctx context.Context, shell, rcFile string, run func(context.Context, io.Writer) error) shellCapabilityProbe {
	if shell == "" {
		return shellCapabilityProbe{Err: context.Canceled}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, bashCapabilityProbeTimeout)
	defer cancel()
	if err := probeCtx.Err(); err != nil {
		return shellCapabilityProbe{Err: err}
	}
	output := new(bashCapabilityOutput)
	runErr := run(probeCtx, output)
	var limitErr error
	if output.overflow {
		limitErr = errBashCapabilityOutputLimit
	}
	if err := errors.Join(runErr, probeCtx.Err(), limitErr); err != nil {
		return shellCapabilityProbe{Err: err}
	}
	return parseBashCapabilityProbe(output.data[:output.n])
}

func newBashCapabilityCommand(ctx context.Context, shell, rcFile string, output io.Writer) *exec.Cmd {
	arguments := []string{"--noprofile"}
	if rcFile != "" {
		arguments = append(arguments, "--rcfile", rcFile)
	} else {
		arguments = append(arguments, "--norc")
	}
	arguments = append(arguments, "-i", "-c", bashCapabilityProbeScript)
	command := exec.CommandContext(ctx, shell, arguments...)
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
	command.Stdout = output
	command.Stderr = output
	// 仅约束取消或直属 child 退出后的管道收尾，不是整个 Start/Wait 的硬截止。
	command.WaitDelay = bashCapabilityProbeWaitDelay
	return command
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
