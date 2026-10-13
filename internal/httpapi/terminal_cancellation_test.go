package httpapi

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/terminal"
	"github.com/coder/websocket"
)

type cancellationTerminalFrame struct {
	kind websocket.MessageType
	data []byte
	err  error
}

type cancellationTerminalReader struct {
	frames []cancellationTerminalFrame
	reads  chan struct{}
}

func (r *cancellationTerminalReader) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	r.reads <- struct{}{}
	if len(r.frames) == 0 {
		<-ctx.Done()
		return 0, nil, ctx.Err()
	}
	frame := r.frames[0]
	r.frames = r.frames[1:]
	return frame.kind, frame.data, frame.err
}

func awaitTerminalReader(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("terminal reader did not return")
	}
}

func TestTerminalInputCancellationUnblocksEverySend(t *testing.T) {
	cases := map[string]cancellationTerminalFrame{
		"input":              {kind: websocket.MessageBinary, data: []byte("x")},
		"resize":             {kind: websocket.MessageText, data: []byte(`{"type":"resize","rows":24,"cols":80}`)},
		"close":              {kind: websocket.MessageText, data: []byte(`{"type":"close"}`)},
		"read error":         {err: io.EOF},
		"empty input":        {kind: websocket.MessageBinary},
		"oversized input":    {kind: websocket.MessageBinary, data: make([]byte, maxWebSocketInput+1)},
		"invalid json":       {kind: websocket.MessageText, data: []byte(`{`)},
		"invalid dimensions": {kind: websocket.MessageText, data: []byte(`{"type":"resize"}`)},
		"unknown control":    {kind: websocket.MessageText, data: []byte(`{"type":"other"}`)},
		"unknown frame":      {kind: websocket.MessageType(100)},
	}
	for name, frame := range cases {
		t.Run(name, func(t *testing.T) {
			for _, capacity := range []int{0, 1} {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				destination := make(chan terminalInput, capacity)
				// 先让真实帧填满缓冲，再停止消费，覆盖 burst 后接收端退出。
				frames := make([]cancellationTerminalFrame, 0, capacity+1)
				for i := 0; i < capacity; i++ {
					frames = append(frames, cancellationTerminalFrame{kind: websocket.MessageBinary, data: []byte("first")})
				}
				frames = append(frames, frame)
				reader := &cancellationTerminalReader{frames: frames, reads: make(chan struct{}, capacity+1)}
				done := make(chan struct{})
				go func() { defer close(done); readTerminalInput(ctx, reader, destination) }()
				for i := 0; i <= capacity; i++ {
					awaitTerminalReader(t, reader.reads)
				}
				cancel()
				awaitTerminalReader(t, done)
				for range destination {
				}
			}
		})
	}
}

func TestTerminalInputCancellationInterruptsRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &cancellationTerminalReader{reads: make(chan struct{}, 1)}
	done := make(chan struct{})
	go func() { defer close(done); readTerminalInput(ctx, reader, make(chan terminalInput)) }()
	awaitTerminalReader(t, reader.reads)
	cancel()
	awaitTerminalReader(t, done)
}

func TestTerminalInputForwardsBurstAndClosesChannel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	reader := &cancellationTerminalReader{frames: []cancellationTerminalFrame{
		{kind: websocket.MessageBinary, data: []byte("input")},
		{kind: websocket.MessageText, data: []byte(`{"type":"resize","rows":24,"cols":80}`)},
		{kind: websocket.MessageText, data: []byte(`{"type":"close"}`)},
	}, reads: make(chan struct{}, 3)}
	destination := make(chan terminalInput, 1)
	go readTerminalInput(ctx, reader, destination)
	for _, want := range []terminal.MessageType{terminal.MessageInput, terminal.MessageResize, terminal.MessageClose} {
		select {
		case got, ok := <-destination:
			if !ok || got.err != nil || got.message.Type != want || got.closeRequested != (want == terminal.MessageClose) {
				t.Fatalf("input = %#v, want %s", got, want)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	select {
	case _, ok := <-destination:
		if ok {
			t.Fatal("input channel remains open")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestTerminalInputForwardsReadError(t *testing.T) {
	reader := &cancellationTerminalReader{frames: []cancellationTerminalFrame{{err: io.EOF}}, reads: make(chan struct{}, 1)}
	destination := make(chan terminalInput, 1)
	readTerminalInput(context.Background(), reader, destination)
	if got := <-destination; !errors.Is(got.err, io.EOF) {
		t.Fatalf("error = %v", got.err)
	}
	if _, ok := <-destination; ok {
		t.Fatal("input channel remains open")
	}
}

func TestTerminalInputDeadlineUnblocksFullChannel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	reader := &cancellationTerminalReader{frames: []cancellationTerminalFrame{
		{kind: websocket.MessageBinary, data: []byte("first")},
		{kind: websocket.MessageBinary, data: []byte("second")},
	}, reads: make(chan struct{}, 2)}
	done := make(chan struct{})
	go func() { defer close(done); readTerminalInput(ctx, reader, make(chan terminalInput, 1)) }()
	awaitTerminalReader(t, done)
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("context error = %v", ctx.Err())
	}
}
