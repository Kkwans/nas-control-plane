package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/Kkwans/nas-control-plane/internal/journal"
	"github.com/Kkwans/nas-control-plane/internal/monitoring"
	"github.com/go-chi/chi/v5"
)

type sseScriptedWriter struct {
	*metricsStreamWriter
	writeCalls, flushCalls, deadlineCalls int
	write                                 func([]byte) (int, error)
	flush                                 func() error
	setDeadline                           func(time.Time) error
}

func sseWriter() *sseScriptedWriter { return &sseScriptedWriter{metricsStreamWriter: metricsWriter()} }
func (w *sseScriptedWriter) Write(data []byte) (int, error) {
	w.writeCalls++
	if w.write != nil {
		return w.write(data)
	}
	return w.metricsStreamWriter.Write(data)
}
func (w *sseScriptedWriter) FlushError() error {
	w.flushCalls++
	if w.flush != nil {
		return w.flush()
	}
	return w.metricsStreamWriter.FlushError()
}
func (w *sseScriptedWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadlineCalls++
	if w.setDeadline != nil {
		return w.setDeadline(deadline)
	}
	return w.metricsStreamWriter.SetWriteDeadline(deadline)
}

type sseLogAgent struct {
	AgentClient
	calls atomic.Int32
	read  func(context.Context, journal.Query) (journal.Page, error)
}

func (a *sseLogAgent) QueryJournal(ctx context.Context, _ string, query journal.Query) (journal.Page, error) {
	a.calls.Add(1)
	if a.read != nil {
		return a.read(ctx, query)
	}
	return journal.Page{Entries: []journal.Entry{{Cursor: "entry", Timestamp: time.Now(), Message: "fixture"}}}, nil
}

type sseJobStore struct {
	jobPersistence
	calls atomic.Int32
	read  func(context.Context) (controlstore.JobRecord, error)
}

func (s *sseJobStore) Job(ctx context.Context, _ string) (controlstore.JobRecord, error) {
	count := s.calls.Add(1)
	if s.read != nil {
		return s.read(ctx)
	}
	return controlstore.JobRecord{ID: "job", Status: "running", Progress: int(count), LayersJSON: "{}"}, nil
}

type sseReadProvider struct {
	SummarySnapshots
	read func() monitoring.State
}

func (p *sseReadProvider) Latest() monitoring.State { return p.read() }

type sseEndpoint struct {
	name, path, code      string
	status, initialFrames int
	serve                 func(*handler, http.ResponseWriter, *http.Request)
}

var sseEndpoints = []sseEndpoint{
	{"system", "/api/v1/system/events?interval=300", "REALTIME_STREAM_UNSUPPORTED", 500, 2, (*handler).systemEvents},
	{"logs", "/api/v1/logs/events?interval=300", "LOG_STREAM_UNSUPPORTED", 500, 2, (*handler).logEvents},
	{"jobs", "/api/v1/jobs/job/events", "JOB_STREAM_UNSUPPORTED", 501, 1, (*handler).jobEvents},
}

func sseRequest(endpoint sseEndpoint, ctx context.Context) *http.Request {
	request := httptest.NewRequest("GET", endpoint.path, nil).WithContext(ctx)
	routes := chi.NewRouteContext()
	routes.URLParams.Add("jobID", "job")
	return request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routes))
}
func sseFixture() (*handler, *sseLogAgent, *sseJobStore, *metricsSnapshotProvider, *metricsTickerFactory) {
	agent, store, provider, tickers := &sseLogAgent{}, &sseJobStore{}, metricsProvider(time.Now()), metricsTickers()
	jobs := newJobRegistry(nil)
	jobs.store = store
	return &handler{agent: agent, agentTimeout: time.Minute, jobs: jobs, summarySnapshots: provider, newRealtimeTicker: tickers.create}, agent, store, provider, tickers
}
func sseStart(endpoint sseEndpoint, api *handler, writer http.ResponseWriter, ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() { defer close(done); endpoint.serve(api, writer, sseRequest(endpoint, ctx)) }()
	return done
}

