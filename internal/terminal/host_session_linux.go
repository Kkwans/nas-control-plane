//go:build linux

package terminal

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const hostOperationTimeout = 2 * time.Second
const hostPollInterval = 10 * time.Millisecond

type hostTransport interface {
	TryRead([]byte) (int, error)
	TryWrite([]byte) (int, error)
	Resize(uint16, uint16) error
	Close() error
}

type hostWaitResult struct {
	err    error
	reaped bool
}

func waitHostCommand(command *exec.Cmd) hostWaitResult {
	err := command.Wait()
	reaped := false
	if command.ProcessState != nil {
		if status, ok := command.ProcessState.Sys().(syscall.WaitStatus); ok {
			reaped = status.Exited() || status.Signaled()
		}
	}
	return hostWaitResult{err: err, reaped: reaped}
}

type hostSession struct {
	transport           hostTransport
	kill                func() error
	parent              context.Context
	ctx                 context.Context
	cancel              context.CancelFunc
	metadata            SessionMetadata
	readGate, inputGate chan struct{}
	mu                  sync.Mutex
	closing             bool
	active              int
	idle                chan struct{}
	abortOnce           sync.Once
	abortDone           chan struct{}
	abortErr            error
	watcherDone         chan struct{}
	waitDone            chan struct{}
	waitResult          hostWaitResult
	closeOnce           sync.Once
	closeDone           chan struct{}
	closeErr            error
}

// 成功启动后立即建立唯一 Wait owner；即使关闭超时也保留它接收最终回收凭据。
func newHostSession(parent context.Context, transport hostTransport, kill func() error, wait func() hostWaitResult) *hostSession {
	ctx, cancel := context.WithCancel(context.Background())
	idle := make(chan struct{})
	close(idle)
	s := &hostSession{transport: transport, kill: kill, parent: parent, ctx: ctx, cancel: cancel,
		readGate: make(chan struct{}, 1), inputGate: make(chan struct{}, 1), idle: idle,
		abortDone: make(chan struct{}), watcherDone: make(chan struct{}), waitDone: make(chan struct{}), closeDone: make(chan struct{})}
	go func() { s.waitResult = wait(); close(s.waitDone) }()
	// 尾部尚未排空时仍拥有 master，保留父取消 watcher，直到 drain/Close 结束本地生命周期。
	go func() {
		defer close(s.watcherDone)
		select {
		case <-parent.Done():
			s.abort()
		case <-ctx.Done():
		}
	}()
	return s
}

// 启动后的失败沿同一 owner 收尾，保留未确认的清理/回收结果。
func (s *hostSession) initialize(init func() error) error {
	if err := init(); err != nil {
		return errors.Join(err, s.Close())
	}
	return nil
}

func (s *hostSession) started() (Session, error) {
	if err := s.parent.Err(); err != nil {
		return nil, coded("TERMINAL_SESSION_CANCELED", errors.Join(err, s.Close()))
	}
	return s, nil
}

func (s *hostSession) Metadata() SessionMetadata { return s.metadata }

// 只发起本地收尾，不等待输入 gate、不调用 Wait，也不对进程树发送信号。
func (s *hostSession) abort() {
	s.abortOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		s.cancel()
		s.mu.Unlock()
		go func() { s.abortErr = s.transport.Close(); close(s.abortDone) }()
	})
}

func (s *hostSession) operationError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.parent.Err(); err != nil {
		return err
	}
	if s.ctx.Err() != nil {
		return os.ErrClosed
	}
	return nil
}

func (s *hostSession) inputError(ctx context.Context) error {
	if err := s.operationError(ctx); err != nil {
		return err
	}
	select {
	case <-s.waitDone:
		return os.ErrClosed
	default:
		return nil
	}
}

func (s *hostSession) enter(ctx context.Context, gate chan struct{}) (func(), error) {
	if err := s.operationError(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return nil, os.ErrClosed
	}
	if s.active == 0 {
		s.idle = make(chan struct{})
	}
	s.active++
	s.mu.Unlock()
	finish := func() {
		s.mu.Lock()
		s.active--
		if s.active == 0 {
			close(s.idle)
		}
		s.mu.Unlock()
	}
	select {
	case gate <- struct{}{}:
		if err := s.operationError(ctx); err != nil {
			<-gate
			finish()
			return nil, err
		}
		return func() { <-gate; finish() }, nil
	case <-ctx.Done():
		finish()
		return nil, ctx.Err()
	case <-s.parent.Done():
		finish()
		return nil, s.parent.Err()
	case <-s.ctx.Done():
		finish()
		return nil, os.ErrClosed
	}
}

