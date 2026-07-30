package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/terminal"
	"github.com/coder/websocket"
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
		api.writeError(response, request, http.StatusServiceUnavailable, "TERMINAL_POC_UNAVAILABLE", "终端验证通道暂不可用。")
		return
	}

	connection, err := websocket.Accept(response, request, nil)
	if err != nil {
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(maxWebSocketInput)

	sessionContext, cancel := context.WithTimeout(context.Background(), api.terminalTimeout)
	defer cancel()
	stream, err := api.terminal.Open(sessionContext, api.agentSocketPath)
	if err != nil {
		_ = connection.Close(websocket.StatusInternalError, "终端验证通道不可用")
		return
	}
	defer stream.Close()

	if err := stream.Send(terminal.Message{
		Type:        terminal.MessageStart,
		Target:      target,
		ContainerID: containerID,
		Rows:        rows,
		Cols:        cols,
	}); err != nil {
		_ = connection.Close(websocket.StatusInternalError, "终端会话无法创建")
		return
	}
	started, err := stream.Recv()
	if err != nil || started.Type != terminal.MessageStarted || started.SessionID == "" {
		_ = connection.Close(websocket.StatusInternalError, "终端会话无法创建")
		return
	}
	if err := writeTerminalControl(sessionContext, connection, terminalControl{
		Type:        "started",
		SessionID:   started.SessionID,
		Shell:       started.Shell,
		Enhancement: started.Enhancement,
		Reason:      started.Reason,
		Rows:        int(started.Rows),
		Cols:        int(started.Cols),
	}); err != nil {
		return
	}

	outputDone := make(chan error, 1)
	go proxyTerminalOutput(sessionContext, connection, stream, outputDone)

	input := make(chan terminalInput, 1)
	go readTerminalInput(sessionContext, connection, input)

	for {
		select {
		case <-sessionContext.Done():
			return
		case <-outputDone:
			return
		case incoming, ok := <-input:
			if !ok || incoming.err != nil {
				return
			}
			if err := stream.Send(incoming.message); err != nil {
				_ = connection.Close(websocket.StatusInternalError, "终端输入无法转发")
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

func readTerminalInput(ctx context.Context, connection *websocket.Conn, destination chan<- terminalInput) {
	defer close(destination)
	for {
		messageType, data, err := connection.Read(ctx)
		if err != nil {
			destination <- terminalInput{err: err}
			return
		}
		switch messageType {
		case websocket.MessageBinary:
			if len(data) == 0 || len(data) > maxWebSocketInput {
				destination <- terminalInput{err: errors.New("terminal input size is invalid")}
				return
			}
			destination <- terminalInput{message: terminal.Message{Type: terminal.MessageInput, Data: data}}
		case websocket.MessageText:
			control, err := decodeTerminalControl(data)
			if err != nil {
				destination <- terminalInput{err: err}
				return
			}
			switch control.Type {
			case "resize":
				rows, cols, err := terminalDimensions(control.Rows, control.Cols)
				if err != nil {
					destination <- terminalInput{err: err}
					return
				}
				destination <- terminalInput{message: terminal.Message{Type: terminal.MessageResize, Rows: rows, Cols: cols}}
			case "close":
				destination <- terminalInput{message: terminal.Message{Type: terminal.MessageClose}, closeRequested: true}
				return
			default:
				destination <- terminalInput{err: errors.New("terminal control type is invalid")}
				return
			}
		default:
			destination <- terminalInput{err: errors.New("websocket message type is invalid")}
			return
		}
	}
}

func proxyTerminalOutput(ctx context.Context, connection *websocket.Conn, stream TerminalStream, done chan<- error) {
	for {
		message, err := stream.Recv()
		if err != nil {
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
			done <- errors.New("terminal response type is invalid")
			return
		}
	}
}

type terminalControl struct {
	Type        string `json:"type"`
	Rows        int    `json:"rows,omitempty"`
	Cols        int    `json:"cols,omitempty"`
	SessionID   string `json:"sessionId,omitempty"`
	Shell       string `json:"shell,omitempty"`
	Enhancement string `json:"enhancement,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

func decodeTerminalControl(data []byte) (terminalControl, error) {
	var control terminalControl
	if err := json.Unmarshal(data, &control); err != nil {
		return terminalControl{}, err
	}
	return control, nil
}

func writeTerminalControl(ctx context.Context, connection *websocket.Conn, control terminalControl) error {
	encoded, err := json.Marshal(control)
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageText, encoded)
}

func terminalDimensions(rows, cols int) (uint16, uint16, error) {
	if rows <= 0 || cols <= 0 || rows > 200 || cols > 400 {
		return 0, 0, errors.New("terminal dimensions are invalid")
	}
	return uint16(rows), uint16(cols), nil
}
