package agentsocket

import (
	"context"
	"errors"
	"sync"

	"github.com/Kkwans/nas-control-plane/internal/terminal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type TerminalStream interface {
	Send(terminal.Message) error
	Recv() (terminal.Message, error)
	// Close 幂等地中止专用传输，可与 Send、Recv 并发调用；必须解除两向
	// 阻塞 I/O 并有界返回，不等待对端确认，也不要求先等待收发协程退出。
	Close() error
}

func OpenTerminal(ctx context.Context, socketPath string) (TerminalStream, error) {
	if err := ctx.Err(); err != nil {
		return nil, coded("AGENT_TERMINAL_CANCELED", err)
	}
	if socketPath == "" {
		return nil, coded("AGENT_RPC_TARGET_INVALID", errors.New("socket path is required"))
	}
	connection, err := grpc.NewClient("unix://"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, coded("AGENT_TERMINAL_CONNECTION_FAILED", err)
	}
	stream, err := NewAgentTerminalPOCServiceClient(connection).Open(ctx)
	if err != nil {
		_ = connection.Close()
		return nil, coded("AGENT_TERMINAL_CONNECTION_FAILED", err)
	}
	return &terminalClientStream{stream: stream, connection: connection}, nil
}

type terminalClientStream struct {
	stream     AgentTerminalPOCService_OpenClient
	connection *grpc.ClientConn
	closeOnce  sync.Once
	closeErr   error
}

func (s *terminalClientStream) Send(message terminal.Message) error {
	encoded, err := terminalMessageToStruct(message)
	if err != nil {
		return coded("AGENT_TERMINAL_MESSAGE_INVALID", err)
	}
	if err := s.stream.Send(encoded); err != nil {
		return coded("AGENT_TERMINAL_SEND_FAILED", err)
	}
	return nil
}

func (s *terminalClientStream) Recv() (terminal.Message, error) {
	encoded, err := s.stream.Recv()
	if err != nil {
		return terminal.Message{}, coded("AGENT_TERMINAL_RECEIVE_FAILED", err)
	}
	message, err := terminalMessageFromStruct(encoded)
	if err != nil {
		return terminal.Message{}, coded("AGENT_TERMINAL_RESPONSE_INVALID", err)
	}
	return message, nil
}

func (s *terminalClientStream) Close() error {
	// CloseSend 不允许与 SendMsg 并发；直接关闭本流独占的 ClientConn，
	// 同时解除阻塞的 Send/Recv，避免清理顺序依赖对端或收发协程。
	s.closeOnce.Do(func() { s.closeErr = s.connection.Close() })
	return s.closeErr
}
