package terminal

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
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
	ExecID string
	Reader io.Reader
	// Reader 必须由同一 Conn 驱动，保留 SDK 已缓冲的数据。
	Conn         net.Conn
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
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, coded("TERMINAL_SESSION_CANCELED", err)
	}
	if s.gateway == nil {
		return nil, coded("TERMINAL_CONTAINER_UNAVAILABLE", errors.New("container gateway is required"))
	}
	info, err := s.gateway.Inspect(ctx, request.ContainerID)
	if err != nil {
		if ctx.Err() != nil {
			return nil, coded("TERMINAL_SESSION_CANCELED", ctx.Err())
		}
		return nil, coded("TERMINAL_CONTAINER_TARGET_REJECTED", err)
	}
	if info.ID == "" || !info.Running {
		return nil, coded("TERMINAL_CONTAINER_TARGET_REJECTED", errors.New("container is not running"))
	}
	attachment, err := s.gateway.Open(ctx, info.ID, request.Rows, request.Cols)
	if err != nil {
		if attachment.Conn != nil {
			err = errors.Join(err, attachment.Conn.Close())
		}
		if ctx.Err() != nil {
			return nil, coded("TERMINAL_SESSION_CANCELED", errors.Join(ctx.Err(), err))
		}
		return nil, coded("TERMINAL_CONTAINER_OPEN_FAILED", err)
	}
	if attachment.ExecID == "" || attachment.Reader == nil || attachment.Conn == nil {
		if attachment.Conn != nil {
			_ = attachment.Conn.Close()
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
	session := newContainerSession(ctx, s.gateway, attachment)
	if err := ctx.Err(); err != nil {
		// 有效会话交给 Manager 的唯一清理 owner，保留退出确认与 closing 名额。
		return session, coded("TERMINAL_SESSION_CANCELED", err)
	}
	return session, nil
}

// ErrContainerExitUnconfirmed 只表示未取得 exec 主进程退出的独立确认。
var ErrContainerExitUnconfirmed = errors.New("container exec exit unconfirmed")

// ErrContainerCleanupUnconfirmed 表示预算内未能确认在途本地 I/O 收尾。
var ErrContainerCleanupUnconfirmed = errors.New("container local cleanup unconfirmed")

type containerSession struct {
	gateway       ContainerGateway
	attachment    ContainerAttachment
	ctx           context.Context
	cancel        context.CancelFunc
	gate          chan struct{}
	closing       chan struct{}
	abortOnce     sync.Once
	abortErr      error
	closeStarted  atomic.Bool
	closeDone     chan struct{}
	closeErr      error
	stopLifecycle func()
	readMu        sync.Mutex
	readers       int
	readsDone     chan struct{}
}

func newContainerSession(ctx context.Context, gateway ContainerGateway, attachment ContainerAttachment) *containerSession {
	ioCtx, cancel := context.WithCancel(ctx)
	s := &containerSession{gateway: gateway, attachment: attachment, ctx: ioCtx, cancel: cancel, gate: make(chan struct{}, 1), closing: make(chan struct{}), closeDone: make(chan struct{})}
	s.readsDone = make(chan struct{})
	close(s.readsDone)
	s.gate <- struct{}{}
	s.stopLifecycle = onContextAbort(ioCtx, s.abort)
	return s
}

// onContextAbort 的收尾等待已启动的回调；不在回调自身调用它。
func onContextAbort(ctx context.Context, abort func()) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(done); abort() })
	return func() {
		if !stop() {
			<-done
		}
	}
}

// abort 不获取输入 gate，也不发送控制字节或请求 Docker API。
func (s *containerSession) abort() {
	s.abortOnce.Do(func() { s.cancel(); s.abortErr = s.attachment.Conn.Close() })
}

func (s *containerSession) activeError() error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	select {
	case <-s.closing:
		return net.ErrClosed
	default:
		return nil
	}
}

func (s *containerSession) acquire() error {
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case <-s.closing:
		return net.ErrClosed
	case <-s.gate:
	}
	if err := s.activeError(); err != nil {
		s.release()
		return err
	}
	return nil
}

func (s *containerSession) release() { s.gate <- struct{}{} }

func (s *containerSession) Metadata() SessionMetadata {
	return SessionMetadata{
		Shell:        s.attachment.Shell,
		Enhancement:  s.attachment.Enhancement,
		Reason:       s.attachment.Reason,
		Capabilities: s.attachment.Capabilities,
	}
}

const (
	containerTerminationTimeout = 2 * time.Second
	containerInputTimeout       = 2 * time.Second
	containerResizeTimeout      = 2 * time.Second
)

