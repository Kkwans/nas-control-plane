package journal

import (
	"context"
	"io"
	"os/exec"
)

// OSRunner executes only fixed, validated journalctl argument lists assembled
// by Reader. It deliberately exposes no generic command execution API.
type OSRunner struct{}

func (OSRunner) Output(ctx context.Context, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "journalctl", args...)
	command.Stderr = io.Discard
	return command.Output()
}
