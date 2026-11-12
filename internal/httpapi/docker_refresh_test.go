package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/Kkwans/nas-control-plane/internal/dockerrealtime"
)

// Refresh 路由测试只用内存 provider、资料存储和 Agent spy，不建立监听或连接。
type dockerRefreshSiteStore struct {
	ControlStore
	reads atomic.Int32
}

func (s *dockerRefreshSiteStore) SiteProfiles(context.Context) ([]controlstore.SiteProfile, error) {
	s.reads.Add(1)
	return nil, nil
}

var dockerRefreshRoutes = []string{"/api/v1/docker/inventory", "/api/v1/services", "/api/v1/sites"}

type dockerRefreshSourceFunc func(context.Context) (docker.Inventory, error)

func (source dockerRefreshSourceFunc) CollectDockerInventory(ctx context.Context) (docker.Inventory, error) {
	return source(ctx)
}

func TestDockerRefreshRoutesCoalesceWithSSEDemand(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls, active, maxActive atomic.Int32
		firstStarted, releaseFirst := make(chan struct{}), make(chan struct{})
		collector, err := dockerrealtime.NewCollector(dockerRefreshSourceFunc(func(ctx context.Context) (docker.Inventory, error) {
			concurrent := active.Add(1)
			defer active.Add(-1)
			for previous := maxActive.Load(); concurrent > previous; previous = maxActive.Load() {
				if maxActive.CompareAndSwap(previous, concurrent) {
					break
				}
			}
			round := calls.Add(1)
			collectedAt := time.Now()
			version := "started-after-get"
			if round == 1 {
				version = "started-before-get"
				close(firstStarted)
				select {
				case <-releaseFirst:
				case <-ctx.Done():
					return docker.Inventory{}, ctx.Err()
				}
			}
			return docker.Inventory{CollectedAt: collectedAt, Engine: docker.EngineInfo{ServerVersion: version}}, nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		ownerContext, stopOwner := context.WithCancel(context.Background())
		defer stopOwner()
		ownerDone := make(chan error, 1)
		go func() { ownerDone <- collector.Run(ownerContext) }()
		tickers := metricsTickers()
		streamAPI := &handler{dockerSnapshots: collector, now: time.Now, newRealtimeTicker: tickers.create}
		writer := metricsWriter()
		stopStream, streamDone := metricsStartStream(streamAPI, writer, "?scope=docker")
		defer stopStream()
		writer.frame(t)
		writer.snapshot(t)
		<-firstStarted
		firstStart := time.Now()
		agent := &metricsAgentSpy{}
		store := &dockerRefreshSiteStore{}
		api := NewHandler(Config{Agent: agent, DockerSnapshots: collector, ControlStore: store})
		type routeResult struct {
			path     string
			response *httptest.ResponseRecorder
		}
		completed := make(chan routeResult, len(dockerRefreshRoutes))
		for _, path := range dockerRefreshRoutes {
			go func() {
				response := httptest.NewRecorder()
				api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
				completed <- routeResult{path: path, response: response}
			}()
		}
		synctest.Wait()
		if calls.Load() != 1 || active.Load() != 1 || len(completed) != 0 {
			t.Fatal("concurrent routes did not wait behind the already-started SSE round")
		}
		close(releaseFirst)
		synctest.Wait()
		state := collector.Latest()
		if state.Inventory == nil || state.Inventory.Engine.ServerVersion != "started-before-get" || calls.Load() != 1 || len(completed) != 0 {
			t.Fatal("GET accepted a sample that started before its refresh barrier")
		}
		time.Sleep(dockerrealtime.Interval - time.Nanosecond)
		synctest.Wait()
		if calls.Load() != 1 || len(completed) != 0 {
			t.Fatal("refresh bypassed the minimum collection interval")
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if calls.Load() != 2 || maxActive.Load() != 1 || active.Load() != 0 || len(completed) != len(dockerRefreshRoutes) {
			t.Fatalf("calls = %d, max active = %d, active = %d, completed = %d", calls.Load(), maxActive.Load(), active.Load(), len(completed))
		}
		for range dockerRefreshRoutes {
			result := <-completed
			var body struct {
				CollectedAt time.Time         `json:"collectedAt"`
				Engine      docker.EngineInfo `json:"engine"`
			}
			if err := json.Unmarshal(result.response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if result.response.Code != http.StatusOK || !body.CollectedAt.Equal(firstStart.Add(dockerrealtime.Interval)) {
				t.Fatalf("%s did not receive the post-barrier round: %d %s", result.path, result.response.Code, result.response.Body.String())
			}
			if result.path == "/api/v1/docker/inventory" && body.Engine.ServerVersion != "started-after-get" {
				t.Fatalf("inventory received old round = %#v", body)
			}
		}
		if agent.dockerCalls.Load() != 0 || store.reads.Load() != 1 {
			t.Fatal("refresh routes bypassed the collector or repeated site processing")
		}
		stopStream()
		synctest.Wait()
		<-streamDone
		stopOwner()
		if err := <-ownerDone; err != nil {
			t.Fatal(err)
		}
		if streamAPI.realtimeSubscribers.Load() != 0 {
			t.Fatal("SSE subscriber leaked")
		}
	})
}

func TestDockerRefreshRoutesUseSharedProvider(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	refreshed := docker.Inventory{
		CollectedAt: now,
		Engine:      docker.EngineInfo{ServerVersion: "fresh-barrier"},
		Projects:    []docker.Project{{ID: "compose:fixture", Name: "fixture", State: "running"}},
		Containers: []docker.InventoryContainer{{
			ID: "fixture", ProjectID: "compose:fixture", Name: "fixture",
			Labels: map[string]string{"com.ncp.site.name": "Refreshed Site"},
			Ports:  []docker.PortMapping{{PublicPort: 8080, PrivatePort: 80, Protocol: "tcp"}},
		}},
	}
	provider := dockerRealtimeProvider(now.Add(-time.Hour))
	provider.refresh = func(context.Context) (docker.Inventory, error) { return refreshed, nil }
	agent := &metricsAgentSpy{}
	store := &dockerRefreshSiteStore{}
	api := NewHandler(Config{Agent: agent, DockerSnapshots: provider, ControlStore: store})
	for _, path := range dockerRefreshRoutes {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			var body struct {
				CollectedAt time.Time         `json:"collectedAt"`
				Engine      docker.EngineInfo `json:"engine"`
				Services    []docker.Project  `json:"services"`
				Sites       []Site            `json:"sites"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if !body.CollectedAt.Equal(now) {
				t.Fatalf("route returned cached timestamp = %v", body.CollectedAt)
			}
			switch path {
			case "/api/v1/docker/inventory":
				if body.Engine.ServerVersion != "fresh-barrier" {
					t.Fatalf("engine = %#v", body.Engine)
				}
			case "/api/v1/services":
				if len(body.Services) != 1 || body.Services[0].ID != "compose:fixture" {
					t.Fatalf("services = %#v", body.Services)
				}
			case "/api/v1/sites":
				if len(body.Sites) != 1 || body.Sites[0].Name != "Refreshed Site" || body.Sites[0].PrimaryPort != 8080 {
					t.Fatalf("sites = %#v", body.Sites)
				}
			}
		})
	}
	if provider.refreshes.Load() != 3 || provider.reads.Load() != 0 || provider.acquires.Load() != 0 || agent.dockerCalls.Load() != 0 || store.reads.Load() != 1 {
		t.Fatal("routes did not exclusively use the shared refresh provider")
	}
}

func TestDockerRefreshRoutesReturnStableUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		missing bool
	}{
		{name: "missing provider", missing: true},
		{name: "unavailable", err: dockerrealtime.ErrUnavailable},
		{name: "waiter limit", err: dockerrealtime.ErrRefreshLimit},
		{name: "canceled", err: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded},
		{name: "source failure", err: errors.New("private source failure")},
	} {
		for _, path := range dockerRefreshRoutes {
			t.Run(tc.name+path, func(t *testing.T) {
				provider := dockerRealtimeProvider(time.Now())
				provider.refresh = func(context.Context) (docker.Inventory, error) { return docker.Inventory{}, tc.err }
				agent := &metricsAgentSpy{}
				store := &dockerRefreshSiteStore{}
				config := Config{Agent: agent, DockerSnapshots: provider, ControlStore: store, RequestID: func() string { return "req-refresh" }}
				if tc.missing {
					config.DockerSnapshots = nil
				}
				response := httptest.NewRecorder()
				NewHandler(config).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
				assertDockerRefreshUnavailable(t, response)
				var body ErrorResponse
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.RequestID != "req-refresh" {
					t.Fatalf("error response = %#v, decode = %v", body, err)
				}
				wantRefreshes := int32(1)
				if tc.missing {
					wantRefreshes = 0
				}
				if provider.refreshes.Load() != wantRefreshes || provider.reads.Load() != 0 || provider.acquires.Load() != 0 || agent.dockerCalls.Load() != 0 || store.reads.Load() != 0 {
					t.Fatal("failed refresh used a cache, direct Agent fallback, or site storage")
				}
			})
		}
	}
}

func TestDockerRefreshRoutesPropagateCancellationAndBudget(t *testing.T) {
	for _, mode := range []string{"request cancellation", "earlier request deadline", "refresh budget"} {
		for _, path := range dockerRefreshRoutes {
			t.Run(mode+path, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					provider := dockerRealtimeProvider(time.Now())
					started := make(chan context.Context, 1)
					provider.refresh = func(ctx context.Context) (docker.Inventory, error) {
						started <- ctx
						<-ctx.Done()
						return docker.Inventory{}, ctx.Err()
					}
					agent := &metricsAgentSpy{}
					store := &dockerRefreshSiteStore{}
					api := NewHandler(Config{Agent: agent, DockerSnapshots: provider, ControlStore: store, AgentTimeout: time.Nanosecond})
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					wantDuration := dockerrealtime.RefreshTimeout
					if mode == "earlier request deadline" {
						var deadlineCancel context.CancelFunc
						wantDuration = time.Second
						ctx, deadlineCancel = context.WithTimeout(ctx, wantDuration)
						defer deadlineCancel()
					}
					request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
					response := httptest.NewRecorder()
					done := make(chan struct{})
					start := time.Now()
					go func() {
						defer close(done)
						api.ServeHTTP(response, request)
					}()
					refreshContext := <-started
					deadline, ok := refreshContext.Deadline()
					if !ok || !deadline.Equal(start.Add(wantDuration)) {
						t.Fatalf("refresh deadline = %v, set = %t, want = %v", deadline, ok, start.Add(wantDuration))
					}
					wantErr := context.DeadlineExceeded
					if mode == "request cancellation" {
						cancel()
						wantErr = context.Canceled
						wantDuration = 0
					}
					<-done
					if !errors.Is(refreshContext.Err(), wantErr) || time.Since(start) != wantDuration {
						t.Fatalf("refresh error = %v, elapsed = %v", refreshContext.Err(), time.Since(start))
					}
					assertDockerRefreshUnavailable(t, response)
					if provider.refreshes.Load() != 1 || provider.reads.Load() != 0 || provider.acquires.Load() != 0 || agent.dockerCalls.Load() != 0 || store.reads.Load() != 0 {
						t.Fatal("canceled refresh fell back or continued site processing")
					}
				})
			})
		}
	}
}

func assertDockerRefreshUnavailable(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	var body ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusServiceUnavailable || body.Code != "DOCKER_INVENTORY_UNAVAILABLE" || body.Message != "Docker 实时清单暂不可用，请确认 Root Agent 与 Docker Engine 已启动。" {
		t.Fatalf("refresh error status = %d, body = %#v", response.Code, body)
	}
}