func TestSSEFrameCancellationAndErrors(t *testing.T) {
	boom, clearBoom := errors.New("frame failure"), errors.New("clear failure")
	for _, name := range []string{"already cancelled", "cancel during deadline", "cancel during write", "cancel during flush", "write", "short write", "flush", "deadline", "clear", "write and clear"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			writer := sseWriter()
			want := boom
			switch name {
			case "already cancelled":
				cancel()
				want = context.Canceled
			case "cancel during deadline":
				want = context.Canceled
				writer.setDeadline = func(deadline time.Time) error {
					if !deadline.IsZero() {
						cancel()
					}
					return writer.metricsStreamWriter.SetWriteDeadline(deadline)
				}
			case "cancel during write":
				want = context.Canceled
				writer.write = func(data []byte) (int, error) { cancel(); return len(data), nil }
			case "cancel during flush":
				want = context.Canceled
				writer.flush = func() error { cancel(); return nil }
			case "write":
				writer.writeErr = boom
			case "short write":
				writer.write = func(data []byte) (int, error) { return len(data) - 1, nil }
				want = io.ErrShortWrite
			case "flush":
				writer.flushErr = boom
			case "deadline":
				writer.deadlineErr = boom
			case "clear":
				writer.clearErr = clearBoom
				want = clearBoom
			case "write and clear":
				writer.writeErr = boom
				writer.clearErr = clearBoom
			}
			api := &handler{}
			err := api.writeSSEFrame(ctx, writer, http.NewResponseController(writer), "fixture")
			if !errors.Is(err, want) {
				t.Fatalf("error=%v want=%v", err, want)
			}
			if name == "write and clear" && !errors.Is(err, clearBoom) {
				t.Fatalf("clear error lost: %v", err)
			}
			if (name == "already cancelled" || name == "cancel during deadline" || name == "deadline") && writer.writeCalls != 0 {
				t.Fatal("write admitted after cancellation/deadline error")
			}
			if name != "clear" && name != "flush" && name != "cancel during flush" && writer.flushCalls != 0 {
				t.Fatal("failed/cancelled write flushed")
			}
			if name != "clear" && name != "write and clear" && !writer.deadline.IsZero() {
				t.Fatal("deadline not cleared")
			}
		})
	}
}

// 此 writer 只暴露基础 ResponseWriter 和 deadline，不能继承 Recorder 的 Flush。
type sseNoFlushWriter struct{ recorder *httptest.ResponseRecorder }

func (w sseNoFlushWriter) Header() http.Header              { return w.recorder.Header() }
func (w sseNoFlushWriter) Write(data []byte) (int, error)   { return w.recorder.Write(data) }
func (w sseNoFlushWriter) WriteHeader(status int)           { w.recorder.WriteHeader(status) }
func (w sseNoFlushWriter) SetWriteDeadline(time.Time) error { return nil }

func TestSSEStreamsRejectUnsupportedBeforeHeaders(t *testing.T) {
	for _, endpoint := range sseEndpoints {
		for _, missing := range []string{"deadline", "flush"} {
			t.Run(endpoint.name+"/"+missing, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				var writer http.ResponseWriter = recorder
				if missing == "flush" {
					writer = sseNoFlushWriter{recorder}
				}
				api := &handler{}
				endpoint.serve(api, &responseRecorder{ResponseWriter: writer}, sseRequest(endpoint, context.Background()))
				if recorder.Code != endpoint.status || strings.Contains(recorder.Header().Get("Content-Type"), "event-stream") {
					t.Fatalf("unsupported response=%d %s", recorder.Code, recorder.Body.String())
				}
				assertErrorCode(t, recorder, endpoint.code)
				if api.realtimeSubscribers.Load() != 0 {
					t.Fatal("unsupported stream leaked subscriber")
				}
			})
		}
	}
}

