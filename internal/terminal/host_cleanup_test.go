//go:build linux

package terminal

import (
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

// 仅使用内存 fake；这些测试不会创建 PTY、shell、子进程或监听器。
type hostCleanupTransport struct {
	read   func([]byte) (int, error)
	write  func([]byte) (int, error)
	resize func(uint16, uint16) error
	close  func() error
	closes atomic.Int32
}

func (f *hostCleanupTransport) TryRead(p []byte) (int, error) {
	if f.read != nil {
		return f.read(p)
	}
	return 0, syscall.EAGAIN
}
func (f *hostCleanupTransport) TryWrite(p []byte) (int, error) {
	if f.write != nil {
		return f.write(p)
	}
	return len(p), nil
}
func (f *hostCleanupTransport) Resize(r, c uint16) error {
	if f.resize != nil {
		return f.resize(r, c)
	}
	return nil
}
func (f *hostCleanupTransport) Close() error {
	f.closes.Add(1)
	if f.close != nil {
		return f.close()
	}
	return nil
}

type hostCleanupFixture struct {
	s            *hostSession
	f            *hostCleanupTransport
	receipt      chan hostWaitResult
	kills, waits atomic.Int32
	once         sync.Once
}

func newHostCleanupFixture(t *testing.T, parent context.Context, f *hostCleanupTransport) *hostCleanupFixture {
	t.Helper()
	h := &hostCleanupFixture{f: f, receipt: make(chan hostWaitResult, 1)}
	h.s = newHostSession(parent, f, func() error { h.kills.Add(1); return nil }, func() hostWaitResult { h.waits.Add(1); return <-h.receipt })
	t.Cleanup(func() {
		h.reap(hostWaitResult{reaped: true})
		h.s.Close()
		<-h.s.waitDone
		<-h.s.abortDone
		<-h.s.watcherDone
		synctest.Wait()
		if h.waits.Load() != 1 || f.closes.Load() != 1 {
			t.Errorf("owners: wait=%d close=%d", h.waits.Load(), f.closes.Load())
		}
	})
	return h
}
func (h *hostCleanupFixture) reap(r hostWaitResult) { h.once.Do(func() { h.receipt <- r }) }
func hostCleanupWantError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error=%v, want %v", got, want)
	}
}

func TestHostCleanupReadCancellation(t *testing.T) {
	for _, parentCancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "caller", true: "parent"}[parentCancel], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				parent, cancelParent := context.WithCancel(context.Background())
				defer cancelParent()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var calls atomic.Int32
				h := newHostCleanupFixture(t, parent, &hostCleanupTransport{read: func([]byte) (int, error) { calls.Add(1); return 0, syscall.EAGAIN }})
				done := make(chan error, 1)
				go func() { _, err := h.s.Read(ctx, make([]byte, 8)); done <- err }()
				synctest.Wait()
				if calls.Load() != 1 {
					t.Fatalf("read calls=%d", calls.Load())
				}
				if parentCancel {
					cancelParent()
				} else {
					cancel()
				}
				hostCleanupWantError(t, <-done, context.Canceled)
			})
		})
	}
	t.Run("already canceled", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			h := newHostCleanupFixture(t, context.Background(), &hostCleanupTransport{read: func([]byte) (int, error) { t.Error("read after cancellation"); return 1, nil }})
			n, err := h.s.Read(ctx, make([]byte, 8))
			if n != 0 {
				t.Fatalf("n=%d", n)
			}
			hostCleanupWantError(t, err, context.Canceled)
		})
	})
	t.Run("discard late bytes", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h := newHostCleanupFixture(t, context.Background(), &hostCleanupTransport{read: func(p []byte) (int, error) { copy(p, "late"); cancel(); return 4, nil }})
			n, err := h.s.Read(ctx, make([]byte, 8))
			if n != 0 {
				t.Fatalf("delivered %d canceled bytes", n)
			}
			hostCleanupWantError(t, err, context.Canceled)
		})
	})
	t.Run("EINTR rechecks cancellation", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			h := newHostCleanupFixture(t, context.Background(), &hostCleanupTransport{read: func([]byte) (int, error) { calls++; cancel(); return 0, syscall.EINTR }})
			_, err := h.s.Read(ctx, make([]byte, 8))
			hostCleanupWantError(t, err, context.Canceled)
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
		})
	})
	t.Run("zero read is EOF", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := newHostCleanupFixture(t, context.Background(), &hostCleanupTransport{read: func([]byte) (int, error) { return 0, nil }})
			_, err := h.s.Read(context.Background(), make([]byte, 1))
			hostCleanupWantError(t, err, io.EOF)
		})
	})
}

