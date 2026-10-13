package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/terminal"
	"github.com/coder/websocket"
	grpcstatus "google.golang.org/grpc/status"
)

const (
	maxWebSocketInput      = 32 * 1024
	defaultTerminalRows    = 24
	defaultTerminalColumns = 80
)

type TerminalClient interface {
	Open(context.Context, string) (TerminalStream, error)
}

type TerminalStream interface {
	Send(terminal.Message) error
	Recv() (terminal.Message, error)
	Close() error
}

type socketTerminalClient struct{}

func (socketTerminalClient) Open(ctx context.Context, socketPath string) (TerminalStream, error) {
	return agentsocket.OpenTerminal(ctx, socketPath)
}

func (api *handler) terminalWebSocket(response http.ResponseWriter, request *http.Request) {
	target, containerID, rows, cols, err := websocketTarget(request)
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "TERMINAL_TARGET_REJECTED", "终端目标不在当前验证范围内。")
		return
	}
	if api.terminal == nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "TERMINAL_UNAVAILABLE", "终端服务暂不可用，请确认 Root Agent 正常运行。")
		return
	}

	if request.Context().Err() != nil {
		return
	}
	connection, err := websocket.Accept(response, request, nil)
	if err != nil {
		return
	}
	connection.SetReadLimit(maxWebSocketInput)
	sessionContext, cancel := context.WithTimeout(request.Context(), api.terminalTimeout)
	defer cancel()
	runTerminalProxy(sessionContext, connection, api.terminal, api.agentSocketPath, terminal.Message{
		Type: terminal.MessageStart, Target: target, ContainerID: containerID, Rows: rows, Cols: cols,
	})
}

// CloseNow 必须中止阻塞的 Read/Write；Close 必须可与 Send/Recv 并发且中止二者。
// 真实实现关闭各自独占的传输，不等待对端 close 握手。
type terminalConnection interface {
	terminalInputReader
	terminalOutputWriter
	CloseNow() error
}

type terminalOutputWriter interface {
	Write(context.Context, websocket.MessageType, []byte) error
}

func runTerminalProxy(parent context.Context, connection terminalConnection, client TerminalClient, socketPath string, start terminal.Message) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	browserClosed := make(chan struct{})
	go func() {
		defer close(browserClosed)
		<-ctx.Done()
		_ = connection.CloseNow()
	}()
	defer func() {
		cancel()
		<-browserClosed
	}()

	if ctx.Err() != nil {
		return
	}
	stream, err := client.Open(ctx, socketPath)
	if err != nil {
		_ = writeTerminalError(ctx, connection, err, "TERMINAL_UNAVAILABLE")
		return
	}
	agentClosed := make(chan struct{})
	go func() {
		defer close(agentClosed)
		<-ctx.Done()
		_ = stream.Close()
	}()
	defer func() {
		cancel()
		<-agentClosed
	}()

	if ctx.Err() != nil {
		return
	}
	if err := stream.Send(start); err != nil {
		_ = writeTerminalError(ctx, connection, err, "TERMINAL_INITIALIZATION_FAILED")
		return
	}
	if ctx.Err() != nil {
		return
	}
	started, err := stream.Recv()
	if ctx.Err() != nil {
		return
	}
	if err != nil || started.Type != terminal.MessageStarted || started.SessionID == "" {
		if err == nil {
			err = errors.New("terminal started response is invalid")
		}
		_ = writeTerminalError(ctx, connection, err, "TERMINAL_INITIALIZATION_FAILED")
		return
	}
	if err := writeTerminalControl(ctx, connection, terminalControl{
		Type: "started", SessionID: started.SessionID, Shell: started.Shell,
		Enhancement: started.Enhancement, Reason: started.Reason,
		Capabilities: started.Capabilities, Rows: int(started.Rows), Cols: int(started.Cols),
	}); err != nil {
		return
	}

	outputDone := make(chan error, 1)
	outputExited := make(chan struct{})
	go func() {
		defer close(outputExited)
		proxyTerminalOutput(ctx, connection, stream, outputDone)
	}()
	input := make(chan terminalInput, 1)
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		readTerminalInput(ctx, connection, input)
	}()
	defer func() {
		// 先取消并关闭两侧传输，再等待两个泵；否则 Recv/Write 可能永远无法退出。
		cancel()
		<-browserClosed
		<-agentClosed
		<-inputDone
		<-outputExited
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-outputDone:
			return
		case incoming, ok := <-input:
			// Done 与缓冲输入同时就绪时，select 可能先取输入，必须再次检查。
			if ctx.Err() != nil || !ok || incoming.err != nil {
				return
			}
			if err := stream.Send(incoming.message); err != nil {
				_ = writeTerminalError(ctx, connection, err, "TERMINAL_SESSION_FAILED")
				return
			}
			if incoming.closeRequested {
				return
			}
		}
	}
}

