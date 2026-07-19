package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/Kkwans/nas-control-plane/internal/system"
	"github.com/Kkwans/nas-control-plane/internal/terminal"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "docker-poc":
			if err := runDockerPOC(ctx, os.Args[2:], os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, dockerPOCErrorCode(err))
				os.Exit(1)
			}
		case "serve":
			if err := runAgentServer(ctx, os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, agentSocketErrorCode(err))
				os.Exit(1)
			}
		case "terminal-poc":
			if err := runTerminalPOC(ctx, os.Args[2:], os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, terminalPOCErrorCode(err))
				os.Exit(1)
			}
		default:
			fmt.Fprintln(os.Stderr, "NCP_AGENT_COMMAND_UNKNOWN")
			os.Exit(1)
		}
		return
	}

	capabilities, err := system.NewProbe(system.NewOSEnvironment()).Collect(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ncp-agent capability probe failed")
		os.Exit(1)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(capabilities); err != nil {
		fmt.Fprintln(os.Stderr, "ncp-agent capability output failed")
		os.Exit(1)
	}
}

func runDockerPOC(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("docker-poc", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	containerID := flags.String("container", "", "受控 Docker PoC 容器 ID 或名称")
	if err := flags.Parse(args); err != nil {
		return err
	}

	gateway, err := docker.NewMobyGateway()
	if err != nil {
		return err
	}
	result, err := docker.NewRunner(gateway).Run(ctx, docker.POCRequest{TestContainerID: *containerID})
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func dockerPOCErrorCode(err error) string {
	if code := docker.ErrorCode(err); code != "" {
		return code
	}
	return "DOCKER_POC_FAILED"
}

func runAgentServer(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	socketGroup := flags.String("socket-group", "", "允许连接 Agent Socket 的 Server 组")
	socketPath := flags.String("socket-path", agentsocket.DefaultSocketPath, "Agent Unix Socket 路径；仅 P0 终端实测可覆写")
	terminalPOC := flags.Bool("terminal-poc", false, "启用受控 P0 终端 Agent 服务")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected serve arguments")
	}
	if err := agentsocket.ValidateServerSocketPath(*socketPath, *terminalPOC); err != nil {
		return err
	}
	if err := agentsocket.ValidateServerSocketGroup(*socketGroup); err != nil {
		return err
	}
	return agentsocket.Serve(ctx, agentsocket.SocketConfig{
		SocketPath:        *socketPath,
		SocketGroup:       *socketGroup,
		EnableTerminalPOC: *terminalPOC,
	})
}

func agentSocketErrorCode(err error) string {
	if code := agentsocket.ErrorCode(err); code != "" {
		return code
	}
	return "AGENT_SOCKET_FAILED"
}

func runTerminalPOC(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("terminal-poc", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	target := flags.String("target", "", "P0 终端目标：host 或 container")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected terminal-poc arguments")
	}
	selectedTarget := terminal.Target(*target)
	if err := terminal.ValidateTarget(selectedTarget); err != nil {
		return err
	}

	manager, err := terminal.NewPOCManager()
	if err != nil {
		return err
	}
	result, err := terminal.RunPOC(ctx, manager, selectedTarget)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func terminalPOCErrorCode(err error) string {
	if code := terminal.ErrorCode(err); code != "" {
		return code
	}
	return "TERMINAL_POC_FAILED"
}
