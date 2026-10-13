package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/Kkwans/nas-control-plane/internal/monitoring"
	"github.com/Kkwans/nas-control-plane/internal/system"
)

type metricsSnapshotProvider struct {
	mu    sync.Mutex
	state monitoring.State
	done  chan struct{}
	reads atomic.Int32
}

func metricsProvider(at time.Time) *metricsSnapshotProvider {
	return &metricsSnapshotProvider{state: monitoring.State{Summary: &system.Summary{CollectedAt: at, Host: system.HostSnapshot{Hostname: "fixture-nas"}}, LastSuccessAt: at}, done: make(chan struct{})}
}
func (p *metricsSnapshotProvider) Latest() monitoring.State {
	p.reads.Add(1)
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}
func (p *metricsSnapshotProvider) Done() <-chan struct{} { return p.done }
func (p *metricsSnapshotProvider) set(state monitoring.State) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state = state
}

type metricsAgentSpy struct {
	AgentClient
	summaryCalls atomic.Int32
	dockerCalls  atomic.Int32
	docker       func(context.Context) (docker.Inventory, error)
}

func (s *metricsAgentSpy) CollectSystemSummary(context.Context, string) (system.Summary, error) {
	s.summaryCalls.Add(1)
	return system.Summary{}, errors.New("summary RPC must not be requested")
}
func (s *metricsAgentSpy) CollectDockerInventory(ctx context.Context, _ string) (docker.Inventory, error) {
	s.dockerCalls.Add(1)
	if s.docker != nil {
		return s.docker(ctx)
	}
	return docker.Inventory{}, nil
}

type metricsTicker struct {
	ticks    chan time.Time
	stopped  atomic.Bool
	interval time.Duration
}

func (t *metricsTicker) C() <-chan time.Time { return t.ticks }
func (t *metricsTicker) Stop()               { t.stopped.Store(true) }

type metricsTickerFactory struct{ created chan *metricsTicker }

func metricsTickers() *metricsTickerFactory {
	return &metricsTickerFactory{created: make(chan *metricsTicker, 256)}
}
func (f *metricsTickerFactory) create(interval time.Duration) realtimeTicker {
	t := &metricsTicker{ticks: make(chan time.Time, 1), interval: interval}
	f.created <- t
	return t
}
func (f *metricsTickerFactory) next(t *testing.T) *metricsTicker {
	t.Helper()
	select {
	case timer := <-f.created:
		return timer
	case <-time.After(3 * time.Second):
		t.Fatal("ticker was not started")
		return nil
	}
}

// 此 writer 明确支持 deadline 与可见的 flush 错误，避免 httptest 默认 writer 隐藏生产约束。
type metricsStreamWriter struct {
	header                                    http.Header
	mu                                        sync.Mutex
	pending                                   bytes.Buffer
	frames                                    chan string
	status                                    int
	deadline                                  time.Time
	deadlines                                 []time.Time
	writeErr, flushErr, deadlineErr, clearErr error
	writes, flushes                           int
	blockWrite                                <-chan struct{}
	blockSnapshot                             bool
	blocked                                   chan struct{}
}

