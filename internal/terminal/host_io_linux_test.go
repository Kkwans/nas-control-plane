//go:build linux

package terminal

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

type hostFakeRawConn struct {
	t                                   *testing.T
	readCalls, writeCalls, controlCalls int
	err                                 error
	skipCallback                        bool
}

func (r *hostFakeRawConn) Control(fn func(uintptr)) error {
	r.controlCalls++
	if !r.skipCallback {
		fn(123)
	}
	return r.err
}
func (r *hostFakeRawConn) Read(fn func(uintptr) bool) error {
	r.readCalls++
	if !r.skipCallback && !fn(123) {
		r.t.Fatal("Read callback requested poller wait")
	}
	return r.err
}
func (r *hostFakeRawConn) Write(fn func(uintptr) bool) error {
	r.writeCalls++
	if !r.skipCallback && !fn(123) {
		r.t.Fatal("Write callback requested poller wait")
	}
	return r.err
}

func TestHostRawTransportOneShot(t *testing.T) {
	for _, op := range []string{"read", "write"} {
		for _, tc := range []struct {
			name string
			n    int
			err  error
		}{
			{"again", -1, unix.EAGAIN}, {"interrupt", -1, unix.EINTR},
			{"partial", 2, unix.EIO}, {"success", 3, nil}, {"zero", 0, nil},
		} {
			t.Run(op+"/"+tc.name, func(t *testing.T) {
				raw := &hostFakeRawConn{t: t}
				transport := newHostPTYTransport(nil)
				transport.raw = raw
				calls := 0
				syscall := func(fd int, p []byte) (int, error) {
					calls++
					if fd != 123 || string(p) != "abc" {
						t.Fatalf("unexpected descriptor/buffer: %d %q", fd, p)
					}
					return tc.n, tc.err
				}
				transport.ops.read, transport.ops.write = syscall, syscall
				try := transport.TryRead
				if op == "write" {
					try = transport.TryWrite
				}
				n, err := try([]byte("abc"))
				wantN := max(0, tc.n)
				wantErr := tc.err
				if op == "read" && tc.n == 0 && wantErr == nil {
					wantErr = io.EOF
				}
				if n != wantN || !errors.Is(err, wantErr) {
					t.Fatalf("got %d,%v; want %d,%v", n, err, wantN, wantErr)
				}
				if calls != 1 || raw.readCalls+raw.writeCalls != 1 || raw.controlCalls != 0 {
					t.Fatalf("not one-shot: syscall=%d raw=%+v", calls, raw)
				}
			})
		}
	}
}

func TestHostRawTransportRawErrors(t *testing.T) {
	rawErr := errors.New("raw connection error")
	for _, skip := range []bool{false, true} {
		for _, op := range []string{"read", "write", "resize", "init"} {
			t.Run(op+map[bool]string{false: "/callback", true: "/no-callback"}[skip], func(t *testing.T) {
				raw := &hostFakeRawConn{t: t, err: rawErr, skipCallback: skip}
				transport := newHostPTYTransport(nil)
				transport.raw = raw
				transport.ops.read = func(int, []byte) (int, error) { return 2, unix.EIO }
				transport.ops.write = transport.ops.read
				transport.ops.setNonblock = func(int, bool) error { return unix.EIO }
				transport.ops.setWinsize = func(int, uint, *unix.Winsize) error { return unix.EIO }
				var err error
				var n int
				switch op {
				case "read":
					n, err = transport.TryRead([]byte("abc"))
				case "write":
					n, err = transport.TryWrite([]byte("abc"))
				case "resize":
					err = transport.Resize(24, 80)
				case "init":
					err = transport.initNonblock()
				}
				if !errors.Is(err, rawErr) || (!skip && !errors.Is(err, unix.EIO)) {
					t.Fatalf("lost error: %v", err)
				}
				if (op == "read" || op == "write") && n != map[bool]int{false: 2, true: 0}[skip] {
					t.Fatalf("lost byte count: %d", n)
				}
			})
		}
	}
}

func TestHostRawTransportControlAndEmpty(t *testing.T) {
	raw := &hostFakeRawConn{t: t}
	transport := newHostPTYTransport(nil)
	if transport.raw != nil {
		t.Fatal("constructor initialized RawConn")
	}
	transport.raw = raw
	for _, try := range []func([]byte) (int, error){transport.TryRead, transport.TryWrite} {
		if n, err := try(nil); n != 0 || err != nil {
			t.Fatalf("empty buffer: %d,%v", n, err)
		}
	}
	if raw.readCalls != 0 || raw.writeCalls != 0 {
		t.Fatal("empty buffer issued syscall")
	}
	setNonblockCalls, resizeCalls := 0, 0
	transport.ops.setNonblock = func(fd int, nonblock bool) error {
		setNonblockCalls++
		if fd != 123 || !nonblock {
			t.Fatalf("invalid nonblock args: %d %v", fd, nonblock)
		}
		return nil
	}
	transport.ops.setWinsize = func(fd int, req uint, size *unix.Winsize) error {
		resizeCalls++
		if fd != 123 || req != unix.TIOCSWINSZ || size.Row != 41 || size.Col != 132 {
			t.Fatalf("invalid resize args: %d %d %+v", fd, req, size)
		}
		return nil
	}
	if err := transport.initNonblock(); err != nil {
		t.Fatal(err)
	}
	if err := transport.Resize(41, 132); err != nil {
		t.Fatal(err)
	}
	if setNonblockCalls != 1 || resizeCalls != 1 || raw.controlCalls != 2 {
		t.Fatalf("unexpected Control calls: %+v", raw)
	}
}

