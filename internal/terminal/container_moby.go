package terminal

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"path/filepath"
	"sync"
	"time"

	"github.com/moby/moby/client"
)

type mobyContainerGateway struct {
	client *client.Client
}

const containerShellProbeTimeout = 2 * time.Second

var errContainerProbeOutputLimit = errors.New("container probe output exceeds wire limit")

// 仅探测使用此窄接口；交互 gateway 仍持有真实 SDK client。
type containerProbeClient interface {
	ExecCreate(context.Context, string, client.ExecCreateOptions) (client.ExecCreateResult, error)
	ExecAttach(context.Context, string, client.ExecAttachOptions) (client.ExecAttachResult, error)
	ExecInspect(context.Context, string, client.ExecInspectOptions) (client.ExecInspectResult, error)
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
	shell, err := g.detectContainerShell(ctx, containerID)
	if err != nil {
		return ContainerAttachment{}, err
	}
	var metadata SessionMetadata
	if filepath.Base(shell) == "bash" {
		metadata = containerShellMetadataWithProbe(shell, g.probeContainerBashCapabilities(ctx, containerID, shell))
	} else {
		metadata = containerShellMetadata(shell)
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
	return ContainerAttachment{
		ExecID:       exec.ID,
		Reader:       attachment.Reader,
		Conn:         attachment.Conn,
		Shell:        metadata.Shell,
		Enhancement:  metadata.Enhancement,
		Reason:       metadata.Reason,
		Capabilities: metadata.Capabilities,
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
	available, _, err := g.probeContainerCommandOutput(ctx, containerID, command...)
	return available, err
}

func (g *mobyContainerGateway) probeContainerCommandOutput(ctx context.Context, containerID string, command ...string) (bool, []byte, error) {
	return probeContainerCommandOutput(ctx, g.client, containerID, command...)
}

func probeContainerCommandOutput(ctx context.Context, api containerProbeClient, containerID string, command ...string) (bool, []byte, error) {
	probeContext, cancel := context.WithTimeout(ctx, containerShellProbeTimeout)
	defer cancel()
	if err := probeContext.Err(); err != nil {
		return false, nil, err
	}
	exec, err := api.ExecCreate(probeContext, containerID, client.ExecCreateOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          command,
	})
	if err = errors.Join(err, probeContext.Err()); err != nil {
		return false, nil, err
	}
	attachment, err := api.ExecAttach(probeContext, exec.ID, client.ExecAttachOptions{})
	if err != nil {
		// SDK 错误通常返回空 attachment；seam 的部分资源也必须收尾。
		if attachment.Conn != nil {
			err = errors.Join(err, attachment.Conn.Close())
		}
		return false, nil, errors.Join(err, probeContext.Err())
	}
	// 即使 Attach 返回时已取消，也先进入唯一同步读取/关闭 owner。
	var reader io.Reader
	if attachment.Reader != nil {
		reader = attachment.Reader
	}
	wire, err := readContainerProbe(probeContext, reader, attachment.Conn)
	if err != nil {
		return false, nil, err
	}
	output, err := decodeContainerProbeOutput(wire)
	if err = errors.Join(err, probeContext.Err()); err != nil {
		return false, nil, err
	}
	inspection, err := api.ExecInspect(probeContext, exec.ID, client.ExecInspectOptions{})
	if err = errors.Join(err, probeContext.Err()); err != nil {
		return false, nil, err
	}
	// SDK 缺失字段会归零；依赖官方 Engine 响应契约，不能验证原始字段存在。
	if inspection.Running || inspection.ExitCode != 0 {
		return false, nil, nil
	}
	return true, output, nil
}

// 探测 attachment 与交互会话采用同一纯 transport 中断契约。
func readContainerProbe(ctx context.Context, reader io.Reader, conn net.Conn) (output []byte, err error) {
	if conn == nil {
		return nil, errors.Join(errors.New("container probe attachment has no connection"), ctx.Err())
	}
	var once sync.Once
	var closeErr error
	abort := func() { once.Do(func() { closeErr = conn.Close() }) }
	stop := onContextAbort(ctx, abort)
	defer func() {
		// 先停止或等待取消回调，再补齐关闭；closeErr 此后不会再被并发写入。
		stop()
		abort()
		err = errors.Join(err, ctx.Err(), closeErr)
		if err != nil {
			output = nil
		}
	}()
	if reader == nil {
		return nil, errors.New("container probe attachment has no reader")
	}
	// 捕获上限包含帧头；额外一字节只用于确认超限，不返回失败前缀。
	var wire [maxCapabilityProbeOutput + 1]byte
	used, emptyReads := 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, readErr := reader.Read(wire[used:])
		used += n
		if readErr == io.EOF {
			readErr = nil
			if used <= maxCapabilityProbeOutput {
				return wire[:used], nil
			}
		}
		if used > maxCapabilityProbeOutput {
			return nil, errors.Join(errContainerProbeOutputLimit, readErr)
		}
		if readErr != nil {
			return nil, readErr
		}
		if n == 0 {
			emptyReads++
			if emptyReads >= 100 {
				return nil, io.ErrNoProgress
			}
		} else {
			emptyReads = 0
		}
	}
}