func TestHostCleanupWriteBudget(t *testing.T) {
	for _, zero := range []bool{false, true} {
		t.Run(map[bool]string{false: "EAGAIN", true: "zero progress"}[zero], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				h := newHostCleanupFixture(t, context.Background(), &hostCleanupTransport{write: func([]byte) (int, error) {
					calls++
					if zero {
						return 0, nil
					}
					return 0, syscall.EAGAIN
				}})
				start := time.Now()
				n, err := h.s.Write([]byte("abc"))
				hostCleanupWantError(t, err, context.DeadlineExceeded)
				if n != 0 || time.Since(start) != hostOperationTimeout {
					t.Fatalf("n=%d elapsed=%v", n, time.Since(start))
				}
				if calls < 2 || calls > 201 {
					t.Fatalf("unexpected retry count %d", calls)
				}
				synctest.Wait()
				if h.f.closes.Load() != 1 {
					t.Fatal("failed write did not abort")
				}
			})
		})
	}
	t.Run("gate consumes total budget", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := newHostCleanupFixture(t, context.Background(), &hostCleanupTransport{write: func([]byte) (int, error) { return 0, syscall.EAGAIN }})
			h.s.inputGate <- struct{}{}
			go func() { time.Sleep(time.Second); <-h.s.inputGate }()
			start := time.Now()
			_, err := h.s.Write([]byte("abc"))
			hostCleanupWantError(t, err, context.DeadlineExceeded)
			if time.Since(start) != hostOperationTimeout {
				t.Fatalf("budget restarted after gate: %v", time.Since(start))
			}
		})
	})
	t.Run("partial suffix survives EINTR", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var seen []string
			h := newHostCleanupFixture(t, context.Background(), &hostCleanupTransport{write: func(p []byte) (int, error) {
				seen = append(seen, string(p))
				switch len(seen) {
				case 1:
					return 2, nil
				case 2:
					return 1, syscall.EINTR
				case 3:
					return 0, syscall.EAGAIN
				default:
					return len(p), nil
				}
			}})
			n, err := h.s.Write([]byte("abcde"))
			if n != 5 || err != nil {
				t.Fatalf("write=(%d,%v)", n, err)
			}
			if !reflect.DeepEqual(seen, []string{"abcde", "cde", "de", "de"}) {
				t.Fatalf("suffixes=%q", seen)
			}
		})
	})
	t.Run("EINTR cancellation preserves count", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			h := newHostCleanupFixture(t, parent, &hostCleanupTransport{write: func([]byte) (int, error) { calls++; cancel(); return 2, syscall.EINTR }})
			n, err := h.s.Write([]byte("abc"))
			hostCleanupWantError(t, err, context.Canceled)
			if n != 2 || calls != 1 {
				t.Fatalf("n=%d calls=%d", n, calls)
			}
		})
	})
	t.Run("fatal error preserves count and aborts", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			failure := errors.New("fake write failure")
			h := newHostCleanupFixture(t, context.Background(), &hostCleanupTransport{write: func([]byte) (int, error) { return 2, failure }})
			n, err := h.s.Write([]byte("abc"))
			hostCleanupWantError(t, err, failure)
			if n != 2 {
				t.Fatalf("n=%d", n)
			}
			synctest.Wait()
			if h.f.closes.Load() != 1 {
				t.Fatal("fatal error did not abort")
			}
		})
	})
}

