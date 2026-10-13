package httpapi

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/monitoring"
	"github.com/Kkwans/nas-control-plane/internal/system"
)

// 本测试只用内存快照与 fake Agent，但必须监听 loopback；在允许绑定端口的 CI 运行。
// 不因受限环境跳过，受限本地验证应明确排除本测试并报告未运行。
func TestRealtimeHTTPIntegrationWrappedDeadlineAndProviderShutdown(t *testing.T) {
	provider := metricsProvider(time.Now())
	agent := &metricsAgentSpy{}
	tickers := metricsTickers()
	api := &handler{summarySnapshots: provider, agent: agent, newRealtimeTicker: tickers.create, newRequestID: defaultRequestID, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	// 使用真实请求日志 wrapper；只替换发送 ticker，保留真实 wall-clock deadline。
	server := httptest.NewServer(api.withRequestID(http.HandlerFunc(api.systemEvents)))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/system/events?scope=summary&interval=300", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("response=%v", response)
	}
	reader := bufio.NewReader(response.Body)
	readFrame := func() string {
		t.Helper()
		var frame strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			frame.WriteString(line)
			if line == "\n" {
				return frame.String()
			}
		}
	}
	if frame := readFrame(); frame != "retry: 300000\n\n" {
		t.Fatal(frame)
	}
	if frame := readFrame(); !strings.Contains(frame, "event: snapshot\n") || !strings.Contains(frame, `"hostname":"fixture-nas"`) {
		t.Fatal(frame)
	}
	poll, heartbeat := tickers.next(t), tickers.next(t)
	if poll.interval != 300*time.Second || heartbeat.interval != 15*time.Second {
		t.Fatal("unexpected ticker intervals")
	}
	// 确实跨过前一帧的 5 秒 deadline，不能用立即关闭 provider 冒充空闲存活验证。
	time.Sleep(6 * time.Second)
	fresh := time.Now()
	provider.set(monitoring.State{Summary: &system.Summary{CollectedAt: fresh, Host: system.HostSnapshot{Hostname: "fixture-refreshed"}}, LastSuccessAt: fresh})
	poll.ticks <- fresh
	if frame := readFrame(); !strings.Contains(frame, "event: snapshot\n") || !strings.Contains(frame, `"hostname":"fixture-refreshed"`) {
		t.Fatalf("no fresh snapshot after >5s idle: %s", frame)
	}
	close(provider.done)
	if _, err := reader.ReadByte(); err != io.EOF {
		t.Fatalf("provider shutdown did not end real HTTP stream: %v", err)
	}
	if agent.summaryCalls.Load() != 0 {
		t.Fatal("HTTP stream requested Agent summary")
	}
}
