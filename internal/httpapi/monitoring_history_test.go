package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
)

func TestMetricsHistoryRangeCompatibility(t *testing.T) {
	now := time.Date(2026, 10, 13, 1, 2, 3, 456789123, time.UTC)
	for _, preset := range []struct {
		value    string
		duration time.Duration
	}{
		{"", 6 * time.Hour}, {"unknown", 6 * time.Hour}, {"1h", time.Hour},
		{"6h", 6 * time.Hour}, {"24h", 24 * time.Hour}, {"7d", 7 * 24 * time.Hour},
	} {
		got, err := ParseMonitoringRange(url.Values{"range": {preset.value}}, now)
		if err != nil || !got.From.Equal(now.Add(-preset.duration)) || !got.To.Equal(now) {
			t.Fatalf("range %q got=%v error=%v", preset.value, got, err)
		}
	}
	values := url.Values{"range": {"7d"}, "from": {now.Add(-time.Hour).Format(time.RFC3339Nano)}, "to": {now.Add(time.Minute).Format(time.RFC3339Nano)}}
	got, err := ParseMonitoringRange(values, now)
	if err != nil || !got.From.Equal(now.Add(-time.Hour)) || !got.To.Equal(now.Add(time.Minute)) {
		t.Fatalf("exact priority/future tolerance got=%v error=%v", got, err)
	}
	values.Set("to", now.Add(time.Minute+time.Nanosecond).Format(time.RFC3339Nano))
	if _, err := ParseMonitoringRange(values, now); !errors.Is(err, ErrMonitoringRangeInvalid) {
		t.Fatalf("future tolerance error=%v", err)
	}
	values.Set("to", now.Format(time.RFC3339Nano))
	values.Set("from", now.Add(-MonitoringRetention).Format(time.RFC3339Nano))
	if _, err := ParseMonitoringRange(values, now); err != nil {
		t.Fatal(err)
	}
	values.Set("from", now.Add(-MonitoringRetention-time.Nanosecond).Format(time.RFC3339Nano))
	if _, err := ParseMonitoringRange(values, now); !errors.Is(err, ErrMonitoringRangeInvalid) {
		t.Fatalf("retention boundary error=%v", err)
	}
}

func TestMetricsHistoryQueryUsesExactNarrowInterfaceAndContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	at := time.Now().UTC()
	historyRange := MonitoringRange{From: at.Add(-time.Minute + 123*time.Nanosecond), To: at.Add(-456 * time.Nanosecond)}
	store := &fakeMonitoringHistoryStore{}
	got, err := QueryMonitoringSamples(ctx, store, historyRange)
	if err != nil || got == nil || store.ctx != ctx || !store.since.Equal(historyRange.From) || !store.to.Equal(historyRange.To) {
		t.Fatalf("exact narrow query: result=%v store=%+v error=%v", got, store, err)
	}
}

func historyRequest() *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/monitoring/samples?range=6h", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "test-session"})
	return request
}

func historyHandler(store MonitoringHistoryStore) http.Handler {
	return NewHandler(Config{Auth: &transientSessionAuth{}, Agent: &fakeAgentClient{}, MonitoringHistory: store, RequestID: func() string { return "history-request" }})
}

func TestMetricsHistoryHTTPCompleteSuccessAndEmpty(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Minute)
	for _, samples := range [][]controlstore.MetricSample{nil, {{CollectedAt: at, CPUPercent: 12.5, Temperatures: []controlstore.MetricTemperature{{Name: "cpu<&>", TemperatureCelsius: 43}}}}} {
		store := &fakeMonitoringHistoryStore{samples: samples}
		response := httptest.NewRecorder()
		historyHandler(store).ServeHTTP(response, historyRequest())
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
			t.Fatalf("success status=%d headers=%v", response.Code, response.Header())
		}
		if len(samples) == 0 && response.Body.String() != "[]\n" {
			t.Fatalf("empty history=%q", response.Body.String())
		}
		var decoded []controlstore.MetricSample
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil || len(decoded) != len(samples) {
			t.Fatalf("complete body=%q error=%v", response.Body.String(), err)
		}
		deadline, ok := store.ctx.Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > MonitoringHistoryTimeout {
			t.Fatalf("missing bounded query deadline=%v", deadline)
		}
	}
}

