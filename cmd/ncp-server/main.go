package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
)

const agentProbeTimeout = 5 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(os.Args) != 2 || os.Args[1] != "agent-probe" {
		fmt.Fprintln(os.Stderr, "NCP_SERVER_COMMAND_UNKNOWN")
		os.Exit(1)
	}
	if err := runAgentProbe(ctx, nil, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, agentProbeErrorCode(err))
		os.Exit(1)
	}
}

func runAgentProbe(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("agent-probe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected agent-probe arguments")
	}

	requestContext, cancel := context.WithTimeout(ctx, agentProbeTimeout)
	defer cancel()
	status, err := agentsocket.Probe(requestContext, agentsocket.DefaultSocketPath)
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(status)
}

func agentProbeErrorCode(err error) string {
	if code := agentsocket.ErrorCode(err); code != "" {
		return code
	}
	return "NCP_SERVER_AGENT_PROBE_FAILED"
}