func websocketTarget(request *http.Request) (terminal.Target, string, uint16, uint16, error) {
	rows, cols, err := terminalDimensionsFromQuery(request)
	if err != nil {
		return "", "", 0, 0, err
	}
	switch request.URL.Query().Get("target") {
	case string(terminal.TargetHost):
		return terminal.TargetHost, "", rows, cols, nil
	case string(terminal.TargetContainer):
		containerID := request.URL.Query().Get("containerId")
		if containerID == "" {
			return "", "", 0, 0, errors.New("container id is required")
		}
		return terminal.TargetContainer, containerID, rows, cols, nil
	default:
		return "", "", 0, 0, errors.New("terminal target is invalid")
	}
}

func terminalDimensionsFromQuery(request *http.Request) (uint16, uint16, error) {
	rowsValue := request.URL.Query().Get("rows")
	colsValue := request.URL.Query().Get("cols")
	if rowsValue == "" && colsValue == "" {
		return defaultTerminalRows, defaultTerminalColumns, nil
	}
	if rowsValue == "" || colsValue == "" {
		return 0, 0, errors.New("terminal rows and columns must be provided together")
	}
	rows, err := strconv.Atoi(rowsValue)
	if err != nil {
		return 0, 0, errors.New("terminal rows are invalid")
	}
	cols, err := strconv.Atoi(colsValue)
	if err != nil {
		return 0, 0, errors.New("terminal columns are invalid")
	}
	return terminalDimensions(rows, cols)
}

type terminalInput struct {
	message        terminal.Message
	closeRequested bool
	err            error
}

type terminalInputReader interface {
	Read(context.Context) (websocket.MessageType, []byte, error)
}

func sendTerminalInput(ctx context.Context, destination chan<- terminalInput, input terminalInput) bool {
	if ctx.Err() != nil {
		return false
	}
	select {
	case destination <- input:
		return true
	case <-ctx.Done():
		return false
	}
}

func readTerminalInput(ctx context.Context, connection terminalInputReader, destination chan<- terminalInput) {
	defer close(destination)
	for {
		if ctx.Err() != nil {
			return
		}
		messageType, data, err := connection.Read(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			sendTerminalInput(ctx, destination, terminalInput{err: err})
			return
		}
		switch messageType {
		case websocket.MessageBinary:
			if len(data) == 0 || len(data) > maxWebSocketInput {
				sendTerminalInput(ctx, destination, terminalInput{err: errors.New("terminal input size is invalid")})
				return
			}
			if !sendTerminalInput(ctx, destination, terminalInput{message: terminal.Message{Type: terminal.MessageInput, Data: data}}) {
				return
			}
		case websocket.MessageText:
			control, err := decodeTerminalControl(data)
			if err != nil {
				sendTerminalInput(ctx, destination, terminalInput{err: err})
				return
			}
			switch control.Type {
			case "resize":
				rows, cols, err := terminalDimensions(control.Rows, control.Cols)
				if err != nil {
					sendTerminalInput(ctx, destination, terminalInput{err: err})
					return
				}
				if !sendTerminalInput(ctx, destination, terminalInput{message: terminal.Message{Type: terminal.MessageResize, Rows: rows, Cols: cols}}) {
					return
				}
			case "close":
				sendTerminalInput(ctx, destination, terminalInput{message: terminal.Message{Type: terminal.MessageClose}, closeRequested: true})
				return
			default:
				sendTerminalInput(ctx, destination, terminalInput{err: errors.New("terminal control type is invalid")})
				return
			}
		default:
			sendTerminalInput(ctx, destination, terminalInput{err: errors.New("websocket message type is invalid")})
			return
		}
	}
}

