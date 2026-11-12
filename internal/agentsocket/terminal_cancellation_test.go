package agentsocket

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

type cancellationTerminalStream struct {
	grpc.ServerStream
	ctx     context.Context
	results []terminalReceiveResult
	reads   chan struct{}
}

func (s *cancellationTerminalStream) Context() context.Context    { return s.ctx }
func (s *cancellationTerminalStream) Send(*structpb.Struct) error { return nil }
func (s *cancellationTerminalStream) Recv() (*structpb.Struct, error) {
	s.reads <- struct{}{}
	if len(s.results) == 0 {
		<-s.ctx.Done()
		return nil, s.ctx.Err()
	}
	result := s.results[0]
	s.results = s.results[1:]
	return result.message, result.err
}

func awaitTerminalReceiver(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("terminal receiver did not return")
	}
}

func TestTerminalReceiveCancellationUnblocksBurst(t *testing.T) {
	for _, receiveErr := range []error{nil, io.EOF, errors.New("receive failed")} {
		for _, capacity := range []int{0, 1} {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			results := make([]terminalReceiveResult, capacity+1)
			results[capacity].err = receiveErr
			// RPC context 保持活跃，证明接收端退出时内部取消足以解除 channel 阻塞。
			stream := &cancellationTerminalStream{ctx: context.Background(), results: results, reads: make(chan struct{}, capacity+1)}
			destination := make(chan terminalReceiveResult, capacity)
			done := make(chan struct{})
			go func() { defer close(done); receiveTerminalMessages(ctx, stream, destination) }()
			for i := 0; i <= capacity; i++ {
				awaitTerminalReceiver(t, stream.reads)
			}
			cancel()
			awaitTerminalReceiver(t, done)
			for range destination {
			}
		}
	}
}

func TestTerminalReceiveCancellationReleasesTransportRead(t *testing.T) {
	transportContext, stopTransport := context.WithCancel(context.Background())
	defer stopTransport()
	ctx, cancel := context.WithCancel(transportContext)
	defer cancel()
	stream := &cancellationTerminalStream{ctx: transportContext, reads: make(chan struct{}, 1)}
	done := make(chan struct{})
	go func() { defer close(done); receiveTerminalMessages(ctx, stream, make(chan terminalReceiveResult)) }()
	awaitTerminalReceiver(t, stream.reads)
	cancel()
	// 模拟 Open 返回后 gRPC 取消传输层：不可在 Open 返回前等待 Recv。
	stopTransport()
	awaitTerminalReceiver(t, done)
}

func TestTerminalReceiveForwardsBurstAndEOF(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	first, second := &structpb.Struct{}, &structpb.Struct{}
	stream := &cancellationTerminalStream{ctx: ctx, results: []terminalReceiveResult{{message: first}, {message: second}, {err: io.EOF}}, reads: make(chan struct{}, 3)}
	destination := make(chan terminalReceiveResult, 1)
	go receiveTerminalMessages(ctx, stream, destination)
	for _, want := range []terminalReceiveResult{{message: first}, {message: second}, {err: io.EOF}} {
		select {
		case got, ok := <-destination:
			if !ok || got.message != want.message || !errors.Is(got.err, want.err) {
				t.Fatalf("result = %#v, want %#v", got, want)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	select {
	case _, ok := <-destination:
		if ok {
			t.Fatal("receive channel remains open")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestTerminalReceiveDeadlineUnblocksFullChannel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	stream := &cancellationTerminalStream{ctx: context.Background(), results: make([]terminalReceiveResult, 2), reads: make(chan struct{}, 2)}
	done := make(chan struct{})
	go func() { defer close(done); receiveTerminalMessages(ctx, stream, make(chan terminalReceiveResult, 1)) }()
	awaitTerminalReceiver(t, done)
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("context error = %v", ctx.Err())
	}
}
