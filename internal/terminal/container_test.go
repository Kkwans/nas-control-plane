package terminal

import (
	"bytes"
	"context"
	"errors"
	"testing"
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
}

func TestProtectedContainerExecOptionsAreInteractiveButNotPrivileged(t *testing.T) {
	options := protectedContainerExecOptions(34, 120)
	if options.Privileged || !options.TTY || !options.AttachStdin || !options.AttachStdout || !options.AttachStderr {
		t.Fatalf("container exec options = %#v", options)
	}
	if options.ConsoleSize.Height != 34 || options.ConsoleSize.Width != 120 {
		t.Fatalf("console size = %#v", options.ConsoleSize)
	}
	if len(options.Cmd) != 1 || options.Cmd[0] != "/bin/sh" {
		t.Fatalf("command = %#v", options.Cmd)
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
