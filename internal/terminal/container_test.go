package terminal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestContainerStarterRejectsStoppedTargetBeforeOpeningExec(t *testing.T) {
	gateway := &fakeContainerGateway{info: ContainerInfo{
		ID:      "container-123",
		Name:    "/project-web",
		Running: false,
	}}
	starter := newContainerStarter(gateway)

	_, err := starter.Start(context.Background(), StartRequest{Target: TargetContainer, ContainerID: "project-web", Rows: 24, Cols: 80})

	if ErrorCode(err) != "TERMINAL_CONTAINER_TARGET_REJECTED" {
		t.Fatalf("error code = %q", ErrorCode(err))
	}
	if gateway.openCalls != 0 {
		t.Fatalf("container exec opens = %d, want 0", gateway.openCalls)
	}
}

func TestContainerStarterUsesSelectedTargetAndClosesAttachment(t *testing.T) {
	closed := false
	closeWrite := false
	var input bytes.Buffer
	gateway := &fakeContainerGateway{
		info: ContainerInfo{
			ID:      "container-123",
			Name:    "/project-web",
			Running: true,
		},
		attachment: ContainerAttachment{
			ExecID:     "exec-123",
			Reader:     bytes.NewBuffer(nil),
			Writer:     &input,
			Close:      func() error { closed = true; return nil },
			CloseWrite: func() error { closeWrite = true; return nil },
		},
	}
	starter := newContainerStarter(gateway)

	session, err := starter.Start(context.Background(), StartRequest{Target: TargetContainer, ContainerID: "project-web", Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("start container terminal: %v", err)
	}
	if gateway.openID != "container-123" || gateway.rows != 24 || gateway.cols != 80 {
		t.Fatalf("open request = %#v", gateway)
	}
	if err := session.Resize(34, 120); err != nil {
		t.Fatalf("resize container terminal: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("close container terminal: %v", err)
	}
	if !closed || !closeWrite || gateway.waitID != "exec-123" || gateway.resizeID != "exec-123" || gateway.resizeRows != 34 || gateway.resizeCols != 120 {
		t.Fatalf("close/resize state = %#v", gateway)
	}
	if got := input.String(); got != "\x03exit\n" {
		t.Fatalf("termination input = %q", got)
	}
	metadata := session.(SessionMetadataProvider).Metadata()
	if metadata.Shell != "sh" || metadata.Enhancement != "native" || metadata.Capabilities.Readline {
		t.Fatalf("fallback metadata = %#v", metadata)
	}
}

func TestProtectedContainerExecOptionsAreInteractiveButNotPrivileged(t *testing.T) {
	options := protectedContainerExecOptions(34, 120)
	if options.Privileged || !options.TTY || !options.AttachStdin || !options.AttachStdout || !options.AttachStderr {
		t.Fatalf("container exec options = %#v", options)
	}
	if options.ConsoleSize.Height != 34 || options.ConsoleSize.Width != 120 {
		t.Fatalf("console size = %#v", options.ConsoleSize)
	}
	if len(options.Cmd) != 4 || options.Cmd[0] != "/bin/bash" || options.Cmd[1] != "--noprofile" || options.Cmd[2] != "--norc" || options.Cmd[3] != "-i" {
		t.Fatalf("command = %#v", options.Cmd)
	}
	if strings.Contains(strings.Join(options.Cmd, " "), " -c") || strings.Contains(strings.Join(options.Cmd, " "), " -lc") {
		t.Fatalf("shell command must not be assembled through -c: %#v", options.Cmd)
	}
	if len(options.Env) < 3 {
		t.Fatalf("interactive shell environment = %#v", options.Env)
	}
	environment := strings.Join(options.Env, "\n")
	if !strings.Contains(environment, "TERM=xterm-256color") || !strings.Contains(environment, "COLORTERM=truecolor") {
		t.Fatalf("terminal environment = %#v", options.Env)
	}
	if !strings.Contains(environment, "PS1=") || strings.Contains(environment, "/opt/ncp/share/blesh") {
		t.Fatalf("interactive shell environment = %#v", options.Env)
	}
}

func TestProtectedContainerExecOptionsUseNativeBusyBoxShellWithoutScript(t *testing.T) {
	options := protectedContainerExecOptionsForShell(24, 80, "/bin/sh")
	if len(options.Cmd) != 2 || options.Cmd[0] != "/bin/sh" || options.Cmd[1] != "-i" {
		t.Fatalf("BusyBox shell command = %#v", options.Cmd)
	}
	if strings.Contains(strings.Join(options.Cmd, " "), "-c") {
		t.Fatalf("BusyBox shell command must not contain -c: %#v", options.Cmd)
	}
}

func TestContainerShellMetadataReportsBashReadline(t *testing.T) {
	metadata := containerShellMetadata("/bin/bash")
	if metadata.Shell != "bash" || metadata.Enhancement != "readline" || metadata.Reason == "" {
		t.Fatalf("Bash metadata = %#v", metadata)
	}
	if !metadata.Capabilities.Readline || !metadata.Capabilities.BracketedPaste || !metadata.Capabilities.MultilinePaste {
		t.Fatalf("Bash capabilities = %#v", metadata.Capabilities)
	}
	if strings.Contains(metadata.Reason, "blesh") && !strings.Contains(metadata.Reason, "不加载") {
		t.Fatalf("Bash metadata must not claim host ble.sh: %q", metadata.Reason)
	}
}

func TestContainerShellMetadataReportsBusyBoxNativeFallback(t *testing.T) {
	metadata := containerShellMetadata("/bin/sh")
	if metadata.Shell != "sh" || metadata.Enhancement != "native" || metadata.Reason == "" {
		t.Fatalf("BusyBox metadata = %#v", metadata)
	}
	if metadata.Capabilities.Readline || metadata.Capabilities.BracketedPaste || metadata.Capabilities.MultilinePaste {
		t.Fatalf("BusyBox capabilities = %#v", metadata.Capabilities)
	}
}

func TestContainerSessionReadHonorsContextCancellation(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})
	readCanceled := make(chan struct{})
	session := &containerSession{attachment: ContainerAttachment{
		Reader: reader,
		CancelRead: func() error {
			close(readCanceled)
			return reader.Close()
		},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := session.Read(ctx, make([]byte, 16))
		result <- err
	}()
	cancel()

	select {
	case <-readCanceled:
	case <-time.After(time.Second):
		t.Fatal("context cancellation did not interrupt container read")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("read error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("container read did not return after cancellation")
	}
}

type fakeContainerGateway struct {
	info       ContainerInfo
	inspectErr error
	openErr    error
	attachment ContainerAttachment
	openCalls  int
	openID     string
	rows       uint16
	cols       uint16
	resizeID   string
	resizeRows uint16
	resizeCols uint16
	waitID     string
}

func (f *fakeContainerGateway) Inspect(context.Context, string) (ContainerInfo, error) {
	return f.info, f.inspectErr
}

func (f *fakeContainerGateway) Open(_ context.Context, containerID string, rows, cols uint16) (ContainerAttachment, error) {
	f.openCalls++
	f.openID = containerID
	f.rows = rows
	f.cols = cols
	if f.openErr != nil {
		return ContainerAttachment{}, f.openErr
	}
	return f.attachment, nil
}

func (f *fakeContainerGateway) Resize(_ context.Context, execID string, rows, cols uint16) error {
	if f.openErr != nil {
		return errors.New("resize unavailable")
	}
	f.resizeID = execID
	f.resizeRows = rows
	f.resizeCols = cols
	return nil
}

func (f *fakeContainerGateway) WaitForExit(_ context.Context, execID string) error {
	f.waitID = execID
	return nil
}
