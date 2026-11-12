package monitoring

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/Kkwans/nas-control-plane/internal/system"
)

type fakeClock struct {
	mu   sync.Mutex
	now  time.Time
	tick chan time.Time
}

func newClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC), tick: make(chan time.Time, 1)}
}
func (f *fakeClock) Now() time.Time                 { f.mu.Lock(); defer f.mu.Unlock(); return f.now }
func (f *fakeClock) advance(d time.Duration)        { f.mu.Lock(); f.now = f.now.Add(d); f.mu.Unlock() }
func (f *fakeClock) NewTicker(time.Duration) Ticker { return fakeTicker{f.tick} }

type fakeTicker struct{ ch chan time.Time }

func (f fakeTicker) C() <-chan time.Time { return f.ch }
func (fakeTicker) Stop()                 {}

type sourceFunc func(context.Context) (system.Summary, error)

func (f sourceFunc) CollectSystemSummary(ctx context.Context) (system.Summary, error) { return f(ctx) }

type fakeStore struct {
	samples  []controlstore.MetricSample
	attempts []controlstore.MetricSample
	prunes   []time.Time
	fail     bool
	write    func(context.Context) error
}

func (s *fakeStore) RecordMetricSample(ctx context.Context, m controlstore.MetricSample) error {
	s.attempts = append(s.attempts, m)
	if s.write != nil {
		return s.write(ctx)
	}
	if s.fail {
		return errors.New("fixture failure")
	}
	s.samples = append(s.samples, m)
	return nil
}
func (s *fakeStore) PruneMetricSamples(_ context.Context, now time.Time) error {
	s.prunes = append(s.prunes, now)
	return nil
}
func validSummary(at time.Time) system.Summary {
	return system.Summary{CollectedAt: at, CPU: system.CPUStats{UsagePercent: 20, Load1: 1}, Memory: system.MemoryStats{TotalBytes: 100, UsedBytes: 25}, Storage: []system.DiskStats{{TotalBytes: 100, UsedBytes: 30}}, Network: []system.NetworkStats{{Name: "fixture", ReceiveBytes: 10}}, Sensors: []system.TemperatureProbe{{Name: "cpu", TemperatureCelsius: 42}}}
}
func fixture(t *testing.T, clock *fakeClock, store *fakeStore, source Source) *Collector {
	t.Helper()
	c, err := NewCollector(source, store, Options{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestMetricsIndependentThreeMinutes(t *testing.T) {
	clock, store := newClock(), &fakeStore{}
	calls := 0
	c := fixture(t, clock, store, sourceFunc(func(context.Context) (system.Summary, error) { calls++; return validSummary(clock.Now()), nil }))
	for i := 0; i <= 36; i++ {
		c.step(context.Background())
		clock.advance(5 * time.Second)
	}
	if calls != 37 || len(store.samples) != 4 || len(store.prunes) != 4 {
		t.Fatalf("calls=%d samples=%d prunes=%d", calls, len(store.samples), len(store.prunes))
	}
}
func TestMetricsInvalidAndIncompleteNeverWrite(t *testing.T) {
	cases := map[string]func(*system.Summary){
		"zero timestamp":   func(s *system.Summary) { s.CollectedAt = time.Time{} },
		"future timestamp": func(s *system.Summary) { s.CollectedAt = s.CollectedAt.Add(6 * time.Second) },
		"old timestamp":    func(s *system.Summary) { s.CollectedAt = s.CollectedAt.Add(-time.Minute) },
		"nan":              func(s *system.Summary) { s.CPU.UsagePercent = math.NaN() },
		"inf load5":        func(s *system.Summary) { s.CPU.Load5 = math.Inf(1) },
		"empty memory":     func(s *system.Summary) { s.Memory.TotalBytes = 0 },
		"empty disks":      func(s *system.Summary) { s.Storage = nil },
		"overflow":         func(s *system.Summary) { s.Network[0].ReceiveBytes = math.MaxUint64 },
		"sensors": func(s *system.Summary) {
			for i := 0; i < 129; i++ {
				s.Sensors = append(s.Sensors, system.TemperatureProbe{Name: fmt.Sprint(i)})
			}
		},
	}
	for _, source := range []string{"cpu", "memory", "storage", "network", "disk-io"} {
		cases[source] = func(s *system.Summary) { s.Warnings = []system.SummaryWarning{{Source: source}} }
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			clock, store := newClock(), &fakeStore{}
			summary := validSummary(clock.Now())
			mutate(&summary)
			c := fixture(t, clock, store, sourceFunc(func(context.Context) (system.Summary, error) { return summary, nil }))
			c.step(context.Background())
			if len(store.samples) != 0 {
				t.Fatal("invalid sample persisted")
			}
			s := c.Latest()
			if s.LastErrorCode == "" && s.PersistenceErrorCode == "" {
				t.Fatal("missing diagnostic")
			}
		})
	}
}
func TestMetricsOptionalWarningsAndClone(t *testing.T) {
	clock, store := newClock(), &fakeStore{}
	summary := validSummary(clock.Now())
	summary.Sensors = nil
	summary.Warnings = []system.SummaryWarning{{Source: "host"}, {Source: "temperature"}}
	c := fixture(t, clock, store, sourceFunc(func(context.Context) (system.Summary, error) { return summary, nil }))
	c.step(context.Background())
	if len(store.samples) != 1 || len(store.samples[0].Temperatures) != 0 {
		t.Fatal("temperature absence must remain absent")
	}
	latest := c.Latest()
	latest.Summary.Storage[0].UsedBytes = 99
	latest.Summary.Network[0].Name = "changed"
	latest.Summary.Warnings[0].Source = "changed"
	summary.Storage[0].UsedBytes = 88
	if c.Latest().Summary.Storage[0].UsedBytes != 30 || c.Latest().Summary.Network[0].Name != "fixture" || c.Latest().Summary.Warnings[0].Source != "host" {
		t.Fatal("snapshot alias")
	}
}
func TestMetricsPendingRetryAndRollover(t *testing.T) {
	clock, store := newClock(), &fakeStore{fail: true}
	c := fixture(t, clock, store, sourceFunc(func(context.Context) (system.Summary, error) {
		s := validSummary(clock.Now())
		s.CPU.UsagePercent = float64(clock.Now().Second())
		return s, nil
	}))
	c.step(context.Background())
	clock.advance(5 * time.Second)
	c.step(context.Background())
	if !reflect.DeepEqual(store.attempts[0], store.attempts[1]) {
		t.Fatal("retry replaced first candidate")
	}
	clock.advance(time.Minute)
	c.step(context.Background())
	if c.Latest().DroppedPending != 1 {
		t.Fatal("pending not bounded")
	}
	store.fail = false
	clock.advance(5 * time.Second)
	c.step(context.Background())
	if len(store.samples) != 1 || store.samples[0].CPUPercent != 5 {
		t.Fatal("did not retry new minute candidate")
	}
	if c.Latest().PersistenceErrorCode != "" {
		t.Fatal("write recovery missing")
	}
}
func TestMetricsPrunesDuringEightDaySourceFailure(t *testing.T) {
	clock, store := newClock(), &fakeStore{}
	c := fixture(t, clock, store, sourceFunc(func(context.Context) (system.Summary, error) { return system.Summary{}, errors.New("offline") }))
	c.step(context.Background())
	clock.advance(8 * 24 * time.Hour)
	c.step(context.Background())
	if len(store.prunes) != 2 || len(store.samples) != 0 || c.Latest().SourceErrors != 2 {
		t.Fatal("prune depends on healthy RPC")
	}
}
func TestMetricsBackwardTimestamp(t *testing.T) {
	clock, store := newClock(), &fakeStore{}
	at := clock.Now()
	c := fixture(t, clock, store, sourceFunc(func(context.Context) (system.Summary, error) { return validSummary(at), nil }))
	c.step(context.Background())
	clock.advance(5 * time.Second)
	c.step(context.Background())
	if c.Latest().LastErrorCode != "MONITORING_INVALID_TIMESTAMP" {
		t.Fatal("stale sample accepted")
	}
}
func TestMetricsCancellationRunAndConcurrentLatest(t *testing.T) {
	for _, blockStore := range []bool{false, true} {
		t.Run(fmt.Sprint(blockStore), func(t *testing.T) {
			clock, store := newClock(), &fakeStore{}
			entered := make(chan struct{})
			source := sourceFunc(func(ctx context.Context) (system.Summary, error) {
				if !blockStore {
					close(entered)
					<-ctx.Done()
					return system.Summary{}, ctx.Err()
				}
				return validSummary(clock.Now()), nil
			})
			if blockStore {
				store.write = func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() }
			}
			c := fixture(t, clock, store, source)
			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() { result <- c.Run(ctx) }()
			<-entered
			var wg sync.WaitGroup
			for i := 0; i < 10; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for j := 0; j < 100; j++ {
						_ = c.Latest()
					}
				}()
			}
			cancel()
			select {
			case <-c.Done():
			case <-time.After(time.Second):
				t.Fatal("cancellation stuck")
			}
			wg.Wait()
			if err := <-result; err != nil {
				t.Fatal(err)
			}
			if c.Run(ctx) == nil {
				t.Fatal("duplicate Run accepted")
			}
		})
	}
}
func TestMetricsRunClockNoHTTP(t *testing.T) {
	clock, store := newClock(), &fakeStore{}
	c := fixture(t, clock, store, sourceFunc(func(context.Context) (system.Summary, error) { return validSummary(clock.Now()), nil }))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	wait := func(at time.Time) {
		deadline := time.After(time.Second)
		for {
			if c.Latest().LastPersistedAt.Equal(at) {
				return
			}
			select {
			case <-deadline:
				t.Fatal("tick did not persist")
			case <-time.After(time.Millisecond):
			}
		}
	}
	wait(clock.Now())
	for i := 0; i < 3; i++ {
		clock.advance(time.Minute)
		clock.tick <- clock.Now()
		wait(clock.Now())
	}
	cancel()
	<-c.Done()
	if len(store.samples) != 4 {
		t.Fatal("browser-independent run failed")
	}
}

