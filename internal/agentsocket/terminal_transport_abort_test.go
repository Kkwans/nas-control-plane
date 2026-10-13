package agentsocket

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/terminal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"
)

type terminalAbortServer struct {
	opened chan struct{}
	done   chan struct{}
}

func (s *terminalAbortServer) Open(stream AgentTerminalPOCService_OpenServer) error {
	defer close(s.done)
	close(s.opened)
	// 不消费客户端输入，也不发送输出，让两个方向都只能由传输中止解除。
	<-stream.Context().Done()
	return stream.Context().Err()
}

type terminalAbortObservedClient struct {
	AgentTerminalPOCService_OpenClient
	sendAttempts chan struct{}
	recvEntered  chan struct{}
	closeSends   atomic.Int32
}

func (s *terminalAbortObservedClient) Send(frame *structpb.Struct) error {
	select {
	case s.sendAttempts <- struct{}{}:
	case <-s.Context().Done():
		return s.Context().Err()
	}
	return s.AgentTerminalPOCService_OpenClient.Send(frame)
}

func (s *terminalAbortObservedClient) Recv() (*structpb.Struct, error) {
	close(s.recvEntered)
	return s.AgentTerminalPOCService_OpenClient.Recv()
}

func (s *terminalAbortObservedClient) CloseSend() error {
	s.closeSends.Add(1)
	return s.AgentTerminalPOCService_OpenClient.CloseSend()
}

func TestTerminalClientCloseAbortsConcurrentSendRecv(t *testing.T) {
	listener := bufconn.Listen(64 * 1024)
	server := grpc.NewServer(grpc.StaticStreamWindowSize(64*1024), grpc.StaticConnWindowSize(64*1024))
	service := &terminalAbortServer{opened: make(chan struct{}), done: make(chan struct{})}
	RegisterAgentTerminalPOCServiceServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })

	connection, err := grpc.NewClient("passthrough:///terminal-abort-test",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := NewAgentTerminalPOCServiceClient(connection).Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	observed := &terminalAbortObservedClient{AgentTerminalPOCService_OpenClient: raw, sendAttempts: make(chan struct{}, 64), recvEntered: make(chan struct{})}
	stream := &terminalClientStream{stream: observed, connection: connection}
	awaitTerminalReceiver(t, service.opened)

	sendDone, recvDone := make(chan error, 1), make(chan error, 1)
	go func() {
		for {
			if err := stream.Send(terminal.Message{Type: terminal.MessageInput, Data: make([]byte, maxTerminalMessageBytes)}); err != nil {
				sendDone <- err
				return
			}
		}
	}()
	go func() { _, err := stream.Recv(); recvDone <- err }()
	awaitTerminalReceiver(t, observed.recvEntered)

	// 固定窗口且服务端不读，少量帧后 Send 必须因流控停住。
	attempts := 0
waitForBlockedSend:
	for {
		select {
		case <-observed.sendAttempts:
			attempts++
			if attempts >= cap(observed.sendAttempts) {
				t.Fatal("send did not stop at the fixed flow-control window")
			}
		case err := <-sendDone:
			t.Fatalf("send returned before transport abort: %v", err)
		case <-time.After(20 * time.Millisecond):
			if attempts == 0 {
				t.Fatal("send did not start")
			}
			break waitForBlockedSend
		}
	}
	select {
	case err := <-recvDone:
		t.Fatalf("receive returned before transport abort: %v", err)
	default:
	}

	const callers = 16
	closeResults := make(chan error, callers)
	var start sync.WaitGroup
	start.Add(1)
	for range callers {
		go func() { start.Wait(); closeResults <- stream.Close() }()
	}
	start.Done()
	for range callers {
		select {
		case err := <-closeResults:
			if err != nil {
				t.Fatalf("idempotent close: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("close waited for blocked send/receive or peer cooperation")
		}
	}
	for _, done := range []<-chan error{sendDone, recvDone} {
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("aborted I/O returned success")
			}
		case <-time.After(time.Second):
			t.Fatal("transport abort did not release blocked I/O")
		}
	}
	if got := observed.closeSends.Load(); got != 0 {
		t.Fatalf("CloseSend called %d times while Send could be active", got)
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("test relied on parent context cancellation: %v", err)
	}
	awaitTerminalReceiver(t, service.done)
}
