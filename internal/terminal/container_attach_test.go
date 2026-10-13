package terminal

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/moby/moby/client"
)

type attachMemoryConn struct {
	eof          bool
	response     string
	blockWrite   bool
	entered      chan struct{}
	closed       chan struct{}
	signal       sync.Once
	closeOnce    sync.Once
	closes       atomic.Int32
	closeErr     error
	closeEntered chan struct{}
	closeRelease chan struct{}
}

func newAttachMemoryConn(response string) *attachMemoryConn {
	return &attachMemoryConn{response: response, entered: make(chan struct{}), closed: make(chan struct{})}
}
func (c *attachMemoryConn) block() (int, error) {
	c.signal.Do(func() { close(c.entered) })
	<-c.closed
	return 0, net.ErrClosed
}
func (c *attachMemoryConn) Read(p []byte) (int, error) {
	if c.response != "" {
		n := copy(p, c.response)
		c.response = c.response[n:]
		return n, nil
	}
	if c.eof {
		return 0, io.EOF
	}
	return c.block()
}
func (c *attachMemoryConn) Write(p []byte) (int, error) {
	if c.blockWrite {
		return c.block()
	}
	return len(p), nil
}
func (c *attachMemoryConn) Close() error {
	c.closes.Add(1)
	c.closeOnce.Do(func() { close(c.closed) })
	if c.closeEntered != nil {
		close(c.closeEntered)
		<-c.closeRelease
	}
	return c.closeErr
}
func (*attachMemoryConn) LocalAddr() net.Addr                                      { return nil }
func (*attachMemoryConn) RemoteAddr() net.Addr                                     { return nil }
func (*attachMemoryConn) SetDeadline(time.Time) error                              { return nil }
func (*attachMemoryConn) SetReadDeadline(time.Time) error                          { return nil }
func (*attachMemoryConn) SetWriteDeadline(time.Time) error                         { return nil }
func (c *attachMemoryConn) dial(context.Context, string, string) (net.Conn, error) { return c, nil }

const attachUpgrade = "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n"

func TestContainerAttachHandshakeCancellation(t *testing.T) {
	for _, stage := range []string{"write", "read", "error-body"} {
		for _, deadline := range []bool{false, true} {
			t.Run(stage+map[bool]string{false: "/cancel", true: "/deadline"}[deadline], func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					conn := newAttachMemoryConn("")
					conn.blockWrite = stage == "write"
					if stage == "error-body" {
						conn.response = "HTTP/1.1 400 Bad Request\r\nContent-Length: 10\r\n\r\n"
					}
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					done := make(chan error, 1)
					go func() {
						_, err := attachContainerExec(ctx, "1.55", "fake", client.ExecAttachOptions{}, conn.dial)
						done <- err
					}()
					<-conn.entered
					want := error(context.Canceled)
					if deadline {
						time.Sleep(2 * time.Second)
						want = context.DeadlineExceeded
					} else {
						cancel()
					}
					err := <-done
					if !errors.Is(err, want) || !errors.Is(err, ErrContainerExitUnconfirmed) {
						t.Fatalf("missing causes: %v", err)
					}
					if conn.closes.Load() != 1 {
						t.Fatalf("closes=%d", conn.closes.Load())
					}
				})
			})
		}
	}
}

func TestContainerAttachPrefetchAndBudgetHandoff(t *testing.T) {
	for _, payload := range []string{"PTY first bytes", string([]byte{1, 0, 0, 0, 0, 0, 0, 3}) + "out"} {
		t.Run(payload[:1], func(t *testing.T) {
			conn := newAttachMemoryConn(attachUpgrade + payload)
			ctx, cancel := context.WithCancel(context.Background())
			result, err := attachContainerExec(ctx, "1.55", "fake", client.ExecAttachOptions{}, conn.dial)
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			if conn.closes.Load() != 0 {
				t.Fatal("startup cancellation closed published attachment")
			}
			if _, ok := result.Conn.(client.CloseWriter); ok {
				t.Fatal("invented CloseWrite")
			}
			b := make([]byte, len(payload))
			if _, err := io.ReadFull(result.Reader, b); err != nil || string(b) != payload {
				t.Fatalf("prefetch %q: %v", b, err)
			}
			_ = result.Conn.Close()
			_ = result.Conn.Close()
			if conn.closes.Load() != 1 {
				t.Fatal("duplicate close")
			}
		})
	}
}

