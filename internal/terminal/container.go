package terminal

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/moby/moby/client"
)

const (
	protectedContainerName  = "/ncp-p0-terminal-poc"
	protectedContainerID    = "ncp-p0-terminal-poc"
	protectedContainerLabel = "terminal"
	localDockerHost         = "unix:///var/run/docker.sock"
)

type ContainerInfo struct {
	ID      string
	Name    string
	Labels  map[string]string
	Running bool
}

type ContainerAttachment struct {
	ExecID     string
	Reader     io.Reader
	Writer     io.Writer
	Close      func() error
	CloseWrite func() error
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
	if s.gateway == nil {
		return nil, coded("TERMINAL_CONTAINER_UNAVAILABLE", errors.New("container gateway is required"))
	}
	info, err := s.gateway.Inspect(ctx, protectedContainerID)
	if err != nil {
		return nil, coded("TERMINAL_CONTAINER_TARGET_REJECTED", err)
	}
	if info.ID == "" || info.Name != protectedContainerName || !info.Running || info.Labels["ncp.poc"] != protectedContainerLabel {
		return nil, coded("TERMINAL_CONTAINER_TARGET_REJECTED", errors.New("container is not the protected terminal POC target"))
	}
	attachment, err := s.gateway.Open(ctx, info.ID, request.Rows, request.Cols)
	if err != nil {
		return nil, coded("TERMINAL_CONTAINER_OPEN_FAILED", err)
	}
	if attachment.ExecID == "" || attachment.Reader == nil || attachment.Writer == nil || attachment.Close == nil {
		if attachment.Close != nil {
			_ = attachment.Close()
		}
		return nil, coded("TERMINAL_CONTAINER_OPEN_FAILED", errors.New("container terminal attachment is incomplete"))
	}
	return &containerSession{gateway: s.gateway, attachment: attachment}, nil
}

type containerSession struct {
	gateway    ContainerGateway
	attachment ContainerAttachment
	close      sync.Once
	closeErr   error
}

const containerTerminationTimeout = 2 * time.Second

func (s *containerSession) Read(_ context.Context, output []byte) (int, error) {
	return s.attachment.Reader.Read(output)
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