func metricsWriter() *metricsStreamWriter {
	return &metricsStreamWriter{header: make(http.Header), frames: make(chan string, 256)}
}
func (w *metricsStreamWriter) Header() http.Header { return w.header }
func (w *metricsStreamWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status = status
}
func (w *metricsStreamWriter) SetWriteDeadline(deadline time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deadlines = append(w.deadlines, deadline)
	if deadline.IsZero() && w.clearErr != nil {
		return w.clearErr
	}
	if w.deadlineErr != nil {
		return w.deadlineErr
	}
	w.deadline = deadline
	return nil
}
func (w *metricsStreamWriter) Write(data []byte) (int, error) {
	if w.blockWrite != nil && (!w.blockSnapshot || bytes.HasPrefix(data, []byte("event: snapshot"))) {
		w.mu.Lock()
		deadline := w.deadline
		w.mu.Unlock()
		if deadline.IsZero() {
			return 0, errors.New("blocked write lacks deadline")
		}
		if w.blocked != nil {
			select {
			case w.blocked <- struct{}{}:
			default:
			}
		}
		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		select {
		case <-w.blockWrite:
		case <-timer.C:
			return 0, os.ErrDeadlineExceeded
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes++
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	if w.status == 0 {
		w.status = 200
	}
	return w.pending.Write(data)
}
func (w *metricsStreamWriter) FlushError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushes++
	if w.flushErr != nil {
		return w.flushErr
	}
	w.frames <- w.pending.String()
	w.pending.Reset()
	return nil
}
func (w *metricsStreamWriter) frame(t *testing.T) string {
	t.Helper()
	select {
	case frame := <-w.frames:
		return frame
	case <-time.After(3 * time.Second):
		t.Fatal("SSE frame missing")
		return ""
	}
}
func (w *metricsStreamWriter) snapshot(t *testing.T) RealtimeSnapshot {
	t.Helper()
	frame := w.frame(t)
	if !strings.HasPrefix(frame, "event: snapshot\ndata: ") {
		t.Fatalf("not a snapshot: %q", frame)
	}
	var snapshot RealtimeSnapshot
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(frame, "event: snapshot\ndata: "), "\n\n")), &snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}
func metricsStartStream(api *handler, writer http.ResponseWriter, query string) (context.CancelFunc, <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		api.systemEvents(writer, httptest.NewRequest(http.MethodGet, "/api/v1/system/events"+query, nil).WithContext(ctx))
	}()
	return cancel, done
}
func metricsAwaitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not stop")
	}
}

func TestMetricsRealtimeSummaryFreshnessAndErrors(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		change  func(*metricsSnapshotProvider)
		missing bool
		want    int
	}{
		{name: "healthy", want: 200},
		{name: "boundary", change: func(p *metricsSnapshotProvider) {
			p.state.Summary.CollectedAt = now.Add(-15 * time.Second)
			p.state.LastSuccessAt = now.Add(-15 * time.Second)
		}, want: 200},
		{name: "source older despite fresh receive", change: func(p *metricsSnapshotProvider) {
			p.state.Summary.CollectedAt = now.Add(-15*time.Second - time.Nanosecond)
		}, want: 503},
		{name: "receive expired", change: func(p *metricsSnapshotProvider) { p.state.LastSuccessAt = now.Add(-15*time.Second - time.Nanosecond) }, want: 503},
		{name: "future boundary", change: func(p *metricsSnapshotProvider) { p.state.Summary.CollectedAt = now.Add(5 * time.Second) }, want: 200},
		{name: "future invalid", change: func(p *metricsSnapshotProvider) {
			p.state.Summary.CollectedAt = now.Add(5*time.Second + time.Nanosecond)
		}, want: 503},
		{name: "zero data time", change: func(p *metricsSnapshotProvider) { p.state.Summary.CollectedAt = time.Time{} }, want: 503},
		{name: "zero receive time", change: func(p *metricsSnapshotProvider) { p.state.LastSuccessAt = time.Time{} }, want: 503},
		{name: "cold", change: func(p *metricsSnapshotProvider) { p.state.Summary = nil }, want: 503},
		{name: "source error preserves old pointer", change: func(p *metricsSnapshotProvider) { p.state.LastErrorCode = "SYSTEM_SUMMARY_UNAVAILABLE" }, want: 503},
		{name: "invalid timestamp source", change: func(p *metricsSnapshotProvider) { p.state.LastErrorCode = "MONITORING_INVALID_TIMESTAMP" }, want: 503},
		{name: "stopped", change: func(p *metricsSnapshotProvider) { close(p.done) }, want: 503},
		{name: "missing provider", missing: true, want: 503},
		{name: "warning remains usable", change: func(p *metricsSnapshotProvider) {
			p.state.Summary.Warnings = []system.SummaryWarning{{Source: "cpu", Code: "CPU_UNAVAILABLE"}}
		}, want: 200},
		{name: "history failure remains usable", change: func(p *metricsSnapshotProvider) { p.state.PersistenceErrorCode = "MONITORING_WRITE_FAILED" }, want: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := metricsProvider(now)
			if tc.change != nil {
				tc.change(p)
			}
			agent := &metricsAgentSpy{}
			api := &handler{summarySnapshots: p, agent: agent, now: func() time.Time { return now }}
			if tc.missing {
				api.summarySnapshots = nil
			}
			response := httptest.NewRecorder()
			api.systemSummary(response, httptest.NewRequest("GET", "/api/v1/system/summary", nil))
			if response.Code != tc.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if tc.want == 503 {
				assertErrorCode(t, response, "SYSTEM_SUMMARY_UNAVAILABLE")
			} else {
				var got system.Summary
				if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if !got.CollectedAt.Equal(p.state.Summary.CollectedAt) || !slices.Equal(got.Warnings, p.state.Summary.Warnings) {
					t.Fatalf("summary changed: %#v", got)
				}
			}
			if agent.summaryCalls.Load() != 0 {
				t.Fatal("GET called source")
			}
		})
	}
}

