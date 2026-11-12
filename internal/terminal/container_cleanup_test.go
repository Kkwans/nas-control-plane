package terminal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// 内存 net.Pipe 提供真实 deadline/Close 中断；无 socket listener 或 Docker。
type cleanupConn struct {
	net.Conn
	writes     atomic.Int32
	closes     atomic.Int32
	writeFn    func([]byte) (int, error)
	deadlineFn func(time.Time) error
	closeFn    func() error
}

func (c *cleanupConn) Write(p []byte) (int, error) {
	c.writes.Add(1)
	if c.writeFn != nil {
		return c.writeFn(p)
	}
	return c.Conn.Write(p)
}
func (c *cleanupConn) Close() error {
	c.closes.Add(1)
	if c.closeFn != nil {
		return c.closeFn()
	}
	return c.Conn.Close()
}
func (c *cleanupConn) SetWriteDeadline(d time.Time) error {
	if c.deadlineFn != nil {
		return c.deadlineFn(d)
	}
	return c.Conn.SetWriteDeadline(d)
}

type cleanupGateway struct {
	attachment ContainerAttachment
	openFn     func()
	resizeFn   func(context.Context) error
	waitFn     func(context.Context) error
	waits      atomic.Int32
	resizes    atomic.Int32
}

func (g *cleanupGateway) Inspect(context.Context, string) (ContainerInfo, error) {
	return ContainerInfo{ID: "container", Running: true}, nil
}
func (g *cleanupGateway) Open(context.Context, string, uint16, uint16) (ContainerAttachment, error) {
	if g.openFn != nil {
		g.openFn()
	}
	return g.attachment, nil
}
func (g *cleanupGateway) Resize(ctx context.Context, _ string, _, _ uint16) error {
	g.resizes.Add(1)
	if g.resizeFn != nil {
		return g.resizeFn(ctx)
	}
	return nil
}
func (g *cleanupGateway) WaitForExit(ctx context.Context, _ string) error {
	g.waits.Add(1)
	if g.waitFn != nil {
		return g.waitFn(ctx)
	}
	return nil
}

func cleanupFixture(t *testing.T, ctx context.Context, immediate bool) (*containerSession, *cleanupConn, *cleanupGateway) {
	t.Helper()
	local, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close(); _ = local.Close() })
	conn := &cleanupConn{Conn: local}
	if immediate {
		conn.writeFn = func(p []byte) (int, error) { return len(p), nil }
	}
	g := &cleanupGateway{attachment: ContainerAttachment{ExecID: "exec", Reader: conn, Conn: conn}}
	s := newContainerSession(ctx, g, g.attachment)
	t.Cleanup(func() { _ = s.Close() })
	return s, conn, g
}
func assertCleanupClosed(t *testing.T, c *cleanupConn, g *cleanupGateway) {
	t.Helper()
	if c.closes.Load() != 1 || g.waits.Load() != 1 {
		t.Fatalf("close=%d wait=%d", c.closes.Load(), g.waits.Load())
	}
}

