package dockerrealtime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/docker"
)

type eventFunc func(context.Context, func(bool)) error

func (f eventFunc) WatchDockerInventory(ctx context.Context, notify func(bool)) error {
	return f(ctx, notify)
}
func startEvents(t *testing.T, source Source, events EventSource) (*Collector, context.CancelFunc) {
	t.Helper()
	c, err := NewEventCollector(source, events)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		if err := c.Run(ctx); err != nil {
			t.Error(err)
		}
	}()
	synctest.Wait()
	return c, cancel
}
func TestDockerRealtimeEventsSubscribeBeforeListAndStorm(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ready := make(chan struct{})
		storm := make(chan struct{})
		listed := make(chan struct{})
		finish := make(chan struct{})
		var calls, watches atomic.Int32
		c, cancel := startEvents(t, sourceFunc(func(ctx context.Context) (docker.Inventory, error) {
			n := calls.Add(1)
			if n == 1 {
				close(listed)
				<-finish
			}
			return docker.Inventory{CollectedAt: time.Now()}, nil
		}), eventFunc(func(ctx context.Context, notify func(bool)) error {
			watches.Add(1)
			select {
			case <-ready:
			case <-ctx.Done():
				return ctx.Err()
			}
			notify(true)
			select {
			case <-storm:
			case <-ctx.Done():
				return ctx.Err()
			}
			for i := 0; i < 10000; i++ {
				notify(false)
			}
			<-ctx.Done()
			return ctx.Err()
		}))
		synctest.Wait()
		if watches.Load() != 0 {
			t.Fatal("idle subscribed")
		}
		releases := make([]func(), 64)
		for i := range releases {
			releases[i], _ = c.Acquire(context.Background())
		}
		release := func() {
			for _, stop := range releases {
				stop()
			}
		}
		synctest.Wait()
		if calls.Load() != 0 || watches.Load() != 1 {
			t.Fatal("list preceded ready")
		}
		close(ready)
		<-listed
		close(storm)
		synctest.Wait()
		close(finish)
		synctest.Wait()
		c.mu.Lock()
		dirty, clean := c.dirtyGeneration, c.reconciledDirty
		c.mu.Unlock()
		if dirty <= clean {
			t.Fatal("events during list were cleared")
		}
		if calls.Load() != 1 {
			t.Fatal("storm bypassed throttle")
		}
		time.Sleep(Interval)
		synctest.Wait()
		if calls.Load() != 2 || c.Latest().LastErrorCode != "" {
			t.Fatal("did not reconcile storm")
		}
		// 没有事件也执行真实周期采集，不能用事件/心跳保鲜。
		time.Sleep(Interval)
		synctest.Wait()
		if calls.Load() != 3 {
			t.Fatal("missing fallback poll")
		}
		release()
		cancel()
		<-c.Done()
	})
}
func TestDockerRealtimeEventsEpochAndReconnectFullList(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		disconnect := make(chan struct{})
		finish := make(chan struct{})
		secondReady := make(chan struct{})
		var calls, watches atomic.Int32
		c, cancel := startEvents(t, sourceFunc(func(ctx context.Context) (docker.Inventory, error) {
			n := calls.Add(1)
			if n == 1 {
				<-finish
			}
			value := docker.Inventory{CollectedAt: time.Now()}
			if n == 1 {
				value.Containers = []docker.InventoryContainer{{ID: "deleted"}}
			}
			return value, nil
		}), eventFunc(func(ctx context.Context, notify func(bool)) error {
			n := watches.Add(1)
			if n == 1 {
				notify(true)
				<-disconnect
				return errors.New("EOF")
			}
			select {
			case <-secondReady:
			case <-ctx.Done():
				return ctx.Err()
			}
			notify(true)
			<-ctx.Done()
			return ctx.Err()
		}))
		release, _ := c.Acquire(context.Background())
		synctest.Wait()
		close(disconnect)
		synctest.Wait()
		close(finish)
		synctest.Wait()
		if c.Latest().LastErrorCode == "" {
			t.Fatal("old epoch cleared disconnected")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if watches.Load() != 2 {
			t.Fatal("missing reconnect")
		}
		close(secondReady)
		synctest.Wait()
		if c.Latest().LastErrorCode == "" {
			t.Fatal("ready acknowledged health")
		}
		time.Sleep(4 * time.Second)
		synctest.Wait()
		state := c.Latest()
		if calls.Load() != 2 || state.LastErrorCode != "" || len(state.Inventory.Containers) != 0 {
			t.Fatal("reconnect did not fully reconcile deletion")
		}
		release()
		cancel()
		<-c.Done()
	})
}
func TestDockerRealtimeEventsBackoffReadyIsNotRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var watches atomic.Int32
		c, cancel := startEvents(t, sourceFunc(func(ctx context.Context) (docker.Inventory, error) {
			return docker.Inventory{}, errors.New("unavailable")
		}), eventFunc(func(ctx context.Context, notify func(bool)) error {
			watches.Add(1)
			notify(true)
			return errors.New("EOF")
		}))
		release, _ := c.Acquire(context.Background())
		synctest.Wait()
		if watches.Load() != 1 {
			t.Fatal("initial attempt")
		}
		for i, delay := range []time.Duration{1, 2, 4, 8, 16, 30, 30} {
			time.Sleep(delay*time.Second - time.Nanosecond)
			synctest.Wait()
			if int(watches.Load()) != i+1 {
				t.Fatalf("early reconnect step %d", i)
			}
			time.Sleep(time.Nanosecond)
			synctest.Wait()
			if int(watches.Load()) != i+2 {
				t.Fatalf("missing reconnect step %d", i)
			}
		}
		if c.Latest().LastErrorCode == "" {
			t.Fatal("failed stream healthy")
		}
		release()
		cancel()
		<-c.Done()
	})
}
func TestDockerRealtimeEventsReleaseJoinsBeforeNewWatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		exited := make(chan struct{})
		unblock := make(chan struct{})
		var watches atomic.Int32
		c, cancel := startEvents(t, sourceFunc(func(ctx context.Context) (docker.Inventory, error) {
			return docker.Inventory{CollectedAt: time.Now()}, nil
		}), eventFunc(func(ctx context.Context, notify func(bool)) error {
			n := watches.Add(1)
			notify(true)
			<-ctx.Done()
			if n == 1 {
				close(exited)
				<-unblock
			}
			return ctx.Err()
		}))
		release, _ := c.Acquire(context.Background())
		synctest.Wait()
		release()
		<-exited
		release2, _ := c.Acquire(context.Background())
		synctest.Wait()
		if watches.Load() != 1 {
			t.Fatal("overlapping watches")
		}
		close(unblock)
		synctest.Wait()
		if watches.Load() != 2 {
			t.Fatal("new demand lost")
		}
		release2()
		cancel()
		<-c.Done()
	})
}