func TestMetricsRealtimeScopesAndHistoryErrors(t *testing.T) {
	now := time.Now()
	for _, scope := range []struct {
		query           string
		summary, docker bool
	}{
		{"", true, false}, {"?scope=unknown", true, false}, {"?scope=summary", true, false}, {"?scope=docker", false, true}, {"?scope=summary&scope=docker&scope=summary", true, true},
	} {
		for _, code := range []string{"", "MONITORING_WRITE_FAILED", "MONITORING_SAMPLE_INCOMPLETE", "MONITORING_SAMPLE_INVALID", "MONITORING_SAMPLE_LIMIT_EXCEEDED"} {
			t.Run(scope.query+"/"+code, func(t *testing.T) {
				provider := metricsProvider(now)
				provider.state.PersistenceErrorCode = code
				provider.state.Summary.Warnings = []system.SummaryWarning{{Source: "temperature", Code: "TEMPERATURE_UNAVAILABLE"}}
				agent := &metricsAgentSpy{}
				dockerProvider := dockerRealtimeProvider(now)
				api := &handler{summarySnapshots: provider, dockerSnapshots: dockerProvider, agent: agent, agentTimeout: time.Second, now: func() time.Time { return now }}
				writer := metricsWriter()
				cancel, done := metricsStartStream(api, &responseRecorder{ResponseWriter: writer}, scope.query)
				defer cancel()
				if got := writer.frame(t); got != "retry: 5000\n\n" {
					t.Fatalf("retry=%q", got)
				}
				snapshot := writer.snapshot(t)
				cancel()
				metricsAwaitDone(t, done)
				if (snapshot.Summary != nil) != scope.summary || (snapshot.Docker != nil) != scope.docker {
					t.Fatalf("snapshot=%#v", snapshot)
				}
				if scope.summary && len(snapshot.Summary.Warnings) != 1 {
					t.Fatal("warning lost")
				}
				if slices.Contains(snapshot.Errors, code) != (code != "" && scope.summary) {
					t.Fatalf("errors=%v", snapshot.Errors)
				}
				if provider.reads.Load() != boolCount(scope.summary) || agent.dockerCalls.Load() != 0 || agent.summaryCalls.Load() != 0 {
					t.Fatal("scope called wrong resource")
				}
				if api.realtimeSubscribers.Load() != 0 {
					t.Fatal("subscriber leaked")
				}
			})
		}
	}
}
func boolCount(value bool) int32 {
	if value {
		return 1
	}
	return 0
}

