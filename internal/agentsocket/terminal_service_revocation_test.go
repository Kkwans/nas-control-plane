package agentsocket

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/terminal"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

type revocationTerminalServerStream struct {
	grpc.ServerStream
	ctx  context.Context
	recv func() (*structpb.Struct, error)
	send func(*structpb.Struct) error
}

func (s *revocationTerminalServerStream) Context() context.Context          { return s.ctx }
func (s *revocationTerminalServerStream) Recv() (*structpb.Struct, error)   { return s.recv() }
func (s *revocationTerminalServerStream) Send(frame *structpb.Struct) error { return s.send(frame) }

type revocationTerminalStarter func(context.Context, terminal.StartRequest) (terminal.Session, error)

func (s revocationTerminalStarter) Start(ctx context.Context, request terminal.StartRequest) (terminal.Session, error) {
	return s(ctx, request)
}

type revocationTerminalSession struct {
	read   func(context.Context, []byte) (int, error)
	write  func([]byte) (int, error)
	resize func(uint16, uint16) error
	close  func() error
}

func (s *revocationTerminalSession) Read(ctx context.Context, output []byte) (int, error) {
	return s.read(ctx, output)
}
func (s *revocationTerminalSession) Write(input []byte) (int, error) { return s.write(input) }
func (s *revocationTerminalSession) Resize(rows, cols uint16) error  { return s.resize(rows, cols) }
func (s *revocationTerminalSession) Close() error                    { return s.close() }

func TestTerminalServiceCancellationDuringStart(t *testing.T) {
	for _, stage := range []string{"before_recv", "after_recv", "after_open"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var receives, starts, sends, closes int
			session := &revocationTerminalSession{close: func() error { closes++; return nil }}
			manager := terminal.NewManager(revocationTerminalStarter(func(context.Context, terminal.StartRequest) (terminal.Session, error) {
				starts++
				cancel()
				return session, nil
			}), nil, 1)
			stream := &revocationTerminalServerStream{ctx: ctx,
				recv: func() (*structpb.Struct, error) {
					receives++
					if stage == "after_recv" {
						cancel()
					}
					return mustTerminalStruct(t, terminal.Message{Type: terminal.MessageStart, Target: terminal.TargetHost, Rows: 24, Cols: 80}), nil
				},
				send: func(*structpb.Struct) error { sends++; return nil },
			}
			if stage == "before_recv" {
				cancel()
			}
			if err := newTerminalPOCService(manager).Open(stream); err != nil {
				t.Fatal(err)
			}
			wantReceives, wantStarts := 1, 0
			if stage == "before_recv" {
				wantReceives = 0
			}
			if stage == "after_open" {
				wantStarts = 1
			}
			if receives != wantReceives || starts != wantStarts || sends != 0 || closes != wantStarts {
				t.Fatalf("receives=%d starts=%d sends=%d closes=%d", receives, starts, sends, closes)
			}
		})
	}
}

