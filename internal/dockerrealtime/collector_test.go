package dockerrealtime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/docker"
)

type sourceFunc func(context.Context) (docker.Inventory, error)

func (f sourceFunc) CollectDockerInventory(ctx context.Context) (docker.Inventory, error) {
	return f(ctx)
}
func start(t *testing.T, source Source) (*Collector, context.CancelFunc) {
	t.Helper()
	c, err := NewCollector(source)
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
func TestDockerRealtimeDemandCadenceAndCopies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		c, cancel := start(t, sourceFunc(func(ctx context.Context) (docker.Inventory, error) {
			calls.Add(1)
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) != SourceTimeout {
				t.Error("wrong source budget")
			}
			now := time.Now()
			return docker.Inventory{CollectedAt: now, Containers: []docker.InventoryContainer{{Labels: map[string]string{"x": "y"}, Ports: []docker.PortMapping{{Protocol: "tcp"}}, StartedAt: &now, FinishedAt: &now}}, Projects: []docker.Project{{ConfigFiles: []string{"compose.yaml"}}}}, nil
		}))
		time.Sleep(time.Minute)
		synctest.Wait()
		if calls.Load() != 0 {
			t.Fatal("idle collected")
		}
		releases := make([]func(), 64)
		for i := range releases {
			releases[i], _ = c.Acquire(context.Background())
		}
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("calls=%d", calls.Load())
		}
		state := c.Latest()
		first := state.LastSuccessAt
		state.Inventory.Containers[0].Labels["x"] = "bad"
		state.Inventory.Containers[0].Ports[0].Protocol = "bad"
		*state.Inventory.Containers[0].StartedAt = time.Time{}
		*state.Inventory.Containers[0].FinishedAt = time.Time{}
		state.Inventory.Projects[0].ConfigFiles[0] = "bad"
		good := c.Latest().Inventory
		if good.Containers[0].Labels["x"] != "y" || good.Containers[0].Ports[0].Protocol != "tcp" || good.Containers[0].StartedAt.IsZero() || good.Containers[0].FinishedAt.IsZero() || good.Projects[0].ConfigFiles[0] != "compose.yaml" {
			t.Fatal("mutable snapshot escaped")
		}
		time.Sleep(4 * time.Second)
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatal("early sample")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if calls.Load() != 2 || !c.Latest().LastSuccessAt.After(first) {
			t.Fatal("missing sample")
		}
		for _, release := range releases {
			release()
			release()
		}
		synctest.Wait()
		time.Sleep(time.Minute)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatal("idle continued")
		}
		cancel()
		synctest.Wait()
		<-c.Done()
		if _, err := c.Acquire(context.Background()); err == nil {
			t.Fatal("stopped admitted demand")
		}
		if err := c.Run(context.Background()); err == nil {
			t.Fatal("ran twice")
		}
	})
}
func TestDockerRealtimeGenerationCancellationAndNoOverlap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		var old context.Context
		unblock := make(chan struct{})
		c, cancel := start(t, sourceFunc(func(ctx context.Context) (docker.Inventory, error) {
			calls.Add(1)
			if calls.Load() == 1 {
				old = ctx
				<-unblock
				return docker.Inventory{CollectedAt: time.Now(), Engine: docker.EngineInfo{ServerVersion: "old"}}, nil
			}
			return docker.Inventory{CollectedAt: time.Now(), Engine: docker.EngineInfo{ServerVersion: "new"}}, nil
		}))
		first, _ := c.Acquire(context.Background())
		synctest.Wait()
		first()
		second, _ := c.Acquire(context.Background())
		synctest.Wait()
		if old.Err() == nil {
			t.Fatal("last release did not cancel")
		}
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatal("overlapping source")
		}
		if c.Latest().Inventory != nil {
			t.Fatal("published in flight")
		}
		close(unblock)
		synctest.Wait()
		if calls.Load() != 2 || c.Latest().Inventory.Engine.ServerVersion != "new" {
			t.Fatal("new generation failed")
		}
		second()
		cancel()
		synctest.Wait()
		<-c.Done()
	})
}
func TestDockerRealtimeTimeoutErrorsAndRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		var good time.Time
		c, cancel := start(t, sourceFunc(func(ctx context.Context) (docker.Inventory, error) {
			calls.Add(1)
			switch calls.Load() {
			case 1:
				good = time.Now()
				return docker.Inventory{CollectedAt: good}, nil
			case 2:
				return docker.Inventory{}, errors.New("fixture")
			case 3:
				return docker.Inventory{CollectedAt: good}, nil
			case 4:
				return docker.Inventory{CollectedAt: time.Now().Add(6 * time.Second)}, nil
			case 5:
				<-ctx.Done()
				return docker.Inventory{CollectedAt: time.Now()}, nil
			default:
				return docker.Inventory{CollectedAt: time.Now()}, nil
			}
		}))
		release, _ := c.Acquire(context.Background())
		synctest.Wait()
		initial := c.Latest()
		for range 3 {
			time.Sleep(5 * time.Second)
			synctest.Wait()
			state := c.Latest()
			if state.LastErrorCode == "" || !state.LastSuccessAt.Equal(initial.LastSuccessAt) || !state.Inventory.CollectedAt.Equal(good) {
				t.Fatal("failure refreshed old data")
			}
		}
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if calls.Load() != 5 {
			t.Fatal(calls.Load())
		}
		// Deadline cancels a source even while the subscriber remains present.
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if calls.Load() != 6 || c.Latest().LastErrorCode != "" {
			t.Fatal("failed to recover after timeout")
		}
		release()
		cancel()
		synctest.Wait()
		<-c.Done()
	})
}
func TestDockerRealtimeCancellationAndShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var request atomic.Value
		c, cancel := start(t, sourceFunc(func(ctx context.Context) (docker.Inventory, error) {
			request.Store(ctx)
			<-ctx.Done()
			return docker.Inventory{}, ctx.Err()
		}))
		subscriber, stop := context.WithCancel(context.Background())
		release, _ := c.Acquire(subscriber)
		synctest.Wait()
		stop()
		synctest.Wait()
		if request.Load().(context.Context).Err() == nil {
			t.Fatal("subscriber cancellation lost")
		}
		release()
		_, _ = c.Acquire(context.Background())
		time.Sleep(5 * time.Second)
		synctest.Wait()
		cancel()
		synctest.Wait()
		<-c.Done()
		if request.Load().(context.Context).Err() == nil {
			t.Fatal("owner did not cancel source")
		}
	})
}
func TestDockerRealtimeBounds(t *testing.T) {
	now := time.Now()
	for _, value := range []docker.Inventory{
		{}, {CollectedAt: now.Add(-16 * time.Second)}, {CollectedAt: now.Add(6 * time.Second)},
		{CollectedAt: now, Engine: docker.EngineInfo{ServerVersion: strings.Repeat("x", MaxSnapshotBytes)}},
		{CollectedAt: now, Containers: []docker.InventoryContainer{{Labels: map[string]string{"secret": strings.Repeat("x", MaxSnapshotBytes)}}}},
	} {
		if validate(value, now) == "" {
			t.Fatal("accepted invalid snapshot")
		}
	}
	if _, err := NewCollector(nil); err == nil {
		t.Fatal("nil source allowed")
	}
}
func TestDockerRealtimeConcurrentReadersAndRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, cancel := start(t, sourceFunc(func(context.Context) (docker.Inventory, error) {
			return docker.Inventory{CollectedAt: time.Now(), Containers: []docker.InventoryContainer{{Labels: map[string]string{"a": "b"}}}}, nil
		}))
		release, _ := c.Acquire(context.Background())
		synctest.Wait()
		var wg sync.WaitGroup
		for range 64 {
			wg.Go(func() {
				for range 20 {
					r, _ := c.Acquire(context.Background())
					s := c.Latest()
					s.Inventory.Containers[0].Labels["a"] = "c"
					r()
				}
			})
		}
		wg.Wait()
		release()
		cancel()
		synctest.Wait()
		<-c.Done()
	})
}

