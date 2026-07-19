//go:build linux

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
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
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	socketPath := filepath.Join(t.TempDir(), "agent.sock")
	manager := terminal.NewManager(terminal.NewHostStarter(), nil, 1)
	agentErrors := make(chan error, 1)
	go func() {
		agentErrors <- agentsocket.Serve(ctx, agentsocket.SocketConfig{
			SocketPath:        socketPath,
			EnableTerminalPOC: true,
			TerminalManager:   manager,
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-agentErrors:
		case <-time.After(time.Second):
			t.Error("terminal Agent did not stop")
		}
	})
	waitForUnixSocket(t, ctx, socketPath)

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
	writeTerminalBinary(t, ctx, connection, []byte("sleep 30\n"))
	time.Sleep(100 * time.Millisecond)
	writeTerminalBinary(t, ctx, connection, []byte{3})
	writeTerminalBinary(t, ctx, connection, []byte("printf 'NCP_P0_WS_CTRL_C_OK\\n'\n"))
	readTerminalMarker(t, ctx, connection, "NCP_P0_WS_CTRL_C_OK")
	if err := connection.Close(websocket.StatusNormalClosure, "done"); err != nil {
		t.Fatalf("close terminal websocket: %v", err)
	}

	secondConnection := waitForNextTerminalSession(t, ctx, server.URL)
	defer secondConnection.CloseNow()
	readStartedControl(t, ctx, secondConnection)
}

func waitForUnixSocket(t *testing.T, ctx context.Context, socketPath string) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(socketPath); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for agent socket: %v", ctx.Err())
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