func TestMetricsSourceDeadlineAndSensorOwnership(t *testing.T) {
	clock, store := newClock(), &fakeStore{}
	source := sourceFunc(func(ctx context.Context) (system.Summary, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("missing source deadline")
		}
		<-ctx.Done()
		return system.Summary{}, ctx.Err()
	})
	c, err := NewCollector(source, store, Options{Clock: clock, SourceTimeout: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	c.step(context.Background())
	if c.Latest().LastErrorCode != "SYSTEM_SUMMARY_UNAVAILABLE" {
		t.Fatal("timeout not reported")
	}
	summary := validSummary(clock.Now())
	summary.Sensors = []system.TemperatureProbe{{Name: "z", TemperatureCelsius: 42}, {Name: "a", TemperatureCelsius: 10}, {Name: "z", TemperatureCelsius: 99}}
	c = fixture(t, clock, store, sourceFunc(func(context.Context) (system.Summary, error) { return summary, nil }))
	c.step(context.Background())
	if len(store.samples) != 1 || len(store.samples[0].Temperatures) != 2 || store.samples[0].Temperatures[0].Name != "a" || store.samples[0].Temperatures[1].TemperatureCelsius != 42 {
		t.Fatal("sensor normalization changed first winner")
	}
	summary.Sensors[0].TemperatureCelsius = 100
	latest := c.Latest()
	latest.Summary.Sensors[0].TemperatureCelsius = 101
	if c.Latest().Summary.Sensors[0].TemperatureCelsius != 42 {
		t.Fatal("sensor snapshot aliased")
	}
}

func TestMetricsHealthyPersistedMinuteClearsCandidateError(t *testing.T) {
	clock, store := newClock(), &fakeStore{}
	incomplete := false
	c := fixture(t, clock, store, sourceFunc(func(context.Context) (system.Summary, error) {
		summary := validSummary(clock.Now())
		if incomplete {
			summary.Warnings = []system.SummaryWarning{{Source: "memory"}}
		}
		return summary, nil
	}))
	c.step(context.Background())
	if len(store.samples) != 1 {
		t.Fatal("initial sample was not persisted")
	}
	incomplete = true
	clock.advance(5 * time.Second)
	c.step(context.Background())
	if c.Latest().PersistenceErrorCode != "MONITORING_SAMPLE_INCOMPLETE" {
		t.Fatal("candidate error missing")
	}
	incomplete = false
	clock.advance(5 * time.Second)
	c.step(context.Background())
	if c.Latest().PersistenceErrorCode != "" {
		t.Fatal("healthy already-persisted minute retained candidate error")
	}
	if len(store.attempts) != 1 {
		t.Fatal("healthy duplicate minute attempted another write")
	}
}

func TestMetricsHealthyCandidateDoesNotHidePendingWriteFailure(t *testing.T) {
	clock, store := newClock(), &fakeStore{fail: true}
	c := fixture(t, clock, store, sourceFunc(func(context.Context) (system.Summary, error) { return validSummary(clock.Now()), nil }))
	c.step(context.Background())
	clock.advance(5 * time.Second)
	c.step(context.Background())
	if c.Latest().PersistenceErrorCode != "MONITORING_WRITE_FAILED" || c.pending == nil {
		t.Fatal("healthy candidate hid failed pending write")
	}
	if len(store.attempts) != 2 || !reflect.DeepEqual(store.attempts[0], store.attempts[1]) {
		t.Fatal("failed candidate was not retried unchanged")
	}
	store.fail = false
	clock.advance(5 * time.Second)
	c.step(context.Background())
	if c.Latest().PersistenceErrorCode != "" {
		t.Fatal("successful retry did not clear write error")
	}
}
