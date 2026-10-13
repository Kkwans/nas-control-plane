package terminal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/moby/moby/client"
)

const containerProbeSDKMarkers = "NCP_READLINE=1\nNCP_BLE_SH=1\nset enable-bracketed-paste on\n"

type containerProbeSDKFake struct {
	create  func(context.Context, string, client.ExecCreateOptions) (client.ExecCreateResult, error)
	attach  func(context.Context, string, client.ExecAttachOptions) (client.ExecAttachResult, error)
	inspect func(context.Context, string, client.ExecInspectOptions) (client.ExecInspectResult, error)
}

func (fake *containerProbeSDKFake) ExecCreate(ctx context.Context, id string, options client.ExecCreateOptions) (client.ExecCreateResult, error) {
	return fake.create(ctx, id, options)
}

func (fake *containerProbeSDKFake) ExecAttach(ctx context.Context, id string, options client.ExecAttachOptions) (client.ExecAttachResult, error) {
	return fake.attach(ctx, id, options)
}

func (fake *containerProbeSDKFake) ExecInspect(ctx context.Context, id string, options client.ExecInspectOptions) (client.ExecInspectResult, error) {
	return fake.inspect(ctx, id, options)
}

// 纯内存连接：没有 socket、Docker 或子进程；Close 次数可由取消回调安全记录。
type containerProbeSDKConn struct {
	reader  io.Reader
	closeFn func() error
	closes  atomic.Int32
}

func (conn *containerProbeSDKConn) Read(p []byte) (int, error) { return conn.reader.Read(p) }
func (conn *containerProbeSDKConn) Write([]byte) (int, error) {
	return 0, errors.New("unexpected probe write")
}
func (conn *containerProbeSDKConn) Close() error {
	conn.closes.Add(1)
	if conn.closeFn != nil {
		return conn.closeFn()
	}
	return nil
}
func (*containerProbeSDKConn) LocalAddr() net.Addr              { return nil }
func (*containerProbeSDKConn) RemoteAddr() net.Addr             { return nil }
func (*containerProbeSDKConn) SetDeadline(time.Time) error      { return nil }
func (*containerProbeSDKConn) SetReadDeadline(time.Time) error  { return nil }
func (*containerProbeSDKConn) SetWriteDeadline(time.Time) error { return nil }

type containerProbeSDKErrorReader struct{ err error }

func (reader containerProbeSDKErrorReader) Read([]byte) (int, error) { return 0, reader.err }

func containerProbeSDKFrame(stream byte, payload string) []byte {
	wire := make([]byte, 8+len(payload))
	wire[0] = stream
	binary.BigEndian.PutUint32(wire[4:8], uint32(len(payload)))
	copy(wire[8:], payload)
	return wire
}

func containerProbeSDKAttachment(conn *containerProbeSDKConn) client.ExecAttachResult {
	return client.ExecAttachResult{HijackedResponse: client.HijackedResponse{Conn: conn, Reader: bufio.NewReader(conn)}}
}

