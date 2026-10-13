package agentsocket

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/terminal"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

// 只模拟 RPC 返回后的传输取消，不启动 listener、Docker 或 PTY。
type cleanupTerminalHarness struct {
	stream        *revocationTerminalServerStream
	input         chan *structpb.Struct
	transportStop chan struct{}
	recvReturned  chan struct{}
	sent          chan terminal.Message
}

func newCleanupTerminalHarness(t *testing.T, ctx context.Context) *cleanupTerminalHarness {
	t.Helper()
	h := &cleanupTerminalHarness{
		input: make(chan *structpb.Struct, 8), transportStop: make(chan struct{}),
		recvReturned: make(chan struct{}), sent: make(chan terminal.Message, 8),
	}
	h.stream = &revocationTerminalServerStream{ctx: ctx,
		recv: func() (*structpb.Struct, error) {
			select {
			case frame := <-h.input:
				return frame, nil
			case <-h.transportStop:
				close(h.recvReturned)
				return nil, context.Canceled
			}
		},
		send: func(frame *structpb.Struct) error {
			message, err := terminalMessageFromStruct(frame)
			if err != nil {
				return err
			}
			h.sent <- message
			return nil
		},
	}
	h.push(t, terminal.Message{Type: terminal.MessageStart, Target: terminal.TargetHost, Rows: 24, Cols: 80})
	return h
}

func (h *cleanupTerminalHarness) push(t *testing.T, message terminal.Message) {
	t.Helper()
	h.input <- mustTerminalStruct(t, message)
}

func TestTerminalCleanupAgentCloseReturnsBeforeBlockedSend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newCleanupTerminalHarness(t, context.Background())
		readClosed, sendEntered, sendReturned := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var closes atomic.Int32
		session := &revocationTerminalSession{
			read:  func(context.Context, []byte) (int, error) { <-readClosed; return 0, io.EOF },
			close: func() error { closes.Add(1); close(readClosed); return nil },
		}
		ordinarySend := h.stream.send
		h.stream.send = func(frame *structpb.Struct) error {
			message, err := terminalMessageFromStruct(frame)
			if err != nil {
				return err
			}
			if message.Type == terminal.MessageClosed {
				close(sendEntered)
				<-h.transportStop
				close(sendReturned)
				return context.Canceled
			}
			return ordinarySend(frame)
		}
		service := newTerminalPOCService(terminal.NewManager(&fakeTerminalStarter{session: session}, nil, 1))
		openDone := make(chan error, 1)
		go func() { openDone <- service.Open(h.stream) }()
		synctest.Wait()
		startedAt := time.Now()
		h.push(t, terminal.Message{Type: terminal.MessageClose})
		<-sendEntered
		if err := <-openDone; err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(startedAt); elapsed != 100*time.Millisecond {
			t.Fatalf("notification elapsed = %v, want 100ms", elapsed)
		}
		if closes.Load() != 1 {
			t.Fatalf("close calls = %d", closes.Load())
		}
		select {
		case <-sendReturned:
			t.Fatal("Send returned before simulated transport cancellation")
		default:
		}
		select {
		case <-h.recvReturned:
			t.Fatal("Recv returned before simulated transport cancellation")
		default:
		}
		close(h.transportStop)
		<-sendReturned
		<-h.recvReturned
		synctest.Wait() // 两个泵已完成，不留下运输层 goroutine。
	})
}

func TestTerminalCleanupAgentNormalCloseStillNotifies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newCleanupTerminalHarness(t, context.Background())
		outputReady, closed := make(chan struct{}), make(chan struct{})
		var writes, resizes, closes int
		reads := 0
		session := &revocationTerminalSession{
			read: func(ctx context.Context, out []byte) (int, error) {
				reads++
				if reads == 1 {
					<-outputReady
					return copy(out, "ready"), nil
				}
				<-closed
				return 0, io.EOF
			},
			write: func(data []byte) (int, error) { writes++; close(outputReady); return len(data), nil },
			resize: func(rows, cols uint16) error {
				if rows != 30 || cols != 100 {
					t.Errorf("resize = %d x %d", rows, cols)
				}
				resizes++
				return nil
			},
			close: func() error { closes++; close(closed); return nil },
		}
		service := newTerminalPOCService(terminal.NewManager(&fakeTerminalStarter{session: session}, nil, 1))
		openDone := make(chan error, 1)
		go func() { openDone <- service.Open(h.stream) }()
		started := <-h.sent
		if started.Type != terminal.MessageStarted {
			t.Fatalf("first frame = %s", started.Type)
		}
		h.push(t, terminal.Message{Type: terminal.MessageInput, Data: []byte("input")})
		output := <-h.sent
		if output.Type != terminal.MessageOutput || string(output.Data) != "ready" {
			t.Fatalf("output = %#v", output)
		}
		h.push(t, terminal.Message{Type: terminal.MessageResize, Rows: 30, Cols: 100})
		h.push(t, terminal.Message{Type: terminal.MessageClose})
		final := <-h.sent
		if final.Type != terminal.MessageClosed || final.SessionID != started.SessionID {
			t.Fatalf("closed = %#v", final)
		}
		if err := <-openDone; err != nil {
			t.Fatal(err)
		}
		if writes != 1 || resizes != 1 || closes != 1 {
			t.Fatalf("writes=%d resizes=%d closes=%d", writes, resizes, closes)
		}
		close(h.transportStop)
		synctest.Wait()
	})
}