func TestHostCleanupResizeCancellation(t *testing.T) {
	t.Run("queued cancellation never calls ioctl", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			h := newHostCleanupFixture(t, parent, &hostCleanupTransport{resize: func(uint16, uint16) error { t.Error("ioctl after cancellation"); return nil }})
			h.s.inputGate <- struct{}{}
			done := make(chan error, 1)
			go func() { done <- h.s.Resize(24, 80) }()
			synctest.Wait()
			cancel()
			hostCleanupWantError(t, <-done, context.Canceled)
			<-h.s.inputGate
		})
	})
	t.Run("gate timeout", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := newHostCleanupFixture(t, context.Background(), &hostCleanupTransport{resize: func(uint16, uint16) error { t.Error("ioctl with unavailable gate"); return nil }})
			h.s.inputGate <- struct{}{}
			start := time.Now()
			err := h.s.Resize(24, 80)
			hostCleanupWantError(t, err, context.DeadlineExceeded)
			if time.Since(start) != hostOperationTimeout {
				t.Fatalf("elapsed=%v", time.Since(start))
			}
			<-h.s.inputGate
		})
	})
	t.Run("stuck ioctl is honestly unconfirmed", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			release := make(chan struct{})
			entered := make(chan struct{})
			h := newHostCleanupFixture(t, context.Background(), &hostCleanupTransport{resize: func(uint16, uint16) error { close(entered); <-release; return nil }})
			done := make(chan error, 1)
			go func() { done <- h.s.Resize(24, 80) }()
			<-entered
			h.reap(hostWaitResult{reaped: true})
			synctest.Wait()
			start := time.Now()
			err := h.s.Close()
			hostCleanupWantError(t, err, ErrHostCleanupUnconfirmed)
			if errors.Is(err, ErrHostExitUnconfirmed) || time.Since(start) != hostOperationTimeout {
				t.Fatalf("close=%v elapsed=%v", err, time.Since(start))
			}
			close(release)
			if resizeErr := <-done; !errors.Is(resizeErr, os.ErrClosed) && !errors.Is(resizeErr, context.DeadlineExceeded) {
				t.Fatalf("late ioctl error=%v", resizeErr)
			}
			if h.s.Close() != err {
				t.Fatal("late ioctl changed published error")
			}
		})
	})
}

func TestHostCleanupCloseReceipt(t *testing.T) {
	t.Run("kill is not a reap receipt", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := newHostCleanupFixture(t, context.Background(), &hostCleanupTransport{})
			done := make(chan error, 1)
			go func() { done <- h.s.Close() }()
			synctest.Wait()
			if h.kills.Load() != 1 {
				t.Fatal("kill not attempted")
			}
			select {
			case err := <-done:
				t.Fatalf("premature close=%v", err)
			default:
			}
			h.reap(hostWaitResult{reaped: true})
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	})
	for _, reaped := range []bool{false, true} {
		t.Run(map[bool]string{false: "invalid receipt", true: "nonzero or signal reaped"}[reaped], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				waitErr := errors.New("fake nonzero wait result")
				h := newHostCleanupFixture(t, context.Background(), &hostCleanupTransport{})
				h.reap(hostWaitResult{reaped: reaped, err: waitErr})
				synctest.Wait()
				err := h.s.Close()
				if reaped {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					hostCleanupWantError(t, err, ErrHostExitUnconfirmed)
					hostCleanupWantError(t, err, waitErr)
				}
			})
		})
	}
	t.Run("timeout persists after late reap", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := newHostCleanupFixture(t, context.Background(), &hostCleanupTransport{})
			start := time.Now()
			err := h.s.Close()
			hostCleanupWantError(t, err, ErrHostExitUnconfirmed)
			if errors.Is(err, ErrHostCleanupUnconfirmed) || time.Since(start) != hostOperationTimeout {
				t.Fatalf("close=%v elapsed=%v", err, time.Since(start))
			}
			h.reap(hostWaitResult{reaped: true})
			synctest.Wait()
			if h.s.Close() != err {
				t.Fatal("late reap changed published error")
			}
		})
	})
}

