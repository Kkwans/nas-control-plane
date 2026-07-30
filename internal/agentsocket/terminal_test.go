package agentsocket

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/terminal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestTerminalPOCServiceRelaysInputResizeAndCloseOverGRPC(t *testing.T) {
	session := newFakeTerminalSession()
	manager := terminal.NewManager(&fakeTerminalStarter{session: session}, &fakeTerminalStarter{}, 1)
	connection := newTerminalPOCTestConnection(t, manager)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := NewAgentTerminalPOCServiceClient(connection).Open(ctx)
	if err != nil {
		t.Fatalf("open terminal stream: %v", err)
	}
	if err := stream.Send(mustTerminalStruct(t, terminal.Message{Type: terminal.MessageStart, Target: terminal.TargetHost, Rows: 24, Cols: 80})); err != nil {
		t.Fatalf("send start: %v", err)
	}
	started, err := stream.Recv()
	if err != nil {
		t.Fatalf("receive started: %v", err)
	}
	startedMessage, err := terminalMessageFromStruct(started)
	if err != nil {
		t.Fatalf("decode started: %v", err)
	}
	if startedMessage.Type != terminal.MessageStarted || startedMessage.SessionID == "" {
		t.Fatalf("started message = %#v", startedMessage)
	}

	if err := stream.Send(mustTerminalStruct(t, terminal.Message{Type: terminal.MessageInput, Data: []byte("printf ready\\n")})); err != nil {
		t.Fatalf("send input: %v", err)
	}
	output, err := stream.Recv()
	if err != nil {
		t.Fatalf("receive output: %v", err)
	}
	outputMessage, err := terminalMessageFromStruct(output)
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if outputMessage.Type != terminal.MessageOutput || string(outputMessage.Data) != "NCP_P0_TERMINAL_READY\\n" {
		t.Fatalf("output = %#v", outputMessage)
	}

	if err := stream.Send(mustTerminalStruct(t, terminal.Message{Type: terminal.MessageResize, Rows: 34, Cols: 120})); err != nil {
		t.Fatalf("send resize: %v", err)
	}
	if err := stream.Send(mustTerminalStruct(t, terminal.Message{Type: terminal.MessageClose})); err != nil {
		t.Fatalf("send close: %v", err)
	}
	closed, err := stream.Recv()
	if err != nil {
		t.Fatalf("receive closed: %v", err)
	}
	closedMessage, err := terminalMessageFromStruct(closed)
	if err != nil {
		t.Fatalf("decode closed: %v", err)
	}
	if closedMessage.Type != terminal.MessageClosed || closedMessage.SessionID != startedMessage.SessionID {
		t.Fatalf("closed message = %#v", closedMessage)
	}
	if session.rows != 34 || session.cols != 120 || !session.closed {
		t.Fatalf("session state rows=%d cols=%d closed=%t", session.rows, session.cols, session.closed)
	}
}

func TestTerminalPOCServiceRejectsCommandBearingStartMessage(t *testing.T) {
	manager := terminal.NewManager(&fakeTerminalStarter{session: newFakeTerminalSession()}, &fakeTerminalStarter{}, 1)
	connection := newTerminalPOCTestConnection(t, manager)
	stream, err := NewAgentTerminalPOCServiceClient(connection).Open(context.Background())
	if err != nil {
		t.Fatalf("open terminal stream: %v", err)
	}
	request, err := structpb.NewStruct(map[string]any{
		"type":    "start",
		"target":  "host",
		"rows":    24,
		"cols":    80,
		"command": "id",
	})
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if err := stream.Send(request); err != nil {
		t.Fatalf("send request: %v", err)
	}
	_, err = stream.Recv()
	if grpcstatus.Code(err) != codes.InvalidArgument {
		t.Fatalf("status = %s, error = %v", grpcstatus.Code(err), err)
	}
}

func TestTerminalStartedMessagePreservesSessionMetadata(t *testing.T) {
	t.Parallel()

	encoded := mustTerminalStruct(t, terminal.Message{
		Type:        terminal.MessageStarted,
		SessionID:   "terminal-session",
		Shell:       "bash",
		Enhancement: "blesh",
		Reason:      "ready",
		Rows:        42,
		Cols:        132,
	})
	decoded, err := terminalMessageFromStruct(encoded)
	if err != nil {
		t.Fatalf("decode started message: %v", err)
	}
	if decoded.Shell != "bash" || decoded.Enhancement != "blesh" || decoded.Reason != "ready" || decoded.Rows != 42 || decoded.Cols != 132 {
		t.Fatalf("started metadata = %#v", decoded)
	}
}

func newTerminalPOCTestConnection(t *testing.T, manager *terminal.Manager) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterAgentTerminalPOCServiceServer(server, newTerminalPOCService(manager))
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	connection, err := grpc.DialContext(context.Background(), "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial terminal service: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}

func mustTerminalStruct(t *testing.T, message terminal.Message) *structpb.Struct {
	t.Helper()
	encoded, err := terminalMessageToStruct(message)
	if err != nil {
		t.Fatalf("encode terminal message: %v", err)
	}
	return encoded
}

type fakeTerminalStarter struct {
	session terminal.Session
}

func (f *fakeTerminalStarter) Start(context.Context, terminal.StartRequest) (terminal.Session, error) {
	if f.session == nil {
		return nil, errors.New("session is required")
	}
	return f.session, nil
}

type fakeTerminalSession struct {
	mu      sync.Mutex
	outputs chan []byte
	closedC chan struct{}
	rows    uint16
	cols    uint16
	closed  bool
	once    sync.Once
}

func newFakeTerminalSession() *fakeTerminalSession {
	return &fakeTerminalSession{outputs: make(chan []byte, 1), closedC: make(chan struct{})}
}

func (f *fakeTerminalSession) Read(ctx context.Context, output []byte) (int, error) {
	select {
	case data := <-f.outputs:
		return copy(output, data), nil
	case <-f.closedC:
		return 0, io.EOF
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (f *fakeTerminalSession) Write(input []byte) (int, error) {
	if string(input) != "printf ready\\n" {
		return 0, errors.New("unexpected terminal input")
	}
	f.outputs <- []byte("NCP_P0_TERMINAL_READY\\n")
	return len(input), nil
}

func (f *fakeTerminalSession) Resize(rows, cols uint16) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = rows
	f.cols = cols
	return nil
}

func (f *fakeTerminalSession) Close() error {
	f.once.Do(func() {
		f.mu.Lock()
		f.closed = true
		f.mu.Unlock()
		close(f.closedC)
	})
	return nil
}
