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

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/Kkwans/nas-control-plane/internal/system"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(os.Args) > 1 && os.Args[1] == "docker-poc" {
		if err := runDockerPOC(ctx, os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, dockerPOCErrorCode(err))
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