func TestMetricsRealtimeCachedPollHeartbeatAndRecovery(t *testing.T) {
	for _, interval := range []int{2, 5, 300} {
		t.Run(fmt.Sprint(interval), func(t *testing.T) {
			now := time.Now()
			provider := metricsProvider(now)
			agent := &metricsAgentSpy{}
			var clock atomic.Int64
			clock.Store(now.UnixNano())
			tickers := metricsTickers()
			api := &handler{summarySnapshots: provider, agent: agent, now: func() time.Time { return time.Unix(0, clock.Load()) }, newRealtimeTicker: tickers.create}
			writer := metricsWriter()
			cancel, done := metricsStartStream(api, &responseRecorder{ResponseWriter: writer}, fmt.Sprintf("?interval=%d", interval))
			defer cancel()
			if got := writer.frame(t); got != fmt.Sprintf("retry: %d\n\n", interval*1000) {
				t.Fatal(got)
			}
			first := writer.snapshot(t)
			poll, heartbeat := tickers.next(t), tickers.next(t)
			if poll.interval != time.Duration(interval)*time.Second || heartbeat.interval != 15*time.Second {
				t.Fatal("wrong cadence")
			}
			heartbeat.ticks <- now
			if frame := writer.frame(t); frame != ": heartbeat\n\n" {
				t.Fatal(frame)
			}
			if provider.reads.Load() != 1 || agent.summaryCalls.Load() != 0 {
				t.Fatal("heartbeat collected data")
			}
			poll.ticks <- now
			repeated := writer.snapshot(t)
			if !repeated.Summary.CollectedAt.Equal(first.Summary.CollectedAt) {
				t.Fatal("cached time changed")
			}
			clock.Store(now.Add(15*time.Second + time.Nanosecond).UnixNano())
			poll.ticks <- now
			stale := writer.snapshot(t)
			if stale.Summary != nil || !slices.Contains(stale.Errors, "SYSTEM_SUMMARY_UNAVAILABLE") {
				t.Fatalf("stale=%#v", stale)
			}
			recovered := now.Add(16 * time.Second)
			clock.Store(recovered.UnixNano())
			provider.set(monitoring.State{Summary: &system.Summary{CollectedAt: recovered}, LastSuccessAt: recovered})
			poll.ticks <- recovered
			if got := writer.snapshot(t); got.Summary == nil || len(got.Errors) != 0 {
				t.Fatalf("recovery=%#v", got)
			}
			close(provider.done)
			metricsAwaitDone(t, done)
			if !poll.stopped.Load() || !heartbeat.stopped.Load() || api.realtimeSubscribers.Load() != 0 {
				t.Fatal("shutdown leaked timers or subscriber")
			}
			if agent.summaryCalls.Load() != 0 {
				t.Fatal("polling called Agent")
			}
		})
	}
}

