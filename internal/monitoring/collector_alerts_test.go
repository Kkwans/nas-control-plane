package monitoring

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/Kkwans/nas-control-plane/internal/system"
)

type alertFailureStore struct {
	reads, writes, prunes int
	beforeWrite           func()
}

func (s *alertFailureStore) RecordMetricSample(context.Context, controlstore.MetricSample) error {
	s.writes++
	if s.beforeWrite != nil {
		s.beforeWrite()
	}
	return errors.New("fixture write failure")
}
func (s *alertFailureStore) PruneMetricSamples(context.Context, time.Time) error {
	s.prunes++
	return errors.New("fixture prune failure")
}

func TestResourceAlertsCollectorSQLIsolationAndReadOnly(t *testing.T) {
	clock, store := newClock(), &alertFailureStore{}
	calls := 0
	c, err := NewCollector(sourceFunc(func(context.Context) (system.Summary, error) {
		calls++
		s := validSummary(clock.Now())
		s.CPU.UsagePercent = 95
		return s, nil
	}), store, Options{Clock: clock, ResourceAlerts: true})
	if err != nil {
		t.Fatal(err)
	}
	store.beforeWrite = func() {
		latest, alerts := c.Latest(), c.LatestAlerts(clock.Now())
		if latest.Summary == nil || alerts.Rules.CPU.SampleAt == nil || !latest.Summary.CollectedAt.Equal(*alerts.Rules.CPU.SampleAt) {
			t.Fatal("summary and alerts not published before SQL")
		}
	}
	for i := 0; i <= 24; i++ {
		c.step(context.Background())
		if i < 24 {
			clock.advance(5 * time.Second)
		}
	}
	if got := c.LatestAlerts(clock.Now()); got.Rules.CPU.Condition != "firing" || got.Rules.Memory.Availability != "fresh" {
		t.Fatalf("SQL suppressed alerts: %+v", got)
	}
	beforeCalls, beforeWrites, beforePrunes := calls, store.writes, store.prunes
	for i := 0; i < 100; i++ {
		snap := c.LatestAlerts(clock.Now().Add(time.Hour))
		*snap.Rules.CPU.Value = 0
		*snap.Rules.CPU.SampleAt = time.Time{}
	}
	if calls != beforeCalls || store.writes != beforeWrites || store.prunes != beforePrunes || *c.LatestAlerts(clock.Now()).Rules.CPU.Value != 95 {
		t.Fatal("read side effect or alias")
	}
	if c.Latest().PersistenceErrorCode != "MONITORING_WRITE_FAILED" || c.Latest().PruneErrorCode != "MONITORING_PRUNE_FAILED" {
		t.Fatal("SQL diagnostics hidden")
	}
}

func TestResourceAlertsCollectorFailuresAndDisabled(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		clock, store := newClock(), &fakeStore{}
		fail := false
		c, err := NewCollector(sourceFunc(func(context.Context) (system.Summary, error) {
			if fail {
				return system.Summary{}, errors.New("fixture offline")
			}
			return validSummary(clock.Now()), nil
		}), store, Options{Clock: clock, ResourceAlerts: enabled})
		if err != nil {
			t.Fatal(err)
		}
		c.step(context.Background())
		fail = true
		clock.advance(5 * time.Second)
		c.step(context.Background())
		got := c.LatestAlerts(clock.Now())
		if got.Enabled != enabled || got.Rules.CPU.Availability != "unknown" {
			t.Fatalf("bad state %+v", got)
		}
		if enabled && got.Rules.CPU.ReasonCode != "SYSTEM_SUMMARY_UNAVAILABLE" {
			t.Fatal(got.Rules.CPU.ReasonCode)
		}
	}
}

func TestResourceAlertsCollectorConcurrentSnapshots(t *testing.T) {
	clock := newClock()
	c, err := NewCollector(sourceFunc(func(context.Context) (system.Summary, error) { return validSummary(clock.Now()), nil }), &fakeStore{}, Options{Clock: clock, ResourceAlerts: true})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = c.LatestAlerts(clock.Now())
			_ = c.Latest()
		}
	}()
	for i := 0; i < 100; i++ {
		c.step(context.Background())
		clock.advance(5 * time.Second)
	}
	wg.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Run(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("owner did not stop")
	}
}