func TestSSEStreamsStopOnInitialAndBodyFailures(t *testing.T) {
	for _, endpoint := range sseEndpoints {
		for _, stage := range []string{"preflight deadline", "preflight clear", "initial write", "initial flush", "body write", "body flush", "body deadline", "body clear"} {
			t.Run(endpoint.name+"/"+stage, func(t *testing.T) {
				api, agent, store, provider, tickers := sseFixture()
				writer := sseWriter()
				boom := errors.New("fixture failure")
				targetFrame := 1
				if strings.HasPrefix(stage, "body") {
					targetFrame = 2
				}
				writer.write = func(data []byte) (int, error) {
					if strings.HasSuffix(stage, "write") && writer.writeCalls == targetFrame {
						return 0, boom
					}
					return writer.metricsStreamWriter.Write(data)
				}
				writer.flush = func() error {
					if strings.HasSuffix(stage, "flush") && writer.flushCalls == targetFrame {
						return boom
					}
					return writer.metricsStreamWriter.FlushError()
				}
				writer.setDeadline = func(deadline time.Time) error {
					failAt := 0
					switch stage {
					case "preflight deadline":
						failAt = 1
					case "preflight clear":
						failAt = 2
					case "body deadline":
						failAt = 5
					case "body clear":
						failAt = 6
					}
					if writer.deadlineCalls == failAt {
						return boom
					}
					return writer.metricsStreamWriter.SetWriteDeadline(deadline)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := sseStart(endpoint, api, &responseRecorder{ResponseWriter: writer}, ctx)
				var bodyTicker *metricsTicker
				if endpoint.name == "jobs" && strings.HasPrefix(stage, "body") {
					writer.frame(t)
					bodyTicker = tickers.next(t)
					bodyTicker.ticks <- time.Now()
				}
				metricsAwaitDone(t, done)
				if bodyTicker != nil && !bodyTicker.stopped.Load() {
					t.Fatal("body failure leaked ticker")
				}
				if strings.HasPrefix(stage, "preflight") {
					if writer.status != endpoint.status {
						t.Fatalf("status=%d", writer.status)
					}
				} else if strings.HasPrefix(stage, "initial") && endpoint.name != "jobs" {
					if agent.calls.Load() != 0 || provider.reads.Load() != 0 || store.calls.Load() != 0 {
						t.Fatal("initial failure still read business data")
					}
				}
				if api.realtimeSubscribers.Load() != 0 {
					t.Fatal("failed stream leaked subscriber")
				}
				for len(tickers.created) > 0 {
					if !tickers.next(t).stopped.Load() {
						t.Fatal("failed stream leaked ticker")
					}
				}
			})
		}
	}
}

func TestSSEStreamsCanceledBeforeStart(t *testing.T) {
	for _, endpoint := range sseEndpoints {
		t.Run(endpoint.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			writer := sseWriter()
			api := &handler{}
			endpoint.serve(api, writer, sseRequest(endpoint, ctx))
			if writer.status != 0 || writer.writeCalls != 0 || writer.deadlineCalls != 0 || api.realtimeSubscribers.Load() != 0 {
				t.Fatal("cancelled stream started work")
			}
		})
	}
}

type sseCancelTicker struct {
	*metricsTicker
	once   sync.Once
	cancel context.CancelFunc
}

func (ticker *sseCancelTicker) C() <-chan time.Time {
	ticker.once.Do(ticker.cancel)
	return ticker.ticks
}

func TestSSEStreamsCanceledWithReadyTick(t *testing.T) {
	for _, endpoint := range sseEndpoints {
		for _, which := range []string{"poll", "heartbeat"} {
			if which == "heartbeat" && endpoint.name != "system" {
				continue
			}
			t.Run(endpoint.name+"/"+which, func(t *testing.T) {
				for iteration := 0; iteration < 64; iteration++ {
					api, agent, store, provider, _ := sseFixture()
					ctx, cancel := context.WithCancel(context.Background())
					var timers []*metricsTicker
					api.newRealtimeTicker = func(interval time.Duration) realtimeTicker {
						ticker := &metricsTicker{ticks: make(chan time.Time, 1), interval: interval}
						timers = append(timers, ticker)
						selected := which == "heartbeat" && interval == realtimeHeartbeatInterval || which == "poll" && interval != realtimeHeartbeatInterval
						if selected {
							ticker.ticks <- time.Now()
							return &sseCancelTicker{metricsTicker: ticker, cancel: cancel}
						}
						return ticker
					}
					writer := sseWriter()
					endpoint.serve(api, writer, sseRequest(endpoint, ctx))
					cancel()
					if writer.writeCalls != endpoint.initialFrames {
						t.Fatalf("iteration %d: wrote after cancellation (%d)", iteration, writer.writeCalls)
					}
					if agent.calls.Load()+store.calls.Load()+provider.reads.Load() != 1 {
						t.Fatal("ready tick read after cancellation")
					}
					for _, ticker := range timers {
						if !ticker.stopped.Load() {
							t.Fatal("ticker leaked")
						}
					}
					if api.realtimeSubscribers.Load() != 0 {
						t.Fatal("subscriber leaked")
					}
				}
			})
		}
	}
}