func TestContainerProbeSDKHandoffsUseOneBudgetAndExactOptions(t *testing.T) {
	command := []string{"/bin/bash", "--noprofile", "--norc", "-i", "-c", bashCapabilityProbeScript}
	wire := append(containerProbeSDKFrame(1, "NCP_READ"), containerProbeSDKFrame(2, "LINE=1\nset enable-bracketed-paste on\n")...)
	conn := &containerProbeSDKConn{reader: bytes.NewReader(wire)}
	attachment := containerProbeSDKAttachment(conn)
	// SDK 交还 attachment 时可能已经预读；直接读取 Conn 会遗失这些字节。
	if _, err := attachment.Reader.Peek(len(wire)); err != nil {
		t.Fatal(err)
	}
	type key struct{}
	parent := context.WithValue(context.Background(), key{}, "same-parent")
	before := time.Now()
	var budget context.Context
	var calls []string
	fake := &containerProbeSDKFake{
		create: func(ctx context.Context, id string, options client.ExecCreateOptions) (client.ExecCreateResult, error) {
			calls = append(calls, "create")
			budget = ctx
			deadline, ok := ctx.Deadline()
			if !ok || deadline.Before(before.Add(containerShellProbeTimeout)) || deadline.After(time.Now().Add(containerShellProbeTimeout)) {
				t.Fatalf("probe deadline = %v, present = %v", deadline, ok)
			}
			if ctx == parent || ctx.Value(key{}) != "same-parent" || ctx.Err() != nil {
				t.Fatalf("probe context did not preserve active parent values")
			}
			want := client.ExecCreateOptions{AttachStdout: true, AttachStderr: true, Cmd: command}
			if id != "container-probe" || !reflect.DeepEqual(options, want) {
				t.Fatalf("create = %q %#v, want %q %#v", id, options, "container-probe", want)
			}
			return client.ExecCreateResult{ID: "exec-probe"}, nil
		},
		attach: func(ctx context.Context, id string, options client.ExecAttachOptions) (client.ExecAttachResult, error) {
			calls = append(calls, "attach")
			if ctx != budget || id != "exec-probe" || options != (client.ExecAttachOptions{}) {
				t.Fatalf("attach did not preserve context, exec ID, or non-TTY options")
			}
			return attachment, nil
		},
		inspect: func(ctx context.Context, id string, options client.ExecInspectOptions) (client.ExecInspectResult, error) {
			calls = append(calls, "inspect")
			if ctx != budget || id != "exec-probe" || options != (client.ExecInspectOptions{}) || ctx.Err() != nil {
				t.Fatalf("inspect did not preserve active context, exec ID, or options")
			}
			if got := conn.closes.Load(); got != 1 {
				t.Fatalf("closes before inspect = %d, want 1", got)
			}
			return client.ExecInspectResult{Running: false, ExitCode: 0}, nil
		},
	}
	available, output, err := probeContainerCommandOutput(parent, fake, "container-probe", command...)
	if err != nil || !available || string(output) != "NCP_READLINE=1\nset enable-bracketed-paste on\n" {
		t.Fatalf("probe = %v %q %v", available, output, err)
	}
	if !reflect.DeepEqual(calls, []string{"create", "attach", "inspect"}) || conn.closes.Load() != 1 {
		t.Fatalf("calls = %v, closes = %d", calls, conn.closes.Load())
	}
	if !errors.Is(budget.Err(), context.Canceled) {
		t.Fatalf("probe budget not released: %v", budget.Err())
	}
}

func TestContainerProbeSDKAcceptsCompletedEmptyStreams(t *testing.T) {
	for _, test := range []struct {
		name string
		wire []byte
	}{
		{name: "empty wire"},
		{name: "empty framed streams", wire: append(append(containerProbeSDKFrame(0, ""), containerProbeSDKFrame(1, "")...), containerProbeSDKFrame(2, "")...)},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn := &containerProbeSDKConn{reader: bytes.NewReader(test.wire)}
			inspectCalls := 0
			fake := &containerProbeSDKFake{
				create: func(context.Context, string, client.ExecCreateOptions) (client.ExecCreateResult, error) {
					return client.ExecCreateResult{ID: "exec"}, nil
				},
				attach: func(context.Context, string, client.ExecAttachOptions) (client.ExecAttachResult, error) {
					return containerProbeSDKAttachment(conn), nil
				},
				inspect: func(context.Context, string, client.ExecInspectOptions) (client.ExecInspectResult, error) {
					inspectCalls++
					if conn.closes.Load() != 1 {
						t.Fatalf("inspect before attachment close")
					}
					return client.ExecInspectResult{}, nil
				},
			}
			available, output, err := probeContainerCommandOutput(context.Background(), fake, "container", "sh", "-n")
			if !available || len(output) != 0 || err != nil || inspectCalls != 1 || conn.closes.Load() != 1 {
				t.Fatalf("empty success = %v %q %v, inspect calls = %d, closes = %d", available, output, err, inspectCalls, conn.closes.Load())
			}
		})
	}
}

