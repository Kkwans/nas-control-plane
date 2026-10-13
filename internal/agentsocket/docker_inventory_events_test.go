package agentsocket

import (
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

type fakeDockerInventoryEventsProvider func(context.Context, func(bool)) error

func (f fakeDockerInventoryEventsProvider) Watch(ctx context.Context, notify func(bool)) error {
	return f(ctx, notify)
}

type fakeDockerInventoryWatchServer struct {
	grpc.ServerStream
	ctx  context.Context
	send func(*structpb.Struct) error
}

func (s *fakeDockerInventoryWatchServer) Context() context.Context          { return s.ctx }
func (s *fakeDockerInventoryWatchServer) Send(frame *structpb.Struct) error { return s.send(frame) }

type fakeDockerInventoryEventsRPC func(*emptypb.Empty, AgentDockerInventoryEventsService_WatchServer) error

func (f fakeDockerInventoryEventsRPC) Watch(request *emptypb.Empty, stream AgentDockerInventoryEventsService_WatchServer) error {
	return f(request, stream)
}

func awaitDockerInventoryEvent[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("fake event barrier timed out")
		var zero T
		return zero
	}
}

func dockerInventoryEventsTestClient(t *testing.T, service AgentDockerInventoryEventsServiceServer) AgentDockerInventoryEventsServiceClient {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	if service != nil {
		RegisterAgentDockerInventoryEventsServiceServer(server, service)
	}
	serverDone := make(chan struct{})
	go func() { defer close(serverDone); _ = server.Serve(listener) }()
	connection, err := grpc.NewClient("passthrough:///inventory-events", grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return listener.DialContext(ctx)
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = connection.Close()
		server.Stop()
		_ = listener.Close()
		awaitDockerInventoryEvent(t, serverDone)
	})
	return NewAgentDockerInventoryEventsServiceClient(connection)
}

func TestDockerRealtimeEventsRPCReadyInvalidatedAndTerminal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	readySeen := make(chan struct{})
	dirtySeen := make(chan struct{})
	joined := make(chan struct{})
	var calls atomic.Int32
	service := newDockerInventoryEventsService(fakeDockerInventoryEventsProvider(func(ctx context.Context, notify func(bool)) error {
		calls.Add(1)
		defer close(joined)
		notify(true)
		select {
		case <-readySeen:
		case <-ctx.Done():
			return ctx.Err()
		}
		notify(false)
		select {
		case <-dirtySeen:
		case <-ctx.Done():
			return ctx.Err()
		}
		return errors.New("private Docker socket details")
	}))
	client := dockerInventoryEventsTestClient(t, service)
	var observed []bool
	err := watchDockerInventory(ctx, client, func(ready bool) {
		observed = append(observed, ready)
		if ready {
			close(readySeen)
		} else {
			close(dirtySeen)
		}
	})
	if ErrorCode(err) != "AGENT_RPC_UNAVAILABLE" || strings.Contains(err.Error(), "private Docker") {
		t.Fatalf("unexpected terminal error: %v", err)
	}
	if !reflect.DeepEqual(observed, []bool{true, false}) || calls.Load() != 1 {
		t.Fatalf("notifications=%v source calls=%d", observed, calls.Load())
	}
	awaitDockerInventoryEvent(t, joined)
}