// 关闭时只等待本地 Read，不等待可能依赖 RPC 返回的网络 Send/Recv。
func (s *containerSession) beginRead() error {
	s.readMu.Lock()
	defer s.readMu.Unlock()
	if err := s.activeError(); err != nil {
		if errors.Is(err, net.ErrClosed) {
			return io.EOF
		}
		return err
	}
	if s.readers == 0 {
		s.readsDone = make(chan struct{})
	}
	s.readers++
	return nil
}

func (s *containerSession) endRead() {
	s.readMu.Lock()
	defer s.readMu.Unlock()
	s.readers--
	if s.readers == 0 {
		close(s.readsDone)
	}
}

func (s *containerSession) Read(ctx context.Context, output []byte) (int, error) {
	if ctx == nil {
		ctx = s.ctx
	}
	if err := ctx.Err(); err != nil {
		s.abort()
		return 0, err
	}
	if err := s.ctx.Err(); err != nil {
		s.abort()
		return 0, err
	}
	if err := s.beginRead(); err != nil {
		return 0, err
	}
	defer s.endRead()
	stop := onContextAbort(ctx, s.abort)
	n, err := s.attachment.Reader.Read(output)
	stop()
	if ctx.Err() != nil {
		s.abort()
		return 0, ctx.Err()
	}
	if s.ctx.Err() != nil {
		s.abort()
		return 0, s.ctx.Err()
	}
	return n, err
}

func (s *containerSession) Write(input []byte) (int, error) {
	if err := s.acquire(); err != nil {
		return 0, err
	}
	defer s.release()
	deadline := time.Now().Add(containerInputTimeout)
	if parentDeadline, ok := s.ctx.Deadline(); ok && parentDeadline.Before(deadline) {
		deadline = parentDeadline
	}
	if err := s.attachment.Conn.SetWriteDeadline(deadline); err != nil {
		s.abort()
		return 0, err
	}
	if err := s.activeError(); err != nil {
		s.abort()
		return 0, err
	}
	n, err := s.attachment.Conn.Write(input)
	if err == nil && n != len(input) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = s.activeError()
	}
	if err == nil {
		err = s.attachment.Conn.SetWriteDeadline(time.Time{})
	}
	if err != nil {
		s.abort()
	}
	return n, err
}

func (s *containerSession) Resize(rows, cols uint16) error {
	if err := s.acquire(); err != nil {
		return err
	}
	defer s.release()
	ctx, cancel := context.WithTimeout(s.ctx, containerResizeTimeout)
	defer cancel()
	return s.gateway.Resize(ctx, s.attachment.ExecID, rows, cols)
}

func (s *containerSession) Close() error {
	deadline := time.Now().Add(containerTerminationTimeout)
	if !s.closeStarted.CompareAndSwap(false, true) {
		<-s.closeDone
		return s.closeErr
	}
	defer close(s.closeDone)
	func() {
		// 总预算在任何锁、控制写和状态查询之前开始；不继承已取消的会话。
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		stopDeadline := onContextAbort(ctx, s.abort)
		s.readMu.Lock()
		close(s.closing)
		readsDone := s.readsDone
		s.readMu.Unlock()
		acquired := false
		if s.ctx.Err() == nil {
			select {
			case <-s.gate:
				acquired = true
			default:
			}
		}
		if acquired {
			if s.ctx.Err() == nil {
				if err := s.attachment.Conn.SetWriteDeadline(deadline); err != nil {
					s.closeErr = err
				} else {
					for _, input := range [][]byte{{3}, []byte("exit\n")} {
						if err := s.ctx.Err(); err != nil {
							s.closeErr = errors.Join(s.closeErr, err)
							break
						}
						if err := writeAll(s.attachment.Conn, input); err != nil {
							s.closeErr = errors.Join(s.closeErr, err)
							break
						}
					}
				}
			}
			s.release()
		}
		// 若输入正在执行，先纯 abort 解除它，绝不等待 gate 后才取消。
		s.abort()
		if !acquired {
			if waitContainerCompletion(ctx, s.gate) {
				s.release()
			} else {
				s.closeErr = errors.Join(s.closeErr, ErrContainerCleanupUnconfirmed)
			}
		}
		// Conn.Close 解除同源 Reader；预算内还未完成时不能伪称本地 owner 已退出。
		if !waitContainerCompletion(ctx, readsDone) {
			s.closeErr = errors.Join(s.closeErr, ErrContainerCleanupUnconfirmed)
		}

		if err := s.gateway.WaitForExit(ctx, s.attachment.ExecID); err != nil {
			s.closeErr = errors.Join(s.closeErr, ErrContainerExitUnconfirmed, err)
		}
		stopDeadline()
		s.stopLifecycle()
		s.closeErr = errors.Join(s.closeErr, s.abortErr)
	}()
	return s.closeErr
}

// 截止与完成可能同时就绪；再次确认完成，避免把已完成 owner 误报为未确认。
func waitContainerCompletion(ctx context.Context, done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
	}
	select {
	case <-done:
		return true
	case <-ctx.Done():
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
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
