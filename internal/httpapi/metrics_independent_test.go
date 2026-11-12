package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
)

type metricsWriteSpy struct {
	ControlStore
	writes int
}

func (s *metricsWriteSpy) RecordMetricSample(context.Context, controlstore.MetricSample) error {
	s.writes++
	return nil
}

type metricsCancelRecorder struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (r metricsCancelRecorder) SetWriteDeadline(time.Time) error { return nil }

func (r metricsCancelRecorder) Flush() {
	r.ResponseRecorder.Flush()
	if strings.Contains(r.Body.String(), "event: snapshot") {
		r.cancel()
	}
}
func TestMetricsRequestsNeverWriteHistory(t *testing.T) {
	store := &metricsWriteSpy{}
	agent := &metricsAgentSpy{}
	api := &handler{agent: agent, summarySnapshots: metricsProvider(time.Now()), agentTimeout: time.Second, controlStore: store}
	for i := 0; i < 100; i++ {
		response := httptest.NewRecorder()
		api.systemSummary(response, httptest.NewRequest(http.MethodGet, "/api/v1/system/summary", nil))
		if response.Code != 200 {
			t.Fatal(response.Code)
		}
	}
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		response := metricsCancelRecorder{httptest.NewRecorder(), cancel}
		api.systemEvents(response, httptest.NewRequest(http.MethodGet, "/api/v1/system/events?scope=summary", nil).WithContext(ctx))
		cancel()
		if !strings.Contains(response.Body.String(), "event: snapshot") {
			t.Fatal("missing snapshot")
		}
	}
	if agent.summaryCalls.Load() != 0 {
		t.Fatal("HTTP requested summary RPC")
	}
	if store.writes != 0 {
		t.Fatalf("HTTP wrote %d history samples", store.writes)
	}
}