func TestDockerRealtimeRefreshStartBarrierAndCoalescing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		old := make(chan struct{})
		c, cancel := start(t, sourceFunc(func(ctx context.Context) (docker.Inventory, error) {
			n := calls.Add(1)
			if n == 1 {
				<-old
			}
			return docker.Inventory{CollectedAt: time.Now(), Engine: docker.EngineInfo{Containers: int(n)}}, nil
		}))
		release, _ := c.Acquire(context.Background())
		synctest.Wait()
		type result struct {
			value docker.Inventory
			err   error
		}
		results := make(chan result, 2)
		for range 2 {
			go func() { v, e := c.Refresh(context.Background()); results <- result{v, e} }()
		}
		synctest.Wait()
		close(old)
		synctest.Wait()
		if len(results) != 0 {
			t.Fatal("older in-flight sample satisfied refresh")
		}
		time.Sleep(Interval - time.Nanosecond)
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatal("refresh bypassed start spacing")
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		for range 2 {
			r := <-results
			if r.err != nil || r.value.Engine.Containers != 2 {
				t.Fatalf("bad refresh: %+v", r)
			}
		}
		if calls.Load() != 2 {
			t.Fatal("requests did not coalesce")
		}
		release()
		cancel()
		synctest.Wait()
		<-c.Done()
	})
}

