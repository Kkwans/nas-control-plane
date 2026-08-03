package terminal

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/moby/moby/client"
)

const localDockerHost = "unix:///var/run/docker.sock"

type ContainerInfo struct {
	ID      string
	Name    string
	Labels  map[string]string
	Running bool
}

type ContainerAttachment struct {
	ExecID       string
	Reader       io.Reader
	Writer       io.Writer
	Close        func() error
	CloseWrite   func() error
	CancelRead   func() error
	Shell        string
	Enhancement  string
	Reason       string
	Capabilities SessionCapabilities
}

type ContainerGateway interface {
	Inspect(context.Context, string) (ContainerInfo, error)
	Open(context.Context, string, uint16, uint16) (ContainerAttachment, error)
	Resize(context.Context, string, uint16, uint16) error
	WaitForExit(context.Context, string) error
}

type containerStarter struct {
	gateway ContainerGateway
}

func NewContainerStarter() (Starter, error) {
	apiClient, err := client.New(client.WithHost(localDockerHost), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, coded("TERMINAL_CONTAINER_UNAVAILABLE", err)
	}
	return newContainerStarter(newMobyContainerGateway(apiClient)), nil
}

func newContainerStarter(gateway ContainerGateway) Starter {
	return &containerStarter{gateway: gateway}
}

func (s *containerStarter) Start(ctx context.Context, request StartRequest) (Session, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, coded("TERMINAL_SESSION_CANCELED", err)
		}
	}
	if s.gateway == nil {
		return nil, coded("TERMINAL_CONTAINER_UNAVAILABLE", errors.New("container gateway is required"))
	}
	info, err := s.gateway.Inspect(ctx, request.ContainerID)
	if err != nil {
		if ctx != nil && ctx.Err() != nil {
			return nil, coded("TERMINAL_SESSION_CANCELED", ctx.Err())
		}
		return nil, coded("TERMINAL_CONTAINER_TARGET_REJECTED", err)
	}
	if info.ID == "" || !info.Running {
		return nil, coded("TERMINAL_CONTAINER_TARGET_REJECTED", errors.New("container is not running"))
	}
	attachment, err := s.gateway.Open(ctx, info.ID, request.Rows, request.Cols)
	if err != nil {
		if ctx != nil && ctx.Err() != nil {
			return nil, coded("TERMINAL_SESSION_CANCELED", ctx.Err())
		}
		return nil, coded("TERMINAL_CONTAINER_OPEN_FAILED", err)
	}
	if attachment.ExecID == "" || attachment.Reader == nil || attachment.Writer == nil || attachment.Close == nil {
		if attachment.Close != nil {
			_ = attachment.Close()
		}
		return nil, coded("TERMINAL_CONTAINER_OPEN_FAILED", errors.New("container terminal attachment is incomplete"))
	}
	if attachment.Shell == "" {
		attachment.Shell = "sh"
	}
	if attachment.Enhancement == "" {
		attachment.Enhancement = "native"
	}
	if attachment.Capabilities == (SessionCapabilities{}) {
		attachment.Capabilities = sessionCapabilitiesFor(attachment.Shell, attachment.Enhancement)
	}
	return &containerSession{gateway: s.gateway, attachment: attachment}, nil
}

type containerSession struct {
	gateway    ContainerGateway
	attachment ContainerAttachment
	close      sync.Once
	closeErr   error
}

func (s *containerSession) Metadata() SessionMetadata {
	return SessionMetadata{
		Shell:        s.attachment.Shell,
		Enhancement:  s.attachment.Enhancement,
		Reason:       s.attachment.Reason,
		Capabilities: s.attachment.Capabilities,
	}
}

const containerTerminationTimeout = 2 * time.Second

func (s *containerSession) Read(ctx context.Context, output []byte) (int, error) {
	if ctx == nil {
		return s.attachment.Reader.Read(output)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	stop := context.AfterFunc(ctx, s.cancelRead)
	read, err := s.attachment.Reader.Read(output)
	if !stop() && ctx.Err() != nil && read == 0 {
		return 0, ctx.Err()
	}
	return read, err
}

func (s *containerSession) cancelRead() {
	if s.attachment.CancelRead != nil {
		_ = s.attachment.CancelRead()
		return
	}
	if closer, ok := s.attachment.Reader.(io.Closer); ok {
		_ = closer.Close()
	}
}

func (s *containerSession) Write(input []byte) (int, error) {
	return s.attachment.Writer.Write(input)
}

func (s *containerSession) Resize(rows, cols uint16) error {
	return s.gateway.Resize(context.Background(), s.attachment.ExecID, rows, cols)
}

func (s *containerSession) Close() error {
	s.close.Do(func() {
		if err := writeAll(s.attachment.Writer, []byte{3}); err != nil {
			s.closeErr = err
		}
		if err := writeAll(s.attachment.Writer, []byte("exit\n")); err != nil && s.closeErr == nil {
			s.closeErr = err
		}
		if s.attachment.CloseWrite != nil {
			if err := s.attachment.CloseWrite(); err != nil && s.closeErr == nil {
				s.closeErr = err
			}
		}
		terminationContext, cancel := context.WithTimeout(context.Background(), containerTerminationTimeout)
		defer cancel()
		if err := s.gateway.WaitForExit(terminationContext, s.attachment.ExecID); err != nil && s.closeErr == nil {
			s.closeErr = err
		}
		if err := s.attachment.Close(); err != nil && s.closeErr == nil {
			s.closeErr = err
		}
	})
	return s.closeErr
}

func writeAll(writer io.Writer, input []byte) error {
	written, err := writer.Write(input)
	if err != nil {
		return err
	}
	if written != len(input) {
		return io.ErrShortWrite
	}
	return nil
}