// 只供已授权的隔离 Linux CI；本地验证必须精确选择 TestHostRawTransport。
func TestHostPTYTransportLinuxCI(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	transport := newHostPTYTransport(master)
	t.Cleanup(func() { _ = transport.Close() })
	if err = transport.Init(); err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		if err = transport.Resize(uint16(24+i), 80); err != nil {
			t.Fatal(err)
		}
		var flags int
		var opErr error
		if err = transport.raw.Control(func(fd uintptr) { flags, opErr = unix.FcntlInt(fd, unix.F_GETFL, 0) }); err != nil {
			t.Fatal(err)
		}
		if opErr != nil || flags&unix.O_NONBLOCK == 0 {
			t.Fatalf("resize lost nonblock: flags=%d err=%v", flags, opErr)
		}
	}
	if n, err := transport.TryRead(make([]byte, 1)); n != 0 || !errors.Is(err, unix.EAGAIN) {
		t.Fatalf("empty PTY read: %d %v", n, err)
	}
	if err = transport.Close(); err != nil {
		t.Fatal(err)
	}
	if err = transport.Close(); err != nil {
		t.Fatalf("repeat close changed result: %v", err)
	}
	if _, err = transport.TryRead(make([]byte, 1)); err == nil {
		t.Fatal("read after close succeeded")
	}
}

// 只供已授权的隔离 Linux CI；不启动 shell，slave 保持无人消费。
func TestHostPTYTransportSessionLinuxCI(t *testing.T) {
	for _, operation := range []string{"read-cancel", "write-budget"} {
		t.Run(operation, func(t *testing.T) {
			master, slave, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = slave.Close() })
			// 原始模式避免 canonical 输入缓冲区饱和时丢弃字符。
			raw, err := slave.SyscallConn()
			if err != nil {
				_ = master.Close()
				t.Fatal(err)
			}
			var opErr error
			err = raw.Control(func(fd uintptr) {
				term, getErr := unix.IoctlGetTermios(int(fd), unix.TCGETS)
				if getErr != nil {
					opErr = getErr
					return
				}
				term.Iflag &^= unix.IXON | unix.ICRNL
				term.Oflag &^= unix.OPOST
				term.Lflag &^= unix.ICANON | unix.ECHO | unix.ISIG
				term.Cc[unix.VMIN], term.Cc[unix.VTIME] = 1, 0
				opErr = unix.IoctlSetTermios(int(fd), unix.TCSETS, term)
			})
			if err != nil || opErr != nil {
				_ = master.Close()
				t.Fatal(errors.Join(err, opErr))
			}
			transport := newHostPTYTransport(master)
			if err = transport.Init(); err != nil {
				_ = transport.Close()
				t.Fatal(err)
			}
			waitDone := make(chan struct{})
			// 这里只验证真实 descriptor I/O；退出凭据由 fake worker 提供。
			session := newHostSession(context.Background(), transport,
				func() error { close(waitDone); return nil },
				func() hostWaitResult { <-waitDone; return hostWaitResult{reaped: true} })
			t.Cleanup(func() {
				if err := session.Close(); err != nil {
					t.Error(err)
				}
			})
			type outcome struct {
				n   int
				err error
			}
			done := make(chan outcome, 1)
			const inputSize = 4 << 20
			go func() {
				if operation == "read-cancel" {
					ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
					defer cancel()
					n, err := session.Read(ctx, make([]byte, 1))
					done <- outcome{n, err}
				} else {
					n, err := session.Write(make([]byte, inputSize))
					done <- outcome{n, err}
				}
			}()
			var result outcome
			select {
			case result = <-done:
			case <-time.After(hostOperationTimeout + 2*time.Second):
				// watchdog 只关闭本测试拥有的两个端点；不能假装内核阻塞已被抢占。
				_ = slave.Close()
				closeErr := session.Close()
				select {
				case <-done:
					t.Fatalf("PTY operation exceeded CI tolerance; cleanup=%v", closeErr)
				case <-time.After(2 * time.Second):
					t.Fatalf("PTY operation owner still unconfirmed after watchdog; cleanup=%v", closeErr)
				}
			}
			if operation == "read-cancel" {
				if result.n != 0 || !errors.Is(result.err, context.DeadlineExceeded) {
					t.Fatalf("read cancel: %d %v", result.n, result.err)
				}
			} else if result.n <= 0 || result.n >= inputSize || !errors.Is(result.err, context.DeadlineExceeded) {
				t.Fatalf("full PTY write: %d %v", result.n, result.err)
			}
		})
	}
}
