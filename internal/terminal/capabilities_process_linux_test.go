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

const (
	capabilityProcessCITolerance = 10 * time.Second
	capabilityProcessMarkers     = "NCP_READLINE=1\nNCP_BLE_SH=1\nset enable-bracketed-paste on\n"
)

type capabilityProcessResult struct {
	probe      shellCapabilityProbe
	command    *exec.Cmd
	output     *bashCapabilityOutput
	runErr     error
	contextErr error
	waitCalls  int
}

type capabilityProcessStarted struct {
	command *exec.Cmd
	err     error
}

type capabilityProcessProbe struct {
	cancel  context.CancelFunc
	started chan capabilityProcessStarted
	done    chan struct{}
	result  capabilityProcessResult
}

// 仅供隔离 Linux CI；本地只编译，fake 测试选择器不得包含 TestCapabilityProcess。
// 每个直接子进程只有一个 Wait 拥有者，取消与失败清理始终 join 原拥有者。
func startCapabilityProcessProbe(t *testing.T, parent context.Context, mode string, configure func(*exec.Cmd)) *capabilityProcessProbe {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(parent)
	p := &capabilityProcessProbe{
		cancel:  cancel,
		started: make(chan capabilityProcessStarted, 1),
		done:    make(chan struct{}),
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-p.done:
		case <-time.After(capabilityProcessCITolerance):
			t.Error("capability helper cleanup did not confirm exit; original Wait owner remains responsible")
		}
	})
	go func() {
		defer close(p.done)
		p.result.probe = probeBashCapabilitiesWithRunner(ctx, executable, "", func(probeCtx context.Context, output io.Writer) error {
			command := newBashCapabilityCommand(probeCtx, executable, "", output)
			useCapabilityProcessHelper(command, executable, mode)
			if configure != nil {
				configure(command)
			}
			p.result.command = command
			p.result.output = output.(*bashCapabilityOutput)
			p.result.runErr = command.Start()
			p.started <- capabilityProcessStarted{command: command, err: p.result.runErr}
			if p.result.runErr != nil {
				return p.result.runErr
			}
			p.result.waitCalls++
			p.result.runErr = command.Wait()
			p.result.contextErr = probeCtx.Err()
			return p.result.runErr
		})
	}()
	return p
}

