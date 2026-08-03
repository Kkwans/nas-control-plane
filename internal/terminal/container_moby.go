package terminal

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"time"

	"github.com/moby/moby/client"
)

type mobyContainerGateway struct {
	client *client.Client
}

const containerShellProbeTimeout = 2 * time.Second

func newMobyContainerGateway(apiClient *client.Client) *mobyContainerGateway {
	return &mobyContainerGateway{client: apiClient}
}

func (g *mobyContainerGateway) Inspect(ctx context.Context, containerID string) (ContainerInfo, error) {
	response, err := g.client.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return ContainerInfo{}, err
	}
	labels := make(map[string]string)
	if response.Container.Config != nil {
		for key, value := range response.Container.Config.Labels {
			labels[key] = value
		}
	}
	return ContainerInfo{
		ID:      response.Container.ID,
		Name:    response.Container.Name,
		Labels:  labels,
		Running: response.Container.State != nil && response.Container.State.Running,
	}, nil
}

func (g *mobyContainerGateway) Open(ctx context.Context, containerID string, rows, cols uint16) (ContainerAttachment, error) {
	shell, err := g.detectContainerShell(ctx, containerID)
	if err != nil {
		return ContainerAttachment{}, err
	}
	exec, err := g.client.ExecCreate(ctx, containerID, protectedContainerExecOptionsForShell(rows, cols, shell))
	if err != nil {
		return ContainerAttachment{}, err
	}
	attachment, err := g.client.ExecAttach(ctx, exec.ID, client.ExecAttachOptions{
		TTY:         true,
		ConsoleSize: client.ConsoleSize{Height: uint(rows), Width: uint(cols)},
	})
	if err != nil {
		return ContainerAttachment{}, err
	}
	metadata := containerShellMetadata(shell)
	return ContainerAttachment{
		ExecID:       exec.ID,
		Reader:       attachment.Reader,
		Writer:       attachment.Conn,
		Shell:        metadata.Shell,
		Enhancement:  metadata.Enhancement,
		Reason:       metadata.Reason,
		Capabilities: metadata.Capabilities,
		Close: func() error {
			attachment.Close()
			return nil
		},
		CloseWrite: attachment.CloseWrite,
		CancelRead: func() error {
			var cancelErr error
			if err := writeAll(attachment.Conn, []byte{3}); err != nil {
				cancelErr = err
			}
			if err := writeAll(attachment.Conn, []byte("exit\n")); err != nil && cancelErr == nil {
				cancelErr = err
			}
			if attachment.CloseWrite != nil {
				if err := attachment.CloseWrite(); err != nil && cancelErr == nil {
					cancelErr = err
				}
			}
			attachment.Close()
			return cancelErr
		},
	}, nil
}

func (g *mobyContainerGateway) detectContainerShell(ctx context.Context, containerID string) (string, error) {
	for _, shell := range []string{"/bin/bash", "/usr/bin/bash", "bash"} {
		available, err := g.probeContainerCommand(ctx, containerID, shell, "--version")
		if err != nil && ctx.Err() != nil {
			return "", ctx.Err()
		}
		if available {
			return shell, nil
		}
	}
	for _, shell := range []string{"/bin/sh", "/usr/bin/sh", "sh"} {
		available, err := g.probeContainerCommand(ctx, containerID, shell, "-n")
		if err != nil && ctx.Err() != nil {
			return "", ctx.Err()
		}
		if available {
			return shell, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", errors.New("container does not provide an interactive shell")
}

func (g *mobyContainerGateway) probeContainerCommand(ctx context.Context, containerID string, command ...string) (bool, error) {
	probeContext, cancel := context.WithTimeout(ctx, containerShellProbeTimeout)
	defer cancel()
	exec, err := g.client.ExecCreate(probeContext, containerID, client.ExecCreateOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          command,
	})
	if err != nil {
		return false, err
	}
	attachment, err := g.client.ExecAttach(probeContext, exec.ID, client.ExecAttachOptions{})
	if err != nil {
		return false, err
	}
	defer attachment.Close()
	if _, err := io.Copy(io.Discard, attachment.Reader); err != nil {
		return false, err
	}
	inspection, err := g.client.ExecInspect(probeContext, exec.ID, client.ExecInspectOptions{})
	if err != nil {
		return false, err
	}
	return inspection.ExitCode == 0, nil
}

func containerShellMetadata(shellPath string) SessionMetadata {
	shell := filepath.Base(shellPath)
	if shell == "bash" {
		return SessionMetadata{
			Shell:        shell,
			Enhancement:  "readline",
			Reason:       "容器使用 Bash 内置 readline，不加载主机 ble.sh",
			Capabilities: sessionCapabilitiesFor(shell, "readline"),
		}
	}
	if shell == "" || shell == "." {
		shell = "sh"
	}
	return SessionMetadata{
		Shell:        shell,
		Enhancement:  "native",
		Reason:       "容器未发现 Bash，已回退原生 /bin/sh；不具备 readline",
		Capabilities: sessionCapabilitiesFor(shell, "native"),
	}
}

func (g *mobyContainerGateway) Resize(ctx context.Context, execID string, rows, cols uint16) error {
	_, err := g.client.ExecResize(ctx, execID, client.ExecResizeOptions{Height: uint(rows), Width: uint(cols)})
	return err
}

func (g *mobyContainerGateway) WaitForExit(ctx context.Context, execID string) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		inspection, err := g.client.ExecInspect(ctx, execID, client.ExecInspectOptions{})
		if err != nil {
			return err
		}
		if !inspection.Running {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func protectedContainerExecOptions(rows, cols uint16) client.ExecCreateOptions {
	return protectedContainerExecOptionsForShell(rows, cols, "/bin/bash")
}

func protectedContainerExecOptionsForShell(rows, cols uint16, shell string) client.ExecCreateOptions {
	if shell == "" {
		shell = "/bin/sh"
	}
	arguments := []string{shell, "-i"}
	if filepath.Base(shell) == "bash" {
		arguments = []string{shell, "--noprofile", "--norc", "-i"}
	}
	return client.ExecCreateOptions{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		TTY:          true,
		ConsoleSize:  client.ConsoleSize{Height: uint(rows), Width: uint(cols)},
		Env: []string{
			"TERM=xterm-256color",
			"COLORTERM=truecolor",
			"LANG=C",
			"LC_ALL=C",
			"CLICOLOR=1",
			"SYSTEMD_COLORS=1",
			"SYSTEMD_PAGER=cat",
			"GIT_PAGER=cat",
			"LESS=-R",
			"LS_COLORS=di=38;5;25:ln=38;5;30:ex=38;5;28:*.tar=38;5;130:*.gz=38;5;130:*.zip=38;5;130",
			"HISTFILE=/tmp/.ncp_shell_history",
			"HISTCONTROL=ignoredups",
			"HISTSIZE=1000",
			"PS1=\\[\\e[38;5;25m\\]\\u@\\h\\[\\e[0m\\]:\\[\\e[38;5;30m\\]\\w\\[\\e[0m\\]\\$ ",
		},
		Cmd: arguments,
	}
}
