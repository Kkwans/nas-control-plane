//go:build linux

package terminal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

const localeProcessCITolerance = 10 * time.Second

type localeProcessResult struct {
	locale     string
	command    *exec.Cmd
	output     *localeOutput
	err        error
	contextErr error
	waitCalls  int
}

type localeProcessStarted struct {
	command *exec.Cmd
	err     error
}

type localeProcessProbe struct {
	cancel  context.CancelFunc
	started chan localeProcessStarted
	done    chan struct{}
	result  localeProcessResult
}

// 仅供隔离 Linux CI；本地只编译，fake 测试选择器不得包含 TestLocaleProcess。
// Start/Wait 的唯一拥有者在同一 goroutine，测试取消后也不另起 Wait。
func startLocaleProcessProbe(t *testing.T, parent context.Context, mode string, configure func(*exec.Cmd)) *localeProcessProbe {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(parent)
	p := &localeProcessProbe{
		cancel:  cancel,
		started: make(chan localeProcessStarted, 1),
		done:    make(chan struct{}),
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-p.done:
		case <-time.After(localeProcessCITolerance):
			t.Error("locale helper cleanup did not confirm exit; original Wait owner remains responsible")
		}
	})
	go func() {
		defer close(p.done)
		p.result.locale = supportedTerminalLocaleWithRunner(ctx, func(probeCtx context.Context, output io.Writer) error {
			command := newLocaleCommand(probeCtx, output)
			useLocaleProcessHelper(command, executable, mode)
			if configure != nil {
				configure(command)
			}
			p.result.command = command
			p.result.output = output.(*localeOutput)
			p.result.err = command.Start()
			p.started <- localeProcessStarted{command: command, err: p.result.err}
			if p.result.err != nil {
				return p.result.err
			}
			p.result.waitCalls++
			p.result.err = command.Wait()
			p.result.contextErr = probeCtx.Err()
			return p.result.err
		})
	}()
	return p
}

func useLocaleProcessHelper(command *exec.Cmd, executable, mode string) {
	// 保留生产命令的 context、stdout 和 WaitDelay；仅替换测试可执行文件。
	command.Path = executable
	command.Args = []string{executable, "-test.run=^TestLocaleProcessHelperLinuxCI$"}
	command.Err = nil // 不让测试机器是否安装 locale 影响自有 helper。
	command.Env = append(os.Environ(), "NCP_LOCALE_PROCESS_HELPER="+mode,
		"GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
}

func (p *localeProcessProbe) wait(t *testing.T) localeProcessResult {
	t.Helper()
	select {
	case <-p.done:
		return p.result
	case <-time.After(localeProcessCITolerance):
		p.cancel()
		select {
		case <-p.done:
			t.Fatal("locale probe exceeded CI tolerance before cancellation cleanup completed")
		case <-time.After(localeProcessCITolerance):
			t.Fatal("locale probe exit remains unconfirmed; original Wait owner remains responsible")
		}
	}
	return localeProcessResult{}
}

func (p *localeProcessProbe) waitStarted(t *testing.T) *exec.Cmd {
	t.Helper()
	select {
	case started := <-p.started:
		if started.err != nil {
			t.Fatalf("start locale helper: %v", started.err)
		}
		return started.command
	case <-time.After(localeProcessCITolerance):
		t.Fatal("locale helper did not start within CI tolerance")
		return nil
	}
}

func requireLocaleProcessReaped(t *testing.T, result localeProcessResult) {
	t.Helper()
	if result.command == nil || result.command.ProcessState == nil || result.waitCalls != 1 {
		t.Fatalf("direct helper not reaped by exactly one Wait: command=%v waits=%d err=%v", result.command, result.waitCalls, result.err)
	}
	if result.command.Stderr != nil {
		t.Fatal("locale stderr must stay uncaptured")
	}
}

func TestLocaleProcessResultsLinuxCI(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want string
	}{
		{mode: "success", want: "C.UTF-8"},
		{mode: "nonzero", want: "C"},
		{mode: "stderr-flood", want: "C.UTF-8"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			result := startLocaleProcessProbe(t, context.Background(), tc.mode, nil).wait(t)
			requireLocaleProcessReaped(t, result)
			if result.locale != tc.want {
				t.Fatalf("locale=%q, want %q; err=%v", result.locale, tc.want, result.err)
			}
			if tc.mode == "nonzero" {
				var exitErr *exec.ExitError
				if !errors.As(result.err, &exitErr) || result.command.ProcessState.ExitCode() != 7 {
					t.Fatalf("nonzero helper status=%v err=%v", result.command.ProcessState, result.err)
				}
			} else if result.err != nil || !result.command.ProcessState.Success() {
				t.Fatalf("successful helper status=%v err=%v", result.command.ProcessState, result.err)
			}
		})
	}
}

