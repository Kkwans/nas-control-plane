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