func proxyTerminalOutput(ctx context.Context, connection terminalOutputWriter, stream TerminalStream, done chan<- error) {
	for {
		if ctx.Err() != nil {
			done <- ctx.Err()
			return
		}
		message, err := stream.Recv()
		if ctx.Err() != nil {
			done <- ctx.Err()
			return
		}
		if err != nil {
			_ = writeTerminalError(ctx, connection, err, "TERMINAL_SESSION_FAILED")
			done <- err
			return
		}
		switch message.Type {
		case terminal.MessageOutput:
			if err := connection.Write(ctx, websocket.MessageBinary, message.Data); err != nil {
				done <- err
				return
			}
		case terminal.MessageClosed:
			if err := writeTerminalControl(ctx, connection, terminalControl{Type: "closed", SessionID: message.SessionID}); err != nil {
				done <- err
				return
			}
			done <- nil
			return
		default:
			err := errors.New("terminal response type is invalid")
			_ = writeTerminalError(ctx, connection, err, "TERMINAL_SESSION_FAILED")
			done <- err
			return
		}
	}
}

type terminalControl struct {
	Type         string                       `json:"type"`
	Rows         int                          `json:"rows,omitempty"`
	Cols         int                          `json:"cols,omitempty"`
	SessionID    string                       `json:"sessionId,omitempty"`
	Shell        string                       `json:"shell,omitempty"`
	Enhancement  string                       `json:"enhancement,omitempty"`
	Reason       string                       `json:"reason,omitempty"`
	Code         string                       `json:"code,omitempty"`
	Message      string                       `json:"message,omitempty"`
	Capabilities terminal.SessionCapabilities `json:"capabilities,omitempty"`
}

func decodeTerminalControl(data []byte) (terminalControl, error) {
	var control terminalControl
	if err := json.Unmarshal(data, &control); err != nil {
		return terminalControl{}, err
	}
	return control, nil
}

func writeTerminalControl(ctx context.Context, connection terminalOutputWriter, control terminalControl) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	encoded, err := json.Marshal(control)
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageText, encoded)
}

func writeTerminalError(ctx context.Context, connection terminalOutputWriter, err error, fallbackCode string) error {
	code := terminalErrorCode(err, fallbackCode)
	return writeTerminalControl(ctx, connection, terminalControl{Type: "error", Code: code, Message: terminalErrorMessage(code)})
}

func terminalErrorCode(err error, fallback string) string {
	code := grpcstatus.Convert(err).Message()
	if strings.HasPrefix(code, "TERMINAL_") {
		return terminal.NormalizeErrorCode(code)
	}
	text := err.Error()
	for _, candidate := range []string{
		"TERMINAL_UNAVAILABLE", "TERMINAL_INITIALIZATION_FAILED", "TERMINAL_INITIALIZATION_TIMEOUT",
		"TERMINAL_TARGET_UNAVAILABLE", "TERMINAL_SESSION_FAILED", "TERMINAL_INPUT_INVALID",
		"TERMINAL_OUTPUT_CLOSED", "TERMINAL_OUTPUT_FAILED", "TERMINAL_STREAM_FAILED",
		"TERMINAL_POC_UNAVAILABLE", "TERMINAL_POC_INITIALIZATION_FAILED", "TERMINAL_POC_TIMEOUT",
		"TERMINAL_POC_OUTPUT_CLOSED", "TERMINAL_POC_OUTPUT_FAILED",
	} {
		if strings.Contains(text, candidate) {
			return terminal.NormalizeErrorCode(candidate)
		}
	}
	return terminal.NormalizeErrorCode(fallback)
}

func terminalErrorMessage(code string) string {
	switch code {
	case "TERMINAL_UNAVAILABLE":
		return "终端服务暂不可用，请确认 Root Agent 正常运行。"
	case "TERMINAL_INITIALIZATION_TIMEOUT":
		return "终端初始化超时，请稍后重试。"
	case "TERMINAL_INITIALIZATION_FAILED":
		return "终端会话初始化失败，请检查 Agent 终端能力。"
	case "TERMINAL_TARGET_UNAVAILABLE":
		return "当前终端目标不可用，请确认主机或容器状态。"
	case "TERMINAL_INPUT_INVALID":
		return "终端输入无效或超出允许范围。"
	default:
		return "终端会话异常结束，请重新连接。"
	}
}

func terminalDimensions(rows, cols int) (uint16, uint16, error) {
	if rows <= 0 || cols <= 0 || rows > 200 || cols > 400 {
		return 0, 0, errors.New("terminal dimensions are invalid")
	}
	return uint16(rows), uint16(cols), nil
}
