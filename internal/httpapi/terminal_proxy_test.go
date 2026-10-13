package httpapi

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/terminal"
	"github.com/coder/websocket"
)

type proxyTestConnection struct {
	closed     chan struct{}
	once       sync.Once
	closeCalls atomic.Int32
	active     atomic.Int32
	read       func(context.Context) (websocket.MessageType, []byte, error)
	write      func(context.Context, websocket.MessageType, []byte) error
}

func newProxyTestConnection() *proxyTestConnection {
	return &proxyTestConnection{closed: make(chan struct{})}
}
func (c *proxyTestConnection) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	c.active.Add(1)
	defer c.active.Add(-1)
	if c.read != nil {
		return c.read(ctx)
	}
	<-c.closed
	return 0, nil, io.EOF
}
func (c *proxyTestConnection) Write(ctx context.Context, typ websocket.MessageType, data []byte) error {
	c.active.Add(1)
	defer c.active.Add(-1)
	if c.write != nil {
		return c.write(ctx, typ, data)
	}
	return nil
}
func (c *proxyTestConnection) CloseNow() error {
	c.closeCalls.Add(1)
	c.once.Do(func() { close(c.closed) })
	return nil
}

type proxyTestStream struct {
	closed     chan struct{}
	once       sync.Once
	closeCalls atomic.Int32
	active     atomic.Int32
	recvs      atomic.Int32
	inputs     atomic.Int32
	send       func(terminal.Message) error
	recv       func(int32) (terminal.Message, error)
}

func newProxyTestStream() *proxyTestStream { return &proxyTestStream{closed: make(chan struct{})} }
func (s *proxyTestStream) Send(m terminal.Message) error {
	s.active.Add(1)
	defer s.active.Add(-1)
	if m.Type == terminal.MessageInput {
		s.inputs.Add(1)
	}
	if s.send != nil {
		return s.send(m)
	}
	return nil
}
func (s *proxyTestStream) Recv() (terminal.Message, error) {
	s.active.Add(1)
	defer s.active.Add(-1)
	n := s.recvs.Add(1)
	if s.recv != nil {
		return s.recv(n)
	}
	if n == 1 {
		return terminal.Message{Type: terminal.MessageStarted, SessionID: "fake"}, nil
	}
	<-s.closed
	return terminal.Message{}, io.EOF
}
func (s *proxyTestStream) Close() error {
	s.closeCalls.Add(1)
	s.once.Do(func() { close(s.closed) })
	return nil
}

type proxyTestClient struct {
	open func(context.Context, string) (TerminalStream, error)
}

func (c proxyTestClient) Open(ctx context.Context, path string) (TerminalStream, error) {
	return c.open(ctx, path)
}
func awaitProxySignal(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for %s", label)
	}
}
func assertProxyClosed(t *testing.T, c *proxyTestConnection, s *proxyTestStream) {
	t.Helper()
	if c.closeCalls.Load() != 1 || s.closeCalls.Load() != 1 || c.active.Load() != 0 || s.active.Load() != 0 {
		t.Fatalf("cleanup browser=%d Agent=%d active=%d/%d", c.closeCalls.Load(), s.closeCalls.Load(), c.active.Load(), s.active.Load())
	}
}

func TestTerminalProxyCancellationAbortsEveryIOStage(t *testing.T) {
	for _, stage := range []string{"start_send", "start_recv", "started_write", "input_send", "output_recv", "output_write"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c, s := newProxyTestConnection(), newProxyTestStream()
			entered := make(chan struct{})
			var enterOnce sync.Once
			enter := func() { enterOnce.Do(func() { close(entered) }) }
			switch stage {
			case "start_send":
				s.send = func(terminal.Message) error { enter(); <-s.closed; return io.EOF }
			case "start_recv":
				s.recv = func(int32) (terminal.Message, error) { enter(); <-s.closed; return terminal.Message{}, io.EOF }
			case "started_write":
				c.write = func(context.Context, websocket.MessageType, []byte) error { enter(); <-c.closed; return io.EOF }
			case "input_send":
				var reads atomic.Int32
				c.read = func(context.Context) (websocket.MessageType, []byte, error) {
					if reads.Add(1) == 1 {
						return websocket.MessageBinary, []byte("x"), nil
					}
					<-c.closed
					return 0, nil, io.EOF
				}
				s.send = func(m terminal.Message) error {
					if m.Type == terminal.MessageInput {
						enter()
						<-s.closed
						return io.EOF
					}
					return nil
				}
			case "output_recv":
				s.recv = func(n int32) (terminal.Message, error) {
					if n == 1 {
						return terminal.Message{Type: terminal.MessageStarted, SessionID: "fake"}, nil
					}
					enter()
					<-s.closed
					return terminal.Message{}, io.EOF
				}
			case "output_write":
				s.recv = func(n int32) (terminal.Message, error) {
					if n == 1 {
						return terminal.Message{Type: terminal.MessageStarted, SessionID: "fake"}, nil
					}
					return terminal.Message{Type: terminal.MessageOutput, Data: []byte("out")}, nil
				}
				c.write = func(_ context.Context, typ websocket.MessageType, _ []byte) error {
					if typ == websocket.MessageBinary {
						enter()
						<-c.closed
						return io.EOF
					}
					return nil
				}
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				runTerminalProxy(ctx, c, proxyTestClient{open: func(context.Context, string) (TerminalStream, error) { return s, nil }}, "fake", terminal.Message{Type: terminal.MessageStart})
			}()
			awaitProxySignal(t, entered, stage)
			cancel()
			awaitProxySignal(t, done, "proxy exit")
			assertProxyClosed(t, c, s)
		})
	}
}