func TestTerminalCleanupAgentReportsExitUnconfirmedOnce(t *testing.T) {
	for _, path := range []string{"explicit", "cancel", "invalid_message"} {
		t.Run(path, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				h := newCleanupTerminalHarness(t, ctx)
				var logs bytes.Buffer
				var closes int
				var sessionCtx context.Context
				session := &revocationTerminalSession{
					read: func(ctx context.Context, _ []byte) (int, error) { <-ctx.Done(); return 0, ctx.Err() },
					close: func() error {
						closes++
						if path != "explicit" && sessionCtx.Err() == nil {
							t.Error("error cleanup did not cancel before Close")
						}
						if path == "explicit" && sessionCtx.Err() != nil {
							t.Error("explicit Close lost its best-effort opportunity")
						}
						return errors.Join(terminal.ErrContainerExitUnconfirmed, terminal.ErrContainerCleanupUnconfirmed, errors.New("secret-cookie-and-command"))
					},
				}
				starter := revocationTerminalStarter(func(ctx context.Context, _ terminal.StartRequest) (terminal.Session, error) {
					sessionCtx = ctx
					return session, nil
				})
				service := newTerminalPOCService(terminal.NewManager(starter, nil, 1))
				service.logger = slog.New(slog.NewTextHandler(&logs, nil))
				openDone := make(chan error, 1)
				go func() { openDone <- service.Open(h.stream) }()
				synctest.Wait()
				switch path {
				case "explicit":
					h.push(t, terminal.Message{Type: terminal.MessageClose})
				case "cancel":
					cancel()
				case "invalid_message":
					h.push(t, terminal.Message{Type: terminal.MessageStart, Target: terminal.TargetHost, Rows: 24, Cols: 80})
				}
				err := <-openDone
				want := codes.Internal
				if path == "cancel" {
					want = codes.OK
				}
				if path == "invalid_message" {
					want = codes.InvalidArgument
				}
				if grpcstatus.Code(err) != want {
					t.Fatalf("status = %v, want %v", err, want)
				}
				if closes != 1 {
					t.Fatalf("close calls = %d", closes)
				}
				logged := logs.String()
				if strings.Count(logged, "TERMINAL_CONTAINER_EXIT_UNCONFIRMED") != 1 || !strings.Contains(logged, "session_id=term-p0-1") || !strings.Contains(logged, "target=host") || !strings.Contains(logged, "local_cleanup_unconfirmed=true") {
					t.Fatalf("cleanup classification missing or duplicated: %s", logged)
				}
				if strings.Contains(logged, "secret") {
					t.Fatal("raw close error leaked")
				}
				close(h.transportStop)
				synctest.Wait()
			})
		})
	}
}

func TestTerminalCleanupAgentWriteCancellationCompletes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		h := newCleanupTerminalHarness(t, ctx)
		writeEntered, writeReturned, readReturned := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var sessionCtx context.Context
		var writes, resizes, closes int
		session := &revocationTerminalSession{
			read: func(ctx context.Context, _ []byte) (int, error) {
				defer close(readReturned)
				<-ctx.Done()
				return 0, ctx.Err()
			},
			write: func([]byte) (int, error) {
				writes++
				close(writeEntered)
				defer close(writeReturned)
				<-sessionCtx.Done()
				return 0, sessionCtx.Err()
			},
			resize: func(uint16, uint16) error { resizes++; return nil },
			close:  func() error { closes++; <-writeReturned; return nil },
		}
		starter := revocationTerminalStarter(func(ctx context.Context, _ terminal.StartRequest) (terminal.Session, error) {
			sessionCtx = ctx
			return session, nil
		})
		service := newTerminalPOCService(terminal.NewManager(starter, nil, 1))
		openDone := make(chan error, 1)
		go func() { openDone <- service.Open(h.stream) }()
		synctest.Wait()
		h.push(t, terminal.Message{Type: terminal.MessageInput, Data: []byte("inflight")})
		<-writeEntered
		h.push(t, terminal.Message{Type: terminal.MessageInput, Data: []byte("buffered")})
		h.push(t, terminal.Message{Type: terminal.MessageResize, Rows: 25, Cols: 81})
		synctest.Wait()
		cancel() // 只有父取消解除同步 Write，测试不手动释放 writer。
		<-openDone
		<-writeReturned
		<-readReturned
		if writes != 1 || resizes != 0 || closes != 1 {
			t.Fatalf("writes=%d resizes=%d closes=%d", writes, resizes, closes)
		}
		close(h.transportStop)
		synctest.Wait()
	})
}

func TestTerminalCleanupAgentStartSendReturnsOnCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		h := newCleanupTerminalHarness(t, ctx)
		sendEntered, sendReturned := make(chan struct{}), make(chan struct{})
		var closes atomic.Int32
		h.stream.send = func(frame *structpb.Struct) error {
			message, err := terminalMessageFromStruct(frame)
			if err != nil {
				return err
			}
			if message.Type != terminal.MessageStarted {
				t.Errorf("unexpected frame %s", message.Type)
			}
			close(sendEntered)
			<-h.transportStop
			close(sendReturned)
			return context.Canceled
		}
		session := &revocationTerminalSession{
			read: func(context.Context, []byte) (int, error) {
				t.Error("read started after canceled initial send")
				return 0, io.EOF
			},
			close: func() error { closes.Add(1); return nil },
		}
		service := newTerminalPOCService(terminal.NewManager(&fakeTerminalStarter{session: session}, nil, 1))
		done := make(chan error, 1)
		go func() { done <- service.Open(h.stream) }()
		<-sendEntered
		synctest.Wait()
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if closes.Load() != 1 {
			t.Fatalf("close count=%d", closes.Load())
		}
		select {
		case <-sendReturned:
			t.Fatal("Send returned before transport cancellation")
		default:
		}
		close(h.transportStop)
		<-sendReturned
		synctest.Wait()
	})
}
