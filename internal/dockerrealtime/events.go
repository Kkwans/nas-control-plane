package dockerrealtime

import (
	"context"
	"errors"
	"time"
)

// EventSource 在调用返回前持续读取；ready 只允许开始对账，不是健康确认。
// notify 必须串行调用。false 是可合并的失效提示，不携带 Engine 属性。
type EventSource interface {
	WatchDockerInventory(context.Context, func(ready bool)) error
}

// NewEventCollector 要求显式事件源；生产不能静默退回无事件轮询。
func NewEventCollector(source Source, events EventSource) (*Collector, error) {
	if events == nil {
		return nil, errors.New("docker event source is required")
	}
	c, err := NewCollector(source)
	if err != nil {
		return nil, err
	}
	c.events = events
	return c, nil
}

func (c *Collector) watchEvents(ctx context.Context, generation uint64) {
	backoff := time.Second
	for ctx.Err() == nil {
		c.mu.Lock()
		if generation != c.generation || c.demand == 0 {
			c.mu.Unlock()
			return
		}
		c.eventEpoch++
		epoch := c.eventEpoch
		c.eventReady = false
		c.dirtyGeneration++
		c.state.LastErrorCode = "DOCKER_INVENTORY_UNAVAILABLE"
		c.mu.Unlock()
		c.signal()
		// 同步 Watch 保证旧 reader 返回后才重连；故障期间不叠加订阅。
		_ = c.events.WatchDockerInventory(ctx, func(ready bool) {
			c.mu.Lock()
			if ctx.Err() == nil && generation == c.generation && epoch == c.eventEpoch {
				if ready {
					c.eventReady, c.everReady = true, true
				}
				c.dirtyGeneration++
			}
			c.mu.Unlock()
			c.signal()
		})
		c.mu.Lock()
		recovered := c.reconciledEpoch == epoch
		if generation == c.generation && epoch == c.eventEpoch {
			c.eventEpoch++ // 使断线前已在途的清单不能清除恢复状态。
			c.eventReady = false
			c.dirtyGeneration++
			c.state.LastErrorCode = "DOCKER_INVENTORY_UNAVAILABLE"
		}
		c.mu.Unlock()
		c.signal()
		if ctx.Err() != nil {
			return
		}
		if recovered {
			backoff = time.Second
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}