func TestContainerProbeSDKRejectsMalformedAttachments(t *testing.T) {
	for _, missing := range []string{"reader", "connection", "both"} {
		t.Run(missing, func(t *testing.T) {
			conn := &containerProbeSDKConn{reader: bytes.NewReader(nil)}
			attachment := containerProbeSDKAttachment(conn)
			if missing == "reader" || missing == "both" {
				attachment.Reader = nil
			}
			if missing == "connection" || missing == "both" {
				attachment.Conn = nil
			}
			fake := &containerProbeSDKFake{
				create: func(context.Context, string, client.ExecCreateOptions) (client.ExecCreateResult, error) {
					return client.ExecCreateResult{ID: "exec"}, nil
				},
				attach: func(context.Context, string, client.ExecAttachOptions) (client.ExecAttachResult, error) {
					return attachment, nil
				},
				inspect: func(context.Context, string, client.ExecInspectOptions) (client.ExecInspectResult, error) {
					t.Fatal("malformed attachment reached inspect")
					return client.ExecInspectResult{}, nil
				},
			}
			available, output, err := probeContainerCommandOutput(context.Background(), fake, "container", "sh", "-n")
			if available || output != nil || err == nil {
				t.Fatalf("malformed attachment = %v %q %v", available, output, err)
			}
			wantCloses := int32(0)
			if missing == "reader" {
				wantCloses = 1
			}
			if got := conn.closes.Load(); got != wantCloses {
				t.Fatalf("closes = %d, want %d", got, wantCloses)
			}
		})
	}
}

func TestContainerProbeSDKRejectsIncompleteSuccess(t *testing.T) {
	readErr := errors.New("probe read failed")
	closeErr := errors.New("probe close failed")
	inspectErr := errors.New("probe inspect failed")
	for _, test := range []struct {
		name        string
		wire        []byte
		readErr     error
		closeErr    error
		inspection  client.ExecInspectResult
		inspectErr  error
		wantCause   error
		wantError   bool
		wantInspect bool
	}{
		{name: "still running", wire: containerProbeSDKFrame(1, containerProbeSDKMarkers), inspection: client.ExecInspectResult{Running: true}, wantInspect: true},
		{name: "completed nonzero", wire: containerProbeSDKFrame(1, containerProbeSDKMarkers), inspection: client.ExecInspectResult{ExitCode: 3}, wantInspect: true},
		{name: "inspect error", wire: containerProbeSDKFrame(1, containerProbeSDKMarkers), inspectErr: inspectErr, wantCause: inspectErr, wantError: true, wantInspect: true},
		{name: "read error after markers", wire: containerProbeSDKFrame(1, containerProbeSDKMarkers), readErr: readErr, wantCause: readErr, wantError: true},
		{name: "close error", wire: containerProbeSDKFrame(1, containerProbeSDKMarkers), closeErr: closeErr, wantCause: closeErr, wantError: true},
		{name: "malformed after markers", wire: append(containerProbeSDKFrame(1, containerProbeSDKMarkers), 1, 0), wantError: true},
		{name: "overflow after markers", wire: append(containerProbeSDKFrame(1, containerProbeSDKMarkers), make([]byte, maxCapabilityProbeOutput)...), wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var reader io.Reader = bytes.NewReader(test.wire)
			if test.readErr != nil {
				reader = io.MultiReader(reader, containerProbeSDKErrorReader{err: test.readErr})
			}
			conn := &containerProbeSDKConn{reader: reader, closeFn: func() error { return test.closeErr }}
			inspectCalls := 0
			fake := &containerProbeSDKFake{
				create: func(context.Context, string, client.ExecCreateOptions) (client.ExecCreateResult, error) {
					return client.ExecCreateResult{ID: "exec"}, nil
				},
				attach: func(context.Context, string, client.ExecAttachOptions) (client.ExecAttachResult, error) {
					return containerProbeSDKAttachment(conn), nil
				},
				inspect: func(context.Context, string, client.ExecInspectOptions) (client.ExecInspectResult, error) {
					inspectCalls++
					if conn.closes.Load() != 1 {
						t.Fatalf("inspect before sole attachment close")
					}
					return test.inspection, test.inspectErr
				},
			}
			available, output, err := probeContainerCommandOutput(context.Background(), fake, "container", "/bin/bash", "--version")
			if available || output != nil || (test.wantError && err == nil) || (test.wantCause != nil && !errors.Is(err, test.wantCause)) {
				t.Fatalf("failed probe = %v %q %v, want no prefix and cause %v", available, output, err, test.wantCause)
			}
			if err != nil && strings.Contains(err.Error(), "NCP_READLINE") {
				t.Fatalf("error exposed captured output: %v", err)
			}
			wantInspect := 0
			if test.wantInspect {
				wantInspect = 1
			}
			if inspectCalls != wantInspect || conn.closes.Load() != 1 {
				t.Fatalf("inspect calls = %d, closes = %d", inspectCalls, conn.closes.Load())
			}
		})
	}
}

