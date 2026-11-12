//go:build linux

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
	localeProbeTimeout   = 750 * time.Millisecond
	localeProbeWaitDelay = 100 * time.Millisecond
	localeOutputLimit    = 32 * 1024
)

var errLocaleOutputLimit = errors.New("terminal locale output limit exceeded")

// localeOutput 只保留固定容量的 stdout；超限后不再接受数据。
type localeOutput struct {
	data     [localeOutputLimit]byte
	n        int
	overflow bool
}

func (output *localeOutput) Write(p []byte) (int, error) {
	if output.overflow {
		return 0, errLocaleOutputLimit
	}
	n := copy(output.data[output.n:], p)
	output.n += n
	if n != len(p) {
		output.overflow = true
		return n, errLocaleOutputLimit
	}
	return n, nil
}

func supportedTerminalLocale(ctx context.Context) string {
	return supportedTerminalLocaleWithRunner(ctx, func(ctx context.Context, output io.Writer) error {
		// Run 同步且独占直属 child 的 Wait，不在超时后遗弃回收责任。
		return newLocaleCommand(ctx, output).Run()
	})
}

func newLocaleCommand(ctx context.Context, output io.Writer) *exec.Cmd {
	command := exec.CommandContext(ctx, "locale", "-a")
	command.Stdout = output
	// 只限制取消或直属 child 退出后的管道收尾；不是 Start/Wait 的硬墙钟截止。
	command.WaitDelay = localeProbeWaitDelay
	// nil Stderr 直接接 /dev/null；继承原有 PATH、环境和工作目录。
	return command
}

// seam 仅供 locale 探测测试；每次调用都重新观察，不缓存失败或成功。
func supportedTerminalLocaleWithRunner(ctx context.Context, run func(context.Context, io.Writer) error) string {
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, localeProbeTimeout)
	defer cancel()
	if probeCtx.Err() != nil {
		return "C"
	}
	output := new(localeOutput)
	if err := run(probeCtx, output); err != nil || probeCtx.Err() != nil || output.overflow {
		return "C"
	}
	for _, locale := range strings.Fields(strings.ToLower(string(output.data[:output.n]))) {
		if locale == "c.utf8" || locale == "c.utf-8" {
			return "C.UTF-8"
		}
	}
	return "C"
}
