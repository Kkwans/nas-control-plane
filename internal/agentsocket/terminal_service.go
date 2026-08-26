package agentsocket

import (
	"context"
	"errors"
	"io"

	"github.com/Kkwans/nas-control-plane/internal/terminal"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

const terminalOutputChunkSize = 16 * 1024

type terminalPOCService struct {
	manager *terminal.Manager
}

func newTerminalPOCService(manager *terminal.Manager) *terminalPOCService {
	return &terminalPOCService{manager: manager}
}

func (s *terminalPOCService) Open(stream AgentTerminalPOCService_OpenServer) error {
	if s.manager == nil {
		return grpcstatus.Error(codes.FailedPrecondition, "TERMINAL_UNAVAILABLE")
	}
	startFrame, err := stream.Recv()
	if err != nil {
		return terminalStreamReceiveError(err)
	}
	start, err := terminalMessageFromStruct(startFrame)
	if err != nil || start.Type != terminal.MessageStart {
		return grpcstatus.Error(codes.InvalidArgument, "TERMINAL_START_INVALID")
	}
	opened, err := s.manager.Open(stream.Context(), terminal.StartRequest{Target: start.Target, ContainerID: start.ContainerID, Rows: start.Rows, Cols: start.Cols})
	if err != nil {
		return terminalManagerError(err)
	}
	defer func() { _ = s.manager.Close(opened.ID) }()

	started, err := terminalMessageToStruct(terminal.Message{
		Type:         terminal.MessageStarted,
		SessionID:    opened.ID,
		Shell:        opened.Shell,
		Enhancement:  opened.Enhancement,
		Reason:       opened.Reason,
		Capabilities: opened.Capabilities,
		Rows:         opened.Rows,
		Cols:         opened.Cols,
	})
	if err != nil {
		return grpcstatus.Error(codes.Internal, "TERMINAL_STREAM_FAILED")
	}
	if err := stream.Send(started); err != nil {
		return terminalStreamSendError(err)
	}

	outputDone := make(chan error, 1)
	go s.copyTerminalOutput(stream.Context(), stream, opened.ID, outputDone)

	incoming := make(chan terminalReceiveResult, 1)
	go receiveTerminalMessages(stream, incoming)

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case outputError := <-outputDone:
			return outputError
		case received := <-incoming:
			if received.err != nil {
				return terminalStreamReceiveError(received.err)
			}
			message, err := terminalMessageFromStruct(received.message)
			if err != nil {
				return grpcstatus.Error(codes.InvalidArgument, "TERMINAL_MESSAGE_INVALID")
			}
			switch message.Type {
			case terminal.MessageInput:
				if err := s.manager.Write(opened.ID, message.Data); err != nil {
					return terminalManagerError(err)
				}
			case terminal.MessageResize:
				if err := s.manager.Resize(opened.ID, message.Rows, message.Cols); err != nil {
					return terminalManagerError(err)
				}
			case terminal.MessageClose:
				if err := s.manager.Close(opened.ID); err != nil && terminal.ErrorCode(err) != "TERMINAL_SESSION_NOT_FOUND" {
					return terminalManagerError(err)
				}
				return <-outputDone
			default:
				return grpcstatus.Error(codes.InvalidArgument, "TERMINAL_MESSAGE_INVALID")
			}
		}
	}
}

func (s *terminalPOCService) copyTerminalOutput(ctx context.Context, stream AgentTerminalPOCService_OpenServer, sessionID string, done chan<- error) {
	output := make([]byte, terminalOutputChunkSize)
	for {
		read, err := s.manager.Read(ctx, sessionID, output)
		if read > 0 {
			frame, frameErr := terminalMessageToStruct(terminal.Message{Type: terminal.MessageOutput, Data: append([]byte(nil), output[:read]...)})
			if frameErr != nil {
				done <- grpcstatus.Error(codes.Internal, "TERMINAL_STREAM_FAILED")
				return
			}
			if sendErr := stream.Send(frame); sendErr != nil {
				done <- terminalStreamSendError(sendErr)
				return
			}
		}
		if err == nil {
			continue
		}
		if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
			done <- grpcstatus.Error(codes.Internal, "TERMINAL_STREAM_FAILED")
			return
		}
		closed, closeErr := terminalMessageToStruct(terminal.Message{Type: terminal.MessageClosed, SessionID: sessionID})
		if closeErr != nil {
			done <- grpcstatus.Error(codes.Internal, "TERMINAL_STREAM_FAILED")
			return
		}
		if sendErr := stream.Send(closed); sendErr != nil {
			done <- terminalStreamSendError(sendErr)
			return
		}
		done <- nil
		return
	}
}

type terminalReceiveResult struct {
	message *structpb.Struct
	err     error
}

func receiveTerminalMessages(stream AgentTerminalPOCService_OpenServer, destination chan<- terminalReceiveResult) {
	for {
		message, err := stream.Recv()
		destination <- terminalReceiveResult{message: message, err: err}
		if err != nil {
			return
		}
	}
}

func terminalStreamReceiveError(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return nil
	}
	return grpcstatus.Error(codes.Unavailable, "TERMINAL_STREAM_RECEIVE_FAILED")
}

func terminalStreamSendError(error) error {
	return grpcstatus.Error(codes.Unavailable, "TERMINAL_STREAM_SEND_FAILED")
}

func terminalManagerError(err error) error {
	switch terminal.ErrorCode(err) {
	case "TERMINAL_SESSION_CANCELED":
		return nil
	case "TERMINAL_TARGET_REJECTED", "TERMINAL_DIMENSIONS_INVALID", "TERMINAL_INPUT_INVALID":
		return grpcstatus.Error(codes.InvalidArgument, "TERMINAL_REQUEST_INVALID")
	case "TERMINAL_SESSION_LIMIT_REACHED":
		return grpcstatus.Error(codes.ResourceExhausted, "TERMINAL_SESSION_LIMIT_REACHED")
	case "TERMINAL_TARGET_UNAVAILABLE", "TERMINAL_CONTAINER_TARGET_REJECTED", "TERMINAL_HOST_UNSUPPORTED":
		return grpcstatus.Error(codes.FailedPrecondition, "TERMINAL_TARGET_UNAVAILABLE")
	default:
		return grpcstatus.Error(codes.Internal, "TERMINAL_SESSION_FAILED")
	}
}