func TestLocaleProcessStdoutFloodLinuxCI(t *testing.T) {
	result := startLocaleProcessProbe(t, context.Background(), "stdout-flood", nil).wait(t)
	requireLocaleProcessReaped(t, result)
	if result.locale != "C" || result.err == nil {
		t.Fatalf("overflow locale=%q err=%v", result.locale, result.err)
	}
	if result.output == nil {
		t.Fatal("stdout capture is missing")
	}
	if !result.output.overflow || result.output.n != localeOutputLimit || len(result.output.data) != localeOutputLimit {
		t.Fatalf("stdout capture did not remain bounded: overflow=%v bytes=%d capacity=%d", result.output.overflow, result.output.n, len(result.output.data))
	}
}

func TestLocaleProcessParentCancellationLinuxCI(t *testing.T) {
	readyReader, readyWriter := localeProcessPipe(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := startLocaleProcessProbe(t, ctx, "wait-cancel", func(command *exec.Cmd) {
		command.ExtraFiles = []*os.File{readyWriter}
	})
	probe.waitStarted(t)
	_ = readyWriter.Close()
	localeProcessReadReady(t, readyReader)
	// helper 明确到达 barrier 后才取消，不能把启动前取消误当成进程回收证据。
	cancel()
	result := probe.wait(t)
	requireLocaleProcessReaped(t, result)
	if result.locale != "C" || result.err == nil || result.command.ProcessState.Success() {
		t.Fatalf("canceled locale=%q status=%v err=%v", result.locale, result.command.ProcessState, result.err)
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("parent cancellation=%v", ctx.Err())
	}
	if !errors.Is(result.contextErr, context.Canceled) {
		t.Fatalf("probe ended before parent cancellation could be verified: %v", result.contextErr)
	}
}

func TestLocaleProcessInheritedStdoutWaitDelayLinuxCI(t *testing.T) {
	readyReader, readyWriter := localeProcessPipe(t)
	gateReader, gateWriter := localeProcessPipe(t)
	probe := startLocaleProcessProbe(t, context.Background(), "gate-success", func(command *exec.Cmd) {
		command.Stdin = gateReader
		command.ExtraFiles = []*os.File{readyWriter}
	})
	direct := probe.waitStarted(t)
	_ = readyWriter.Close()
	_ = gateReader.Close()
	localeProcessReadReady(t, readyReader)

	// 复用直属 helper 的真实 os/exec stdout 管道，不替换生产限额 writer。
	// 两个 helper 都是测试的直接子进程；不创建无人 Wait 的孙进程。
	sharedWriter, err := os.OpenFile(fmt.Sprintf("/proc/%d/fd/1", direct.Process.Pid), os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("duplicate owned helper stdout: %v", err)
	}
	t.Cleanup(func() { _ = sharedWriter.Close() })
	holderReadyReader, holderReadyWriter := localeProcessPipe(t)
	holderGateReader, holderGateWriter := localeProcessPipe(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	holderCtx, cancelHolder := context.WithCancel(context.Background())
	defer cancelHolder()
	holder := exec.CommandContext(holderCtx, executable)
	useLocaleProcessHelper(holder, executable, "hold-writer")
	holder.Stdout = sharedWriter
	holder.Stdin = holderGateReader
	holder.ExtraFiles = []*os.File{holderReadyWriter}
	if err := holder.Start(); err != nil {
		t.Fatalf("start owned stdout holder: %v", err)
	}
	holderDone := make(chan struct{})
	var holderErr error
	// 这个 goroutine 是第二个直接子进程的唯一 Wait 拥有者，失败清理也只 join 它。
	go func() {
		holderErr = holder.Wait()
		close(holderDone)
	}()
	t.Cleanup(func() {
		_ = holderGateWriter.Close()
		cancelHolder()
		select {
		case <-holderDone:
			if holder.ProcessState == nil {
				t.Error("stdout holder was not reaped")
			}
		case <-time.After(localeProcessCITolerance):
			t.Error("stdout holder exit remains unconfirmed; original Wait owner remains responsible")
		}
	})
	_ = sharedWriter.Close()
	_ = holderReadyWriter.Close()
	_ = holderGateReader.Close()
	localeProcessReadReady(t, holderReadyReader)
	_ = gateWriter.Close()

	result := probe.wait(t)
	requireLocaleProcessReaped(t, result)
	if result.locale != "C" || !errors.Is(result.err, exec.ErrWaitDelay) || !result.command.ProcessState.Success() {
		t.Fatalf("inherited stdout locale=%q status=%v err=%v", result.locale, result.command.ProcessState, result.err)
	}
	if result.output.overflow || string(result.output.data[:result.output.n]) != "C\nC.utf8\nPOSIX\n" {
		t.Fatal("WaitDelay fixture did not capture the complete valid locale output")
	}
	select {
	case <-holderDone:
		t.Fatalf("stdout holder exited before explicit release: %v", holderErr)
	default:
	}
	_ = holderGateWriter.Close()
	select {
	case <-holderDone:
		if holderErr != nil || holder.ProcessState == nil || !holder.ProcessState.Success() {
			t.Fatalf("released stdout holder status=%v err=%v", holder.ProcessState, holderErr)
		}
	case <-time.After(localeProcessCITolerance):
		t.Fatal("released stdout holder did not exit within CI tolerance")
	}
}

func localeProcessPipe(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})
	return reader, writer
}