func TestDockerRealtimeRefreshCapacityCancellationAndFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		var request context.Context
		unblock := make(chan struct{})
		c, cancel := start(t, sourceFunc(func(ctx context.Context) (docker.Inventory, error) {
			request = ctx
			if calls.Add(1) == 1 {
				return docker.Inventory{CollectedAt: time.Now()}, nil
			}
			<-unblock
			return docker.Inventory{}, errors.New("fixture failure")
		}))
		release, _ := c.Acquire(context.Background())
		synctest.Wait()
		good := c.Latest().Inventory.CollectedAt
		results := make(chan error, MaxRefreshWaiters)
		contexts := make([]context.CancelFunc, MaxRefreshWaiters)
		for i := range contexts {
			ctx, stop := context.WithCancel(context.Background())
			contexts[i] = stop
			go func() { _, err := c.Refresh(ctx); results <- err }()
		}
		synctest.Wait()
		if _, err := c.Refresh(context.Background()); !errors.Is(err, ErrRefreshLimit) {
			t.Fatal("missing waiter limit", err)
		}
		time.Sleep(Interval)
		synctest.Wait()
		contexts[0]()
		synctest.Wait()
		if err := <-results; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if request.Err() != nil {
			t.Fatal("single cancellation canceled shared RPC")
		}
		close(unblock)
		synctest.Wait()
		for range MaxRefreshWaiters - 1 {
			if err := <-results; !errors.Is(err, ErrUnavailable) {
				t.Fatal("failure returned last-good success", err)
			}
		}
		if !c.Latest().Inventory.CollectedAt.Equal(good) {
			t.Fatal("failure replaced last-good")
		}
		c.mu.Lock()
		remaining := len(c.waiters)
		demand := c.demand
		c.mu.Unlock()
		if remaining != 0 || demand != 1 {
			t.Fatal("waiter demand leaked", remaining, demand)
		}
		release()
		cancel()
		synctest.Wait()
		<-c.Done()
	})
}

func TestDockerRealtimeRefreshIdleCancellationAndShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		var request context.Context
		c, cancel := start(t, sourceFunc(func(ctx context.Context) (docker.Inventory, error) {
			calls.Add(1)
			request = ctx
			<-ctx.Done()
			return docker.Inventory{}, ctx.Err()
		}))
		ctx, stop := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { _, err := c.Refresh(ctx); result <- err }()
		synctest.Wait()
		stop()
		synctest.Wait()
		if !errors.Is(<-result, context.Canceled) || request.Err() == nil {
			t.Fatal("last waiter did not cancel source")
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatal("idle polling continued")
		}
		go func() { _, err := c.Refresh(context.Background()); result <- err }()
		synctest.Wait()
		cancel()
		synctest.Wait()
		<-c.Done()
		if !errors.Is(<-result, ErrUnavailable) {
			t.Fatal("shutdown did not release waiter")
		}
		if _, err := c.Refresh(context.Background()); !errors.Is(err, ErrUnavailable) {
			t.Fatal("shutdown admitted refresh")
		}
	})
}

func TestDockerRealtimeRefreshBudgetsAndRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		c, cancel := start(t, sourceFunc(func(ctx context.Context) (docker.Inventory, error) {
			n := calls.Add(1)
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) != SourceTimeout {
				t.Error("source budget changed")
			}
			if n <= 2 {
				<-ctx.Done()
				return docker.Inventory{}, ctx.Err()
			}
			return docker.Inventory{CollectedAt: time.Now(), Containers: []docker.InventoryContainer{{Labels: map[string]string{"a": "b"}}}}, nil
		}))
		releases := make([]func(), 64)
		for i := range releases {
			releases[i], _ = c.Acquire(context.Background())
		}
		synctest.Wait()
		ctx, stop := context.WithTimeout(context.Background(), RefreshTimeout)
		defer stop()
		result := make(chan error, 1)
		go func() { _, err := c.Refresh(ctx); result <- err }()
		synctest.Wait()
		time.Sleep(SourceTimeout)
		synctest.Wait()
		if len(result) != 0 {
			t.Fatal("old source timeout completed newer barrier")
		}
		time.Sleep(SourceTimeout)
		synctest.Wait()
		if err := <-result; !errors.Is(err, ErrUnavailable) {
			t.Fatal("covered failure hidden by recovery", err)
		}
		if calls.Load() != 3 {
			t.Fatal("unexpected calls", calls.Load())
		}
		go func() { _, err := c.Refresh(ctx); result <- err }()
		synctest.Wait()
		// The existing 12-second request budget expires while waiting for the next start.
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("HTTP wait budget ignored", err)
		}
		for _, release := range releases {
			release()
		}
		cancel()
		synctest.Wait()
		<-c.Done()
	})
}

