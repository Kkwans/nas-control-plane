// Package dockerrealtime 管理 Docker SSE 与强刷新共享的按需清单。
package dockerrealtime

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/docker"
)

const (
	Interval          = 5 * time.Second
	SourceTimeout     = 5 * time.Second
	RefreshTimeout    = 12 * time.Second
	MaxRefreshWaiters = 64
	MaxAge            = 15 * time.Second
	FutureSkew        = 5 * time.Second
	MaxSnapshotBytes  = 1 << 20
)

var (
	ErrUnavailable  = errors.New("docker realtime unavailable")
	ErrRefreshLimit = errors.New("docker refresh waiter limit reached")
)

// 每个等待者只引用一份不可变的轮次结果，不分配 payload 队列。
type refreshWaiter struct {
	completed bool
	inventory *docker.Inventory
	err       error
}

type Source interface {
	CollectDockerInventory(context.Context) (docker.Inventory, error)
}
type State struct {
	Inventory                    *docker.Inventory
	LastAttemptAt, LastSuccessAt time.Time
	LastErrorCode                string
}

type Collector struct {
	source            Source
	events            EventSource
	eventCancel       context.CancelFunc
	eventEpoch        uint64
	dirtyGeneration   uint64
	reconciledDirty   uint64
	eventReady        bool
	everReady         bool
	reconciledEpoch   uint64
	mu                sync.Mutex
	state             State
	started, stopped  bool
	demand            int
	generation        uint64
	requestedRevision uint64
	waiters           map[uint64]*refreshWaiter
	changed           chan struct{}
	cancel            context.CancelFunc
	wake              chan struct{}
	done              chan struct{}
}

// NewCollector 构造显式的仅轮询 owner，保留隔离 fixture 与旧接口兼容。
// 生产装配必须用 NewEventCollector，事件缺失不得静默降级为此构造器。
func NewCollector(source Source) (*Collector, error) {
	if source == nil {
		return nil, errors.New("docker realtime source is required")
	}
	return &Collector{source: source, wake: make(chan struct{}, 1), done: make(chan struct{}), changed: make(chan struct{}), waiters: make(map[uint64]*refreshWaiter)}, nil
}
func (c *Collector) Done() <-chan struct{} { return c.done }
func (c *Collector) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Acquire 只登记需求；不创建采集 goroutine。最后一个订阅释放立即取消在途 RPC。
func (c *Collector) Acquire(ctx context.Context) (func(), error) {
	c.mu.Lock()
	if c.stopped || ctx.Err() != nil {
		c.mu.Unlock()
		return nil, ErrUnavailable
	}
	c.addDemandLocked()
	c.mu.Unlock()
	c.signal()
	var once sync.Once
	release := func() {
		once.Do(func() {
			c.mu.Lock()
			c.removeDemandLocked()
			c.mu.Unlock()
			c.signal()
		})
	}
	stop := context.AfterFunc(ctx, release)
	return func() { stop(); release() }, nil
}

// Refresh 只接受登记后开始的采集；失败轮次不会用 last-good 冒充成功。
// 取消只释放自身需求；其他等待者或 SSE 仍需要的源 RPC 保持运行。
func (c *Collector) Refresh(ctx context.Context) (docker.Inventory, error) {
	c.mu.Lock()
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return docker.Inventory{}, err
	}
	if c.stopped {
		c.mu.Unlock()
		return docker.Inventory{}, ErrUnavailable
	}
	if len(c.waiters) >= MaxRefreshWaiters {
		c.mu.Unlock()
		return docker.Inventory{}, ErrRefreshLimit
	}
	c.requestedRevision++
	revision := c.requestedRevision
	waiter := &refreshWaiter{}
	c.waiters[revision] = waiter
	c.addDemandLocked()
	c.mu.Unlock()
	c.signal()
	defer func() {
		c.mu.Lock()
		delete(c.waiters, revision)
		c.removeDemandLocked()
		c.mu.Unlock()
		c.signal()
	}()
	for {
		c.mu.Lock()
		changed := c.changed
		completed, value, err := waiter.completed, waiter.inventory, waiter.err
		stopped := c.stopped
		c.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return docker.Inventory{}, err
		}
		if stopped {
			return docker.Inventory{}, ErrUnavailable
		}
		if completed {
			if err != nil {
				return docker.Inventory{}, err
			}
			return clone(*value), nil
		}
		select {
		case <-ctx.Done():
			return docker.Inventory{}, ctx.Err()
		case <-c.done:
			return docker.Inventory{}, ErrUnavailable
		case <-changed:
		}
	}
}
func (c *Collector) addDemandLocked() {
	if c.demand == 0 {
		c.generation++
		c.state.LastErrorCode = "DOCKER_INVENTORY_UNAVAILABLE"
	}
	c.demand++
}
func (c *Collector) removeDemandLocked() {
	c.demand--
	if c.demand == 0 {
		c.generation++
		c.state.LastErrorCode = "DOCKER_INVENTORY_UNAVAILABLE"
		if c.eventCancel != nil {
			c.eventCancel()
		}
		if c.cancel != nil {
			c.cancel()
		}
	}
}

func (c *Collector) Latest() State {
	c.mu.Lock()
	state := c.state
	c.mu.Unlock()
	// 已发布对象不再修改；复制不占 owner 锁，也不等待源 RPC。
	if state.Inventory != nil {
		value := clone(*state.Inventory)
		state.Inventory = &value
	}
	return state
}
func clone(value docker.Inventory) docker.Inventory {
	value.Containers = slices.Clone(value.Containers)
	for i := range value.Containers {
		item := &value.Containers[i]
		item.Labels = maps.Clone(item.Labels)
		item.Ports = slices.Clone(item.Ports)
		if item.StartedAt != nil {
			at := *item.StartedAt
			item.StartedAt = &at
		}
		if item.FinishedAt != nil {
			at := *item.FinishedAt
			item.FinishedAt = &at
		}
	}
	value.Projects = slices.Clone(value.Projects)
	for i := range value.Projects {
		value.Projects[i].ConfigFiles = slices.Clone(value.Projects[i].ConfigFiles)
	}
	return value
}