func TestMetricsHistoryHTTPStableErrorsBeforeSuccessHeaders(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Minute)
	for _, tc := range []struct {
		name   string
		store  *fakeMonitoringHistoryStore
		status int
		code   string
	}{
		{"row limit", &fakeMonitoringHistoryStore{err: controlstore.ErrMetricHistoryLimitExceeded}, 422, "MONITORING_HISTORY_LIMIT_EXCEEDED"},
		{"invalid retained row", &fakeMonitoringHistoryStore{err: controlstore.ErrInvalidMetricHistory}, 500, "MONITORING_HISTORY_INVALID"},
		{"storage detail redacted", &fakeMonitoringHistoryStore{err: errors.New("private-storage-value")}, 500, "MONITORING_READ_FAILED"},
		{"deadline", &fakeMonitoringHistoryStore{err: context.DeadlineExceeded}, 504, "MONITORING_READ_TIMEOUT"},
		{"encode invalid float", &fakeMonitoringHistoryStore{samples: []controlstore.MetricSample{{CollectedAt: at, CPUPercent: math.Inf(1)}}}, 500, "MONITORING_HISTORY_INVALID"},
		{"encode oversized name", &fakeMonitoringHistoryStore{samples: []controlstore.MetricSample{{CollectedAt: at, Temperatures: []controlstore.MetricTemperature{{Name: strings.Repeat("x", controlstore.MaxMetricHistoryResponseBytes+1)}}}}}, 422, "MONITORING_HISTORY_LIMIT_EXCEEDED"},
		{"oversized parent result", &fakeMonitoringHistoryStore{samples: make([]controlstore.MetricSample, controlstore.MaxMetricSamples+1)}, 422, "MONITORING_HISTORY_LIMIT_EXCEEDED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			historyHandler(tc.store).ServeHTTP(response, historyRequest())
			if response.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, tc.status, response.Body.String())
			}
			var failure ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil || failure.Code != tc.code || failure.RequestID != "history-request" {
				t.Fatalf("error body=%s decode=%v", response.Body.String(), err)
			}
			if strings.Contains(response.Body.String(), "private-storage-value") || strings.HasPrefix(response.Body.String(), "[") {
				t.Fatal("returned private detail or partial successful array")
			}
			if (tc.status == 422 || tc.status == 504) && !strings.Contains(failure.Message, "缩小时间范围") {
				t.Fatal("missing smaller range guidance")
			}
		})
	}
}

type historyLegacyOnlyStore struct {
	ControlStore
	calls int
}

func (s *historyLegacyOnlyStore) MetricSamples(context.Context, time.Time) ([]controlstore.MetricSample, error) {
	s.calls++
	return []controlstore.MetricSample{}, nil
}

func TestMetricsHistoryHTTPNeverFallsBackToLegacyStore(t *testing.T) {
	store := &historyLegacyOnlyStore{}
	api := NewHandler(Config{Auth: &transientSessionAuth{}, Agent: &fakeAgentClient{}, ControlStore: store})
	response := httptest.NewRecorder()
	api.ServeHTTP(response, historyRequest())
	if response.Code != 503 || store.calls != 0 {
		t.Fatalf("legacy fallback status=%d calls=%d", response.Code, store.calls)
	}
	assertErrorCode(t, response, "MONITORING_STORE_UNAVAILABLE")
}

func TestMetricsHistoryHTTPUsesBoundedConcreteControlStore(t *testing.T) {
	store, err := controlstore.Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	at := time.Now().UTC().Truncate(time.Minute)
	if err := store.RecordMetricSample(context.Background(), controlstore.MetricSample{CollectedAt: at, CPUPercent: 17}); err != nil {
		t.Fatal(err)
	}
	api := NewHandler(Config{Auth: &transientSessionAuth{}, Agent: &fakeAgentClient{}, ControlStore: store})
	response := httptest.NewRecorder()
	api.ServeHTTP(response, historyRequest())
	var decoded []controlstore.MetricSample
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil || response.Code != 200 || len(decoded) != 1 || decoded[0].CPUPercent != 17 {
		t.Fatalf("bounded production wiring status=%d body=%s error=%v", response.Code, response.Body.String(), err)
	}
}

func TestMetricsHistoryHTTPCancellationAndAuthentication(t *testing.T) {
	store := &fakeMonitoringHistoryStore{}
	api := historyHandler(store)
	request := historyRequest()
	request.Header.Del("Cookie")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != 401 || store.ctx != nil {
		t.Fatalf("unauthenticated query status=%d called=%v", response.Code, store.ctx != nil)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response = httptest.NewRecorder()
	(&handler{monitoringHistory: store}).monitoringSamples(response, historyRequest().WithContext(ctx))
	if response.Body.Len() != 0 || store.ctx != nil {
		t.Fatalf("cancelled query body=%s called=%v", response.Body.String(), store.ctx != nil)
	}
}