func TestMetricsRealtimeSubscriberBudgetAndIsolation(t *testing.T) {
	now := time.Now()
	provider := metricsProvider(now)
	agent := &metricsAgentSpy{}
	tickers := metricsTickers()
	api := &handler{summarySnapshots: provider, agent: agent, now: func() time.Time { return now }, newRealtimeTicker: tickers.create}
	var cancelers []context.CancelFunc
	var finished []<-chan struct{}
	var pollers []*metricsTicker
	var writers []*metricsStreamWriter
	defer func() {
		for _, cancel := range cancelers {
			cancel()
		}
		for _, done := range finished {
			metricsAwaitDone(t, done)
		}
	}()
	if provider.reads.Load() != 0 {
		t.Fatal("zero subscribers read provider")
	}
	for i := 0; i < realtimeSubscriberLimit; i++ {
		writer := metricsWriter()
		cancel, done := metricsStartStream(api, &responseRecorder{ResponseWriter: writer}, "?interval=300")
		cancelers = append(cancelers, cancel)
		finished = append(finished, done)
		writer.frame(t)
		writer.snapshot(t)
		pollers = append(pollers, tickers.next(t))
		writers = append(writers, writer)
		tickers.next(t)
	}
	for _, ticker := range pollers {
		ticker.ticks <- now
	}
	for _, writer := range writers {
		writer.snapshot(t)
	}
	if provider.reads.Load() != 128 || agent.summaryCalls.Load() != 0 {
		t.Fatal("64 cache ticks requested source")
	}
	if api.realtimeSubscribers.Load() != 64 || provider.reads.Load() != 128 {
		t.Fatal("64 subscribers not established")
	}
	rejected := httptest.NewRecorder()
	api.systemEvents(rejected, httptest.NewRequest("GET", "/api/v1/system/events?scope=docker", nil))
	if rejected.Code != 503 || strings.Contains(rejected.Header().Get("Content-Type"), "event-stream") {
		t.Fatalf("rejected=%d %s", rejected.Code, rejected.Body.String())
	}
	assertErrorCode(t, rejected, "REALTIME_SUBSCRIBER_LIMIT")
	cancelers[0]()
	metricsAwaitDone(t, finished[0])
	if api.realtimeSubscribers.Load() != 63 {
		t.Fatal("slot not reclaimed")
	}
	writer := metricsWriter()
	cancel, done := metricsStartStream(api, writer, "?interval=300")
	cancelers = append(cancelers, cancel)
	finished = append(finished, done)
	writer.frame(t)
	writer.snapshot(t)
	if api.realtimeSubscribers.Load() != 64 || agent.summaryCalls.Load() != 0 || agent.dockerCalls.Load() != 0 {
		t.Fatal("cache traffic escaped or slot failed")
	}
}

func TestMetricsRealtimeSlowWriterDoesNotBlockOtherSubscribers(t *testing.T) {
	collector, clock, source, _ := metricsRunningCollector(t)
	api := &handler{summarySnapshots: collector, now: clock.Now}
	blocked := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(blocked) })
	slow := metricsWriter()
	slow.blockWrite, slow.blockSnapshot, slow.blocked = blocked, true, make(chan struct{}, 1)
	cancelSlow, slowDone := metricsStartStream(api, &responseRecorder{ResponseWriter: slow}, "")
	defer cancelSlow()
	slow.frame(t)
	select {
	case <-slow.blocked:
	case <-time.After(3 * time.Second):
		t.Fatal("snapshot write did not block after Latest")
	}
	// 真正的 Collector 在慢 snapshot Write 阻塞期间继续采集、发布；不是手工替换 provider 指针。
	clock.advanceAndWait(t, 5*time.Second)
	if source.calls.Load() != 2 {
		t.Fatal("blocked subscriber prevented collector source progress")
	}
	fast := metricsWriter()
	cancelFast, fastDone := metricsStartStream(api, &responseRecorder{ResponseWriter: fast}, "")
	defer cancelFast()
	fast.frame(t)
	if got := fast.snapshot(t); got.Summary == nil || !got.Summary.CollectedAt.Equal(clock.Now()) {
		t.Fatalf("fast reader did not see new collector state: %#v", got)
	}
	cancelFast()
	metricsAwaitDone(t, fastDone)
	cancelSlow()
	release.Do(func() { close(blocked) })
	metricsAwaitDone(t, slowDone)
	if api.realtimeSubscribers.Load() != 0 {
		t.Fatal("subscriber leaked")
	}
}