func TestSSEStreamsDiscardReadsCompletedAfterCancellation(t *testing.T) {
	for _, endpoint := range sseEndpoints {
		for _, result := range []string{"success", "error"} {
			t.Run(endpoint.name+"/"+result, func(t *testing.T) {
				api, agent, store, provider, _ := sseFixture()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				entered, release := make(chan struct{}), make(chan struct{})
				read := func() { close(entered); <-release }
				var readErr error
				if result == "error" {
					readErr = errors.New("source unavailable")
				}
				agent.read = func(callCtx context.Context, _ journal.Query) (journal.Page, error) {
					read()
					if callCtx.Err() == nil {
						t.Error("log clone lost cancelled lease context")
					}
					return journal.Page{}, readErr
				}
				store.read = func(context.Context) (controlstore.JobRecord, error) {
					read()
					return controlstore.JobRecord{ID: "job", Status: "completed"}, readErr
				}
				api.summarySnapshots = &sseReadProvider{SummarySnapshots: provider, read: func() monitoring.State { read(); return provider.Latest() }}
				writer := sseWriter()
				done := sseStart(endpoint, api, writer, ctx)
				select {
				case <-entered:
				case <-time.After(3 * time.Second):
					t.Fatal("read did not begin")
				}
				cancel()
				close(release)
				metricsAwaitDone(t, done)
				want := 1
				if endpoint.name == "jobs" {
					want = 0
				}
				if writer.writeCalls != want {
					t.Fatalf("read emitted frame after cancellation: %d", writer.writeCalls)
				}
			})
		}
	}
}

func TestSSELogCancellationInterruptsAgentRead(t *testing.T) {
	api, agent, _, _, _ := sseFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	agent.read = func(callCtx context.Context, query journal.Query) (journal.Page, error) {
		if query.Since == nil {
			t.Error("stream query lost since")
		}
		close(entered)
		<-callCtx.Done()
		return journal.Page{}, callCtx.Err()
	}
	writer := sseWriter()
	done := sseStart(sseEndpoints[1], api, writer, ctx)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("read did not begin")
	}
	cancel()
	metricsAwaitDone(t, done)
	if writer.writeCalls != 1 {
		t.Fatal("cancelled log read emitted unavailable/heartbeat")
	}
}

func TestSSEStreamsCancelIdleAndLeaveJobRunning(t *testing.T) {
	for _, endpoint := range sseEndpoints {
		t.Run(endpoint.name, func(t *testing.T) {
			api, _, _, provider, tickers := sseFixture()
			jobContext, jobCancel := context.WithCancel(context.Background())
			defer jobCancel()
			var cancelled atomic.Int32
			api.jobs = newJobRegistry(nil)
			api.jobs.jobs["job"] = &jobEntry{view: jobSnapshot{ID: "job", Status: "running"}, ctx: jobContext, cancel: func() { cancelled.Add(1); jobCancel() }}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			writer := sseWriter()
			done := sseStart(endpoint, api, writer, ctx)
			for i := 0; i < endpoint.initialFrames; i++ {
				writer.frame(t)
			}
			poll := tickers.next(t)
			var heartbeat *metricsTicker
			if endpoint.name == "system" {
				heartbeat = tickers.next(t)
			}
			cancel()
			metricsAwaitDone(t, done)
			if !poll.stopped.Load() || heartbeat != nil && !heartbeat.stopped.Load() || api.realtimeSubscribers.Load() != 0 {
				t.Fatal("idle stream leaked resources")
			}
			if cancelled.Load() != 0 || jobContext.Err() != nil {
				t.Fatal("observation cancellation cancelled job execution")
			}
			select {
			case <-provider.Done():
				t.Fatal("observation cancellation stopped collector")
			default:
			}
			api.jobs.update("job", "completed", "done", "", 100)
			job, ok := api.jobs.get("job")
			if !ok || job.Status != "completed" {
				t.Fatal("job failed to finish independently")
			}
			// 新会话可以继续观察同一任务的终态；没有重放任务。
			if endpoint.name == "jobs" {
				next := sseWriter()
				endpoint.serve(api, next, sseRequest(endpoint, context.Background()))
				if !strings.Contains(next.frame(t), `"status":"completed"`) || cancelled.Load() != 0 {
					t.Fatal("new observation lost completed job")
				}
			}
		})
	}
}