func TestDockerRealtimeEventsRPCLimitsConcurrentWatchAndReleases(t *testing.T) {
	var calls atomic.Int32
	service := newDockerInventoryEventsService(fakeDockerInventoryEventsProvider(func(ctx context.Context, notify func(bool)) error {
		calls.Add(1)
		notify(true)
		<-ctx.Done()
		return ctx.Err()
	}))
	finished := make(chan error, 3)
	client := dockerInventoryEventsTestClient(t, fakeDockerInventoryEventsRPC(func(request *emptypb.Empty, stream AgentDockerInventoryEventsService_WatchServer) error {
		err := service.Watch(request, stream)
		finished <- err
		return err
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, err := client.Watch(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := first.Recv()
	if err != nil || frame.GetFields()["kind"].GetStringValue() != "ready" {
		t.Fatalf("first ready=%v, %v", frame, err)
	}
	second, err := client.Watch(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = second.Recv()
	if grpcstatus.Code(err) != codes.ResourceExhausted || grpcstatus.Convert(err).Message() != "AGENT_DOCKER_INVENTORY_EVENTS_LIMIT_EXCEEDED" {
		t.Fatalf("concurrent watch = %v", err)
	}
	_ = awaitDockerInventoryEvent(t, finished)
	if calls.Load() != 1 {
		t.Fatal("rejected watch opened another Docker source")
	}
	cancel()
	_ = awaitDockerInventoryEvent(t, finished)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	third, err := client.Watch(ctx2, &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := third.Recv(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("source was not reopened after prior watch joined")
	}
	cancel2()
	_ = awaitDockerInventoryEvent(t, finished)
}

func TestDockerRealtimeEventsSlowSendCoalescesWithoutBlockingSource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startStorm := make(chan struct{})
	stormConsumed := make(chan struct{})
	stopSource := make(chan struct{})
	service := newDockerInventoryEventsService(fakeDockerInventoryEventsProvider(func(ctx context.Context, notify func(bool)) error {
		notify(true)
		select {
		case <-startStorm:
		case <-ctx.Done():
			return ctx.Err()
		}
		for range 10000 {
			notify(false)
		}
		close(stormConsumed)
		select {
		case <-stopSource:
			return io.EOF
		case <-ctx.Done():
			return ctx.Err()
		}
	}))
	frames := make(chan dockerInventoryEventKind, 3)
	releaseReady := make(chan struct{})
	releaseDirty := make(chan struct{})
	stream := &fakeDockerInventoryWatchServer{ctx: ctx, send: func(frame *structpb.Struct) error {
		kind, err := decodeDockerInventoryEvent(frame)
		if err != nil {
			return err
		}
		frames <- kind
		release := releaseReady
		if kind == dockerInventoryEventInvalidated {
			release = releaseDirty
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	result := make(chan error, 1)
	go func() { result <- service.Watch(&emptypb.Empty{}, stream) }()
	if awaitDockerInventoryEvent(t, frames) != dockerInventoryEventReady {
		t.Fatal("first frame was not ready")
	}
	close(startStorm)
	awaitDockerInventoryEvent(t, stormConsumed)
	close(releaseReady)
	if awaitDockerInventoryEvent(t, frames) != dockerInventoryEventInvalidated {
		t.Fatal("missing coalesced invalidation")
	}
	close(stopSource)
	// service 的源 goroutine 先退出，确保残留 dirty 不会在终止后发送。
	close(releaseDirty)
	if err := awaitDockerInventoryEvent(t, result); grpcstatus.Code(err) != codes.Unavailable {
		t.Fatal(err)
	}
	if len(frames) != 0 {
		t.Fatal("event storm accumulated payload frames")
	}
}

func TestDockerRealtimeEventsSourceTerminalReturnsBeforeBlockedSend(t *testing.T) {
	readySending := make(chan struct{})
	finishSource := make(chan struct{})
	transportRelease := make(chan struct{})
	senderReleased := make(chan struct{})
	service := newDockerInventoryEventsService(fakeDockerInventoryEventsProvider(func(ctx context.Context, notify func(bool)) error {
		notify(true)
		select {
		case <-finishSource:
		case <-ctx.Done():
			return ctx.Err()
		}
		notify(false)
		return io.EOF
	}))
	var sends atomic.Int32
	stream := &fakeDockerInventoryWatchServer{ctx: context.Background(), send: func(frame *structpb.Struct) error {
		if sends.Add(1) != 1 {
			t.Error("queued dirty was sent after terminal")
		}
		close(readySending)
		// 模拟 gRPC：只有 handler 返回后，传输才解除阻塞的 Send。
		<-transportRelease
		close(senderReleased)
		return context.Canceled
	}}
	result := make(chan error, 1)
	go func() { result <- service.Watch(&emptypb.Empty{}, stream) }()
	awaitDockerInventoryEvent(t, readySending)
	close(finishSource)
	if err := awaitDockerInventoryEvent(t, result); grpcstatus.Code(err) != codes.Unavailable {
		t.Fatal(err)
	}
	if service.watching.Load() {
		t.Fatal("terminal source retained Watch slot")
	}
	select {
	case <-senderReleased:
		t.Fatal("fake Send was not blocked until handler returned")
	default:
	}
	close(transportRelease)
	awaitDockerInventoryEvent(t, senderReleased)
	if sends.Load() != 1 {
		t.Fatalf("sent %d frames", sends.Load())
	}
}

func TestDockerRealtimeEventsServiceCancellationJoinsBeforeReleasingSlot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	canceled := make(chan struct{})
	allowExit := make(chan struct{})
	service := newDockerInventoryEventsService(fakeDockerInventoryEventsProvider(func(ctx context.Context, notify func(bool)) error {
		notify(true)
		<-ctx.Done()
		close(canceled)
		<-allowExit
		return ctx.Err()
	}))
	stream := &fakeDockerInventoryWatchServer{ctx: ctx, send: func(*structpb.Struct) error { close(ready); return nil }}
	result := make(chan error, 1)
	go func() { result <- service.Watch(&emptypb.Empty{}, stream) }()
	awaitDockerInventoryEvent(t, ready)
	cancel()
	awaitDockerInventoryEvent(t, canceled)
	select {
	case <-result:
		t.Fatal("Watch returned before source joined")
	default:
	}
	if !service.watching.Load() {
		t.Fatal("slot released before source joined")
	}
	second := &fakeDockerInventoryWatchServer{ctx: context.Background(), send: func(*structpb.Struct) error { return nil }}
	if err := service.Watch(&emptypb.Empty{}, second); grpcstatus.Code(err) != codes.ResourceExhausted {
		t.Fatal(err)
	}
	close(allowExit)
	_ = awaitDockerInventoryEvent(t, result)
	if service.watching.Load() {
		t.Fatal("slot was not released after join")
	}
}

func TestDockerRealtimeEventsRejectMalformedFramesAndOrder(t *testing.T) {
	for _, test := range []struct {
		name      string
		frames    []*structpb.Struct
		callbacks []bool
	}{
		{"invalidated before ready", []*structpb.Struct{dockerInventoryEventFrame(dockerInventoryEventInvalidated)}, nil},
		{"unknown kind", []*structpb.Struct{dockerInventoryEventFrame("raw")}, nil},
		{"repeated ready", []*structpb.Struct{dockerInventoryEventFrame(dockerInventoryEventReady), dockerInventoryEventFrame(dockerInventoryEventReady)}, []bool{true}},
		{"extra raw field", []*structpb.Struct{{Fields: map[string]*structpb.Value{"kind": structpb.NewStringValue("ready"), "actor": structpb.NewStringValue("private")}}}, nil},
		{"numeric kind", []*structpb.Struct{{Fields: map[string]*structpb.Value{"kind": structpb.NewNumberValue(1)}}}, nil},
		{"missing kind", []*structpb.Struct{{}}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := dockerInventoryEventsTestClient(t, fakeDockerInventoryEventsRPC(func(_ *emptypb.Empty, stream AgentDockerInventoryEventsService_WatchServer) error {
				for _, frame := range test.frames {
					if err := stream.Send(frame); err != nil {
						return err
					}
				}
				<-stream.Context().Done()
				return stream.Context().Err()
			}))
			var observed []bool
			err := watchDockerInventory(context.Background(), client, func(ready bool) { observed = append(observed, ready) })
			if ErrorCode(err) != "AGENT_RPC_RESPONSE_INVALID" {
				t.Fatalf("malformed frame = %v", err)
			}
			if !reflect.DeepEqual(observed, test.callbacks) {
				t.Fatalf("callbacks=%v", observed)
			}
		})
	}
}

func TestDockerRealtimeEventsClientCancellationAndEOF(t *testing.T) {
	for _, terminal := range []string{"cancel", "EOF", "oversized"} {
		t.Run(terminal, func(t *testing.T) {
			joined := make(chan struct{})
			client := dockerInventoryEventsTestClient(t, fakeDockerInventoryEventsRPC(func(_ *emptypb.Empty, stream AgentDockerInventoryEventsService_WatchServer) error {
				defer close(joined)
				if err := stream.Send(dockerInventoryEventFrame(dockerInventoryEventReady)); err != nil {
					return err
				}
				if terminal == "EOF" {
					return nil
				}
				if terminal == "oversized" {
					return stream.Send(dockerInventoryEventFrame(dockerInventoryEventKind(strings.Repeat("x", 2048))))
				}
				<-stream.Context().Done()
				return stream.Context().Err()
			}))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var callbacks int
			err := watchDockerInventory(ctx, client, func(ready bool) {
				callbacks++
				if !ready {
					t.Error("unexpected invalidation")
				}
				if terminal == "cancel" {
					cancel()
				}
			})
			expected := "AGENT_RPC_UNAVAILABLE"
			if terminal == "cancel" {
				expected = "AGENT_RPC_CANCELED"
			}
			if ErrorCode(err) != expected || callbacks != 1 {
				t.Fatalf("terminal=%v callbacks=%d", err, callbacks)
			}
			awaitDockerInventoryEvent(t, joined)
		})
	}
}

func TestDockerRealtimeEventsMissingSourceDoesNotUseLiveDocker(t *testing.T) {
	service := newDockerInventoryEventsService(nil)
	stream := &fakeDockerInventoryWatchServer{ctx: context.Background(), send: func(*structpb.Struct) error { t.Error("missing provider sent frame"); return nil }}
	if err := service.Watch(&emptypb.Empty{}, stream); grpcstatus.Code(err) != codes.Unavailable {
		t.Fatal(err)
	}
}

func TestDockerRealtimeEventsOldAgentReturnsProtocolMismatch(t *testing.T) {
	client := dockerInventoryEventsTestClient(t, nil) // 旧 Agent 未注册该独立服务。
	err := watchDockerInventory(context.Background(), client, func(bool) { t.Error("unsupported Agent emitted readiness") })
	if ErrorCode(err) != "AGENT_PROTOCOL_MISMATCH" {
		t.Fatalf("old Agent = %v", err)
	}
}

func TestDockerRealtimeEventsClientEstablishmentTimeoutCancelsReader(t *testing.T) {
	entered := make(chan struct{})
	joined := make(chan struct{})
	client := dockerInventoryEventsTestClient(t, fakeDockerInventoryEventsRPC(func(_ *emptypb.Empty, stream AgentDockerInventoryEventsService_WatchServer) error {
		defer close(joined)
		close(entered)
		<-stream.Context().Done()
		return stream.Context().Err()
	}))
	fireTimer := make(chan func(), 1)
	var fired atomic.Bool
	afterFunc := func(duration time.Duration, callback func()) func() bool {
		if duration != 5*time.Second {
			t.Errorf("establish budget = %v", duration)
		}
		fireTimer <- func() { fired.Store(true); callback() }
		return func() bool { return !fired.Load() }
	}
	result := make(chan error, 1)
	go func() {
		result <- watchDockerInventoryWithTimer(context.Background(), client, func(bool) { t.Error("timed-out Agent emitted readiness") }, afterFunc)
	}()
	fire := awaitDockerInventoryEvent(t, fireTimer)
	awaitDockerInventoryEvent(t, entered)
	fire()
	if err := awaitDockerInventoryEvent(t, result); ErrorCode(err) != "AGENT_RPC_TIMEOUT" {
		t.Fatalf("establish timeout = %v", err)
	}
	awaitDockerInventoryEvent(t, joined)
}

func TestDockerRealtimeEventsRejectUnknownProtobufFields(t *testing.T) {
	for _, nested := range []bool{false, true} {
		frame := dockerInventoryEventFrame(dockerInventoryEventReady)
		// 未知 protobuf 字段也不能变成隐含的原始事件载荷。
		if nested {
			frame.Fields["kind"].ProtoReflect().SetUnknown([]byte{0x40, 0x01})
		} else {
			frame.ProtoReflect().SetUnknown([]byte{0x40, 0x01})
		}
		if _, err := decodeDockerInventoryEvent(frame); ErrorCode(err) != "AGENT_RPC_RESPONSE_INVALID" {
			t.Fatalf("unknown protobuf field = %v", err)
		}
	}
}