func TestMetricsRealtimeWriteAndFlushFailures(t *testing.T) {
	boom := errors.New("write failure")
	for _, tc := range []struct {
		name      string
		configure func(*metricsStreamWriter)
	}{
		{"write", func(w *metricsStreamWriter) { w.writeErr = boom }},
		{"flush", func(w *metricsStreamWriter) { w.flushErr = boom }},
		{"deadline", func(w *metricsStreamWriter) { w.deadlineErr = boom }},
		{"clear deadline", func(w *metricsStreamWriter) { w.clearErr = boom }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writer := metricsWriter()
			tc.configure(writer)
			recorder := &responseRecorder{ResponseWriter: writer}
			api := &handler{summarySnapshots: metricsProvider(time.Now())}
			cancel, done := metricsStartStream(api, recorder, "")
			defer cancel()
			metricsAwaitDone(t, done)
			if api.realtimeSubscribers.Load() != 0 {
				t.Fatal("failed stream leaked slot")
			}
			if tc.name == "flush" && writer.flushes != 1 {
				t.Fatal("wrapped flush failure was hidden")
			}
		})
	}
	t.Run("unsupported deadline", func(t *testing.T) {
		api := &handler{}
		writer := httptest.NewRecorder()
		api.systemEvents(&responseRecorder{ResponseWriter: writer}, httptest.NewRequest("GET", "/api/v1/system/events", nil))
		if writer.Code != 500 {
			t.Fatal(writer.Code)
		}
		assertErrorCode(t, writer, "REALTIME_STREAM_UNSUPPORTED")
		if api.realtimeSubscribers.Load() != 0 {
			t.Fatal("unsupported writer leaked slot")
		}
	})
	t.Run("deadline reset after every frame", func(t *testing.T) {
		now := time.Now()
		api := &handler{now: func() time.Time { return now }}
		writer := metricsWriter()
		recorder := &responseRecorder{ResponseWriter: writer}
		for _, frame := range []string{"retry: 300000\n\n", "event: snapshot\ndata: {}\n\n", ": heartbeat\n\n"} {
			if err := api.writeSSEFrame(context.Background(), recorder, http.NewResponseController(recorder), frame); err != nil {
				t.Fatal(err)
			}
		}
		if len(writer.deadlines) != 6 {
			t.Fatalf("deadlines=%v", writer.deadlines)
		}
		for i, d := range writer.deadlines {
			if i%2 == 0 && !d.Equal(now.Add(5*time.Second)) || i%2 == 1 && !d.IsZero() {
				t.Fatalf("deadline %d=%v", i, d)
			}
		}
		if recorder.status != 200 || writer.flushes != 3 {
			t.Fatal("wrapper did not record status/flush")
		}
	})
}

func TestMetricsRealtimeIntervalCompatibility(t *testing.T) {
	for raw, expected := range map[string]time.Duration{"": 5 * time.Second, "garbage": 5 * time.Second, "1": 5 * time.Second, "301": 5 * time.Second, "2": 2 * time.Second, "5": 5 * time.Second, "300": 300 * time.Second} {
		if got := realtimeInterval(httptest.NewRequest("GET", "/?interval="+raw, nil)); got != expected {
			t.Fatalf("%q=%v", raw, got)
		}
	}
}

