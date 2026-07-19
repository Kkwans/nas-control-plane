package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/terminal"
	"github.com/coder/websocket"
)

const (
	initialTerminalRows = 24
	initialTerminalCols = 80
	maxWebSocketInput   = 32 * 1024
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
	target, err := websocketTarget(request)
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
		Type:   terminal.MessageStart,
		Target: target,
		Rows:   initialTerminalRows,
		Cols:   initialTerminalCols,
	}); err != nil {
		_ = connection.Close(websocket.StatusInternalError, "终端会话无法创建")
		return
	}
	started, err := stream.Recv()
	if err != nil || started.Type != terminal.MessageStarted || started.SessionID == "" {
		_ = connection.Close(websocket.StatusInternalError, "终端会话无法创建")
		return
	}
	if err := writeTerminalControl(sessionContext, connection, terminalControl{Type: "started", SessionID: started.SessionID}); err != nil {
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

func websocketTarget(request *http.Request) (terminal.Target, error) {
	switch request.URL.Query().Get("target") {
	case string(terminal.TargetHost):
		return terminal.TargetHost, nil
	case string(terminal.TargetContainer):
		return terminal.TargetContainer, nil
	default:
		return "", errors.New("terminal target is invalid")
	}
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
	Type      string `json:"type"`
	Rows      int    `json:"rows,omitempty"`
	Cols      int    `json:"cols,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
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