func TestSSELogFramesRetainWireFormatAndDeadlines(t *testing.T) {
	for _, mode := range []string{"logs", "heartbeat", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			api, agent, _, _, tickers := sseFixture()
			if mode != "logs" {
				agent.read = func(context.Context, journal.Query) (journal.Page, error) {
					if mode == "unavailable" {
						return journal.Page{}, errors.New("unavailable")
					}
					return journal.Page{}, nil
				}
			}
			writer := sseWriter()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := sseStart(sseEndpoints[1], api, &responseRecorder{ResponseWriter: writer}, ctx)
			if got := writer.frame(t); got != "retry: 300000\n\n" {
				t.Fatal(got)
			}
			frame := writer.frame(t)
			if mode == "heartbeat" && frame != ": heartbeat\n\n" || mode != "heartbeat" && !strings.HasPrefix(frame, "event: "+mode+"\n") {
				t.Fatal(frame)
			}
			tickers.next(t)
			cancel()
			metricsAwaitDone(t, done)
			if writer.deadlineCalls != 6 || !writer.deadline.IsZero() {
				t.Fatalf("unbounded frame: deadlines=%v", writer.deadlines)
			}
		})
	}
}

func TestSSEStreamsSlowWritesAndFlushesHonorBudget(t *testing.T) {
	for _, endpoint := range sseEndpoints {
		for _, stage := range []string{"write", "flush"} {
			t.Run(endpoint.name+"/"+stage, func(t *testing.T) {
				api, _, _, _, tickers := sseFixture()
				// 仍传入生产 5 秒预算；fake 的服务时间让 deadline 在 10 毫秒后触发。
				api.now = func() time.Time { return time.Now().Add(-realtimeWriteTimeout + 10*time.Millisecond) }
				writer := sseWriter()
				entered := make(chan struct{})
				var expired bool
				budgetedWait := func() error {
					close(entered)
					deadline := writer.deadline
					if deadline.IsZero() {
						t.Error("missing deadline")
						return fmt.Errorf("missing deadline")
					}
					timer := time.NewTimer(time.Until(deadline))
					defer timer.Stop()
					<-timer.C
					expired = true
					return os.ErrDeadlineExceeded
				}
				if stage == "write" {
					writer.write = func([]byte) (int, error) { return 0, budgetedWait() }
				} else {
					writer.flush = budgetedWait
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := sseStart(endpoint, api, writer, ctx)
				select {
				case <-entered:
				case <-time.After(3 * time.Second):
					t.Fatal("write/flush never started")
				}
				cancel()
				metricsAwaitDone(t, done)
				if !expired {
					t.Fatal("slow writer did not reach deadline")
				}
				if !writer.deadline.IsZero() || api.realtimeSubscribers.Load() != 0 {
					t.Fatal("slow writer leaked deadline/subscriber")
				}
				for len(tickers.created) > 0 {
					if !tickers.next(t).stopped.Load() {
						t.Fatal("slow writer leaked ticker")
					}
				}
			})
		}
	}
}

// 仅使用假 Agent、假 ticker 和内存 writer；不启动监听器或宿主机命令。
func TestSSELogRecentDedupWindowAndCursor(t *testing.T) {
	api, agent, _, _, tickers := sseFixture()
	at := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	type step struct {
		page journal.Page
		err  error
		want []string
	}
	var steps []step
	for start := 0; start < logDedupMaxIDs; start += 200 {
		var entries []journal.Entry
		var ids []string
		for i := start; i < start+200 && i < logDedupMaxIDs; i++ {
			id := fmt.Sprintf("id-%d", i)
			entries = append(entries, journal.Entry{Cursor: id, Timestamp: at.Add(time.Duration(i) * time.Second), Message: "fixture"})
			ids = append(ids, id)
		}
		steps = append(steps, step{page: journal.Page{Entries: entries, NextCursor: fmt.Sprint(start)}, want: ids})
	}
	page := func(cursor string, ids ...string) journal.Page {
		var entries []journal.Entry
		for _, id := range ids {
			entries = append(entries, journal.Entry{Cursor: id, Timestamp: at.Add(time.Hour), Message: "fixture"})
		}
		return journal.Page{Entries: entries, NextCursor: cursor}
	}
	oversized := strings.Repeat("x", logDedupMaxBytes+1)
	steps = append(steps,
		step{page: page("ignored-heartbeat", "id-0", "id-2047")},
		step{err: errors.New("fixture unavailable")},
		step{page: page("new", "new"), want: []string{"new"}},
		step{page: page("replay", "id-0", "id-0", "id-2047"), want: []string{"id-0"}},
		step{page: page("oversized", oversized, oversized), want: []string{oversized, oversized}},
		step{page: page("empty", "", ""), want: []string{""}},
	)
	var expectedCursor string
	var expectedSince *time.Time
	index := 0
	agent.read = func(_ context.Context, query journal.Query) (journal.Page, error) {
		if index >= len(steps) {
			t.Error("unexpected poll")
			return journal.Page{}, errors.New("extra poll")
		}
		if query.Cursor != expectedCursor {
			t.Errorf("cursor=%q want=%q", query.Cursor, expectedCursor)
		}
		if query.Since == nil || expectedSince != nil && !query.Since.Equal(*expectedSince) {
			t.Error("since advancement changed")
		}
		current := steps[index]
		index++
		if len(current.want) > 0 {
			expectedCursor = current.page.NextCursor
			latest := current.page.Entries[len(current.page.Entries)-1].Timestamp.Add(time.Nanosecond)
			expectedSince = &latest
		}
		return current.page, current.err
	}
	writer := sseWriter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := sseStart(sseEndpoints[1], api, writer, ctx)
	if got := writer.frame(t); got != "retry: 300000\n\n" {
		t.Fatal(got)
	}
	var ticker *metricsTicker
	for i, current := range steps {
		if i > 0 {
			ticker.ticks <- at
		}
		frame := writer.frame(t)
		if current.err != nil {
			if frame != "event: unavailable\ndata: {}\n\n" {
				t.Fatal("unavailable frame changed")
			}
		} else if len(current.want) == 0 {
			if frame != ": heartbeat\n\n" {
				t.Fatal("duplicate-only poll did not heartbeat")
			}
		} else {
			const prefix = "event: logs\ndata: "
			if !strings.HasPrefix(frame, prefix) || !strings.HasSuffix(frame, "\n\n") {
				t.Fatal("logs framing changed")
			}
			var result logResponse
			if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(frame, prefix), "\n\n")), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Entries) != len(current.want) {
				t.Fatalf("step %d: entries=%d want=%d", i, len(result.Entries), len(current.want))
			}
			for j, entry := range result.Entries {
				if entry.ID != current.want[j] {
					t.Fatalf("step %d entry %d: unexpected ID", i, j)
				}
			}
			if result.NextCursor != current.page.NextCursor {
				t.Fatal("wire cursor changed")
			}
		}
		if i == 0 {
			ticker = tickers.next(t)
		}
	}
	cancel()
	metricsAwaitDone(t, done)
	if !ticker.stopped.Load() || int(agent.calls.Load()) != len(steps) {
		t.Fatal("stream cancellation leaked polling")
	}
}