func TestTerminalCleanupCanceledWriteAbortsWithoutRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		s, c, g := cleanupFixture(t, ctx, false)
		done := make(chan error, 1)
		go func() { _, err := s.Write([]byte("input")); done <- err }()
		synctest.Wait()
		if c.writes.Load() != 1 {
			t.Fatal("write did not start")
		}
		cancel()
		if err := <-done; err == nil {
			t.Fatal("write unexpectedly succeeded")
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		assertCleanupClosed(t, c, g)
	})
}
func TestTerminalCleanupWriteDeadlineUnblocksWithoutCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, c, g := cleanupFixture(t, context.Background(), false)
		start := time.Now()
		n, err := s.Write([]byte("input"))
		if n != 0 || err == nil || time.Since(start) != containerInputTimeout {
			t.Fatalf("n=%d err=%v elapsed=%v", n, err, time.Since(start))
		}
		_ = s.Close()
		assertCleanupClosed(t, c, g)
		if c.writes.Load() != 1 {
			t.Fatal("input retried")
		}
	})
}
func TestTerminalCleanupResizeUsesSessionContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		s, _, g := cleanupFixture(t, ctx, true)
		g.resizeFn = func(ctx context.Context) error {
			if _, ok := ctx.Deadline(); !ok {
				t.Error("missing resize budget")
			}
			<-ctx.Done()
			return ctx.Err()
		}
		done := make(chan error, 1)
		go func() { done <- s.Resize(24, 80) }()
		synctest.Wait()
		cancel()
		if !errors.Is(<-done, context.Canceled) {
			t.Fatal("resize not canceled")
		}
		if s.Resize(24, 80) == nil || g.resizes.Load() != 1 {
			t.Fatal("canceled resize admitted")
		}
	})
}
func TestTerminalCleanupCancelReadNeverWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, c, g := cleanupFixture(t, context.Background(), true)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { _, err := s.Read(ctx, make([]byte, 10)); done <- err }()
		synctest.Wait()
		cancel()
		if !errors.Is(<-done, context.Canceled) {
			t.Fatal("read not canceled")
		}
		_ = s.Close()
		assertCleanupClosed(t, c, g)
		if c.writes.Load() != 0 {
			t.Fatal("cancellation sent input")
		}
	})
}
func TestTerminalCleanupCloseBudgetStartsBeforeControlWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, c, g := cleanupFixture(t, context.Background(), false)
		g.waitFn = func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
		start := time.Now()
		err := s.Close()
		if time.Since(start) != containerTerminationTimeout || !errors.Is(err, ErrContainerExitUnconfirmed) {
			t.Fatalf("elapsed=%v err=%v", time.Since(start), err)
		}
		assertCleanupClosed(t, c, g)
		if c.writes.Load() != 1 {
			t.Fatal("second control write attempted")
		}
	})
}
func TestTerminalCleanupCloseAbortsContendedWriter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, c, g := cleanupFixture(t, context.Background(), false)
		done := make(chan error, 1)
		go func() { _, err := s.Write([]byte("input")); done <- err }()
		synctest.Wait()
		start := time.Now()
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if time.Since(start) != 0 {
			t.Fatal("close waited for input deadline")
		}
		if <-done == nil || c.writes.Load() != 1 {
			t.Fatal("write not aborted or controls interleaved")
		}
		assertCleanupClosed(t, c, g)
	})
}
func TestTerminalCleanupCanceledCloseSendsNoControlInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s, c, g := cleanupFixture(t, ctx, true)
	cancel()
	g.waitFn = func(ctx context.Context) error {
		if ctx.Err() != nil {
			t.Fatal("cleanup inherited canceled parent")
		}
		return nil
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if c.writes.Load() != 0 {
		t.Fatal("canceled close sent controls")
	}
	assertCleanupClosed(t, c, g)
}
func TestTerminalCleanupCloseAlwaysAbortsOnEveryFailure(t *testing.T) {
	failure := errors.New("failure")
	for _, kind := range []string{"deadline", "short", "write", "inspect404", "inspectNetwork", "close"} {
		t.Run(kind, func(t *testing.T) {
			s, c, g := cleanupFixture(t, context.Background(), true)
			switch kind {
			case "deadline":
				c.deadlineFn = func(time.Time) error { return failure }
			case "short":
				c.writeFn = func([]byte) (int, error) { return 0, nil }
			case "write":
				c.writeFn = func([]byte) (int, error) { return 0, failure }
			case "inspect404", "inspectNetwork":
				g.waitFn = func(context.Context) error { return failure }
			case "close":
				c.closeFn = func() error { _ = c.Conn.Close(); return failure }
			}
			err := s.Close()
			if err == nil {
				t.Fatal("failure hidden")
			}
			assertCleanupClosed(t, c, g)
			unconfirmed := kind == "inspect404" || kind == "inspectNetwork"
			if errors.Is(err, ErrContainerExitUnconfirmed) != unconfirmed {
				t.Fatalf("incorrect exit confirmation: %v", err)
			}
			if s.Close() != err {
				t.Fatal("repeated result differs")
			}
		})
	}
}
func TestTerminalCleanupWriteDeadlineClearFailureAborts(t *testing.T) {
	s, c, g := cleanupFixture(t, context.Background(), true)
	failure := errors.New("clear deadline")
	c.deadlineFn = func(d time.Time) error {
		if d.IsZero() {
			return failure
		}
		return nil
	}
	if _, err := s.Write([]byte("input")); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	_ = s.Close()
	assertCleanupClosed(t, c, g)
}
func TestTerminalCleanupRepeatedCloseReturnsSameResult(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		s, c, g := cleanupFixture(t, ctx, true)
		g.waitFn = func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
		done := make(chan error, 2)
		go func() { done <- s.Close() }()
		go func() { done <- s.Close() }()
		cancel()
		a, b := <-done, <-done
		if a != b || !errors.Is(a, ErrContainerExitUnconfirmed) {
			t.Fatalf("results %v %v", a, b)
		}
		assertCleanupClosed(t, c, g)
	})
}
func TestTerminalCleanupOpenCanceledBeforePublish(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &cleanupConn{closeFn: func() error { return nil }}
	g := &cleanupGateway{attachment: ContainerAttachment{ExecID: "exec", Reader: c, Conn: c}, openFn: cancel}
	s, err := newContainerStarter(g).Start(ctx, StartRequest{})
	if s == nil || !errors.Is(err, context.Canceled) || ErrorCode(err) != "TERMINAL_SESSION_CANCELED" {
		t.Fatalf("session=%v error=%v", s, err)
	}
	if g.waits.Load() != 0 {
		t.Fatal("starter consumed the caller's cleanup ownership")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	assertCleanupClosed(t, c, g)
	if c.writes.Load() != 0 {
		t.Fatal("canceled unpublished session sent control input")
	}
}
func TestTerminalCleanupExitConfirmationIsNotTransportEOF(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, c, g := cleanupFixture(t, context.Background(), true)
		s.attachment.Reader = bytes.NewReader(nil)
		if _, err := s.Read(context.Background(), make([]byte, 1)); err != io.EOF {
			t.Fatal(err)
		}
		g.waitFn = func(ctx context.Context) error {
			if c.closes.Load() != 1 {
				t.Error("inspect before abort")
			}
			<-ctx.Done()
			return ctx.Err()
		}
		if !errors.Is(s.Close(), ErrContainerExitUnconfirmed) {
			t.Fatal("EOF falsely confirms exit")
		}
	})
}
func TestTerminalCleanupReadDropsBufferedOutputAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s, _, _ := cleanupFixture(t, ctx, true)
	s.attachment.Reader = bytes.NewBufferString("stale")
	cancel()
	if n, err := s.Read(context.Background(), make([]byte, 10)); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