func TestTerminalServiceCancellationDropsBufferedInput(t *testing.T) {
	for _, messageType := range []terminal.MessageType{terminal.MessageInput, terminal.MessageResize} {
		t.Run(string(messageType), func(t *testing.T) {
			// 多次执行同时就绪的 Done/input 分支，避免只覆盖 select 恰好选择 Done。
			for range 64 {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				firstWrite, releaseWrite := make(chan struct{}), make(chan struct{})
				buffered, transportStop, recvReturned := make(chan struct{}), make(chan struct{}), make(chan struct{})
				readEntered, readRelease, readReturned := make(chan struct{}), make(chan struct{}), make(chan struct{})
				stopTransport := sync.OnceFunc(func() { close(transportStop) })
				finishWrite := sync.OnceFunc(func() { close(releaseWrite) })
				defer stopTransport()
				defer finishWrite()
				var writes, resizes, sends atomic.Int32
				var closeOnce sync.Once
				session := &revocationTerminalSession{
					read: func(ctx context.Context, _ []byte) (int, error) {
						defer close(readReturned)
						close(readEntered)
						<-readRelease
						return 0, ctx.Err()
					},
					write: func(data []byte) (int, error) {
						if writes.Add(1) == 1 {
							close(firstWrite)
							<-releaseWrite
						}
						return len(data), nil
					},
					resize: func(uint16, uint16) error { resizes.Add(1); return nil },
					close:  func() error { closeOnce.Do(func() { close(readRelease) }); return nil },
				}
				defer session.Close()
				manager := terminal.NewManager(&fakeTerminalStarter{session: session}, nil, 1)
				frames := []*structpb.Struct{
					mustTerminalStruct(t, terminal.Message{Type: terminal.MessageStart, Target: terminal.TargetHost, Rows: 24, Cols: 80}),
					mustTerminalStruct(t, terminal.Message{Type: terminal.MessageInput, Data: []byte("in-flight")}),
					mustTerminalStruct(t, terminal.Message{Type: messageType, Data: []byte("queued"), Rows: 25, Cols: 81}),
				}
				receives := 0
				stream := &revocationTerminalServerStream{ctx: ctx,
					recv: func() (*structpb.Struct, error) {
						i := receives
						receives++
						if i < len(frames) {
							return frames[i], nil
						}
						close(buffered)
						<-transportStop
						close(recvReturned)
						return nil, context.Canceled
					},
					send: func(*structpb.Struct) error { sends.Add(1); return nil },
				}
				openDone := make(chan error, 1)
				go func() { openDone <- newTerminalPOCService(manager).Open(stream) }()
				awaitTerminalReceiver(t, firstWrite)
				awaitTerminalReceiver(t, buffered)
				awaitTerminalReceiver(t, readEntered)
				cancel()
				finishWrite()
				select {
				case err := <-openDone:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
					t.Fatal("Open waited for transport-only Recv instead of returning")
				}
				stopTransport()
				awaitTerminalReceiver(t, recvReturned)
				awaitTerminalReceiver(t, readReturned)
				if writes.Load() != 1 || resizes.Load() != 0 || sends.Load() != 1 {
					t.Fatalf("cancel admitted queued work: writes=%d resizes=%d sends=%d", writes.Load(), resizes.Load(), sends.Load())
				}
			}
		})
	}
}

func TestTerminalServiceReturnsBeforeTransportReceiveStops(t *testing.T) {
	transportStop, recvEntered, recvReturned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	stopTransport := sync.OnceFunc(func() { close(transportStop) })
	defer stopTransport()
	var sends atomic.Int32
	session := &revocationTerminalSession{
		read:  func(context.Context, []byte) (int, error) { <-recvEntered; return 0, io.EOF },
		close: func() error { return nil },
	}
	manager := terminal.NewManager(&fakeTerminalStarter{session: session}, nil, 1)
	start := mustTerminalStruct(t, terminal.Message{Type: terminal.MessageStart, Target: terminal.TargetHost, Rows: 24, Cols: 80})
	receivedStart := false
	stream := &revocationTerminalServerStream{ctx: context.Background(),
		recv: func() (*structpb.Struct, error) {
			if !receivedStart {
				receivedStart = true
				return start, nil
			}
			close(recvEntered)
			<-transportStop
			close(recvReturned)
			return nil, context.Canceled
		},
		send: func(*structpb.Struct) error { sends.Add(1); return nil },
	}
	openDone := make(chan error, 1)
	go func() { openDone <- newTerminalPOCService(manager).Open(stream) }()
	select {
	case err := <-openDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Open joined Recv, which needs Open to return before transport cancellation")
	}
	if sends.Load() != 2 {
		t.Fatalf("sent frames = %d, want started and closed", sends.Load())
	}
	stopTransport()
	awaitTerminalReceiver(t, recvReturned)
}

func TestTerminalServiceOutputRechecksCancellation(t *testing.T) {
	for _, stage := range []string{"before_read", "after_read", "after_output"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reads, sends := 0, 0
			session := &revocationTerminalSession{
				read: func(context.Context, []byte) (int, error) {
					reads++
					if stage == "after_read" {
						cancel()
					}
					return 1, io.EOF
				},
				close: func() error { return nil },
			}
			manager := terminal.NewManager(&fakeTerminalStarter{session: session}, nil, 1)
			opened, err := manager.Open(context.Background(), terminal.StartRequest{Target: terminal.TargetHost, Rows: 24, Cols: 80})
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close(opened.ID)
			stream := &revocationTerminalServerStream{ctx: ctx, send: func(*structpb.Struct) error { sends++; cancel(); return nil }}
			if stage == "before_read" {
				cancel()
			}
			done := make(chan error, 1)
			newTerminalPOCService(manager).copyTerminalOutput(ctx, stream, opened.ID, done)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			wantReads, wantSends := 1, 0
			if stage == "before_read" {
				wantReads = 0
			}
			if stage == "after_output" {
				wantSends = 1
			}
			if reads != wantReads || sends != wantSends {
				t.Fatalf("reads=%d sends=%d", reads, sends)
			}
		})
	}
}
