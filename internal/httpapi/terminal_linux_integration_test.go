//go:build linux

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/terminal"
	"github.com/coder/websocket"
)

func TestTerminalWebSocketBridgesUnixAgentAndHostPTY(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// Unix socket paths are limited to roughly 108 bytes on Linux. Keep the
	// integration socket short even when Go uses a long GOTMPDIR build path.
	socketDirectory, err := os.MkdirTemp("/tmp", "ncp-ws-")
	if err != nil {
		t.Fatalf("create terminal socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDirectory) })
	socketPath := filepath.Join(socketDirectory, "agent.sock")
	manager := terminal.NewManager(terminal.NewHostStarter(), nil, 1)
	agentErrors := make(chan error, 1)
	go func() {
		agentErrors <- agentsocket.Serve(ctx, agentsocket.SocketConfig{
			SocketPath:        socketPath,
			EnableTerminalPOC: true,
			TerminalManager:   manager,
		})
	}()
	agentStopped := false
	t.Cleanup(func() {
		cancel()
		if agentStopped {
			return
		}
		select {
		case <-agentErrors:
		case <-time.After(time.Second):
			t.Error("terminal Agent did not stop")
		}
	})
	if err := waitForUnixSocket(ctx, socketPath, agentErrors); err != nil {
		agentStopped = true
		t.Fatalf("wait for agent socket: %v", err)
	}

	server := httptest.NewServer(NewHandler(Config{
		AgentSocketPath:    socketPath,
		TerminalPOCEnabled: true,
	}))
	defer server.Close()
	connection := dialHostTerminal(t, ctx, server.URL)

	readStartedControl(t, ctx, connection)
	writeTerminalBinary(t, ctx, connection, []byte("printf 'NCP_P0_WS_READY\\n'\n"))
	readTerminalMarker(t, ctx, connection, "NCP_P0_WS_READY")
	sendTerminalControl(t, ctx, connection, terminalControl{Type: "resize", Rows: 34, Cols: 120})
	writeTerminalBinary(t, ctx, connection, []byte("stty size\n"))
	readTerminalMarker(t, ctx, connection, "34 120")
	writeTerminalBinary(t, ctx, connection, []byte("printf 'NCP_P0_WS_INTERRUPT_READY\\n'; sleep 30\n"))
	readTerminalMarker(t, ctx, connection, "NCP_P0_WS_INTERRUPT_READY")
	time.Sleep(250 * time.Millisecond)
	writeTerminalBinary(t, ctx, connection, []byte{3})
	// ble.sh/readline redraws the prompt after SIGINT. Waiting for that redraw
	// keeps the fixed verification command from being consumed by the transition.
	time.Sleep(250 * time.Millisecond)
	writeTerminalBinary(t, ctx, connection, []byte("printf 'NCP_P0_WS_CTRL_C_OK\\n'\n"))
	readTerminalMarker(t, ctx, connection, "NCP_P0_WS_CTRL_C_OK")
	if err := connection.Close(websocket.StatusNormalClosure, "done"); err != nil {
		t.Fatalf("close terminal websocket: %v", err)
	}

	secondConnection := waitForNextTerminalSession(t, ctx, server.URL)
	defer secondConnection.CloseNow()
	readStartedControl(t, ctx, secondConnection)
}

func waitForUnixSocket(ctx context.Context, socketPath string, agentErrors <-chan error) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(socketPath); err == nil {
			return nil
		}
		select {
		case err := <-agentErrors:
			if err == nil {
				return errors.New("terminal Agent exited before creating its socket")
			}
			return fmt.Errorf("terminal Agent exited before creating its socket: %w: %v", err, errors.Unwrap(err))
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func dialHostTerminal(t *testing.T, ctx context.Context, serverURL string) *websocket.Conn {
	t.Helper()
	endpoint := "ws" + strings.TrimPrefix(serverURL, "http") + "/ws/terminal?target=host"
	connection, _, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		t.Fatalf("dial terminal websocket: %v", err)
	}
	return connection
}

func waitForNextTerminalSession(t *testing.T, ctx context.Context, serverURL string) *websocket.Conn {
	t.Helper()
	for {
		connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(serverURL, "http")+"/ws/terminal?target=host", nil)
		if err == nil {
			return connection
		}
		select {
		case <-ctx.Done():
			t.Fatalf("previous terminal session was not terminated: %v", ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func readStartedControl(t *testing.T, ctx context.Context, connection *websocket.Conn) {
	t.Helper()
	messageType, data, err := connection.Read(ctx)
	if err != nil {
		t.Fatalf("read terminal started control: %v", err)
	}
	var control terminalControl
	if messageType != websocket.MessageText || json.Unmarshal(data, &control) != nil || control.Type != "started" || control.SessionID == "" {
		t.Fatalf("started control = %q", data)
	}
}

func writeTerminalBinary(t *testing.T, ctx context.Context, connection *websocket.Conn, data []byte) {
	t.Helper()
	if err := connection.Write(ctx, websocket.MessageBinary, data); err != nil {
		t.Fatalf("write terminal binary: %v", err)
	}
}

func sendTerminalControl(t *testing.T, ctx context.Context, connection *websocket.Conn, control terminalControl) {
	t.Helper()
	encoded, err := json.Marshal(control)
	if err != nil {
		t.Fatalf("encode terminal control: %v", err)
	}
	if err := connection.Write(ctx, websocket.MessageText, encoded); err != nil {
		t.Fatalf("write terminal control: %v", err)
	}
}

func readTerminalMarker(t *testing.T, ctx context.Context, connection *websocket.Conn, marker string) {
	t.Helper()
	var observed bytes.Buffer
	for {
		messageType, data, err := connection.Read(ctx)
		if err != nil {
			t.Fatalf("read terminal output: %v", err)
		}
		if messageType != websocket.MessageBinary {
			continue
		}
		_, _ = observed.Write(data)
		if bytes.Contains(observed.Bytes(), []byte(marker)) {
			return
		}
		if observed.Len() > 64*1024 {
			t.Fatalf("terminal output did not contain marker %q", marker)
		}
	}
}