func TestTerminalProxyCancellationClosesBrowserWhileOpenBlocks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newProxyTestConnection()
	entered, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		runTerminalProxy(ctx, c, proxyTestClient{open: func(ctx context.Context, _ string) (TerminalStream, error) {
			close(entered)
			<-ctx.Done()
			<-c.closed
			return nil, ctx.Err()
		}}, "fake", terminal.Message{})
	}()
	awaitProxySignal(t, entered, "Open")
	cancel()
	awaitProxySignal(t, done, "Open cancellation")
	if c.closeCalls.Load() != 1 {
		t.Fatalf("browser closes=%d", c.closeCalls.Load())
	}
}

func TestTerminalProxyAlreadyCanceledDoesNotOpenAgent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := newProxyTestConnection()
	runTerminalProxy(ctx, c, proxyTestClient{open: func(context.Context, string) (TerminalStream, error) {
		t.Fatal("canceled proxy opened Agent")
		return nil, errors.New("unreachable")
	}}, "fake", terminal.Message{})
	if c.closeCalls.Load() != 1 {
		t.Fatal("browser was not closed")
	}
}

func TestTerminalProxyCanceledReadDoesNotForwardBufferedInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, s := newProxyTestConnection(), newProxyTestStream()
	c.read = func(context.Context) (websocket.MessageType, []byte, error) {
		cancel()
		return websocket.MessageBinary, []byte("late"), nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runTerminalProxy(ctx, c, proxyTestClient{open: func(context.Context, string) (TerminalStream, error) { return s, nil }}, "fake", terminal.Message{Type: terminal.MessageStart})
	}()
	awaitProxySignal(t, done, "canceled buffered input")
	if s.inputs.Load() != 0 {
		t.Fatal("forwarded input after cancellation")
	}
	assertProxyClosed(t, c, s)
}

func TestTerminalProxyCanceledRecvDoesNotWriteBufferedOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, s := newProxyTestConnection(), newProxyTestStream()
	var outputs atomic.Int32
	s.recv = func(n int32) (terminal.Message, error) {
		if n == 1 {
			return terminal.Message{Type: terminal.MessageStarted, SessionID: "fake"}, nil
		}
		cancel()
		return terminal.Message{Type: terminal.MessageOutput, Data: []byte("late")}, nil
	}
	c.write = func(_ context.Context, typ websocket.MessageType, _ []byte) error {
		if typ == websocket.MessageBinary {
			outputs.Add(1)
		}
		return nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runTerminalProxy(ctx, c, proxyTestClient{open: func(context.Context, string) (TerminalStream, error) { return s, nil }}, "fake", terminal.Message{Type: terminal.MessageStart})
	}()
	awaitProxySignal(t, done, "canceled buffered output")
	if outputs.Load() != 0 {
		t.Fatal("wrote output after cancellation")
	}
	assertProxyClosed(t, c, s)
}

func TestTerminalProxyBrowserDisconnectJoinsPumps(t *testing.T) {
	c, s := newProxyTestConnection(), newProxyTestStream()
	c.read = func(context.Context) (websocket.MessageType, []byte, error) { return 0, nil, io.EOF }
	done := make(chan struct{})
	go func() {
		defer close(done)
		runTerminalProxy(context.Background(), c, proxyTestClient{open: func(context.Context, string) (TerminalStream, error) { return s, nil }}, "fake", terminal.Message{Type: terminal.MessageStart})
	}()
	awaitProxySignal(t, done, "browser disconnect")
	assertProxyClosed(t, c, s)
}

func TestTerminalProxyExpiredTimeoutDoesNotOpenAgent(t *testing.T) {
	// 立即截止的 deadline 验证与会话取消相同的传输清理路径，无真实长等待。
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	c := newProxyTestConnection()
	runTerminalProxy(ctx, c, proxyTestClient{open: func(context.Context, string) (TerminalStream, error) {
		t.Fatal("expired timeout opened Agent")
		return nil, nil
	}}, "fake", terminal.Message{})
	if c.closeCalls.Load() != 1 {
		t.Fatal("browser was not closed")
	}
}

func TestTerminalProxyDequeuedInputRechecksCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, s := newProxyTestConnection(), newProxyTestStream()
	queued := make(chan struct{})
	var reads atomic.Int32
	c.read = func(context.Context) (websocket.MessageType, []byte, error) {
		switch reads.Add(1) {
		case 1, 2:
			return websocket.MessageBinary, []byte("buffered"), nil
		default:
			close(queued)
			<-c.closed
			return 0, nil, io.EOF
		}
	}
	s.send = func(m terminal.Message) error {
		if m.Type == terminal.MessageInput {
			<-queued
			cancel()
		}
		return nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runTerminalProxy(ctx, c, proxyTestClient{open: func(context.Context, string) (TerminalStream, error) { return s, nil }}, "fake", terminal.Message{Type: terminal.MessageStart})
	}()
	awaitProxySignal(t, done, "queued input cancellation")
	if s.inputs.Load() != 1 {
		t.Fatalf("input sends=%d, wanted only the in-flight send", s.inputs.Load())
	}
	assertProxyClosed(t, c, s)
}
