package agentsocket

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/terminal"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

const terminalOutputChunkSize = 16 * 1024

const terminalCloseNotificationTimeout = 100 * time.Millisecond

type terminalPOCService struct {
	manager *terminal.Manager
	logger  *slog.Logger
}

func newTerminalPOCService(manager *terminal.Manager) *terminalPOCService {
	return &terminalPOCService{manager: manager, logger: slog.Default()}
}

func (s *terminalPOCService) Open(stream AgentTerminalPOCService_OpenServer) error {
	if s.manager == nil {
		return grpcstatus.Error(codes.FailedPrecondition, "TERMINAL_UNAVAILABLE")
	}
	if stream.Context().Err() != nil {
		return nil
	}
	startFrame, err := stream.Recv()
	if stream.Context().Err() != nil {
		return nil
	}
	if err != nil {
		return terminalStreamReceiveError(err)
	}
	start, err := terminalMessageFromStruct(startFrame)
	if err != nil || start.Type != terminal.MessageStart {
		return grpcstatus.Error(codes.InvalidArgument, "TERMINAL_START_INVALID")
	}
	sessionContext, cancel := context.WithCancel(stream.Context())
	defer cancel()
	opened, err := s.manager.Open(sessionContext, terminal.StartRequest{Target: start.Target, ContainerID: start.ContainerID, Rows: start.Rows, Cols: start.Cols})
	if err != nil {
		return terminalManagerError(err)
	}
	// 显式关闭与错误收尾共享结果；日志不包含底层错误或终端内容。
	cleanup := sync.OnceValue(func() error {
		err := s.manager.Close(opened.ID)
		if err != nil && terminal.ErrorCode(err) != "TERMINAL_SESSION_NOT_FOUND" {
			classification := "TERMINAL_CLOSE_FAILED"
			if errors.Is(err, terminal.ErrContainerExitUnconfirmed) {
				classification = "TERMINAL_CONTAINER_EXIT_UNCONFIRMED"
			}
			logger := s.logger
			if logger == nil {
				logger = slog.Default()
			}
			logger.Warn("terminal cleanup incomplete", "code", classification, "session_id", opened.ID, "target", opened.Target, "local_cleanup_unconfirmed", errors.Is(err, terminal.ErrContainerCleanupUnconfirmed))
		}
		return err
	})
	defer func() {
		cancel()
		_ = cleanup()
	}()

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
	if sessionContext.Err() != nil {
		return nil
	}
	// started 与后续输出使用同一个 sender；初始 Send 也可能只在 RPC 返回后解除。
	outputDone := make(chan error, 1)
	go func() {
		if sessionContext.Err() != nil {
			outputDone <- nil
			return
		}
		if err := stream.Send(started); err != nil {
			outputDone <- terminalStreamSendError(err)
			return
		}
		s.copyTerminalOutput(sessionContext, stream, opened.ID, outputDone)
	}()

	incoming := make(chan terminalReceiveResult, 1)
	go receiveTerminalMessages(sessionContext, stream, incoming)

	for {
		select {
		case <-sessionContext.Done():
			return nil
		case outputError := <-outputDone:
			return outputError
		case received, ok := <-incoming:
			// 取消与缓冲输入可能同时就绪；出队后重新检查，不能继续准入输入。
			if sessionContext.Err() != nil {
				return nil
			}
			if !ok {
				return nil
			}
			if received.err != nil {
				return terminalStreamReceiveError(received.err)
			}
			message, err := terminalMessageFromStruct(received.message)
			if err != nil {
				return grpcstatus.Error(codes.InvalidArgument, "TERMINAL_MESSAGE_INVALID")
			}
			if sessionContext.Err() != nil {
				return nil
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
				if err := cleanup(); err != nil && terminal.ErrorCode(err) != "TERMINAL_SESSION_NOT_FOUND" {
					return terminalManagerError(err)
				}
				// Send/Recv 可能必须等 RPC 返回才解除，最后通知只占短窗口。
				notification := time.NewTimer(terminalCloseNotificationTimeout)
				defer notification.Stop()
				select {
				case <-notification.C:
					return nil
				case err := <-outputDone:
					return err
				case <-sessionContext.Done():
					return nil
				}
			default:
				return grpcstatus.Error(codes.InvalidArgument, "TERMINAL_MESSAGE_INVALID")
			}
		}
	}
}

func (s *terminalPOCService) copyTerminalOutput(ctx context.Context, stream AgentTerminalPOCService_OpenServer, sessionID string, done chan<- error) {
	output := make([]byte, terminalOutputChunkSize)
	for {
		if ctx.Err() != nil {
			done <- nil
			return
		}
		read, err := s.manager.Read(ctx, sessionID, output)
		if ctx.Err() != nil {
			done <- nil
			return
		}
		if read > 0 {
			frame, frameErr := terminalMessageToStruct(terminal.Message{Type: terminal.MessageOutput, Data: append([]byte(nil), output[:read]...)})
			if frameErr != nil {
				done <- grpcstatus.Error(codes.Internal, "TERMINAL_STREAM_FAILED")
				return
			}
			if ctx.Err() != nil {
				done <- nil
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
		if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) && terminal.ErrorCode(err) != "TERMINAL_SESSION_NOT_FOUND" {
			done <- grpcstatus.Error(codes.Internal, "TERMINAL_STREAM_FAILED")
			return
		}
		closed, closeErr := terminalMessageToStruct(terminal.Message{Type: terminal.MessageClosed, SessionID: sessionID})
		if closeErr != nil {
			done <- grpcstatus.Error(codes.Internal, "TERMINAL_STREAM_FAILED")
			return
		}
		if ctx.Err() != nil {
			done <- nil
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

func receiveTerminalMessages(ctx context.Context, stream AgentTerminalPOCService_OpenServer, destination chan<- terminalReceiveResult) {
	// Recv 的阻塞由 RPC 返回后的传输层取消解除，不能在 Open 返回前等待它结束。
	defer close(destination)
	for {
		if ctx.Err() != nil {
			return
		}
		message, err := stream.Recv()
		select {
		case destination <- terminalReceiveResult{message: message, err: err}:
		case <-ctx.Done():
			return
		}
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