func TestTerminalCleanupProbeReadClosesAttachedTransport(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		local, peer := net.Pipe()
		defer peer.Close()
		c := &cleanupConn{Conn: local}
		ctx, cancel := context.WithTimeout(context.Background(), containerShellProbeTimeout)
		defer cancel()
		output, err := readContainerProbe(ctx, c, c)
		if len(output) != 0 || !errors.Is(err, context.DeadlineExceeded) || c.closes.Load() != 1 {
			t.Fatalf("output=%q err=%v closes=%d", output, err, c.closes.Load())
		}
	})
}
func TestTerminalCleanupDeadlineCallbacksAreJoined(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		entered, release := make(chan struct{}), make(chan struct{})
		var complete atomic.Bool
		stop := onContextAbort(ctx, func() { close(entered); <-release; complete.Store(true) })
		cancel()
		<-entered
		done := make(chan struct{})
		go func() { stop(); close(done) }()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("returned before callback completion")
		default:
		}
		close(release)
		<-done
		if !complete.Load() {
			t.Fatal("callback incomplete")
		}
	})
}

// 编译期确认 fixture 保持真实 Conn 契约。
var _ net.Conn = (*cleanupConn)(nil)

func TestTerminalCleanupResizeDeadlineAndClose(t *testing.T) {
	for _, mode := range []string{"deadline", "close"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, c, g := cleanupFixture(t, context.Background(), true)
				g.resizeFn = func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
				done := make(chan error, 1)
				start := time.Now()
				go func() { done <- s.Resize(24, 80) }()
				synctest.Wait()
				if mode == "close" {
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
				}
				err := <-done
				if mode == "deadline" {
					if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != containerResizeTimeout {
						t.Fatalf("error=%v elapsed=%v", err, time.Since(start))
					}
					_ = s.Close()
				} else if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				assertCleanupClosed(t, c, g)
				if err := s.Resize(24, 80); err == nil || g.resizes.Load() != 1 {
					t.Fatal("closing resize admitted")
				}
			})
		})
	}
}