type attachMemoryCloseWriter struct {
	*attachMemoryConn
	halfCloses int
}

func (c *attachMemoryCloseWriter) CloseWrite() error { c.halfCloses++; return nil }
func TestContainerAttachCloseWritePreserved(t *testing.T) {
	conn := &attachMemoryCloseWriter{attachMemoryConn: newAttachMemoryConn(attachUpgrade + "prefetch")}
	result, err := attachContainerExec(context.Background(), "1.55", "fake", client.ExecAttachOptions{}, func(context.Context, string, string) (net.Conn, error) { return conn, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer result.Conn.Close()
	cw, ok := result.Conn.(client.CloseWriter)
	if !ok {
		t.Fatal("lost CloseWrite")
	}
	_ = cw.CloseWrite()
	if conn.halfCloses != 1 {
		t.Fatal("half close not forwarded")
	}
}

func TestContainerAttachDialOwnership(t *testing.T) {
	t.Run("already-canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := attachContainerExec(ctx, "1.55", "fake", client.ExecAttachOptions{}, func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("dial after cancellation")
			return nil, nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "late-cancel", true: "partial-error"}[partial], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			conn := newAttachMemoryConn("")
			closeErr := errors.New("close sentinel")
			dialErr := errors.New("dial sentinel")
			conn.closeErr = closeErr
			_, err := attachContainerExec(ctx, "1.55", "fake", client.ExecAttachOptions{}, func(context.Context, string, string) (net.Conn, error) {
				if partial {
					return conn, dialErr
				}
				cancel()
				return conn, nil
			})
			want := error(context.Canceled)
			if partial {
				want = dialErr
			}
			if !errors.Is(err, want) || !errors.Is(err, closeErr) || errors.Is(err, ErrContainerExitUnconfirmed) {
				t.Fatalf("causes: %v", err)
			}
			if conn.closes.Load() != 1 {
				t.Fatal("close count")
			}
		})
	}
}

func TestContainerAttachJoinsStartedClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn := newAttachMemoryConn("")
		conn.closeEntered = make(chan struct{})
		conn.closeRelease = make(chan struct{})
		sentinel := errors.New("close error")
		conn.closeErr = sentinel
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := attachContainerExec(ctx, "1.55", "fake", client.ExecAttachOptions{}, conn.dial)
			done <- err
		}()
		<-conn.entered
		cancel()
		<-conn.closeEntered
		select {
		case <-done:
			t.Fatal("returned before close callback finished")
		default:
		}
		close(conn.closeRelease)
		if err := <-done; !errors.Is(err, sentinel) {
			t.Fatalf("lost close error: %v", err)
		}
		if conn.closes.Load() != 1 {
			t.Fatal("close count")
		}
	})
}

func TestContainerAttachConcurrentIsolation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		one, two := newAttachMemoryConn(""), newAttachMemoryConn(attachUpgrade+"second")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := attachContainerExec(ctx, "1.55", "one", client.ExecAttachOptions{}, one.dial)
			done <- err
		}()
		<-one.entered
		result, err := attachContainerExec(context.Background(), "1.55", "two", client.ExecAttachOptions{}, two.dial)
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		<-done
		if two.closes.Load() != 0 {
			t.Fatal("cross-attach close")
		}
		b := make([]byte, 6)
		_, err = io.ReadFull(result.Reader, b)
		if err != nil || string(b) != "second" {
			t.Fatalf("second attachment: %q %v", b, err)
		}
		_ = result.Conn.Close()
	})
}

type attachRoundTripper func(*http.Request) (*http.Response, error)

func (f attachRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestContainerAttachNegotiationGate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		var calls atomic.Int32
		api, err := client.New(client.WithHost(localDockerHost), client.WithHTTPClient(&http.Client{Transport: attachRoundTripper(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/_ping" {
				t.Errorf("unexpected path: %s", req.URL.Path)
			}
			if calls.Add(1) == 1 {
				close(entered)
				<-release
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Api-Version": []string{"1.50"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		})}))
		if err != nil {
			t.Fatal(err)
		}
		defer api.Close()
		g := newMobyContainerGateway(api)
		done := make(chan error, 2)
		go func() { _, err := g.sdk(context.Background()); done <- err }()
		<-entered
		ctx, cancel := context.WithCancel(context.Background())
		go func() { _, err := g.sdk(ctx); done <- err }()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("gate wait: %v", err)
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				sdk, err := g.sdk(context.Background())
				if err != nil || sdk.version != "1.50" {
					t.Errorf("snapshot: %v %v", sdk, err)
				}
			})
		}
		wg.Wait()
		if calls.Load() != 1 {
			t.Fatalf("repeated negotiation %d", calls.Load())
		}
	})
}

