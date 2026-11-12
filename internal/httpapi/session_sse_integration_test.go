package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/auth"
)

// 真实临时 SQLite 会话贯通准入包装器和三个 SSE handler；无监听端口或真实 Agent。
func TestLiveSessionLogoutClosesAllSSEStreamsWithoutCancelingJob(t *testing.T) {
	service := openTestAuthService(t)
	first := streamTestSession(t, service)
	second, err := service.Login(context.Background(), first.Principal.Username, testLoginPassword(t))
	if err != nil {
		t.Fatal(err)
	}
	api, agent, _, provider, tickers := sseFixture()
	api.auth = service
	api.jobs = newJobRegistry(nil)
	jobContext, jobCancel := context.WithCancel(context.Background())
	defer jobCancel()
	var jobCancellations atomic.Int32
	api.jobs.jobs["job"] = &jobEntry{
		view:   jobSnapshot{ID: "job", Status: "running", Progress: 10},
		ctx:    jobContext,
		cancel: func() { jobCancellations.Add(1); jobCancel() },
	}

	type activeStream struct {
		endpoint     sseEndpoint
		writer       *sseScriptedWriter
		done         <-chan struct{}
		cancel       context.CancelFunc
		leaseContext context.Context
		timers       []*metricsTicker
	}
	var streams []*activeStream
	t.Cleanup(func() {
		for _, stream := range streams {
			stream.cancel()
		}
		for _, stream := range streams {
			metricsAwaitDone(t, stream.done)
		}
	})
	start := func(endpoint sseEndpoint, session auth.Session) *activeStream {
		t.Helper()
		requestContext, cancel := context.WithCancel(context.Background())
		writer := sseWriter()
		done := make(chan struct{})
		stream := &activeStream{endpoint: endpoint, writer: writer, done: done, cancel: cancel}
		streams = append(streams, stream)
		admitted := make(chan context.Context, 1)
		next := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			admitted <- request.Context()
			endpoint.serve(api, response, request)
		})
		wrapped := api.requireAuthentication(api.requireLiveSession(next))
		request := sseRequest(endpoint, requestContext)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token})
		go func() {
			defer close(done)
			wrapped.ServeHTTP(&responseRecorder{ResponseWriter: writer}, request)
		}()
		select {
		case stream.leaseContext = <-admitted:
		case <-done:
			t.Fatal("authenticated SSE did not start")
		case <-time.After(3 * time.Second):
			t.Fatal("live-session admission did not finish")
		}
		for i := 0; i < endpoint.initialFrames; i++ {
			writer.frame(t)
		}
		stream.timers = append(stream.timers, tickers.next(t))
		if endpoint.name == "system" {
			stream.timers = append(stream.timers, tickers.next(t))
		}
		return stream
	}
	var revoked, retained []*activeStream
	for _, endpoint := range sseEndpoints {
		revoked = append(revoked, start(endpoint, first))
	}
	for _, endpoint := range sseEndpoints {
		retained = append(retained, start(endpoint, second))
	}
	if api.realtimeSubscribers.Load() != 2 {
		t.Fatal("shared system subscribers were not established")
	}

	if err := service.Logout(context.Background(), first.Token); err != nil {
		t.Fatal(err)
	}
	for _, stream := range revoked {
		metricsAwaitDone(t, stream.done)
		if !errors.Is(context.Cause(stream.leaseContext), auth.StreamLoggedOut) {
			t.Fatalf("%s: missing session revocation cause", stream.endpoint.name)
		}
		if stream.writer.writeCalls != stream.endpoint.initialFrames || !stream.writer.deadline.IsZero() {
			t.Fatalf("%s: revoked stream wrote again or retained its deadline", stream.endpoint.name)
		}
		for _, ticker := range stream.timers {
			if !ticker.stopped.Load() {
				t.Fatalf("%s: revoked ticker leaked", stream.endpoint.name)
			}
		}
	}
	if api.realtimeSubscribers.Load() != 1 {
		t.Fatal("revocation affected wrong system subscriber count")
	}
	job, exists := api.jobs.get("job")
	if !exists || job.Status != "running" || job.Progress != 10 || jobCancellations.Load() != 0 || jobContext.Err() != nil {
		t.Fatal("logout cancelled or mutated accepted job execution")
	}
	select {
	case <-provider.Done():
		t.Fatal("logout stopped shared collector")
	default:
	}

	// 同账号另一 cookie 仍能产生实际业务帧，不能仅以 context 尚未关闭作为证据。
	api.jobs.update("job", "running", "still running", "", 50)
	for _, stream := range retained {
		if stream.leaseContext.Err() != nil {
			t.Fatalf("%s: independent session revoked", stream.endpoint.name)
		}
		select {
		case <-stream.done:
			t.Fatalf("%s: independent stream stopped", stream.endpoint.name)
		default:
		}
		stream.timers[0].ticks <- time.Now()
		frame := stream.writer.frame(t)
		switch stream.endpoint.name {
		case "system":
			if !strings.HasPrefix(frame, "event: snapshot\n") {
				t.Fatal(frame)
			}
		case "logs":
			if frame != ": heartbeat\n\n" {
				t.Fatal(frame)
			}
		case "jobs":
			if !strings.Contains(frame, `"message":"still running"`) {
				t.Fatal(frame)
			}
		}
	}
	if agent.calls.Load() != 3 || provider.reads.Load() != 3 {
		t.Fatal("retained subscribers did not independently continue reads")
	}
	job, exists = api.jobs.get("job")
	if !exists || job.Status != "running" || jobCancellations.Load() != 0 {
		t.Fatal("continued observation changed execution")
	}

	if err := service.Logout(context.Background(), second.Token); err != nil {
		t.Fatal(err)
	}
	for _, stream := range retained {
		metricsAwaitDone(t, stream.done)
		for _, ticker := range stream.timers {
			if !ticker.stopped.Load() {
				t.Fatal("retained session ticker leaked during cleanup")
			}
		}
	}
	if api.realtimeSubscribers.Load() != 0 || jobCancellations.Load() != 0 || jobContext.Err() != nil {
		t.Fatal("final stream cleanup leaked subscriber or cancelled job")
	}
	api.jobs.update("job", "completed", "done", "", 100)
	job, exists = api.jobs.get("job")
	if !exists || job.Status != "completed" {
		t.Fatal("accepted job could not complete after all sessions logged out")
	}
}