// 断言 io.Writer 协议错误不会被 Flush 掩盖。
func TestMetricsRealtimeFramePreservesWriteError(t *testing.T) {
	writer := metricsWriter()
	writer.writeErr = io.ErrClosedPipe
	recorder := &responseRecorder{ResponseWriter: writer}
	api := &handler{}
	if err := api.writeSSEFrame(context.Background(), recorder, http.NewResponseController(recorder), "fixture"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	if writer.flushes != 0 || !writer.deadline.IsZero() {
		t.Fatal("write failure not cleaned up")
	}
}

func TestMetricsRealtimeUnavailableAndIndependentErrors(t *testing.T) {
	for _, condition := range []string{"missing", "cold", "source", "stopped", "expired", "future"} {
		t.Run(condition, func(t *testing.T) {
			now := time.Now()
			provider := metricsProvider(now)
			provider.state.PersistenceErrorCode = "MONITORING_WRITE_FAILED"
			switch condition {
			case "cold":
				provider.state.Summary = nil
			case "source":
				provider.state.LastErrorCode = "SYSTEM_SUMMARY_UNAVAILABLE"
			case "stopped":
				close(provider.done)
			case "expired":
				provider.state.Summary.CollectedAt = now.Add(-16 * time.Second)
			case "future":
				provider.state.Summary.CollectedAt = now.Add(6 * time.Second)
			}
			agent := &metricsAgentSpy{docker: func(context.Context) (docker.Inventory, error) {
				return docker.Inventory{}, errors.New("fixture Docker unavailable")
			}}
			api := &handler{summarySnapshots: provider, agent: agent, agentTimeout: time.Second, now: func() time.Time { return now }}
			if condition == "missing" {
				api.summarySnapshots = nil
			}
			writer := metricsWriter()
			cancel, done := metricsStartStream(api, writer, "?scope=summary&scope=docker")
			defer cancel()
			writer.frame(t)
			snapshot := writer.snapshot(t)
			cancel()
			metricsAwaitDone(t, done)
			if snapshot.Summary != nil || snapshot.Docker != nil || !slices.Contains(snapshot.Errors, "SYSTEM_SUMMARY_UNAVAILABLE") || !slices.Contains(snapshot.Errors, "DOCKER_INVENTORY_UNAVAILABLE") {
				t.Fatalf("snapshot=%#v", snapshot)
			}
			if slices.Contains(snapshot.Errors, "MONITORING_WRITE_FAILED") != (condition != "missing") {
				t.Fatalf("history error lost: %v", snapshot.Errors)
			}
			if agent.summaryCalls.Load() != 0 {
				t.Fatal("unavailable cache used RPC fallback")
			}
		})
	}
}

// 单个 Collector 的 fake 时钟在每次真正进入等待下一个采样点时发出握手。
// 不通过 sleep 猜测 step 是否完成，也不把待发送 tick 放进会被丢弃的追赶队列。
type metricsCollectorClock struct {
	now   atomic.Int64
	ticks chan time.Time
	ready chan struct{}
	reads atomic.Int32
}

func (c *metricsCollectorClock) Now() time.Time                            { return time.Unix(0, c.now.Load()) }
func (c *metricsCollectorClock) NewTicker(time.Duration) monitoring.Ticker { return c }
func (c *metricsCollectorClock) C() <-chan time.Time {
	if c.reads.Add(1)%2 == 0 {
		c.ready <- struct{}{}
	}
	return c.ticks
}
func (c *metricsCollectorClock) Stop() {}
func (c *metricsCollectorClock) awaitReady(t *testing.T) {
	t.Helper()
	select {
	case <-c.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("Collector did not complete step")
	}
}
func (c *metricsCollectorClock) advanceAndWait(t *testing.T, interval time.Duration) {
	t.Helper()
	c.now.Add(int64(interval))
	select {
	case c.ticks <- c.Now():
	case <-time.After(3 * time.Second):
		t.Fatal("Collector did not accept tick")
	}
	c.awaitReady(t)
}

type metricsCollectorSource struct {
	clock *metricsCollectorClock
	calls atomic.Int32
}

func (s *metricsCollectorSource) CollectSystemSummary(context.Context) (system.Summary, error) {
	s.calls.Add(1)
	return system.Summary{CollectedAt: s.clock.Now(), CPU: system.CPUStats{UsagePercent: 12}, Memory: system.MemoryStats{TotalBytes: 100, UsedBytes: 25}, Storage: []system.DiskStats{{TotalBytes: 100, UsedBytes: 30}}, Network: []system.NetworkStats{{Name: "fixture", ReceiveBytes: 10}}}, nil
}

type metricsCollectorStore struct{ writes atomic.Int32 }

func (s *metricsCollectorStore) RecordMetricSample(context.Context, controlstore.MetricSample) error {
	s.writes.Add(1)
	return nil
}
func (s *metricsCollectorStore) PruneMetricSamples(context.Context, time.Time) error { return nil }
func metricsRunningCollector(t *testing.T) (*monitoring.Collector, *metricsCollectorClock, *metricsCollectorSource, *metricsCollectorStore) {
	t.Helper()
	clock := &metricsCollectorClock{ticks: make(chan time.Time), ready: make(chan struct{}, 1)}
	// 为 fake socket writer 提供未过期的真实基准，但采样推进完全由测试控制。
	clock.now.Store(time.Now().UnixNano())
	source, store := &metricsCollectorSource{clock: clock}, &metricsCollectorStore{}
	collector, err := monitoring.NewCollector(source, store, monitoring.Options{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = collector.Run(ctx) }()
	t.Cleanup(func() { cancel(); metricsAwaitDone(t, collector.Done()) })
	clock.awaitReady(t)
	return collector, clock, source, store
}

func TestMetricsRealtimeCollectorSourceIndependentOfConsumerCount(t *testing.T) {
	for _, count := range []int{0, 1, 64} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			collector, clock, source, store := metricsRunningCollector(t)
			tickers := metricsTickers()
			agent := &metricsAgentSpy{}
			api := &handler{summarySnapshots: collector, agent: agent, now: clock.Now, newRealtimeTicker: tickers.create}
			var writers []*metricsStreamWriter
			var pollers, heartbeats []*metricsTicker
			var cancelers []context.CancelFunc
			var done []<-chan struct{}
			defer func() {
				for _, cancel := range cancelers {
					cancel()
				}
				for _, finished := range done {
					metricsAwaitDone(t, finished)
				}
			}()
			for i := 0; i < count; i++ {
				writer := metricsWriter()
				cancel, finished := metricsStartStream(api, &responseRecorder{ResponseWriter: writer}, "?interval=300")
				writers, cancelers, done = append(writers, writer), append(cancelers, cancel), append(done, finished)
				writer.frame(t)
				writer.snapshot(t)
				pollers, heartbeats = append(pollers, tickers.next(t)), append(heartbeats, tickers.next(t))
			}
			for i := 0; i < 100; i++ {
				response := httptest.NewRecorder()
				api.systemSummary(response, httptest.NewRequest("GET", "/api/v1/system/summary", nil))
				if response.Code != 200 {
					t.Fatal(response.Code)
				}
			}
			if source.calls.Load() != 1 || store.writes.Load() != 1 {
				t.Fatal("connections or GET drove Collector work")
			}
			for i, poller := range pollers {
				poller.ticks <- clock.Now()
				writers[i].snapshot(t)
			}
			for i, heartbeat := range heartbeats {
				heartbeat.ticks <- clock.Now()
				if got := writers[i].frame(t); got != ": heartbeat\n\n" {
					t.Fatal(got)
				}
			}
			if source.calls.Load() != 1 || store.writes.Load() != 1 || agent.summaryCalls.Load() != 0 {
				t.Fatal("consumer ticks drove source/history work")
			}
			clock.advanceAndWait(t, 5*time.Second)
			if source.calls.Load() != 2 {
				t.Fatal("Collector did not independently advance")
			}
			for i, poller := range pollers {
				poller.ticks <- clock.Now()
				if got := writers[i].snapshot(t); got.Summary == nil || !got.Summary.CollectedAt.Equal(clock.Now()) {
					t.Fatal("consumer missed new shared snapshot")
				}
			}
			if source.calls.Load() != 2 || agent.summaryCalls.Load() != 0 {
				t.Fatal("consumer refresh added RPC work")
			}
		})
	}
}

func TestMetricsRealtimeBlockedWriterHonorsDeadline(t *testing.T) {
	writer := metricsWriter()
	writer.blockWrite = make(chan struct{})
	recorder := &responseRecorder{ResponseWriter: writer}
	// 保留生产 5 秒预算，只将测试服务时间置于 5 秒前，使 fake writer 快速观察 deadline 超时。
	api := &handler{now: func() time.Time { return time.Now().Add(-realtimeWriteTimeout + 10*time.Millisecond) }}
	err := api.writeSSEFrame(context.Background(), recorder, http.NewResponseController(recorder), "event: snapshot\ndata: {}\n\n")
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("blocked writer ignored deadline: %v", err)
	}
	if !writer.deadline.IsZero() || writer.flushes != 0 {
		t.Fatal("expired write deadline not cleared or flush attempted")
	}
}