func TestContainerProbeSDKContextCancellationAtHandoffs(t *testing.T) {
	for _, stage := range []string{"before create", "after create", "after attach", "after read close", "after inspect"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			conn := &containerProbeSDKConn{reader: bytes.NewReader(containerProbeSDKFrame(1, containerProbeSDKMarkers))}
			if stage == "after read close" {
				conn.closeFn = func() error { cancel(); return nil }
			}
			var calls []string
			fake := &containerProbeSDKFake{
				create: func(context.Context, string, client.ExecCreateOptions) (client.ExecCreateResult, error) {
					calls = append(calls, "create")
					if stage == "after create" {
						cancel()
					}
					return client.ExecCreateResult{ID: "exec"}, nil
				},
				attach: func(context.Context, string, client.ExecAttachOptions) (client.ExecAttachResult, error) {
					calls = append(calls, "attach")
					attachment := containerProbeSDKAttachment(conn)
					if stage == "after attach" {
						if _, err := attachment.Reader.Peek(1); err != nil {
							t.Fatal(err)
						}
						cancel()
					}
					return attachment, nil
				},
				inspect: func(context.Context, string, client.ExecInspectOptions) (client.ExecInspectResult, error) {
					calls = append(calls, "inspect")
					if stage == "after inspect" {
						cancel()
					}
					return client.ExecInspectResult{}, nil
				},
			}
			if stage == "before create" {
				cancel()
			}
			available, output, err := probeContainerCommandOutput(ctx, fake, "container", "/bin/bash", "--version")
			if available || output != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled probe = %v %q %v", available, output, err)
			}
			wantCalls := []string{"create", "attach"}
			wantClose := int32(1)
			switch stage {
			case "before create":
				wantCalls, wantClose = nil, 0
			case "after create":
				wantCalls, wantClose = []string{"create"}, 0
			case "after inspect":
				wantCalls = append(wantCalls, "inspect")
			}
			if !reflect.DeepEqual(calls, wantCalls) || conn.closes.Load() != wantClose {
				t.Fatalf("calls = %v, closes = %d; want %v, %d", calls, conn.closes.Load(), wantCalls, wantClose)
			}
		})
	}
}

func TestContainerProbeSDKHonorsParentDeadlineDuringInspect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		deadline := time.Now().Add(time.Second)
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		conn := &containerProbeSDKConn{reader: bytes.NewReader(containerProbeSDKFrame(1, containerProbeSDKMarkers))}
		fake := &containerProbeSDKFake{
			create: func(ctx context.Context, _ string, _ client.ExecCreateOptions) (client.ExecCreateResult, error) {
				if got, ok := ctx.Deadline(); !ok || !got.Equal(deadline) {
					t.Fatalf("deadline = %v, want %v", got, deadline)
				}
				return client.ExecCreateResult{ID: "exec"}, nil
			},
			attach: func(context.Context, string, client.ExecAttachOptions) (client.ExecAttachResult, error) {
				return containerProbeSDKAttachment(conn), nil
			},
			inspect: func(ctx context.Context, _ string, _ client.ExecInspectOptions) (client.ExecInspectResult, error) {
				<-ctx.Done()
				return client.ExecInspectResult{}, nil
			},
		}
		available, output, err := probeContainerCommandOutput(ctx, fake, "container", "/bin/bash", "--version")
		if available || output != nil || !errors.Is(err, context.DeadlineExceeded) || conn.closes.Load() != 1 {
			t.Fatalf("expired probe = %v %q %v, closes = %d", available, output, err, conn.closes.Load())
		}
	})
}