func TestTerminalCleanupQueuedOperationsRejectClosing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, c, _ := cleanupFixture(t, context.Background(), false)
		first, queued := make(chan error, 1), make(chan error, 2)
		go func() { _, err := s.Write([]byte("first")); first <- err }()
		synctest.Wait()
		go func() { _, err := s.Write([]byte("queued")); queued <- err }()
		go func() { queued <- s.Resize(24, 80) }()
		synctest.Wait()
		_ = s.Close()
		if <-first == nil || <-queued == nil || <-queued == nil || c.writes.Load() != 1 {
			t.Fatal("queued operation admitted after closing")
		}
	})
}

func TestTerminalCleanupReadChecksCancellationAfterBufferedRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s, _, _ := cleanupFixture(t, ctx, true)
	s.attachment.Reader = cleanupReaderFunc(func(p []byte) (int, error) { cancel(); return copy(p, "stale"), nil })
	if n, err := s.Read(context.Background(), make([]byte, 8)); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("n=%d error=%v", n, err)
	}
}

type cleanupReaderFunc func([]byte) (int, error)

func (f cleanupReaderFunc) Read(p []byte) (int, error) { return f(p) }

func TestTerminalCleanupCloseJoinsLocalRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, c, g := cleanupFixture(t, context.Background(), true)
		readReleased, readReturning := make(chan struct{}), make(chan struct{})
		s.attachment.Reader = cleanupReaderFunc(func(p []byte) (int, error) {
			_, err := c.Conn.Read(p)
			close(readReleased)
			<-readReturning
			return 0, err
		})
		readDone := make(chan error, 1)
		go func() { _, err := s.Read(context.Background(), make([]byte, 1)); readDone <- err }()
		synctest.Wait()
		closed := make(chan error, 1)
		go func() { closed <- s.Close() }()
		<-readReleased
		synctest.Wait()
		select {
		case <-closed:
			t.Fatal("Close returned before local Reader ended")
		default:
		}
		close(readReturning)
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
		if !errors.Is(<-readDone, context.Canceled) {
			t.Fatal("read did not end canceled")
		}
		if _, err := s.Read(context.Background(), make([]byte, 1)); err == nil {
			t.Fatal("read admitted after Close")
		}
		assertCleanupClosed(t, c, g)
	})
}

func TestTerminalCleanupCloseReportsUnfinishedLocalReadAtBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, c, _ := cleanupFixture(t, context.Background(), true)
		release := make(chan struct{})
		s.attachment.Reader = cleanupReaderFunc(func(p []byte) (int, error) { _, err := c.Conn.Read(p); <-release; return 0, err })
		readDone := make(chan struct{})
		go func() { defer close(readDone); _, _ = s.Read(context.Background(), make([]byte, 1)) }()
		synctest.Wait()
		start := time.Now()
		err := s.Close()
		if !errors.Is(err, ErrContainerCleanupUnconfirmed) || errors.Is(err, ErrContainerExitUnconfirmed) || time.Since(start) != containerTerminationTimeout {
			t.Fatalf("error=%v elapsed=%v", err, time.Since(start))
		}
		close(release)
		<-readDone
	})
}

func TestTerminalCleanupCloseReportsUnfinishedResizeAtBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _, g := cleanupFixture(t, context.Background(), true)
		release := make(chan struct{})
		g.resizeFn = func(ctx context.Context) error { <-ctx.Done(); <-release; return ctx.Err() }
		resized := make(chan error, 1)
		go func() { resized <- s.Resize(24, 80) }()
		synctest.Wait()
		start := time.Now()
		err := s.Close()
		if !errors.Is(err, ErrContainerCleanupUnconfirmed) || errors.Is(err, ErrContainerExitUnconfirmed) || time.Since(start) != containerTerminationTimeout {
			t.Fatalf("error=%v elapsed=%v", err, time.Since(start))
		}
		close(release)
		if <-resized == nil {
			t.Fatal("resize unexpectedly succeeded")
		}
	})
}

func TestTerminalCleanupReadDuringCloseReturnsStreamEnd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, c, _ := cleanupFixture(t, context.Background(), true)
		entered, release := make(chan struct{}), make(chan struct{})
		c.writeFn = func(p []byte) (int, error) {
			if c.writes.Load() == 1 {
				close(entered)
				<-release
			}
			return len(p), nil
		}
		done := make(chan error, 1)
		go func() { done <- s.Close() }()
		<-entered
		if n, err := s.Read(context.Background(), make([]byte, 1)); n != 0 || err != io.EOF {
			t.Fatalf("closing read n=%d err=%v", n, err)
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}
