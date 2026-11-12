package agentsocket

import (
	"context"
	"sync/atomic"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

type DockerInventoryEventsProvider interface {
	// Watch 必须响应取消，并在返回前回收自己的读流。notify 必须串行调用。
	Watch(context.Context, func(ready bool)) error
}

type dockerInventoryEventKind string

const (
	dockerInventoryEventReady       dockerInventoryEventKind = "ready"
	dockerInventoryEventInvalidated dockerInventoryEventKind = "invalidated"
)

type dockerInventoryEventsService struct {
	provider DockerInventoryEventsProvider
	watching atomic.Bool
}

// 不允许 nil 隐式启动 live Docker；只有 Serve 的生产装配负责选择真实 provider。
func newDockerInventoryEventsService(provider DockerInventoryEventsProvider) *dockerInventoryEventsService {
	return &dockerInventoryEventsService{provider: provider}
}

func (s *dockerInventoryEventsService) Watch(_ *emptypb.Empty, stream AgentDockerInventoryEventsService_WatchServer) error {
	if err := stream.Context().Err(); err != nil {
		return grpcstatus.FromContextError(err).Err()
	}
	if s.provider == nil {
		return grpcstatus.Error(codes.Unavailable, "AGENT_DOCKER_INVENTORY_EVENTS_UNAVAILABLE")
	}
	if !s.watching.CompareAndSwap(false, true) {
		return grpcstatus.Error(codes.ResourceExhausted, "AGENT_DOCKER_INVENTORY_EVENTS_LIMIT_EXCEEDED")
	}
	defer s.watching.Store(false)
	ctx, cancel := context.WithCancel(stream.Context())
	ready := make(chan struct{}, 1)
	dirty := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.provider.Watch(ctx, func(isReady bool) {
			notification := dirty
			if isReady {
				notification = ready
			}
			select {
			case notification <- struct{}{}:
			default: // dirty 是容量一的失效标志，合并无需传输原始事件。
			}
		})
	}()
	defer func() {
		cancel()
		<-done
	}()
	terminal := func() error {
		if err := stream.Context().Err(); err != nil {
			return grpcstatus.FromContextError(err).Err()
		}
		// 不跨 Root 边界传送 Engine 错误详情。
		return grpcstatus.Error(codes.Unavailable, "AGENT_DOCKER_INVENTORY_EVENTS_UNAVAILABLE")
	}
	// 整条流只有一个 sender，首帧也不能阻塞 handler 对源终止的处理。
	// Send 可能要等 handler 返回、gRPC 取消传输后才解除，因此不可在返回前 join sender。
	// source reader 则必须在上面的 defer 中取消并 join，释放名额前不允许重开 Engine 流。
	sendDone := make(chan error, 1)
	go func() { sendDone <- sendDockerInventoryEvents(ctx, stream, ready, dirty, done) }()
	select {
	case <-ctx.Done():
		return grpcstatus.FromContextError(ctx.Err()).Err()
	case <-done:
		return terminal()
	case err := <-sendDone:
		select {
		case <-done:
			return terminal()
		default:
		}
		return err
	}
}

func sendDockerInventoryEvents(ctx context.Context, stream AgentDockerInventoryEventsService_WatchServer, ready, dirty, done <-chan struct{}) error {
	terminal := func() error {
		if err := ctx.Err(); err != nil {
			return grpcstatus.FromContextError(err).Err()
		}
		return grpcstatus.Error(codes.Unavailable, "AGENT_DOCKER_INVENTORY_EVENTS_UNAVAILABLE")
	}
	// 首帧必须为 ready；源先结束时不能让残留 ready 继续宣称可对账。
	select {
	case <-ctx.Done():
		return grpcstatus.FromContextError(ctx.Err()).Err()
	case <-done:
		return terminal()
	case <-ready:
	}
	if err := ctx.Err(); err != nil {
		return grpcstatus.FromContextError(err).Err()
	}
	select {
	case <-done:
		return terminal()
	default:
	}
	if err := stream.Send(dockerInventoryEventFrame(dockerInventoryEventReady)); err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return grpcstatus.FromContextError(err).Err()
		}
		select {
		case <-done:
			return terminal()
		default:
		}
		select {
		case <-ctx.Done():
			return grpcstatus.FromContextError(ctx.Err()).Err()
		case <-done:
			return terminal()
		case <-dirty:
			if err := ctx.Err(); err != nil {
				return grpcstatus.FromContextError(err).Err()
			}
			select {
			case <-done:
				return terminal()
			default:
			}
			// Send 与源 reader 分离。传输阻塞期间仍持续消费 Docker 并合并 dirty。
			// handler 返回后由 gRPC 取消解除 Send；不为每条通知创建 goroutine。
			if err := stream.Send(dockerInventoryEventFrame(dockerInventoryEventInvalidated)); err != nil {
				return err
			}
		}
	}
}

func dockerInventoryEventFrame(kind dockerInventoryEventKind) *structpb.Struct {
	return &structpb.Struct{Fields: map[string]*structpb.Value{"kind": structpb.NewStringValue(string(kind))}}
}