func TestHostCleanupCloseErrorsPersist(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		killErr, closeErr := errors.New("fake kill failure"), errors.New("fake close failure")
		release := make(chan struct{})
		entered := make(chan struct{})
		f := &hostCleanupTransport{close: func() error { return closeErr }, read: func([]byte) (int, error) { close(entered); <-release; return 0, syscall.EAGAIN }}
		h := newHostCleanupFixture(t, context.Background(), f)
		h.s.kill = func() error { h.kills.Add(1); return killErr }
		readDone := make(chan error, 1)
		go func() { _, err := h.s.Read(context.Background(), make([]byte, 1)); readDone <- err }()
		<-entered
		const callers = 12
		results := make(chan error, callers)
		for range callers {
			go func() { results <- h.s.Close() }()
		}
		synctest.Wait()
		time.Sleep(hostOperationTimeout)
		synctest.Wait()
		first := <-results
		for _, want := range []error{killErr, closeErr, ErrHostCleanupUnconfirmed, ErrHostExitUnconfirmed} {
			hostCleanupWantError(t, first, want)
		}
		for i := 1; i < callers; i++ {
			if got := <-results; got != first {
				t.Fatalf("concurrent close error differs: %v", got)
			}
		}
		if h.kills.Load() != 1 || f.closes.Load() != 1 {
			t.Fatalf("kill=%d close=%d", h.kills.Load(), f.closes.Load())
		}
		close(release)
		hostCleanupWantError(t, <-readDone, os.ErrClosed)
		h.reap(hostWaitResult{reaped: true})
		synctest.Wait()
		if h.s.Close() != first {
			t.Fatal("result changed after owners finished")
		}
	})
}

func TestHostCleanupLifecycleCallback(t *testing.T) {
	for _, natural := range []bool{false, true} {
		t.Run(map[bool]string{false: "parent canceled", true: "natural exit"}[natural], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				parent, cancel := context.WithCancel(context.Background())
				defer cancel()
				h := newHostCleanupFixture(t, parent, &hostCleanupTransport{})
				if natural {
					h.reap(hostWaitResult{reaped: true})
				} else {
					cancel()
				}
				synctest.Wait()
				if natural {
					select {
					case <-h.s.watcherDone:
						t.Fatal("watcher abandoned owned unread master")
					default:
					}

					if h.f.closes.Load() != 0 {
						t.Fatal("natural exit discarded unread tail")
					}
					_, err := h.s.Read(context.Background(), make([]byte, 1))
					hostCleanupWantError(t, err, io.EOF)
					synctest.Wait()
				}
				select {
				case <-h.s.watcherDone:
				default:
					t.Fatal("parent watcher retained after local cleanup")
				}
				select {
				case <-h.s.abortDone:
				default:
					t.Fatal("local close not completed")
				}
				if h.kills.Load() != 0 {
					t.Fatal("local watcher killed child")
				}
				cancel()
				h.reap(hostWaitResult{reaped: true})
				if err := h.s.Close(); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestHostCleanupStartOwnership(t *testing.T) {
	t.Run("initialization failure joins cleanup", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			initErr, closeErr := errors.New("fake nonblocking initialization failure"), errors.New("fake transport close failure")
			h := newHostCleanupFixture(t, context.Background(), &hostCleanupTransport{close: func() error { return closeErr }})
			calls := 0
			start := time.Now()
			err := h.s.initialize(func() error { calls++; return initErr })
			for _, want := range []error{initErr, closeErr, ErrHostExitUnconfirmed} {
				hostCleanupWantError(t, err, want)
			}
			if calls != 1 || h.kills.Load() != 1 || h.f.closes.Load() != 1 || time.Since(start) != hostOperationTimeout {
				t.Fatalf("init=%d kill=%d close=%d elapsed=%v", calls, h.kills.Load(), h.f.closes.Load(), time.Since(start))
			}
			h.reap(hostWaitResult{reaped: true})
			synctest.Wait()
		})
	})
	t.Run("late parent cancel cannot deliver session", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			h := newHostCleanupFixture(t, parent, &hostCleanupTransport{})
			if err := h.s.initialize(func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			cancel()
			session, err := h.s.started()
			if session != nil {
				t.Fatal("delivered canceled session")
			}
			hostCleanupWantError(t, err, context.Canceled)
			hostCleanupWantError(t, err, ErrHostExitUnconfirmed)
			if ErrorCode(err) != "TERMINAL_SESSION_CANCELED" {
				t.Fatalf("code=%q", ErrorCode(err))
			}
			if h.kills.Load() != 1 || h.f.closes.Load() != 1 {
				t.Fatalf("kill=%d close=%d", h.kills.Load(), h.f.closes.Load())
			}
			h.reap(hostWaitResult{reaped: true})
			synctest.Wait()
		})
	})
	t.Run("successful initialization keeps ownership", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := newHostCleanupFixture(t, context.Background(), &hostCleanupTransport{})
			if err := h.s.initialize(func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			session, err := h.s.started()
			if session != h.s || err != nil {
				t.Fatalf("session=%v err=%v", session, err)
			}
			if h.kills.Load() != 0 || h.f.closes.Load() != 0 {
				t.Fatal("successful start closed its owner")
			}
		})
	})
}

