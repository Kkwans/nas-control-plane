package agentsocket

import (
	"context"
	"errors"
	"io"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

const dockerInventoryWatchEstablishTimeout = 5 * time.Second

// WatchDockerInventory 只连接一次，不自行重连。notify(true) 仅出现一次，随后
// notify(false) 表示需要完整对账。notify 必须立即返回（如只置 dirty），不可执行 RPC。
// ready 不是健康证明；任何返回都要求 owner 使当前事件代次失效。
func WatchDockerInventory(ctx context.Context, socketPath string, notify func(ready bool)) error {
	if err := ctx.Err(); err != nil {
		return contextError(err)
	}
	if notify == nil {
		return coded("AGENT_RPC_REQUEST_INVALID", errors.New("inventory event callback is required"))
	}
	connection, err := dialSocket(socketPath)
	if err != nil {
		return err
	}
	defer connection.Close()
	return watchDockerInventory(ctx, NewAgentDockerInventoryEventsServiceClient(connection), notify)
}

func watchDockerInventory(ctx context.Context, client AgentDockerInventoryEventsServiceClient, notify func(ready bool)) error {
	return watchDockerInventoryWithTimer(ctx, client, notify, func(duration time.Duration, callback func()) func() bool {
		return time.AfterFunc(duration, callback).Stop
	})
}

func watchDockerInventoryWithTimer(ctx context.Context, client AgentDockerInventoryEventsServiceClient, notify func(ready bool), afterFunc func(time.Duration, func()) func() bool) error {
	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	timedOut := make(chan struct{})
	stopTimer := afterFunc(dockerInventoryWatchEstablishTimeout, func() {
		cancel()
		close(timedOut)
	})
	defer stopTimer()
	ready := false
	terminal := func(err error) error {
		if ctx.Err() != nil {
			return contextError(ctx.Err())
		}
		if !ready && !stopTimer() {
			<-timedOut
			return contextError(context.DeadlineExceeded)
		}
		if errors.Is(err, io.EOF) {
			return coded("AGENT_RPC_UNAVAILABLE", err)
		}
		return rpcError(err)
	}
	stream, err := client.Watch(watchCtx, &emptypb.Empty{}, grpc.MaxCallRecvMsgSize(1024))
	if err != nil {
		return terminal(err)
	}
	for {
		frame, err := stream.Recv()
		if err != nil {
			return terminal(err)
		}
		if ctx.Err() != nil {
			return contextError(ctx.Err())
		}
		kind, err := decodeDockerInventoryEvent(frame)
		if err != nil {
			return err
		}
		if !ready {
			if kind != dockerInventoryEventReady {
				return coded("AGENT_RPC_RESPONSE_INVALID", errors.New("inventory events must begin with ready"))
			}
			if !stopTimer() {
				<-timedOut
				return contextError(context.DeadlineExceeded)
			}
			if ctx.Err() != nil {
				return contextError(ctx.Err())
			}
			ready = true
			notify(true)
			continue
		}
		if kind != dockerInventoryEventInvalidated {
			return coded("AGENT_RPC_RESPONSE_INVALID", errors.New("inventory events repeated ready"))
		}
		notify(false)
	}
}

func decodeDockerInventoryEvent(frame *structpb.Struct) (dockerInventoryEventKind, error) {
	if frame == nil || len(frame.Fields) != 1 || len(frame.ProtoReflect().GetUnknown()) != 0 {
		return "", coded("AGENT_RPC_RESPONSE_INVALID", errors.New("unexpected inventory event fields"))
	}
	value, ok := frame.Fields["kind"]
	if !ok || value == nil || len(value.ProtoReflect().GetUnknown()) != 0 {
		return "", coded("AGENT_RPC_RESPONSE_INVALID", errors.New("inventory event kind is missing"))
	}
	kind := dockerInventoryEventKind(value.GetStringValue())
	if kind != dockerInventoryEventReady && kind != dockerInventoryEventInvalidated {
		return "", coded("AGENT_RPC_RESPONSE_INVALID", errors.New("inventory event kind is invalid"))
	}
	return kind, nil
}