func TestContainerAttachNegotiationFailureRetry(t *testing.T) {
	var calls int
	failure := errors.New("ping failure")
	api, err := client.New(client.WithHost(localDockerHost), client.WithHTTPClient(&http.Client{Transport: attachRoundTripper(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls <= 2 {
			return nil, failure
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Api-Version": []string{"1.50"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	g := newMobyContainerGateway(api)
	if _, err := g.sdk(context.Background()); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if g.api != nil {
		t.Fatal("published failed negotiation")
	}
	sdk, err := g.sdk(context.Background())
	if err != nil || sdk.version != "1.50" || calls != 3 {
		t.Fatalf("retry: %v %v %d", sdk, err, calls)
	}
}

func TestContainerAttachStartupBudget(t *testing.T) {
	for _, stage := range []string{"create-canceled", "create-empty", "attach-expired", "success"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var budget context.Context
				conn := newAttachMemoryConn(attachUpgrade + "hello")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				api := &containerProbeSDKFake{
					create: func(c context.Context, id string, o client.ExecCreateOptions) (client.ExecCreateResult, error) {
						budget = c
						deadline, ok := c.Deadline()
						if !ok || deadline != time.Now().Add(containerExecStartTimeout) {
							t.Fatalf("startup deadline %v", deadline)
						}
						if id != "container" || !o.TTY || o.Cmd[0] != "/bin/sh" {
							t.Fatalf("create options: %s %#v", id, o)
						}
						if stage == "create-canceled" {
							cancel()
						}
						if stage == "create-empty" {
							return client.ExecCreateResult{}, nil
						}
						time.Sleep(time.Second)
						return client.ExecCreateResult{ID: "exec"}, nil
					},
					attach: func(c context.Context, id string, o client.ExecAttachOptions) (client.ExecAttachResult, error) {
						if stage == "create-canceled" || stage == "create-empty" {
							t.Fatal("attach after failed create")
						}
						if c != budget || id != "exec" || !o.TTY {
							t.Fatal("lost startup budget or options")
						}
						if stage == "attach-expired" {
							<-c.Done()
							return client.ExecAttachResult{HijackedResponse: client.NewHijackedResponse(conn, "")}, nil
						}
						return attachContainerExec(c, "1.55", id, o, conn.dial)
					},
				}
				id, result, err := startContainerExec(ctx, api, "container", protectedContainerExecOptionsForShell(24, 80, "/bin/sh"), client.ExecAttachOptions{TTY: true})
				if stage == "success" {
					if err != nil || id != "exec" {
						t.Fatalf("start: %s %v", id, err)
					}
					if conn.closes.Load() != 0 || budget.Err() != context.Canceled {
						t.Fatal("budget handoff")
					}
					// 专属预算已取消；真正父取消仍交给 session 的生命周期 owner。
					session := newContainerSession(ctx, nil, ContainerAttachment{ExecID: id, Conn: result.Conn, Reader: result.Reader})
					cancel()
					session.stopLifecycle()
					if conn.closes.Load() != 1 {
						t.Fatal("parent cancellation lost owner")
					}
				} else {
					if err == nil || result.Conn != nil {
						t.Fatal("unexpected startup success")
					}
					if stage == "create-canceled" && !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					if stage == "attach-expired" && (!errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrContainerExitUnconfirmed) || conn.closes.Load() != 1) {
						t.Fatalf("late attachment: %v, closes %d", err, conn.closes.Load())
					}
				}
			})
		})
	}
}

func TestContainerAttachShellFallbackAndUncertainty(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary-nonzero", true: "uncertain-start"}[uncertain], func(t *testing.T) {
			calls := 0
			ctx := context.Background()
			shell, err := detectContainerShell(ctx, func(context.Context, string, ...string) (bool, error) {
				calls++
				if uncertain {
					return false, ErrContainerExitUnconfirmed
				}
				return calls == 4, nil
			}, "container")
			if uncertain {
				if !errors.Is(err, ErrContainerExitUnconfirmed) || calls != 1 || shell != "" {
					t.Fatalf("uncertain replay: %s %v %d", shell, err, calls)
				}
			} else if err != nil || shell != "/bin/sh" || calls != 4 {
				t.Fatalf("shell fallback: %s %v %d", shell, err, calls)
			}
		})
	}
}