func TestDockerRealtimeEventsFallbackBeforeReadyCannotRecover(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		disconnected := make(chan struct{})
		ready := make(chan struct{})
		collecting := make(chan struct{})
		finish := make(chan struct{})
		var watches, calls atomic.Int32
		c, cancel := startEvents(t, sourceFunc(func(ctx context.Context) (docker.Inventory, error) {
			if calls.Add(1) == 2 {
				close(collecting)
				<-finish
			}
			return docker.Inventory{CollectedAt: time.Now()}, nil
		}), eventFunc(func(ctx context.Context, notify func(bool)) error {
			if watches.Add(1) == 1 {
				notify(true)
				<-disconnected
				return errors.New("EOF")
			}
			select {
			case <-ready:
			case <-ctx.Done():
				return ctx.Err()
			}
			notify(true)
			<-ctx.Done()
			return ctx.Err()
		}))
		release, _ := c.Acquire(context.Background())
		synctest.Wait()
		close(disconnected)
		synctest.Wait()
		time.Sleep(Interval)
		<-collecting
		close(ready)
		synctest.Wait()
		close(finish)
		synctest.Wait()
		if c.Latest().LastErrorCode == "" {
			t.Fatal("list started before ready acknowledged recovery")
		}
		time.Sleep(Interval)
		synctest.Wait()
		if c.Latest().LastErrorCode != "" {
			t.Fatal("current epoch full list did not recover")
		}
		release()
		cancel()
		<-c.Done()
	})
}
func TestDockerRealtimeEventsRefreshFailsDuringRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first := make(chan struct{})
		disconnect := make(chan struct{})
		var watches, calls atomic.Int32
		c, cancel := startEvents(t, sourceFunc(func(ctx context.Context) (docker.Inventory, error) {
			if calls.Add(1) == 1 {
				close(first)
			}
			return docker.Inventory{CollectedAt: time.Now()}, nil
		}), eventFunc(func(ctx context.Context, notify func(bool)) error {
			if watches.Add(1) == 1 {
				notify(true)
				<-disconnect
				return errors.New("EOF")
			}
			<-ctx.Done()
			return ctx.Err()
		}))
		release, _ := c.Acquire(context.Background())
		<-first
		synctest.Wait()
		close(disconnect)
		synctest.Wait()
		result := make(chan error, 1)
		go func() { _, err := c.Refresh(context.Background()); result <- err }()
		synctest.Wait()
		time.Sleep(Interval)
		synctest.Wait()
		if err := <-result; !errors.Is(err, ErrUnavailable) {
			t.Fatalf("recovery refresh error=%v", err)
		}
		release()
		cancel()
		<-c.Done()
	})
}

func TestDockerRealtimeEventsBackoffResetsAfterReconcile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		disconnect := make(chan struct{})
		var watches atomic.Int32
		c, cancel := startEvents(t, sourceFunc(func(ctx context.Context) (docker.Inventory, error) {
			return docker.Inventory{CollectedAt: time.Now()}, nil
		}), eventFunc(func(ctx context.Context, notify func(bool)) error {
			n := watches.Add(1)
			if n < 3 {
				return errors.New("unavailable")
			}
			notify(true)
			if n == 3 {
				<-disconnect
				return errors.New("EOF")
			}
			<-ctx.Done()
			return ctx.Err()
		}))
		release, _ := c.Acquire(context.Background())
		synctest.Wait()
		time.Sleep(3 * time.Second)
		synctest.Wait()
		if watches.Load() != 3 || c.Latest().LastErrorCode != "" {
			t.Fatal("third attempt did not reconcile")
		}
		close(disconnect)
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		if watches.Load() != 4 {
			t.Fatal("successful reconcile did not reset backoff")
		}
		release()
		cancel()
		<-c.Done()
	})
}

func TestDockerRealtimeEventsShutdownWaitsForReader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		canceled := make(chan struct{})
		unblock := make(chan struct{})
		c, cancel := startEvents(t, sourceFunc(func(ctx context.Context) (docker.Inventory, error) {
			return docker.Inventory{CollectedAt: time.Now()}, nil
		}), eventFunc(func(ctx context.Context, notify func(bool)) error {
			notify(true)
			<-ctx.Done()
			close(canceled)
			<-unblock
			return ctx.Err()
		}))
		release, _ := c.Acquire(context.Background())
		synctest.Wait()
		cancel()
		<-canceled
		synctest.Wait()
		select {
		case <-c.Done():
			t.Fatal("owner reported completion before reader joined")
		default:
		}
		close(unblock)
		<-c.Done()
		release()
	})
}
