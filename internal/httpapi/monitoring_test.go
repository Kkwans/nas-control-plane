package httpapi

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
)

func TestParseMonitoringRangeSupportsQuickAndExactRanges(t *testing.T) {
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)

	quick, err := ParseMonitoringRange(url.Values{"range": []string{"1h"}}, now)
	if err != nil {
		t.Fatalf("quick range error = %v", err)
	}
	if !quick.From.Equal(now.Add(-time.Hour)) || !quick.To.Equal(now) {
		t.Fatalf("quick range = %#v", quick)
	}

	exact, err := ParseMonitoringRange(url.Values{
		"from": []string{"2026-07-20T12:00:00+08:00"},
		"to":   []string{"2026-07-23T12:00:00Z"},
	}, now)
	if err != nil {
		t.Fatalf("exact range error = %v", err)
	}
	if !exact.From.Equal(now.Add(-72*time.Hour)) || !exact.To.Equal(now) {
		t.Fatalf("exact range = %#v", exact)
	}
}

func TestParseMonitoringRangeRejectsInvalidExactRanges(t *testing.T) {
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	cases := []url.Values{
		{"from": []string{"2026-07-22T12:00:00Z"}},
		{"to": []string{"2026-07-22T12:00:00Z"}},
		{"from": []string{"not-a-time"}, "to": []string{"2026-07-22T12:00:00Z"}},
		{"from": []string{"2026-07-22T12:00:00Z"}, "to": []string{"2026-07-21T12:00:00Z"}},
		{"from": []string{"2026-07-15T11:59:59Z"}, "to": []string{"2026-07-23T12:00:00Z"}},
		{"from": []string{"2026-07-23T12:00:00Z"}, "to": []string{"2026-07-23T12:02:00Z"}},
	}
	for _, values := range cases {
		if _, err := ParseMonitoringRange(values, now); !errors.Is(err, ErrMonitoringRangeInvalid) {
			t.Errorf("ParseMonitoringRange(%v) error = %v, want ErrMonitoringRangeInvalid", values, err)
		}
	}
}

func TestQueryMonitoringSamplesBoundsResultsAndPassesContext(t *testing.T) {
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	historyRange := MonitoringRange{From: now.Add(-2 * time.Hour), To: now}
	store := &fakeMonitoringHistoryStore{samples: []controlstore.MetricSample{
		{CollectedAt: now.Add(-3 * time.Hour), CPUPercent: 1},
		{CollectedAt: now.Add(-2 * time.Hour), CPUPercent: 2},
		{CollectedAt: now.Add(-time.Hour), CPUPercent: 3},
		{CollectedAt: now.Add(time.Minute), CPUPercent: 4},
	}}

	result, err := QueryMonitoringSamples(context.Background(), store, historyRange)
	if err != nil {
		t.Fatalf("QueryMonitoringSamples() error = %v", err)
	}
	if !store.since.Equal(historyRange.From) {
		t.Fatalf("since = %s, want %s", store.since, historyRange.From)
	}
	if len(result) != 2 || result[0].CPUPercent != 2 || result[1].CPUPercent != 3 {
		t.Fatalf("result = %#v", result)
	}
}

func TestQueryMonitoringSamplesHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := QueryMonitoringSamples(ctx, &fakeMonitoringHistoryStore{}, MonitoringRange{
		From: time.Now().Add(-time.Hour),
		To:   time.Now(),
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("QueryMonitoringSamples() error = %v, want context.Canceled", err)
	}
}

type fakeMonitoringHistoryStore struct {
	samples []controlstore.MetricSample
	since   time.Time
	err     error
}

func (f *fakeMonitoringHistoryStore) MetricSamples(_ context.Context, since time.Time) ([]controlstore.MetricSample, error) {
	f.since = since
	return f.samples, f.err
}