func TestContainerProbeSDKErrorsKeepAttachmentOwnership(t *testing.T) {
	createErr := errors.New("create rejected")
	attachErr := errors.New("attach rejected")
	closeErr := errors.New("attachment close rejected")
	for _, test := range []struct {
		name       string
		create     bool
		attachment bool
		cancel     bool
		closeErr   error
	}{
		{name: "create error", create: true},
		{name: "create error and cancellation", create: true, cancel: true},
		{name: "attach error"},
		{name: "attachment plus error", attachment: true},
		{name: "attachment plus error and cancellation", attachment: true, cancel: true},
		{name: "attachment plus error and close error", attachment: true, closeErr: closeErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			conn := &containerProbeSDKConn{reader: bytes.NewReader(containerProbeSDKFrame(1, containerProbeSDKMarkers)), closeFn: func() error { return test.closeErr }}
			attachCalls, inspectCalls := 0, 0
			fake := &containerProbeSDKFake{
				create: func(context.Context, string, client.ExecCreateOptions) (client.ExecCreateResult, error) {
					if test.create {
						if test.cancel {
							cancel()
						}
						return client.ExecCreateResult{}, createErr
					}
					return client.ExecCreateResult{ID: "exec"}, nil
				},
				attach: func(context.Context, string, client.ExecAttachOptions) (client.ExecAttachResult, error) {
					attachCalls++
					if test.cancel {
						cancel()
					}
					if test.attachment {
						return containerProbeSDKAttachment(conn), attachErr
					}
					return client.ExecAttachResult{}, attachErr
				},
				inspect: func(context.Context, string, client.ExecInspectOptions) (client.ExecInspectResult, error) {
					inspectCalls++
					return client.ExecInspectResult{}, nil
				},
			}
			available, output, err := probeContainerCommandOutput(ctx, fake, "container", "sh", "-n")
			cause := attachErr
			wantAttach := 1
			if test.create {
				cause, wantAttach = createErr, 0
			}
			if available || output != nil || !errors.Is(err, cause) {
				t.Fatalf("failed SDK result = %v %q %v, want %v", available, output, err, cause)
			}
			if test.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation cause: %v", err)
			}
			if test.closeErr != nil && !errors.Is(err, test.closeErr) {
				t.Fatalf("lost close cause: %v", err)
			}
			wantCloses := int32(0)
			if test.attachment {
				wantCloses = 1
			}
			if attachCalls != wantAttach || inspectCalls != 0 || conn.closes.Load() != wantCloses {
				t.Fatalf("attach calls = %d, inspect calls = %d, closes = %d", attachCalls, inspectCalls, conn.closes.Load())
			}
		})
	}
}

func TestContainerProbeBashWrapperRejectsAllFailedPrefixes(t *testing.T) {
	runErr := errors.New("probe unsuccessful")
	for _, test := range []struct {
		name      string
		available bool
		err       error
		cancel    bool
	}{
		{name: "unavailable with markers"},
		{name: "available with error", available: true, err: runErr},
		{name: "unavailable with error", err: runErr},
		{name: "available after cancellation", available: true, cancel: true},
		{name: "error and cancellation", available: true, err: runErr, cancel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			run := func(gotCtx context.Context, id string, command ...string) (bool, []byte, error) {
				wantCommand := []string{"/usr/bin/bash", "--noprofile", "--norc", "-i", "-c", bashCapabilityProbeScript}
				if gotCtx != ctx || id != "container-wrapper" || !reflect.DeepEqual(command, wantCommand) {
					t.Fatalf("wrapper changed runner context, ID, or command: %q %q", id, command)
				}
				if test.cancel {
					cancel()
				}
				return test.available, []byte(containerProbeSDKMarkers), test.err
			}
			probe := containerBashCapabilityProbe(ctx, run, "container-wrapper", "/usr/bin/bash")
			if probe.Readline || probe.BracketedPaste || probe.BleSH || probe.Err == nil {
				t.Fatalf("failed prefix was trusted: %#v", probe)
			}
			if test.err != nil && !errors.Is(probe.Err, test.err) {
				t.Fatalf("lost runner error: %v", probe.Err)
			}
			if test.cancel && !errors.Is(probe.Err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", probe.Err)
			}
			metadata := containerShellMetadataWithProbe("/usr/bin/bash", probe)
			if metadata.Shell != "bash" || metadata.Enhancement != "native" || metadata.Capabilities.Readline || metadata.Capabilities.BracketedPaste || metadata.Capabilities.MultilinePaste || !metadata.Capabilities.Resize || !metadata.Capabilities.ANSIColors {
				t.Fatalf("failed probe metadata = %#v", metadata)
			}
		})
	}
}

