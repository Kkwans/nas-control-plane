package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/Kkwans/nas-control-plane/internal/dockerrealtime"
)

// 所有测试仅使用内存 provider、Agent spy 和 HTTP writer，不连接真实服务。
type dockerRealtimeSnapshotProvider struct {
	state     dockerrealtime.State
	done      chan struct{}
	acquired  chan context.Context
	refresh   func(context.Context) (docker.Inventory, error)
	reads     atomic.Int32
	acquires  atomic.Int32
	releases  atomic.Int32
	refreshes atomic.Int32
}

func dockerRealtimeProvider(at time.Time) *dockerRealtimeSnapshotProvider {
	return &dockerRealtimeSnapshotProvider{
		state: dockerrealtime.State{Inventory: &docker.Inventory{CollectedAt: at, Engine: docker.EngineInfo{ServerVersion: "cached"}}, LastAttemptAt: at, LastSuccessAt: at},
		done:  make(chan struct{}), acquired: make(chan context.Context, 16),
	}
}
func (p *dockerRealtimeSnapshotProvider) Acquire(ctx context.Context) (func(), error) {
	p.acquires.Add(1)
	p.acquired <- ctx
	return func() { p.releases.Add(1) }, nil
}
func (p *dockerRealtimeSnapshotProvider) Latest() dockerrealtime.State {
	p.reads.Add(1)
	return p.state
}
func (p *dockerRealtimeSnapshotProvider) Refresh(ctx context.Context) (docker.Inventory, error) {
	p.refreshes.Add(1)
	if p.refresh != nil {
		return p.refresh(ctx)
	}
	return docker.Inventory{}, dockerrealtime.ErrUnavailable
}
func (p *dockerRealtimeSnapshotProvider) Done() <-chan struct{} { return p.done }

func TestDockerRealtimeNoProviderNeverCallsAgent(t *testing.T) {
	now := time.Now()
	agent := &metricsAgentSpy{}
	api := &handler{agent: agent, summarySnapshots: metricsProvider(now), now: func() time.Time { return now }}
	writer := metricsWriter()
	cancel, done := metricsStartStream(api, writer, "?scope=summary&scope=docker")
	defer cancel()
	writer.frame(t)
	snapshot := writer.snapshot(t)
	cancel()
	metricsAwaitDone(t, done)
	if snapshot.Summary == nil || snapshot.Docker != nil || !slices.Contains(snapshot.Errors, "DOCKER_INVENTORY_UNAVAILABLE") {
		t.Fatalf("missing provider snapshot=%#v", snapshot)
	}
	if agent.dockerCalls.Load() != 0 || agent.summaryCalls.Load() != 0 {
		t.Fatal("SSE fell back to direct Agent RPC")
	}
}

func TestDockerRealtimeCachedSnapshotDoesNotBlockSummaryOrHeartbeat(t *testing.T) {
	now := time.Now()
	provider := dockerRealtimeProvider(now)
	// 若实现意外回退到请求内 RPC，这个假调用会一直等待请求取消。
	agent := &metricsAgentSpy{docker: func(ctx context.Context) (docker.Inventory, error) {
		<-ctx.Done()
		return docker.Inventory{}, ctx.Err()
	}}
	tickers := metricsTickers()
	api := &handler{agent: agent, agentTimeout: time.Hour, summarySnapshots: metricsProvider(now), dockerSnapshots: provider, now: func() time.Time { return now }, newRealtimeTicker: tickers.create}
	writer := metricsWriter()
	cancel, done := metricsStartStream(api, writer, "?scope=summary&scope=docker")
	defer cancel()
	writer.frame(t)
	first := writer.snapshot(t)
	if first.Summary == nil || first.Docker == nil || first.Docker.Engine.ServerVersion != "cached" {
		t.Fatalf("snapshot=%#v", first)
	}
	poll, heartbeat := tickers.next(t), tickers.next(t)
	heartbeat.ticks <- now
	if frame := writer.frame(t); frame != ": heartbeat\n\n" {
		t.Fatalf("heartbeat=%q", frame)
	}
	if provider.reads.Load() != 1 {
		t.Fatal("heartbeat read Docker provider")
	}
	poll.ticks <- now
	second := writer.snapshot(t)
	if second.Summary == nil || second.Docker == nil || !second.Docker.CollectedAt.Equal(first.Docker.CollectedAt) {
		t.Fatalf("cached snapshot=%#v", second)
	}
	cancel()
	metricsAwaitDone(t, done)
	if agent.dockerCalls.Load() != 0 || provider.refreshes.Load() != 0 || provider.reads.Load() != 2 || provider.acquires.Load() != 1 || provider.releases.Load() != 1 {
		t.Fatal("SSE did not use one demand lease and cached reads")
	}
	if !poll.stopped.Load() || !heartbeat.stopped.Load() {
		t.Fatal("SSE leaked timers")
	}
}