func TestHostCleanupCloseInterruptsOperations(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var reads, writes atomic.Int32
		h := newHostCleanupFixture(t, context.Background(), &hostCleanupTransport{
			read:   func([]byte) (int, error) { reads.Add(1); return 0, syscall.EAGAIN },
			write:  func([]byte) (int, error) { writes.Add(1); return 0, syscall.EAGAIN },
			resize: func(uint16, uint16) error { t.Error("queued resize passed closing gate"); return nil },
		})
		h.s.kill = func() error { h.kills.Add(1); h.reap(hostWaitResult{reaped: true}); return os.ErrProcessDone }
		readDone, writeDone, resizeDone := make(chan error, 1), make(chan error, 1), make(chan error, 1)
		go func() { _, err := h.s.Read(context.Background(), make([]byte, 1)); readDone <- err }()
		go func() { _, err := h.s.Write([]byte("abc")); writeDone <- err }()
		synctest.Wait()
		if reads.Load() != 1 || writes.Load() != 1 {
			t.Fatalf("reads=%d writes=%d", reads.Load(), writes.Load())
		}
		go func() { resizeDone <- h.s.Resize(24, 80) }()
		synctest.Wait()
		start := time.Now()
		if err := h.s.Close(); err != nil {
			t.Fatal(err)
		}
		if time.Since(start) != 0 {
			t.Fatalf("Close waited for gates: %v", time.Since(start))
		}
		for _, done := range []chan error{readDone, writeDone, resizeDone} {
			hostCleanupWantError(t, <-done, os.ErrClosed)
		}
		if h.kills.Load() != 1 {
			t.Fatalf("kills=%d", h.kills.Load())
		}
	})
}

func TestHostCleanupNaturalExitDrainsTail(t *testing.T) {
	for _, endErr := range []error{syscall.EAGAIN, nil, syscall.EIO} {
		name := "zero EOF"
		if endErr != nil {
			name = endErr.Error()
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				h := newHostCleanupFixture(t, context.Background(), &hostCleanupTransport{read: func(p []byte) (int, error) {
					calls++
					switch calls {
					case 1:
						return copy(p, "final "), nil
					case 2:
						return copy(p, "output"), nil
					default:
						return 0, endErr
					}
				}})
				h.reap(hostWaitResult{reaped: true})
				synctest.Wait()
				if h.f.closes.Load() != 0 {
					t.Fatal("reap closed unread tail")
				}
				start := time.Now()
				var output string
				for range 2 {
					p := make([]byte, 16)
					n, err := h.s.Read(context.Background(), p)
					if err != nil {
						t.Fatal(err)
					}
					output += string(p[:n])
				}
				if output != "final output" {
					t.Fatalf("tail=%q", output)
				}
				n, err := h.s.Read(context.Background(), make([]byte, 16))
				want := error(io.EOF)
				if endErr == syscall.EIO {
					want = endErr
				}
				hostCleanupWantError(t, err, want)
				if n != 0 || calls != 3 || time.Since(start) != 0 {
					t.Fatalf("n=%d calls=%d waited=%v", n, calls, time.Since(start))
				}
				synctest.Wait()
				if h.f.closes.Load() != 1 {
					t.Fatal("drained tail did not release transport")
				}
			})
		})
	}
	t.Run("canceled late tail is not delivered", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h := newHostCleanupFixture(t, context.Background(), &hostCleanupTransport{read: func(p []byte) (int, error) { cancel(); return copy(p, "tail"), nil }})
			h.reap(hostWaitResult{reaped: true})
			synctest.Wait()
			n, err := h.s.Read(ctx, make([]byte, 16))
			hostCleanupWantError(t, err, context.Canceled)
			if n != 0 {
				t.Fatalf("delivered canceled tail: %d", n)
			}
		})
	})
	t.Run("input rejected without discarding tail", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			calls := 0
			h := newHostCleanupFixture(t, context.Background(), &hostCleanupTransport{
				read: func(p []byte) (int, error) {
					calls++
					if calls == 1 {
						return copy(p, "tail"), nil
					}
					return 0, syscall.EAGAIN
				},
				write:  func([]byte) (int, error) { t.Error("write after reap"); return 0, nil },
				resize: func(uint16, uint16) error { t.Error("resize after reap"); return nil },
			})
			h.reap(hostWaitResult{reaped: true})
			synctest.Wait()
			hostCleanupWantError(t, h.s.Resize(24, 80), os.ErrClosed)
			for _, input := range [][]byte{[]byte("input"), nil} {
				n, err := h.s.Write(input)
				hostCleanupWantError(t, err, os.ErrClosed)
				if n != 0 {
					t.Fatalf("n=%d", n)
				}
			}
			synctest.Wait()
			if h.f.closes.Load() != 0 {
				t.Fatal("rejected input closed unread tail")
			}
			p := make([]byte, 16)
			n, err := h.s.Read(context.Background(), p)
			if err != nil || string(p[:n]) != "tail" {
				t.Fatalf("tail=%q err=%v", p[:n], err)
			}
			_, err = h.s.Read(context.Background(), p)
			hostCleanupWantError(t, err, io.EOF)
			synctest.Wait()
			if h.f.closes.Load() != 1 {
				t.Fatal("drained tail did not close master")
			}
		})
	})
}