func TestContainerProbeBashWrapperPreservesSuccessfulCapabilityMapping(t *testing.T) {
	for _, test := range []struct {
		name, output, enhancement, reason string
		readline, paste, ble              bool
	}{
		{name: "full", output: containerProbeSDKMarkers, enhancement: "readline", reason: "容器使用 Bash 内置 readline，不加载主机 ble.sh", readline: true, paste: true, ble: true},
		{name: "readline only", output: "NCP_READLINE=1\n", enhancement: "readline", reason: "容器使用 Bash 内置 readline，不加载主机 ble.sh；未启用 bracketed paste，多行粘贴将按行发送", readline: true},
		{name: "no markers", output: "ordinary output\n", enhancement: "native", reason: "容器 Bash 未确认 readline，已降级原生 Bash；不伪称补全或历史编辑"},
		{name: "empty", enhancement: "native", reason: "容器 Bash 未确认 readline，已降级原生 Bash；不伪称补全或历史编辑"},
		{name: "paste only", output: "set enable-bracketed-paste on\n", enhancement: "native", reason: "容器 Bash 未确认 readline，已降级原生 Bash；不伪称补全或历史编辑", paste: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			probe := containerBashCapabilityProbe(context.Background(), func(context.Context, string, ...string) (bool, []byte, error) { return true, []byte(test.output), nil }, "container", "/bin/bash")
			if probe.Err != nil || probe.Readline != test.readline || probe.BracketedPaste != test.paste || probe.BleSH != test.ble {
				t.Fatalf("successful probe = %#v", probe)
			}
			metadata := containerShellMetadataWithProbe("/bin/bash", probe)
			wantPaste := test.readline && test.paste
			if metadata.Shell != "bash" || metadata.Enhancement != test.enhancement || metadata.Reason != test.reason || metadata.Capabilities.Readline != test.readline || metadata.Capabilities.BracketedPaste != wantPaste || metadata.Capabilities.MultilinePaste != wantPaste || !metadata.Capabilities.Resize || !metadata.Capabilities.ANSIColors {
				t.Fatalf("successful metadata = %#v", metadata)
			}
		})
	}
}

func TestContainerProbeMetadataDefensivelyZerosErrorFlags(t *testing.T) {
	metadata := containerShellMetadataWithProbe("/bin/bash", shellCapabilityProbe{Readline: true, BracketedPaste: true, BleSH: true, Err: errors.New("failed")})
	if metadata.Enhancement != "native" || metadata.Capabilities.Readline || metadata.Capabilities.BracketedPaste || metadata.Capabilities.MultilinePaste || !metadata.Capabilities.Resize || !metadata.Capabilities.ANSIColors {
		t.Fatalf("error-bearing flags escaped through metadata: %#v", metadata)
	}
}

func TestContainerProbeProtectedInteractiveOptionsSnapshot(t *testing.T) {
	environment := []string{
		"TERM=xterm-256color", "COLORTERM=truecolor", "LANG=C", "LC_ALL=C", "CLICOLOR=1",
		"SYSTEMD_COLORS=1", "SYSTEMD_PAGER=cat", "GIT_PAGER=cat", "LESS=-R",
		"LS_COLORS=di=38;5;25:ln=38;5;30:ex=38;5;28:*.tar=38;5;130:*.gz=38;5;130:*.zip=38;5;130",
		"HISTFILE=/tmp/.ncp_shell_history", "HISTCONTROL=ignoredups", "HISTSIZE=1000",
		"PS1=\\[\\e[38;5;25m\\]\\u@\\h\\[\\e[0m\\]:\\[\\e[38;5;30m\\]\\w\\[\\e[0m\\]\\$ ",
	}
	for _, test := range []struct {
		shell   string
		command []string
	}{
		{shell: "/bin/bash", command: []string{"/bin/bash", "--noprofile", "--norc", "-i"}},
		{shell: "/usr/bin/bash", command: []string{"/usr/bin/bash", "--noprofile", "--norc", "-i"}},
		{shell: "bash", command: []string{"bash", "--noprofile", "--norc", "-i"}},
		{shell: "/bin/sh", command: []string{"/bin/sh", "-i"}},
		{shell: "sh", command: []string{"sh", "-i"}},
		{shell: "", command: []string{"/bin/sh", "-i"}},
	} {
		t.Run(test.shell, func(t *testing.T) {
			want := client.ExecCreateOptions{AttachStdin: true, AttachStdout: true, AttachStderr: true, TTY: true, ConsoleSize: client.ConsoleSize{Height: 34, Width: 120}, Env: environment, Cmd: test.command}
			if got := protectedContainerExecOptionsForShell(34, 120, test.shell); !reflect.DeepEqual(got, want) {
				t.Fatalf("protected options = %#v, want %#v", got, want)
			}
		})
	}
}
