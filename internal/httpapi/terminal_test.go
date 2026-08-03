package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/terminal"
	"github.com/coder/websocket"
)

func TestTerminalWebSocketProxiesInputResizeAndTermination(t *testing.T) {
	stream := newFakeTerminalStream()
	handler := NewHandler(Config{
		TerminalPOCEnabled: true,
		Terminal:           fakeTerminalClient{stream: stream},
		RequestID:          func() string { return "req-terminal" },
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/terminal?target=host&rows=42&cols=132"
	connection, _, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		t.Fatalf("dial terminal websocket: %v", err)
	}
	defer connection.CloseNow()

	messageType, started, err := connection.Read(ctx)
	if err != nil {
		t.Fatalf("read started message: %v", err)
	}
	if messageType != websocket.MessageText ||
		!strings.Contains(string(started), `"type":"started"`) ||
		!strings.Contains(string(started), `"shell":"bash"`) ||
		!strings.Contains(string(started), `"enhancement":"blesh"`) ||
		!strings.Contains(string(started), `"capabilities":`) ||
		!strings.Contains(string(started), `"readline":true`) ||
		!strings.Contains(string(started), `"rows":42`) ||
		!strings.Contains(string(started), `"cols":132`) {
		t.Fatalf("started message = %q", started)
	}
	if !stream.hasStart(42, 132) {
		t.Fatal("initial terminal dimensions were not proxied")
	}

	if err := connection.Write(ctx, websocket.MessageBinary, []byte("printf ready\\n")); err != nil {
		t.Fatalf("write terminal input: %v", err)
	}
	messageType, output, err := connection.Read(ctx)
	if err != nil {
		t.Fatalf("read terminal output: %v", err)
	}
	if messageType != websocket.MessageBinary || string(output) != "NCP_P0_TERMINAL_READY\\n" {
		t.Fatalf("terminal output = %q", output)
	}

	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","rows":34,"cols":120}`)); err != nil {
		t.Fatalf("write resize: %v", err)
	}
	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"type":"close"}`)); err != nil {
		t.Fatalf("write close: %v", err)
	}

	select {
	case <-stream.closed:
	case <-ctx.Done():
		t.Fatal("terminal stream was not closed")
	}
	if !stream.hasMessage("input", []byte("printf ready\\n")) {
		t.Fatal("terminal input was not proxied")
	}
	if !stream.hasResize(34, 120) {
		t.Fatal("terminal resize was not proxied")
	}
}

func TestTerminalWebSocketReclaimsAgentStreamOnBrowserDisconnect(t *testing.T) {
	stream := newFakeTerminalStream()
	handler := NewHandler(Config{
		TerminalPOCEnabled: true,
		Terminal:           fakeTerminalClient{stream: stream},
		TerminalTimeout:    3 * time.Second,
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/terminal?target=host"
	connection, _, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		t.Fatalf("dial terminal websocket: %v", err)
	}
	if _, _, err := connection.Read(ctx); err != nil {
		t.Fatalf("read started message: %v", err)
	}
	connection.CloseNow()

	select {
	case <-stream.closed:
	case <-ctx.Done():
		t.Fatal("Agent terminal stream was not closed after browser disconnect")
	}
}

func TestTerminalWebSocketIsAbsentUnlessExplicitlyEnabled(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(Config{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ws/terminal?target=host", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

type fakeTerminalClient struct {
	stream *fakeTerminalStream
}

func (f fakeTerminalClient) Open(context.Context, string) (TerminalStream, error) {
	return f.stream, nil
}

type fakeTerminalStream struct {
	mu        sync.Mutex
	sent      []terminal.Message
	received  chan terminal.Message
	closed    chan struct{}
	closeOnce sync.Once
}

func newFakeTerminalStream() *fakeTerminalStream {
	return &fakeTerminalStream{
		received: make(chan terminal.Message, 4),
		closed:   make(chan struct{}),
	}
}

func (f *fakeTerminalStream) Send(message terminal.Message) error {
	f.mu.Lock()
	f.sent = append(f.sent, message)
	f.mu.Unlock()

	switch message.Type {
	case terminal.MessageStart:
		f.received <- terminal.Message{
			Type:        terminal.MessageStarted,
			SessionID:   "term-test",
			Shell:       "bash",
			Enhancement: "blesh",
			Capabilities: terminal.SessionCapabilities{
				Resize:         true,
				Readline:       true,
				BracketedPaste: true,
				MultilinePaste: true,
				ANSIColors:     true,
			},
			Rows: message.Rows,
			Cols: message.Cols,
		}
	case terminal.MessageInput:
		f.received <- terminal.Message{Type: terminal.MessageOutput, Data: []byte("NCP_P0_TERMINAL_READY\\n")}
	}
	return nil
}

func (f *fakeTerminalStream) Recv() (terminal.Message, error) {
	message, ok := <-f.received
	if !ok {
		return terminal.Message{}, ioEOF
	}
	return message, nil
}

func (f *fakeTerminalStream) Close() error {
	f.closeOnce.Do(func() {
		close(f.closed)
		close(f.received)
	})
	return nil
}

func (f *fakeTerminalStream) hasStart(rows, cols uint16) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, message := range f.sent {
		if message.Type == terminal.MessageStart && message.Rows == rows && message.Cols == cols {
			return true
		}
	}
	return false
}

func (f *fakeTerminalStream) hasMessage(messageType terminal.MessageType, data []byte) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, message := range f.sent {
		if message.Type == messageType && string(message.Data) == string(data) {
			return true
		}
	}
	return false
}

func (f *fakeTerminalStream) hasResize(rows, cols uint16) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, message := range f.sent {
		if message.Type == terminal.MessageResize && message.Rows == rows && message.Cols == cols {
			return true
		}
	}
	return false
}

var ioEOF = errors.New("terminal stream closed")