// Run 由服务 owner 显式启动且仅一次。同步调用源保证取消后的旧代退出前不启动新调用。
func (c *Collector) Run(ctx context.Context) error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return errors.New("docker realtime already started")
	}
	c.started = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.stopped = true
		c.state.LastErrorCode = "DOCKER_INVENTORY_UNAVAILABLE"
		c.mu.Unlock()
		close(c.done)
	}()
	var lastStart time.Time
	var eventDone chan struct{}
	var eventCancel context.CancelFunc
	var eventGeneration uint64
	defer func() {
		if eventCancel != nil {
			eventCancel()
			<-eventDone // Done 不早于响应取消的事件 reader 退出。
		}
	}()
	for {
		if ctx.Err() != nil {
			return nil
		}
		c.mu.Lock()
		demand, generation := c.demand, c.generation
		c.mu.Unlock()
		if eventDone != nil && (demand == 0 || generation != eventGeneration) {
			eventCancel()
			<-eventDone
			eventDone = nil
			eventCancel = nil
		}
		if c.events != nil && demand > 0 && eventDone == nil {
			c.mu.Lock()
			if c.demand == 0 || generation != c.generation {
				c.mu.Unlock()
				continue
			}
			eventCtx, cancel := context.WithCancel(ctx)
			eventCancel, c.eventCancel = cancel, cancel
			eventGeneration = generation
			c.everReady, c.eventReady = false, false
			eventDone = make(chan struct{})
			c.mu.Unlock()
			go func(done chan struct{}) { defer close(done); c.watchEvents(eventCtx, generation) }(eventDone)
		}
		if demand == 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-c.wake:
			}
			continue
		}
		c.mu.Lock()
		awaitReady := c.events != nil && !c.everReady
		c.mu.Unlock()
		if awaitReady {
			select {
			case <-ctx.Done():
				return nil
			case <-c.wake:
			}
			continue
		}
		// 迟到调用和需求切换均不能绕过真实 5 秒最小采集间隔。
		if delay := time.Until(lastStart.Add(Interval)); !lastStart.IsZero() && delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-c.wake:
				timer.Stop()
				continue
			case <-timer.C:
			}
		}
		c.mu.Lock()
		if c.demand == 0 || ctx.Err() != nil || (c.events != nil && (c.generation != eventGeneration || !c.everReady)) {
			c.mu.Unlock()
			continue
		}
		generation = c.generation
		epoch, dirty, ready := c.eventEpoch, c.dirtyGeneration, c.eventReady
		revision := c.requestedRevision
		request, cancel := context.WithTimeout(ctx, SourceTimeout)
		c.cancel = cancel
		lastStart = time.Now()
		c.state.LastAttemptAt = lastStart
		c.mu.Unlock()
		value, err := c.source.CollectDockerInventory(request)
		if err == nil {
			err = request.Err()
		}
		cancel()
		received := time.Now()
		code := ""
		if err != nil {
			code = "DOCKER_INVENTORY_UNAVAILABLE"
		} else {
			code = validate(value, received)
		}
		var copied *docker.Inventory
		if code == "" {
			item := clone(value)
			copied = &item
		}
		c.mu.Lock()
		c.cancel = nil
		if ctx.Err() == nil && c.demand > 0 && generation == c.generation {
			if code == "" && c.state.Inventory != nil && !value.CollectedAt.After(c.state.Inventory.CollectedAt) {
				code = "DOCKER_INVENTORY_UNAVAILABLE"
			}
			c.state.LastErrorCode = code
			if c.events != nil && (!ready || !c.eventReady || epoch != c.eventEpoch) {
				c.state.LastErrorCode = "DOCKER_INVENTORY_UNAVAILABLE"
			}
			if code == "" {
				if c.events != nil && ready && c.eventReady && epoch == c.eventEpoch {
					c.reconciledEpoch, c.reconciledDirty = epoch, dirty
				}
				c.state.Inventory = copied
				c.state.LastSuccessAt = received
			}
			for requested, waiter := range c.waiters {
				if requested <= revision && !waiter.completed {
					waiter.completed = true
					if c.state.LastErrorCode == "" {
						waiter.inventory = copied
					} else {
						waiter.err = ErrUnavailable
					}
				}
			}
			close(c.changed)
			c.changed = make(chan struct{})
		}
		c.mu.Unlock()
	}
}
func validate(value docker.Inventory, now time.Time) string {
	if value.CollectedAt.IsZero() || value.CollectedAt.After(now.Add(FutureSkew)) || now.Sub(value.CollectedAt) > MaxAge {
		return "DOCKER_INVENTORY_UNAVAILABLE"
	}
	data, err := json.Marshal(value)
	if err != nil || len(data) > MaxSnapshotBytes {
		return "DOCKER_INVENTORY_UNAVAILABLE"
	}
	// Labels 不在线上 JSON 中，但必须计入内部保留预算，不能形成无界旁路。
	size := len(data)
	for _, item := range value.Containers {
		for key, value := range item.Labels {
			size += len(key) + len(value) + 32
			if size > MaxSnapshotBytes {
				return "DOCKER_INVENTORY_UNAVAILABLE"
			}
		}
	}
	return ""
}
