package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Kkwans/nas-control-plane/internal/system"
)

func main() {
	context, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	capabilities, err := system.NewProbe(system.NewOSEnvironment()).Collect(context)
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