func TestDockerRealtimeRefreshOnlyCancellationCapacityReuseAndCopies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		unblock := make(chan struct{})
		var request context.Context
		var calls atomic.Int32
		c, err := NewCollector(sourceFunc(func(ctx context.Context) (docker.Inventory, error) {
			request = ctx
			calls.Add(1)
			<-unblock
			now := time.Now()
			return docker.Inventory{CollectedAt: now, Containers: []docker.InventoryContainer{{Labels: map[string]string{"a": "b"}, Ports: []docker.PortMapping{{Protocol: "tcp"}}, StartedAt: &now, FinishedAt: &now}}, Projects: []docker.Project{{ConfigFiles: []string{"compose.yaml"}}}}, nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		type result struct {
			value docker.Inventory
			err   error
		}
		results := make(chan result, MaxRefreshWaiters)
		contexts := make([]context.CancelFunc, MaxRefreshWaiters)
		for i := range contexts {
			ctx, stop := context.WithCancel(context.Background())
			contexts[i] = stop
			go func() { v, e := c.Refresh(ctx); results <- result{v, e} }()
		}
		synctest.Wait()
		if _, err := c.Refresh(context.Background()); !errors.Is(err, ErrRefreshLimit) {
			t.Fatal(err)
		}
		contexts[0]()
		synctest.Wait()
		if r := <-results; !errors.Is(r.err, context.Canceled) {
			t.Fatal(r.err)
		}
		go func() { v, e := c.Refresh(context.Background()); results <- result{v, e} }()
		synctest.Wait()
		c.mu.Lock()
		count := len(c.waiters)
		c.mu.Unlock()
		if count != MaxRefreshWaiters {
			t.Fatal("canceled capacity was not reusable", count)
		}
		owner, cancel := context.WithCancel(context.Background())
		go func() {
			if err := c.Run(owner); err != nil {
				t.Error(err)
			}
		}()
		synctest.Wait()
		contexts[1]()
		synctest.Wait()
		if r := <-results; !errors.Is(r.err, context.Canceled) {
			t.Fatal(r.err)
		}
		if request.Err() != nil {
			t.Fatal("one refresh cancellation canceled peers' source")
		}
		close(unblock)
		synctest.Wait()
		first := <-results
		if first.err != nil {
			t.Fatal(first.err)
		}
		first.value.Containers[0].Labels["a"] = "bad"
		first.value.Containers[0].Ports[0].Protocol = "bad"
		*first.value.Containers[0].StartedAt = time.Time{}
		*first.value.Containers[0].FinishedAt = time.Time{}
		first.value.Projects[0].ConfigFiles[0] = "bad"
		assertCopy := func(v docker.Inventory) {
			if v.Containers[0].Labels["a"] != "b" || v.Containers[0].Ports[0].Protocol != "tcp" || v.Containers[0].StartedAt.IsZero() || v.Containers[0].FinishedAt.IsZero() || v.Projects[0].ConfigFiles[0] != "compose.yaml" {
				t.Fatal("Refresh result shared mutable storage")
			}
		}
		for range MaxRefreshWaiters - 2 {
			r := <-results
			if r.err != nil {
				t.Fatal(r.err)
			}
			assertCopy(r.value)
		}
		assertCopy(*c.Latest().Inventory)
		c.mu.Lock()
		count = len(c.waiters)
		demand := c.demand
		c.mu.Unlock()
		if count != 0 || demand != 0 || calls.Load() != 1 {
			t.Fatal("refresh-only demand leaked or duplicated", count, demand, calls.Load())
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatal("refresh-only demand kept polling")
		}
		cancel()
		synctest.Wait()
		<-c.Done()
	})
}
