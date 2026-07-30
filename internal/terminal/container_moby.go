package terminal

import (
	"context"
	"time"

	"github.com/moby/moby/client"
)

type mobyContainerGateway struct {
	client *client.Client
}

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
	exec, err := g.client.ExecCreate(ctx, containerID, protectedContainerExecOptions(rows, cols))
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
	return ContainerAttachment{
		ExecID:      exec.ID,
		Reader:      attachment.Reader,
		Writer:      attachment.Conn,
		Shell:       "auto",
		Enhancement: "native",
		Reason:      "容器使用自身 Shell，已启用 ANSI 色彩；未向容器注入 ble.sh",
		Close: func() error {
			attachment.Close()
			return nil
		},
		CloseWrite: attachment.CloseWrite,
	}, nil
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
			"LS_COLORS=di=38;5;25:ln=38;5;30:ex=38;5;28:*.tar=38;5;130:*.gz=38;5;130:*.zip=38;5;130",
			"HISTFILE=/tmp/.ncp_shell_history",
			"HISTCONTROL=ignoredups",
			"HISTSIZE=1000",
		},
		Cmd: []string{
			"/bin/sh", "-lc",
			`if command -v locale >/dev/null 2>&1 && locale -a 2>/dev/null | grep -Eiq '^C([.-])?UTF-?8$'; then export LANG=C.UTF-8 LC_ALL=C.UTF-8; else export LANG=C LC_ALL=C; fi; alias ls='ls --color=auto' 2>/dev/null || true; alias grep='grep --color=auto' 2>/dev/null || true; if command -v bash >/dev/null 2>&1; then export PS1='\[\e[38;5;25m\]\u@\h\[\e[0m\]:\[\e[38;5;30m\]\w\[\e[0m\]\$ '; exec bash --noprofile --norc -i; fi; export PS1='\u@\h:\w\$ '; exec /bin/sh -i`,
		},
	}
}