func TestHostCleanupGateCancelCloseRace(t *testing.T) {
	for _, operation := range []string{"read", "write", "resize"} {
		t.Run(operation, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				parent, cancel := context.WithCancel(context.Background())
				defer cancel()
				h := newHostCleanupFixture(t, parent, &hostCleanupTransport{
					read:   func([]byte) (int, error) { t.Error("read after cancellation"); return 0, nil },
					write:  func([]byte) (int, error) { t.Error("write after cancellation"); return 0, nil },
					resize: func(uint16, uint16) error { t.Error("resize after cancellation"); return nil },
				})
				gate := h.s.inputGate
				if operation == "read" {
					gate = h.s.readGate
				}
				gate <- struct{}{}
				done := make(chan error, 1)
				go func() {
					switch operation {
					case "read":
						_, err := h.s.Read(context.Background(), make([]byte, 1))
						done <- err
					case "write":
						_, err := h.s.Write([]byte("x"))
						done <- err
					default:
						done <- h.s.Resize(24, 80)
					}
				}()
				synctest.Wait()
				cancel()
				<-gate
				h.reap(hostWaitResult{reaped: true})
				closed := make(chan error, 1)
				go func() { closed <- h.s.Close() }()
				hostCleanupWantError(t, <-done, context.Canceled)
				if err := <-closed; err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestHostCleanupNaturalExitThenParentCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()
		h := newHostCleanupFixture(t, parent, &hostCleanupTransport{read: func([]byte) (int, error) { t.Error("unexpected read"); return 0, nil }})
		h.reap(hostWaitResult{reaped: true})
		synctest.Wait()
		if h.f.closes.Load() != 0 {
			t.Fatal("natural receipt discarded owned tail")
		}
		select {
		case <-h.s.watcherDone:
			t.Fatal("watcher stopped while master remains owned")
		default:
		}
		cancel()
		synctest.Wait()
		select {
		case <-h.s.watcherDone:
		default:
			t.Fatal("parent cancel did not finish watcher")
		}
		select {
		case <-h.s.abortDone:
		default:
			t.Fatal("parent cancel did not close unread master")
		}
		if h.f.closes.Load() != 1 || h.kills.Load() != 0 {
			t.Fatalf("local cancellation close=%d kill=%d", h.f.closes.Load(), h.kills.Load())
		}
		if err := h.s.Close(); err != nil {
			t.Fatal(err)
		}
	})
}
