package terminal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	pocInitialRows = 24
	pocInitialCols = 80
	pocResizeRows  = 34
	pocResizeCols  = 120
	pocOutputChunk = 16 * 1024
	pocSettleDelay = 250 * time.Millisecond
)

func NewPOCManager() (*Manager, error) {
	container, err := NewContainerStarter()
	if err != nil {
		return nil, err
	}
	return NewManager(NewHostStarter(), container, defaultMaxSessions), nil
}

type POCResult struct {
	Target            Target `json:"target"`
	PTY               bool   `json:"pty"`
	Resize            bool   `json:"resize"`
	CtrlC             bool   `json:"ctrlC"`
	SessionTerminated bool   `json:"sessionTerminated"`
}

// RunPOC 只发送固定验证指令，验证 PTY、resize、控制字符和会话关闭，不接受外部命令。
func RunPOC(ctx context.Context, manager *Manager, target Target) (POCResult, error) {
	if manager == nil {
		return POCResult{}, coded("TERMINAL_UNAVAILABLE", errors.New("terminal manager is required"))
	}
	opened, err := manager.Open(ctx, StartRequest{Target: target, Rows: pocInitialRows, Cols: pocInitialCols})
	if err != nil {
		return POCResult{}, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = manager.Close(opened.ID)
		}
	}()

	readContext, cancel := context.WithCancel(ctx)
	defer cancel()
	output := startOutputPump(readContext, manager, opened.ID)
	result := POCResult{Target: target}

	if err := manager.Write(opened.ID, []byte("printf 'NCP_P0_TERMINAL_READY\\n'\n")); err != nil {
		return POCResult{}, err
	}
	if err := waitForTerminalMarker(ctx, output, "NCP_P0_TERMINAL_READY"); err != nil {
		return POCResult{}, err
	}
	result.PTY = true

	if err := manager.Resize(opened.ID, pocResizeRows, pocResizeCols); err != nil {
		return POCResult{}, err
	}
	if err := manager.Write(opened.ID, []byte("stty size\n")); err != nil {
		return POCResult{}, err
	}
	if err := waitForTerminalMarker(ctx, output, fmt.Sprintf("%d %d", pocResizeRows, pocResizeCols)); err != nil {
		return POCResult{}, err
	}
	result.Resize = true

	if err := manager.Write(opened.ID, []byte("printf 'NCP_P0_INTERRUPT_READY\\n'; sleep 30\n")); err != nil {
		return POCResult{}, err
	}
	if err := waitForTerminalMarker(ctx, output, "NCP_P0_INTERRUPT_READY"); err != nil {
		return POCResult{}, err
	}
	if err := waitForTerminalSettle(ctx); err != nil {
		return POCResult{}, err
	}
	if err := manager.Write(opened.ID, []byte{3}); err != nil {
		return POCResult{}, err
	}
	// Interactive editors such as ble.sh redraw the prompt after SIGINT. Give
	// the PTY a short settle window before sending the next fixed command so
	// those bytes cannot be consumed by the interrupt/redraw transition.
	if err := waitForTerminalSettle(ctx); err != nil {
		return POCResult{}, err
	}
	if err := manager.Write(opened.ID, []byte("printf 'NCP_P0_CTRL_C_OK\\n'\n")); err != nil {
		return POCResult{}, err
	}
	if err := waitForTerminalMarker(ctx, output, "NCP_P0_CTRL_C_OK"); err != nil {
		return POCResult{}, err
	}
	result.CtrlC = true

	if err := manager.Close(opened.ID); err != nil {
		return POCResult{}, err
	}
	closed = true
	result.SessionTerminated = true
	return result, nil
}

type terminalReadResult struct {
	data []byte
	err  error
}

func startOutputPump(ctx context.Context, manager *Manager, sessionID string) <-chan terminalReadResult {
	results := make(chan terminalReadResult, 4)
	go func() {
		defer close(results)
		buffer := make([]byte, pocOutputChunk)
		for {
			read, err := manager.Read(ctx, sessionID, buffer)
			if read > 0 {
				select {
				case results <- terminalReadResult{data: append([]byte(nil), buffer[:read]...)}:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				select {
				case results <- terminalReadResult{err: err}:
				case <-ctx.Done():
				}
				return
			}
		}
	}()
	return results
}

func waitForTerminalMarker(ctx context.Context, results <-chan terminalReadResult, marker string) error {
	var observed bytes.Buffer
	for {
		select {
		case <-ctx.Done():
			return coded("TERMINAL_INITIALIZATION_TIMEOUT", ctx.Err())
		case result, ok := <-results:
			if !ok {
				return coded("TERMINAL_OUTPUT_CLOSED", io.EOF)
			}
			if len(result.data) > 0 {
				_, _ = observed.Write(result.data)
				if bytes.Contains(observed.Bytes(), []byte(marker)) {
					return nil
				}
			}
			if result.err != nil {
				return coded("TERMINAL_OUTPUT_FAILED", result.err)
			}
		}
	}
}

func waitForTerminalSettle(ctx context.Context) error {
	timer := time.NewTimer(pocSettleDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return coded("TERMINAL_INITIALIZATION_TIMEOUT", ctx.Err())
	case <-timer.C:
		return nil
	}
}