func TestDockerRealtimeScopeDemandAndCancellation(t *testing.T) {
	for _, query := range []string{"", "?scope=unknown", "?scope=summary", "?scope=docker", "?scope=summary&scope=docker&scope=docker"} {
		t.Run(query, func(t *testing.T) {
			now := time.Now()
			provider := dockerRealtimeProvider(now)
			api := &handler{dockerSnapshots: provider, summarySnapshots: metricsProvider(now), now: func() time.Time { return now }}
			writer := metricsWriter()
			cancel, done := metricsStartStream(api, writer, query)
			defer cancel()
			writer.frame(t)
			snapshot := writer.snapshot(t)
			want := boolCount(strings.Contains(query, "scope=docker"))
			if provider.acquires.Load() != want || provider.refreshes.Load() != 0 || provider.reads.Load() != want || (snapshot.Docker != nil) != (want == 1) {
				t.Fatalf("scope %q acquired or read wrong resource", query)
			}
			var acquiredContext context.Context
			if want == 1 {
				acquiredContext = <-provider.acquired
			}
			cancel()
			metricsAwaitDone(t, done)
			if provider.releases.Load() != want || api.realtimeSubscribers.Load() != 0 {
				t.Fatal("canceled stream leaked demand or subscriber")
			}
			if acquiredContext != nil && acquiredContext.Err() != context.Canceled {
				t.Fatal("provider did not receive request context cancellation")
			}
		})
	}
}

func TestDockerRealtimeRejectedSetupDoesNotAcquireDemand(t *testing.T) {
	for _, failure := range []string{"unsupported writer", "subscriber limit", "canceled request"} {
		t.Run(failure, func(t *testing.T) {
			provider := dockerRealtimeProvider(time.Now())
			api := &handler{dockerSnapshots: provider}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/system/events?scope=docker", nil)
			if failure == "subscriber limit" {
				api.realtimeSubscribers.Store(realtimeSubscriberLimit)
			}
			if failure == "canceled request" {
				ctx, cancel := context.WithCancel(request.Context())
				cancel()
				request = request.WithContext(ctx)
			}
			api.systemEvents(httptest.NewRecorder(), request)
			if provider.acquires.Load() != 0 || provider.refreshes.Load() != 0 || provider.reads.Load() != 0 || provider.releases.Load() != 0 {
				t.Fatal("rejected stream touched Docker provider")
			}
			expected := int32(0)
			if failure == "subscriber limit" {
				expected = realtimeSubscriberLimit
			}
			if api.realtimeSubscribers.Load() != expected {
				t.Fatal("rejected stream leaked subscriber")
			}
		})
	}
}

func TestDockerRealtimeMixedScopeStopsWhenEitherOwnerStops(t *testing.T) {
	for _, owner := range []string{"summary", "docker"} {
		t.Run(owner, func(t *testing.T) {
			now := time.Now()
			provider, summary := dockerRealtimeProvider(now), metricsProvider(now)
			tickers := metricsTickers()
			api := &handler{dockerSnapshots: provider, summarySnapshots: summary, now: func() time.Time { return now }, newRealtimeTicker: tickers.create}
			writer := metricsWriter()
			cancel, done := metricsStartStream(api, writer, "?scope=summary&scope=docker")
			defer cancel()
			writer.frame(t)
			writer.snapshot(t)
			poll, heartbeat := tickers.next(t), tickers.next(t)
			if owner == "summary" {
				close(summary.done)
			} else {
				close(provider.done)
			}
			metricsAwaitDone(t, done)
			if provider.releases.Load() != 1 || api.realtimeSubscribers.Load() != 0 || !poll.stopped.Load() || !heartbeat.stopped.Load() {
				t.Fatal("owner shutdown leaked demand, subscriber, or timers")
			}
		})
	}
}