// 固定非 TTY 的 multiplex 契约；不猜测 raw 输出，不按宣告长度分配。
func decodeContainerProbeOutput(wire []byte) ([]byte, error) {
	if len(wire) > maxCapabilityProbeOutput {
		return nil, errContainerProbeOutputLimit
	}
	remaining := wire
	payloadBytes := 0
	for len(remaining) > 0 {
		if len(remaining) < 8 {
			return nil, errors.New("container probe has incomplete frame header")
		}
		// 接受 SDK legacy stdin(0)，与 stdout(1)、stderr(2) 按到达顺序合并。
		if remaining[0] > 2 || remaining[1] != 0 || remaining[2] != 0 || remaining[3] != 0 {
			return nil, errors.New("container probe has invalid frame header")
		}
		size := binary.BigEndian.Uint32(remaining[4:8])
		remaining = remaining[8:]
		if uint64(size) > uint64(len(remaining)) {
			return nil, errors.New("container probe has incomplete frame payload")
		}
		payloadBytes += int(size)
		remaining = remaining[int(size):]
	}
	// 全部帧验证完才分配；容量不超过已经取得的有界 wire bytes。
	output := make([]byte, 0, payloadBytes)
	for len(wire) > 0 {
		size := int(binary.BigEndian.Uint32(wire[4:8]))
		wire = wire[8:]
		output = append(output, wire[:size]...)
		wire = wire[size:]
	}
	return output, nil
}

func (g *mobyContainerGateway) probeContainerBashCapabilities(ctx context.Context, containerID, shell string) shellCapabilityProbe {
	return containerBashCapabilityProbe(ctx, g.probeContainerCommandOutput, containerID, shell)
}

func containerBashCapabilityProbe(ctx context.Context, run func(context.Context, string, ...string) (bool, []byte, error), containerID, shell string) shellCapabilityProbe {
	if err := ctx.Err(); err != nil {
		return shellCapabilityProbe{Err: err}
	}
	available, output, err := run(ctx, containerID, shell, "--noprofile", "--norc", "-i", "-c", bashCapabilityProbeScript)
	if err = errors.Join(err, ctx.Err()); err != nil {
		return shellCapabilityProbe{Err: err}
	}
	if !available {
		return shellCapabilityProbe{Err: errors.New("container Bash capability probe failed")}
	}
	return parseBashCapabilityProbe(output)
}

func containerShellMetadata(shellPath string) SessionMetadata {
	shell := filepath.Base(shellPath)
	if shell == "bash" {
		return containerShellMetadataWithProbe(shellPath, shellCapabilityProbe{Err: errors.New("Bash capability probe was not run")})
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

func containerShellMetadataWithProbe(shellPath string, probe shellCapabilityProbe) SessionMetadata {
	if probe.Err != nil {
		probe = shellCapabilityProbe{Err: probe.Err}
	}
	shell := filepath.Base(shellPath)
	if shell == "" || shell == "." {
		shell = "sh"
	}
	if shell != "bash" {
		return SessionMetadata{
			Shell:        shell,
			Enhancement:  "native",
			Reason:       "容器未发现 Bash，已回退原生 /bin/sh；不具备 readline",
			Capabilities: sessionCapabilitiesFor(shell, "native"),
		}
	}

	metadata := SessionMetadata{
		Shell:        shell,
		Enhancement:  "readline",
		Reason:       "容器使用 Bash 内置 readline，不加载主机 ble.sh",
		Capabilities: sessionCapabilitiesForProbe(shell, "readline", probe),
	}
	if !probe.Readline {
		metadata.Enhancement = "native"
		metadata.Reason = "容器 Bash 未确认 readline，已降级原生 Bash；不伪称补全或历史编辑"
		metadata.Capabilities = sessionCapabilitiesForProbe(shell, metadata.Enhancement, probe)
	} else if !probe.BracketedPaste {
		metadata.Reason += "；未启用 bracketed paste，多行粘贴将按行发送"
	}
	return metadata
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