func (s *hostSession) pause(ctx context.Context) error {
	timer := time.NewTimer(hostPollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.parent.Done():
		return s.parent.Err()
	case <-s.ctx.Done():
		return os.ErrClosed
	case <-timer.C:
		return s.operationError(ctx)
	}
}

func hostWouldBlock(err error) bool {
	return errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK)
}

func (s *hostSession) Read(ctx context.Context, output []byte) (int, error) {
	finish, err := s.enter(ctx, s.readGate)
	if err != nil {
		return 0, err
	}
	defer finish()
	if len(output) == 0 {
		return 0, nil
	}
	for {
		if err := s.operationError(ctx); err != nil {
			return 0, err
		}
		n, err := s.transport.TryRead(output)
		// 取消后已读取的数据不得再交付到流。
		if canceled := s.operationError(ctx); canceled != nil {
			return 0, canceled
		}
		if n > 0 {
			return n, err
		}
		if err == nil {
			s.abort()
			return 0, io.EOF
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if !hostWouldBlock(err) {
			s.abort()
			return 0, err
		}
		// 自然退出后只排空立即可读的尾部，不等待仍持有 slave 的后代。
		select {
		case <-s.waitDone:
			s.abort()
			return 0, io.EOF
		default:
		}
		if err := s.pause(ctx); err != nil {
			return 0, err
		}
	}
}

func (s *hostSession) Write(input []byte) (written int, result error) {
	ctx, cancel := context.WithTimeout(context.Background(), hostOperationTimeout)
	defer cancel()
	defer func() {
		if result != nil {
			// 已自然退出时仍让输出 reader 排空尾部；取消/显式 Close 自行 abort。
			select {
			case <-s.waitDone:
				return
			default:
				s.abort()
			}
		}
	}()
	if err := s.inputError(ctx); err != nil {
		return 0, err
	}
	finish, err := s.enter(ctx, s.inputGate)
	if err != nil {
		return 0, err
	}
	defer finish()
	for written < len(input) {
		if err := s.inputError(ctx); err != nil {
			return written, err
		}
		n, err := s.transport.TryWrite(input[written:])
		written += n
		if canceled := s.inputError(ctx); canceled != nil {
			return written, canceled
		}
		if err == nil && written == len(input) {
			return written, nil
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil && !hostWouldBlock(err) {
			return written, err
		}
		if err != nil || n == 0 {
			if err := s.pause(ctx); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

func (s *hostSession) Resize(rows, cols uint16) error {
	ctx, cancel := context.WithTimeout(context.Background(), hostOperationTimeout)
	defer cancel()
	finish, err := s.enter(ctx, s.inputGate)
	if err != nil {
		return err
	}
	defer finish()
	for {
		if err := s.inputError(ctx); err != nil {
			return err
		}
		// 同步 ioctl 依赖正常内核返回；deadline 不可抢占内核 D-state。
		err := s.transport.Resize(rows, cols)
		if canceled := s.inputError(ctx); canceled != nil {
			return canceled
		}
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

func (s *hostSession) Close() error {
	s.closeOnce.Do(func() { go s.closeOwned() })
	<-s.closeDone
	return s.closeErr
}

func (s *hostSession) closeOwned() {
	// 所有应用层等待共用一个预算，不给每个 owner 重新计时。
	ctx, cancel := context.WithTimeout(context.Background(), hostOperationTimeout)
	defer cancel()
	s.abort()
	var result error
	if err := s.kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		result = errors.Join(result, err)
	}
	s.mu.Lock()
	idle := s.idle
	s.mu.Unlock()
	localDone := true
	for _, done := range []<-chan struct{}{s.abortDone, idle, s.watcherDone} {
		if !hostAwait(ctx, done) {
			localDone = false
		}
	}
	if !localDone {
		result = errors.Join(result, ErrHostCleanupUnconfirmed)
	}
	select {
	case <-s.abortDone:
		if s.abortErr != nil && !errors.Is(s.abortErr, os.ErrClosed) {
			result = errors.Join(result, s.abortErr)
		}
	default:
	}
	if !hostAwait(ctx, s.waitDone) {
		result = errors.Join(result, ErrHostExitUnconfirmed)
	} else if !s.waitResult.reaped {
		result = errors.Join(result, ErrHostExitUnconfirmed, s.waitResult.err)
	}
	s.closeErr = result
	close(s.closeDone)
}

func hostAwait(ctx context.Context, done <-chan struct{}) bool {
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
