package docker

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/moby/moby/client"
)

const inventoryEventsEstablishTimeout = 5 * time.Second

// InventoryEventSource 只报告清单失效，不向 Server 泄露 Docker 原始事件或属性。
// 每次 Watch 独占一个 client；重连及完整对账由 Server 的共享 owner 负责。
type InventoryEventSource struct {
	newClient func() (inventoryEventsClient, error)
	afterFunc func(time.Duration, func()) func() bool
}

type inventoryEventsClient interface {
	Events(context.Context, client.EventsListOptions) client.EventsResult
	Close() error
}

func NewLiveInventoryEventSource() *InventoryEventSource {
	return &InventoryEventSource{
		newClient: func() (inventoryEventsClient, error) {
			return client.New(client.WithHost(localDockerHost), client.WithAPIVersionNegotiation())
		},
		afterFunc: func(timeout time.Duration, callback func()) func() bool {
			return time.AfterFunc(timeout, callback).Stop
		},
	}
}

// Watch 阻塞到取消或源终止；notify 必须立即返回，且不保留原始事件。
// ready=true 仅表示本次 Events 调用已返回，可开始对账，不保证 Engine 健康。
// SDK 在握手失败时可能先返回、后发布 Err，因此任何终止都必须触发新代次。
func (s *InventoryEventSource) Watch(ctx context.Context, notify func(ready bool)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.newClient == nil || s.afterFunc == nil || notify == nil {
		return errors.New("docker inventory event source is unavailable")
	}
	api, err := s.newClient()
	if err != nil {
		return err
	}
	defer api.Close()
	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// 建立连接有界，已建立的静默流不受五秒 deadline 限制。
	timedOut := make(chan struct{})
	stop := s.afterFunc(inventoryEventsEstablishTimeout, func() {
		cancel()
		close(timedOut)
	})
	filters := make(client.Filters)
	filters.Add("type", "container")
	filters.Add("type", "image")
	stream := api.Events(watchCtx, client.EventsListOptions{Filters: filters})
	if !stop() {
		<-timedOut
	}
	// Moby 的 Messages 不会关闭；取消后等待 Err 关闭，回收 SDK 读流 goroutine。
	defer func() {
		cancel()
		if stream.Err != nil {
			for range stream.Err {
			}
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if watchCtx.Err() != nil {
		return context.DeadlineExceeded
	}
	if stream.Messages == nil || stream.Err == nil {
		return errors.New("docker inventory event stream is invalid")
	}
	// 已知终止优先于 ready；零等待检查无法消除 SDK 的异步失败竞态。
	select {
	case err, open := <-stream.Err:
		return inventoryEventTerminal(err, open)
	default:
	}
	notify(true)
	for {
		// 即使 Messages 持续就绪，已可见的 Err 也不能被事件风暴饿死。
		select {
		case err, open := <-stream.Err:
			return inventoryEventTerminal(err, open)
		default:
		}
		select {
		case <-watchCtx.Done():
			return watchCtx.Err()
		case err, open := <-stream.Err:
			return inventoryEventTerminal(err, open)
		case _, open := <-stream.Messages:
			if !open {
				return io.EOF
			}
			select {
			case err, open := <-stream.Err:
				return inventoryEventTerminal(err, open)
			default:
			}
			notify(false)
		}
	}
}

func inventoryEventTerminal(err error, open bool) error {
	if !open || err == nil {
		return io.EOF
	}
	return err
}