func localeProcessReadReady(t *testing.T, reader *os.File) {
	t.Helper()
	if err := reader.SetReadDeadline(time.Now().Add(localeProcessCITolerance)); err != nil {
		t.Fatal(err)
	}
	var ready [1]byte
	if _, err := io.ReadFull(reader, ready[:]); err != nil || ready[0] != 'r' {
		t.Fatalf("locale helper readiness=%q err=%v", ready[:], err)
	}
}

func TestLocaleProcessHelperLinuxCI(t *testing.T) {
	mode := os.Getenv("NCP_LOCALE_PROCESS_HELPER")
	if mode == "" {
		return
	}
	writeLocale := func() {
		if _, err := io.WriteString(os.Stdout, "C\nC.utf8\nPOSIX\n"); err != nil {
			os.Exit(8)
		}
	}
	ready := func() {
		file := os.NewFile(3, "locale-helper-ready")
		if _, err := file.Write([]byte{'r'}); err != nil {
			os.Exit(8)
		}
		_ = file.Close()
	}
	switch mode {
	case "success":
		writeLocale()
	case "nonzero":
		writeLocale()
		os.Exit(7)
	case "stderr-flood":
		block := bytes.Repeat([]byte{'x'}, 32*1024)
		for range 256 {
			if _, err := os.Stderr.Write(block); err != nil {
				os.Exit(8)
			}
		}
		writeLocale()
	case "stdout-flood":
		writeLocale()
		block := bytes.Repeat([]byte{'x'}, 32*1024)
		for {
			if _, err := os.Stdout.Write(block); err != nil {
				os.Exit(8)
			}
		}
	case "wait-cancel":
		ready()
		time.Sleep(time.Minute)
		os.Exit(9)
	case "gate-success", "hold-writer":
		ready()
		if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
			os.Exit(8)
		}
		if mode == "gate-success" {
			writeLocale()
		}
	default:
		os.Exit(10)
	}
	os.Exit(0)
}