func TestContainerAttachOrdinaryRejectionKeepsFallback(t *testing.T) {
	calls := 0
	shell, err := detectContainerShell(context.Background(), func(ctx context.Context, _ string, _ ...string) (bool, error) {
		calls++
		if calls == 4 {
			return true, nil
		}
		conn := newAttachMemoryConn("HTTP/1.1 500 Internal Server Error\r\nContent-Length: 0\r\n\r\n")
		_, err := attachContainerExec(ctx, "1.55", "missing-shell", client.ExecAttachOptions{}, conn.dial)
		if err == nil || errors.Is(err, ErrContainerExitUnconfirmed) || conn.closes.Load() != 1 {
			t.Fatalf("ordinary SDK rejection: %v; closes %d", err, conn.closes.Load())
		}
		return false, err
	}, "container")
	if err != nil || shell != "/bin/sh" || calls != 4 {
		t.Fatalf("fallback: %s %v %d", shell, err, calls)
	}
}

func TestContainerAttachMissingShellStreamKeepsFallback(t *testing.T) {
	calls := 0
	shell, err := detectContainerShell(context.Background(), func(ctx context.Context, _ string, command ...string) (bool, error) {
		calls++
		exit := 1
		output := "exec failed: executable not found"
		if calls == 4 {
			exit = 0
			output = ""
		}
		conn := newAttachMemoryConn(attachUpgrade + string(containerProbeSDKFrame(1, output)))
		conn.eof = true
		api := &containerProbeSDKFake{
			create: func(context.Context, string, client.ExecCreateOptions) (client.ExecCreateResult, error) {
				return client.ExecCreateResult{ID: "exec"}, nil
			},
			attach: func(ctx context.Context, id string, o client.ExecAttachOptions) (client.ExecAttachResult, error) {
				return attachContainerExec(ctx, "1.55", id, o, conn.dial)
			},
			inspect: func(context.Context, string, client.ExecInspectOptions) (client.ExecInspectResult, error) {
				return client.ExecInspectResult{ExitCode: exit}, nil
			},
		}
		available, _, err := probeContainerCommandOutput(ctx, api, "container", command...)
		if conn.closes.Load() != 1 {
			t.Fatal("probe connection not closed exactly once")
		}
		return available, err
	}, "container")
	if err != nil || shell != "/bin/sh" || calls != 4 {
		t.Fatalf("stream fallback: %s %v %d", shell, err, calls)
	}
}

func TestContainerAttachProbeGateUsesSharedBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// 占用 gate 模拟另一调用正在协商，不创建 SDK/真实连接。
		g := newMobyContainerGateway(nil)
		g.versionGate <- struct{}{}
		started := time.Now()
		available, output, err := g.probeContainerCommandOutput(context.Background(), "container", "/bin/sh", "-n")
		<-g.versionGate
		if available || output != nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(started) != containerShellProbeTimeout {
			t.Fatalf("probe gate budget: %v %q %v %v", available, output, err, time.Since(started))
		}
	})
}

func TestContainerAttachProbeNegotiationUsesSharedBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var deadline time.Time
		api, err := client.New(client.WithHost(localDockerHost), client.WithHTTPClient(&http.Client{Transport: attachRoundTripper(func(req *http.Request) (*http.Response, error) {
			d, ok := req.Context().Deadline()
			if !ok {
				t.Fatal("negotiation missing probe deadline")
			}
			if deadline.IsZero() {
				deadline = d
			}
			if d != deadline {
				t.Fatal("negotiation extended deadline")
			}
			<-req.Context().Done()
			return nil, req.Context().Err()
		})}))
		if err != nil {
			t.Fatal(err)
		}
		defer api.Close()
		started := time.Now()
		g := newMobyContainerGateway(api)
		available, output, err := g.probeContainerCommandOutput(context.Background(), "container", "/bin/sh", "-n")
		if available || output != nil || !errors.Is(err, context.DeadlineExceeded) || deadline != started.Add(containerShellProbeTimeout) || time.Since(started) != containerShellProbeTimeout {
			t.Fatalf("probe negotiation budget: %v %q %v %v", available, output, err, time.Since(started))
		}
	})
}
