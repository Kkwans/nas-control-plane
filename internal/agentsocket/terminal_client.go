package agentsocket

import (
	"context"
	"errors"

	"github.com/Kkwans/nas-control-plane/internal/terminal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type TerminalStream interface {
	Send(terminal.Message) error
	Recv() (terminal.Message, error)
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
	closeErr := s.stream.CloseSend()
	connectionErr := s.connection.Close()
	if closeErr != nil {
		return closeErr
	}
	return connectionErr
}
