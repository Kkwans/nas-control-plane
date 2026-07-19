package journal

import (
	"context"
	"errors"
	"io"
	"os/exec"
)

// OSRunner executes only fixed, validated journalctl argument lists assembled
// by Reader. It deliberately exposes no generic command execution API.
type OSRunner struct{}

func (OSRunner) Output(ctx context.Context, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "journalctl", args...)
	command.Stderr = io.Discard
	output, err := command.Output()
	if isNoEntriesExit(err) {
		return output, nil
	}
	return output, err
}

func (OSRunner) Follow(ctx context.Context, args ...string) (io.ReadCloser, <-chan error, error) {
	command := exec.CommandContext(ctx, "journalctl", args...)
	command.Stderr = io.Discard
	output, err := command.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := command.Start(); err != nil {
		return nil, nil, err
	}
	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
		close(done)
	}()
	return output, done, nil
}

func isNoEntriesExit(err error) bool {
	if err == nil {
		return false
	}
	var exitCoder interface{ ExitCode() int }
	return errors.As(err, &exitCoder) && exitCoder.ExitCode() == 1
}