func useCapabilityProcessHelper(command *exec.Cmd, executable, mode string) {
	// 保留生产命令的 context、合并输出、Dir 和 WaitDelay；不依赖真实 Bash。
	command.Path = executable
	command.Args = []string{executable, "-test.run=^TestCapabilityProcessHelperLinuxCI$"}
	command.Err = nil
	command.Env = append(os.Environ(), "NCP_CAPABILITY_PROCESS_HELPER="+mode,
		"GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
}

func (p *capabilityProcessProbe) wait(t *testing.T) capabilityProcessResult {
	t.Helper()
	select {
	case <-p.done:
		return p.result
	case <-time.After(capabilityProcessCITolerance):
		p.cancel()
		select {
		case <-p.done:
			t.Fatal("capability probe exceeded CI tolerance before cancellation cleanup completed")
		case <-time.After(capabilityProcessCITolerance):
			t.Fatal("capability probe exit remains unconfirmed; original Wait owner remains responsible")
		}
	}
	return capabilityProcessResult{}
}

func (p *capabilityProcessProbe) waitStarted(t *testing.T) *exec.Cmd {
	t.Helper()
	select {
	case started := <-p.started:
		if started.err != nil {
			t.Fatalf("start capability helper: %v", started.err)
		}
		return started.command
	case <-time.After(capabilityProcessCITolerance):
		t.Fatal("capability helper did not start within CI tolerance")
		return nil
	}
}

func requireCapabilityProcessReaped(t *testing.T, result capabilityProcessResult) {
	t.Helper()
	if result.command == nil || result.command.ProcessState == nil || result.waitCalls != 1 {
		t.Fatalf("direct helper not reaped by exactly one Wait: command=%v waits=%d err=%v", result.command, result.waitCalls, result.runErr)
	}
	if result.output == nil || result.command.Stdout != result.output || result.command.Stderr != result.output {
		t.Fatal("capability stdout and stderr must use the same bounded writer")
	}
	if result.command.WaitDelay != bashCapabilityProbeWaitDelay {
		t.Fatalf("helper WaitDelay=%v, want %v", result.command.WaitDelay, bashCapabilityProbeWaitDelay)
	}
}

func requireCapabilityProcessFailure(t *testing.T, result capabilityProcessResult) {
	t.Helper()
	if result.probe.Err == nil || result.probe.Readline || result.probe.BracketedPaste || result.probe.BleSH {
		t.Fatalf("failed capability probe must discard every marker: %#v", result.probe)
	}
}

func TestCapabilityProcessResultsLinuxCI(t *testing.T) {
	for _, mode := range []string{"success", "nonzero", "stderr-markers", "mixed-streams"} {
		t.Run(mode, func(t *testing.T) {
			result := startCapabilityProcessProbe(t, context.Background(), mode, nil).wait(t)
			requireCapabilityProcessReaped(t, result)
			if result.output.overflow || string(result.output.data[:result.output.n]) != capabilityProcessMarkers {
				t.Fatal("helper did not capture the complete combined capability markers")
			}
			if mode == "nonzero" {
				requireCapabilityProcessFailure(t, result)
				var exitErr *exec.ExitError
				if !errors.As(result.probe.Err, &exitErr) || exitErr.ExitCode() != 7 || result.command.ProcessState.ExitCode() != 7 {
					t.Fatalf("nonzero helper status=%v err=%v", result.command.ProcessState, result.probe.Err)
				}
			} else if result.runErr != nil || result.probe.Err != nil || !result.command.ProcessState.Success() ||
				!result.probe.Readline || !result.probe.BracketedPaste || !result.probe.BleSH {
				t.Fatalf("successful helper status=%v probe=%#v runErr=%v", result.command.ProcessState, result.probe, result.runErr)
			}
		})
	}
}

func TestCapabilityProcessOutputFloodLinuxCI(t *testing.T) {
	for _, mode := range []string{"stdout-flood", "stderr-flood", "mixed-flood"} {
		t.Run(mode, func(t *testing.T) {
			result := startCapabilityProcessProbe(t, context.Background(), mode, nil).wait(t)
			requireCapabilityProcessReaped(t, result)
			requireCapabilityProcessFailure(t, result)
			if !errors.Is(result.probe.Err, errBashCapabilityOutputLimit) {
				t.Fatalf("overflow reason was lost: %v", result.probe.Err)
			}
			if !result.output.overflow || result.output.n != maxCapabilityProbeOutput || len(result.output.data) != maxCapabilityProbeOutput {
				t.Fatalf("combined output was not bounded: overflow=%v bytes=%d capacity=%d", result.output.overflow, result.output.n, len(result.output.data))
			}
			if !bytes.HasPrefix(result.output.data[:result.output.n], []byte(capabilityProcessMarkers)) {
				t.Fatal("overflow fixture did not retain the valid marker prefix")
			}
		})
	}
}

func TestCapabilityProcessParentCancellationLinuxCI(t *testing.T) {
	readyReader, readyWriter := capabilityProcessPipe(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := startCapabilityProcessProbe(t, ctx, "wait-cancel", func(command *exec.Cmd) {
		command.ExtraFiles = []*os.File{readyWriter}
	})
	probe.waitStarted(t)
	_ = readyWriter.Close()
	capabilityProcessReadReady(t, readyReader)
	// helper 写完有效 marker 并到达 barrier 后才取消，验证真实启动后的回收。
	cancel()
	result := probe.wait(t)
	requireCapabilityProcessReaped(t, result)
	requireCapabilityProcessFailure(t, result)
	if !errors.Is(ctx.Err(), context.Canceled) || !errors.Is(result.contextErr, context.Canceled) || !errors.Is(result.probe.Err, context.Canceled) {
		t.Fatalf("parent cancellation not preserved: parent=%v context=%v probe=%v", ctx.Err(), result.contextErr, result.probe.Err)
	}
	if result.runErr == nil || result.command.ProcessState.Success() {
		t.Fatalf("canceled helper status=%v err=%v", result.command.ProcessState, result.runErr)
	}
	if string(result.output.data[:result.output.n]) != capabilityProcessMarkers {
		t.Fatal("cancellation fixture did not capture the valid marker prefix")
	}
}

func TestCapabilityProcessOwnDeadlineLinuxCI(t *testing.T) {
	readyReader, readyWriter := capabilityProcessPipe(t)
	probe := startCapabilityProcessProbe(t, context.Background(), "wait-deadline", func(command *exec.Cmd) {
		command.ExtraFiles = []*os.File{readyWriter}
	})
	probe.waitStarted(t)
	_ = readyWriter.Close()
	capabilityProcessReadReady(t, readyReader)
	result := probe.wait(t)
	requireCapabilityProcessReaped(t, result)
	requireCapabilityProcessFailure(t, result)
	if !errors.Is(result.contextErr, context.DeadlineExceeded) || !errors.Is(result.probe.Err, context.DeadlineExceeded) {
		t.Fatalf("own deadline not preserved: context=%v probe=%v", result.contextErr, result.probe.Err)
	}
	if result.runErr == nil || result.command.ProcessState.Success() {
		t.Fatalf("deadline helper status=%v err=%v", result.command.ProcessState, result.runErr)
	}
	if string(result.output.data[:result.output.n]) != capabilityProcessMarkers {
		t.Fatal("deadline fixture did not capture the valid marker prefix")
	}
}

func TestCapabilityProcessInheritedOutputWaitDelayLinuxCI(t *testing.T) {
	for _, fd := range []int{1, 2} {
		t.Run(fmt.Sprintf("fd%d", fd), func(t *testing.T) {
			readyReader, readyWriter := capabilityProcessPipe(t)
			gateReader, gateWriter := capabilityProcessPipe(t)
			probe := startCapabilityProcessProbe(t, context.Background(), fmt.Sprintf("gate-success-fd%d", fd), func(command *exec.Cmd) {
				command.Stdin = gateReader
				command.ExtraFiles = []*os.File{readyWriter}
			})
			direct := probe.waitStarted(t)
			_ = readyWriter.Close()
			_ = gateReader.Close()
			capabilityProcessReadReady(t, readyReader)

			// 复制真实的合并输出写端；两个 helper 都由测试直接启动和 Wait。
			sharedWriter, err := os.OpenFile(fmt.Sprintf("/proc/%d/fd/%d", direct.Process.Pid, fd), os.O_WRONLY, 0)
			if err != nil {
				t.Fatalf("duplicate owned helper fd %d: %v", fd, err)
			}
			t.Cleanup(func() { _ = sharedWriter.Close() })
			holderReadyReader, holderReadyWriter := capabilityProcessPipe(t)
			holderGateReader, holderGateWriter := capabilityProcessPipe(t)
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			holderCtx, cancelHolder := context.WithCancel(context.Background())
			defer cancelHolder()
			holder := exec.CommandContext(holderCtx, executable)
			useCapabilityProcessHelper(holder, executable, "hold-writer")
			if fd == 1 {
				holder.Stdout = sharedWriter
			} else {
				holder.Stderr = sharedWriter
			}
			holder.Stdin = holderGateReader
			holder.ExtraFiles = []*os.File{holderReadyWriter}
			if err := holder.Start(); err != nil {
				t.Fatalf("start owned fd %d holder: %v", fd, err)
			}
			holderDone := make(chan struct{})
			var holderErr error
			var holderWaitCalls int
			// 失败清理只取消并 join 此 goroutine，不能再调用第二次 Wait。
			go func() {
				holderWaitCalls++
				holderErr = holder.Wait()
				close(holderDone)
			}()
			t.Cleanup(func() {
				_ = holderGateWriter.Close()
				cancelHolder()
				select {
				case <-holderDone:
					if holder.ProcessState == nil || holderWaitCalls != 1 {
						t.Errorf("fd %d holder not reaped by exactly one Wait: waits=%d", fd, holderWaitCalls)
					}
				case <-time.After(capabilityProcessCITolerance):
					t.Errorf("fd %d holder exit remains unconfirmed; original Wait owner remains responsible", fd)
				}
			})
			_ = sharedWriter.Close()
			_ = holderReadyWriter.Close()
			_ = holderGateReader.Close()
			capabilityProcessReadReady(t, holderReadyReader)
			_ = gateWriter.Close()

			result := probe.wait(t)
			requireCapabilityProcessReaped(t, result)
			requireCapabilityProcessFailure(t, result)
			if !errors.Is(result.runErr, exec.ErrWaitDelay) || !errors.Is(result.probe.Err, exec.ErrWaitDelay) ||
				result.contextErr != nil || !result.command.ProcessState.Success() {
				t.Fatalf("inherited fd %d status=%v context=%v runErr=%v probe=%v", fd, result.command.ProcessState, result.contextErr, result.runErr, result.probe.Err)
			}
			if result.output.overflow || string(result.output.data[:result.output.n]) != capabilityProcessMarkers {
				t.Fatal("WaitDelay fixture did not capture the complete valid capability output")
			}
			select {
			case <-holderDone:
				t.Fatalf("fd %d holder exited before explicit release: %v", fd, holderErr)
			default:
			}
			_ = holderGateWriter.Close()
			select {
			case <-holderDone:
				if holderErr != nil || holder.ProcessState == nil || holderWaitCalls != 1 || !holder.ProcessState.Success() {
					t.Fatalf("released fd %d holder status=%v waits=%d err=%v", fd, holder.ProcessState, holderWaitCalls, holderErr)
				}
			case <-time.After(capabilityProcessCITolerance):
				t.Fatalf("released fd %d holder did not exit within CI tolerance", fd)
			}
		})
	}
}

func capabilityProcessPipe(t *testing.T) (*os.File, *os.File) {
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

func capabilityProcessReadReady(t *testing.T, reader *os.File) {
	t.Helper()
	if err := reader.SetReadDeadline(time.Now().Add(capabilityProcessCITolerance)); err != nil {
		t.Fatal(err)
	}
	var ready [1]byte
	if _, err := io.ReadFull(reader, ready[:]); err != nil || ready[0] != 'r' {
		t.Fatalf("capability helper readiness=%q err=%v", ready[:], err)
	}
}

func TestCapabilityProcessHelperLinuxCI(t *testing.T) {
	mode := os.Getenv("NCP_CAPABILITY_PROCESS_HELPER")
	if mode == "" {
		return
	}
	write := func(writer io.Writer, text string) {
		if _, err := io.WriteString(writer, text); err != nil {
			os.Exit(8)
		}
	}
	ready := func() {
		file := os.NewFile(3, "capability-helper-ready")
		if _, err := file.Write([]byte{'r'}); err != nil {
			os.Exit(8)
		}
		_ = file.Close()
	}
	switch mode {
	case "success":
		write(os.Stdout, capabilityProcessMarkers)
	case "nonzero":
		write(os.Stdout, capabilityProcessMarkers)
		os.Exit(7)
	case "stderr-markers":
		write(os.Stderr, capabilityProcessMarkers)
	case "mixed-streams":
		write(os.Stdout, "NCP_READLINE=1\n")
		write(os.Stderr, "NCP_BLE_SH=1\n")
		write(os.Stdout, "set enable-bracketed-paste on\n")
	case "stdout-flood", "stderr-flood":
		writer := os.Stdout
		if mode == "stderr-flood" {
			writer = os.Stderr
		}
		write(writer, capabilityProcessMarkers)
		block := bytes.Repeat([]byte{'x'}, maxCapabilityProbeOutput)
		for {
			if _, err := writer.Write(block); err != nil {
				os.Exit(8)
			}
		}
	case "mixed-flood":
		// 单个流均未到上限，合并后必须超限。
		write(os.Stdout, capabilityProcessMarkers)
		block := string(bytes.Repeat([]byte{'x'}, maxCapabilityProbeOutput/2+1))
		write(os.Stdout, block)
		write(os.Stderr, block)
	case "wait-cancel", "wait-deadline":
		write(os.Stdout, capabilityProcessMarkers)
		ready()
		time.Sleep(time.Minute)
		os.Exit(9)
	case "gate-success-fd1", "gate-success-fd2", "hold-writer":
		ready()
		if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
			os.Exit(8)
		}
		if mode == "gate-success-fd1" {
			write(os.Stdout, capabilityProcessMarkers)
		} else if mode == "gate-success-fd2" {
			write(os.Stderr, capabilityProcessMarkers)
		}
	default:
		os.Exit(10)
	}
	os.Exit(0)
}
