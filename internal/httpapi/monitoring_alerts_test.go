package httpapi

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/Kkwans/nas-control-plane/internal/monitoring"
	"github.com/Kkwans/nas-control-plane/internal/system"
)

type fakeResourceAlerts struct {
	snapshot monitoring.AlertsSnapshot
	done     chan struct{}
	reads    int
}

func (f *fakeResourceAlerts) LatestAlerts(time.Time) monitoring.AlertsSnapshot {
	f.reads++
	return f.snapshot
}
func (f *fakeResourceAlerts) Done() <-chan struct{} { return f.done }
func alertFixture(enabled bool) *fakeResourceAlerts {
	at := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	return &fakeResourceAlerts{snapshot: monitoring.NewAlertProcessor(enabled, at).Snapshot(at), done: make(chan struct{})}
}
func alertRequest(authenticated bool) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/monitoring/alerts", nil)
	if authenticated {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "fixture"})
	}
	return r
}
func alertHandler(provider ResourceAlertSnapshots) http.Handler {
	return NewHandler(Config{Auth: &transientSessionAuth{}, Agent: &fakeAgentClient{}, ResourceAlerts: provider})
}

func TestResourceAlertsHTTPAuthenticationAndAvailability(t *testing.T) {
	provider := alertFixture(false)
	recorder := httptest.NewRecorder()
	alertHandler(provider).ServeHTTP(recorder, alertRequest(false))
	if recorder.Code != 401 || provider.reads != 0 {
		t.Fatalf("auth bypass %d reads %d", recorder.Code, provider.reads)
	}
	for _, stopped := range []bool{false, true} {
		var p ResourceAlertSnapshots
		if stopped {
			f := alertFixture(false)
			close(f.done)
			p = f
		}
		recorder = httptest.NewRecorder()
		alertHandler(p).ServeHTTP(recorder, alertRequest(true))
		if recorder.Code != 503 {
			t.Fatal(recorder.Code)
		}
		assertErrorCode(t, recorder, "MONITORING_ALERTS_UNAVAILABLE")
	}
}
func TestResourceAlertsHTTPCompleteReadOnlyResponse(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		provider := alertFixture(enabled)
		handler := alertHandler(provider)
		for i := 0; i < 100; i++ {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, alertRequest(true))
			if recorder.Code != 200 || recorder.Body.Len() > 8192 || recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("response %d %s", recorder.Code, recorder.Body)
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"enabled", "observedAt", "startedAt", "rules"} {
				if _, ok := payload[key]; !ok {
					t.Fatal(key)
				}
			}
			var rules map[string]map[string]json.RawMessage
			_ = json.Unmarshal(payload["rules"], &rules)
			for _, name := range []string{"cpu", "memory"} {
				for _, key := range []string{"sampleAt", "receivedAt", "value", "pendingSince", "firingSince", "recoveringSince", "lastRecoveredAt"} {
					if string(rules[name][key]) != "null" {
						t.Fatalf("%s.%s not explicitly null", name, key)
					}
				}
			}
		}
		if provider.reads != 100 {
			t.Fatal(provider.reads)
		}
	}
}
func TestResourceAlertsHTTPMarshalAndBudgetBeforeSuccess(t *testing.T) {
	for _, oversize := range []bool{false, true} {
		provider := alertFixture(true)
		code := "MONITORING_ALERTS_RESPONSE_INVALID"
		if oversize {
			provider.snapshot.Rules.CPU.ReasonCode = strings.Repeat("x", 8192)
			code = "MONITORING_ALERTS_LIMIT_EXCEEDED"
		} else {
			v := math.NaN()
			provider.snapshot.Rules.CPU.Value = &v
		}
		recorder := httptest.NewRecorder()
		alertHandler(provider).ServeHTTP(recorder, alertRequest(true))
		if recorder.Code != 500 {
			t.Fatal(recorder.Code)
		}
		assertErrorCode(t, recorder, code)
	}
}

func TestResourceAlertsHTTPExactByteBudget(t *testing.T) {
	provider := alertFixture(true)
	encoded, err := json.Marshal(provider.snapshot)
	if err != nil {
		t.Fatal(err)
	}
	original := len(provider.snapshot.Rules.CPU.ReasonCode)
	for _, size := range []int{8192, 8193} {
		provider.snapshot.Rules.CPU.ReasonCode = strings.Repeat("x", size-len(encoded)-1+original)
		recorder := httptest.NewRecorder()
		alertHandler(provider).ServeHTTP(recorder, alertRequest(true))
		if size == 8192 {
			if recorder.Code != 200 || recorder.Body.Len() != size {
				t.Fatalf("boundary: %d, %d", recorder.Code, recorder.Body.Len())
			}
		} else {
			if recorder.Code != 500 {
				t.Fatal(recorder.Code)
			}
			assertErrorCode(t, recorder, "MONITORING_ALERTS_LIMIT_EXCEEDED")
		}
	}
}

type alertReadOnlyBackend struct{ sources, writes, prunes int }

func (b *alertReadOnlyBackend) CollectSystemSummary(context.Context) (system.Summary, error) {
	b.sources++
	return system.Summary{}, nil
}
func (b *alertReadOnlyBackend) RecordMetricSample(context.Context, controlstore.MetricSample) error {
	b.writes++
	return nil
}
func (b *alertReadOnlyBackend) PruneMetricSamples(context.Context, time.Time) error {
	b.prunes++
	return nil
}

func TestResourceAlertsHTTPNeverSamplesOrUsesSQL(t *testing.T) {
	backend := &alertReadOnlyBackend{}
	collector, err := monitoring.NewCollector(backend, backend, monitoring.Options{ResourceAlerts: true})
	if err != nil {
		t.Fatal(err)
	}
	api := alertHandler(collector)
	for i := 0; i < 100; i++ {
		response := httptest.NewRecorder()
		api.ServeHTTP(response, alertRequest(true))
		if response.Code != 200 {
			t.Fatal(response.Code)
		}
	}
	if backend.sources != 0 || backend.writes != 0 || backend.prunes != 0 {
		t.Fatalf("GET caused work: %+v", backend)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = collector.Run(ctx); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	api.ServeHTTP(response, alertRequest(true))
	if response.Code != 503 {
		t.Fatal(response.Code)
	}
	assertErrorCode(t, response, "MONITORING_ALERTS_UNAVAILABLE")
}