func TestDockerRealtimeGETInventoryUsesRefreshBarrier(t *testing.T) {
	now := time.Now()
	provider := dockerRealtimeProvider(now.Add(-time.Hour))
	provider.refresh = func(ctx context.Context) (docker.Inventory, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > dockerrealtime.RefreshTimeout || time.Until(deadline) < dockerrealtime.RefreshTimeout-time.Second {
			t.Fatalf("GET refresh deadline = %v, set = %t", deadline, ok)
		}
		return docker.Inventory{CollectedAt: now, Engine: docker.EngineInfo{ServerVersion: "fresh-refresh"}}, nil
	}
	agent := &metricsAgentSpy{}
	api := &handler{dockerSnapshots: provider, agent: agent, agentTimeout: time.Second}
	response := httptest.NewRecorder()
	api.dockerInventory(response, httptest.NewRequest(http.MethodGet, "/api/v1/docker/inventory", nil))
	var inventory docker.Inventory
	if err := json.Unmarshal(response.Body.Bytes(), &inventory); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || inventory.Engine.ServerVersion != "fresh-refresh" || !inventory.CollectedAt.Equal(now) {
		t.Fatalf("GET response=%d %s", response.Code, response.Body.String())
	}
	if agent.dockerCalls.Load() != 0 || provider.refreshes.Load() != 1 || provider.reads.Load() != 0 || provider.acquires.Load() != 0 {
		t.Fatal("GET did not use the shared refresh barrier exclusively")
	}
}

func TestDockerRealtimeFreshnessAndErrorIsolation(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		change    func(*dockerRealtimeSnapshotProvider)
		available bool
	}{
		{name: "healthy", available: true},
		{name: "age boundary", available: true, change: func(p *dockerRealtimeSnapshotProvider) {
			p.state.LastSuccessAt = now.Add(-dockerrealtime.MaxAge)
			p.state.Inventory.CollectedAt = now.Add(-dockerrealtime.MaxAge)
		}},
		{name: "source stale despite fresh receive", change: func(p *dockerRealtimeSnapshotProvider) {
			p.state.Inventory.CollectedAt = now.Add(-dockerrealtime.MaxAge - time.Nanosecond)
		}},
		{name: "receive stale", change: func(p *dockerRealtimeSnapshotProvider) {
			p.state.LastSuccessAt = now.Add(-dockerrealtime.MaxAge - time.Nanosecond)
		}},
		{name: "future boundary", available: true, change: func(p *dockerRealtimeSnapshotProvider) {
			p.state.Inventory.CollectedAt = now.Add(dockerrealtime.FutureSkew)
		}},
		{name: "future source", change: func(p *dockerRealtimeSnapshotProvider) {
			p.state.Inventory.CollectedAt = now.Add(dockerrealtime.FutureSkew + time.Nanosecond)
		}},
		{name: "future receive", change: func(p *dockerRealtimeSnapshotProvider) {
			p.state.LastSuccessAt = now.Add(dockerrealtime.FutureSkew + time.Nanosecond)
		}},
		{name: "empty cache", change: func(p *dockerRealtimeSnapshotProvider) { p.state.Inventory = nil }},
		{name: "missing source time", change: func(p *dockerRealtimeSnapshotProvider) { p.state.Inventory.CollectedAt = time.Time{} }},
		{name: "missing receive time", change: func(p *dockerRealtimeSnapshotProvider) { p.state.LastSuccessAt = time.Time{} }},
		{name: "collector error", change: func(p *dockerRealtimeSnapshotProvider) { p.state.LastErrorCode = "DOCKER_INVENTORY_FAILED" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := dockerRealtimeProvider(now)
			if tc.change != nil {
				tc.change(provider)
			}
			agent := &metricsAgentSpy{}
			api := &handler{dockerSnapshots: provider, summarySnapshots: metricsProvider(now), agent: agent, now: func() time.Time { return now }}
			writer := metricsWriter()
			cancel, done := metricsStartStream(api, writer, "?scope=summary&scope=docker")
			defer cancel()
			writer.frame(t)
			snapshot := writer.snapshot(t)
			cancel()
			metricsAwaitDone(t, done)
			if snapshot.Summary == nil || (snapshot.Docker != nil) != tc.available || slices.Contains(snapshot.Errors, "DOCKER_INVENTORY_UNAVAILABLE") == tc.available {
				t.Fatalf("snapshot=%#v", snapshot)
			}
			if agent.dockerCalls.Load() != 0 || agent.summaryCalls.Load() != 0 || provider.refreshes.Load() != 0 {
				t.Fatal("invalid cache caused direct Agent fallback")
			}
			if provider.acquires.Load() != 1 || provider.releases.Load() != 1 {
				t.Fatal("cache error leaked demand")
			}
		})
	}
}
