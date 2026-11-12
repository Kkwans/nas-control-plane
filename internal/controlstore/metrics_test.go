package controlstore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func metricsTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "metrics.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func metricsCount(t *testing.T, s *Store, table string) int {
	t.Helper()
	var count int
	if err := s.database.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestMetricsAtomicFirstWins(t *testing.T) {
	s := metricsTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 13, 1, 2, 3, 0, time.UTC)
	first := MetricSample{CollectedAt: now, CPUPercent: 10, Temperatures: []MetricTemperature{{"z", 42}, {"a", 20}, {"z", 99}}}
	if err := s.RecordMetricSample(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := MetricSample{CollectedAt: now.Add(time.Second), CPUPercent: 99, Temperatures: []MetricTemperature{{"z", 90}, {"new", 70}}}
	if err := s.RecordMetricSample(ctx, second); err != nil {
		t.Fatal(err)
	}
	samples, err := s.MetricSamples(ctx, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 || samples[0].CPUPercent != 10 || !reflect.DeepEqual(samples[0].Temperatures, []MetricTemperature{{"a", 20}, {"z", 42}}) {
		t.Fatalf("mixed sample: %#v", samples)
	}
	if first.Temperatures[0].Name != "z" {
		t.Fatal("input mutated")
	}
}

func TestMetricsRollbackAndRetry(t *testing.T) {
	s := metricsTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 13, 1, 2, 0, 0, time.UTC)
	// 故障发生在部分温度已写入之后，整个样本必须回滚。
	if _, err := s.database.Exec(`CREATE TRIGGER fail_temperature BEFORE INSERT ON metric_temperature_samples
 WHEN NEW.sensor_name = 'z' BEGIN SELECT RAISE(ABORT, 'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	sample := MetricSample{CollectedAt: now, Temperatures: []MetricTemperature{{"a", 20}, {"z", 42}}}
	if err := s.RecordMetricSample(ctx, sample); err == nil {
		t.Fatal("expected injected error")
	}
	if metricsCount(t, s, "metric_samples") != 0 || metricsCount(t, s, "metric_temperature_samples") != 0 {
		t.Fatal("partial record survived rollback")
	}
	if _, err := s.database.Exec(`DROP TRIGGER fail_temperature`); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.RecordMetricSample(cancelled, sample); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error: %v", err)
	}
	if metricsCount(t, s, "metric_samples") != 0 {
		t.Fatal("cancelled record survived")
	}
	// 旧版同桶孤儿不能与重试后的完整记录混合。
	if _, err := s.database.Exec(`INSERT INTO metric_temperature_samples VALUES (?, 'orphan', 90)`, now.Unix()); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordMetricSample(ctx, sample); err != nil {
		t.Fatal(err)
	}
	if metricsCount(t, s, "metric_samples") != 1 || metricsCount(t, s, "metric_temperature_samples") != 2 {
		t.Fatal("retry did not converge")
	}
}

func TestMetricsValidationAndLimits(t *testing.T) {
	s := metricsTestStore(t)
	now := time.Date(2026, 10, 13, 1, 2, 0, 0, time.UTC)
	for _, sample := range []MetricSample{
		{}, {CollectedAt: now, CPUPercent: math.NaN()}, {CollectedAt: now, MemoryPercent: math.Inf(1)},
		{CollectedAt: now, NetworkReceive: math.MaxUint64},
		{CollectedAt: now, Temperatures: []MetricTemperature{{"x", math.Inf(-1)}}},
		{CollectedAt: now, Temperatures: []MetricTemperature{{string([]byte{0xff}), 20}}},
	} {
		if err := s.RecordMetricSample(context.Background(), sample); !errors.Is(err, ErrInvalidMetricSample) {
			t.Fatalf("invalid error: %v", err)
		}
	}
	for _, n := range []int{MaxMetricTemperatures, MaxMetricTemperatures + 1} {
		sample := MetricSample{CollectedAt: now}
		for i := 0; i < n; i++ {
			sample.Temperatures = append(sample.Temperatures, MetricTemperature{fmt.Sprintf("sensor-%03d", i), 20})
		}
		err := s.RecordMetricSample(context.Background(), sample)
		if n == MaxMetricTemperatures && err != nil {
			t.Fatal(err)
		}
		if n > MaxMetricTemperatures && !errors.Is(err, ErrMetricSampleLimitExceeded) {
			t.Fatalf("limit error: %v", err)
		}
	}
	for _, n := range []int{MaxMetricSensorNameBytes, MaxMetricSensorNameBytes + 1} {
		err := s.RecordMetricSample(context.Background(), MetricSample{CollectedAt: now.Add(time.Minute), Temperatures: []MetricTemperature{{strings.Repeat("x", n), 20}}})
		if n == MaxMetricSensorNameBytes && err != nil {
			t.Fatal(err)
		}
		if n > MaxMetricSensorNameBytes && !errors.Is(err, ErrMetricSampleLimitExceeded) {
			t.Fatalf("name limit error: %v", err)
		}
	}
}

func TestMetricsPruneIndependentAtomicAndLegacyCompatible(t *testing.T) {
	s := metricsTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 13, 1, 2, 59, 0, time.UTC)
	cutoff := now.Truncate(time.Minute).Add(-MetricRetention)
	for _, at := range []time.Time{cutoff.Add(-time.Minute), cutoff, now, now.Add(24 * time.Hour)} {
		if err := s.RecordMetricSample(ctx, MetricSample{CollectedAt: at, Temperatures: []MetricTemperature{{"cpu", 20}}}); err != nil {
			t.Fatal(err)
		}
	}
	// 旧版超限数据保持完整，且清理不需要新的 source 写入。
	for i := 0; i < MaxMetricTemperatures; i++ {
		if _, err := s.database.Exec(`INSERT INTO metric_temperature_samples VALUES (?, ?, 30)`, cutoff.Unix(), fmt.Sprintf("legacy-%03d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.database.Exec(`INSERT INTO metric_temperature_samples VALUES (?, 'orphan', 30)`, now.Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.database.Exec(`CREATE TRIGGER fail_prune BEFORE DELETE ON metric_temperature_samples BEGIN SELECT RAISE(ABORT, 'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneMetricSamples(ctx, now); err == nil {
		t.Fatal("expected prune failure")
	}
	if metricsCount(t, s, "metric_samples") != 4 {
		t.Fatal("prune failed to rollback parents")
	}
	if _, err := s.database.Exec(`DROP TRIGGER fail_prune`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.PruneMetricSamples(ctx, now); err != nil {
			t.Fatal(err)
		}
	}
	if metricsCount(t, s, "metric_samples") != 3 || metricsCount(t, s, "metric_temperature_samples") != MaxMetricTemperatures+3 {
		t.Fatal("retention boundary or legacy preservation failed")
	}
	if err := s.PruneMetricSamples(ctx, now.Add(9*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if metricsCount(t, s, "metric_samples") != 0 || metricsCount(t, s, "metric_temperature_samples") != 0 {
		t.Fatal("source-free aging failed")
	}
}

func TestMetricsRetentionMinuteBudget(t *testing.T) {
	s := metricsTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 13, 1, 2, 59, 0, time.UTC)
	// 仅临时 SQLite 大 fixture：含边界的七天为 10,081 个合法分钟桶。
	_, err := s.database.Exec(`WITH RECURSIVE buckets(n) AS (SELECT 0 UNION ALL SELECT n+1 FROM buckets WHERE n < ?)
 INSERT INTO metric_samples SELECT ?-n*60,0,0,0,0,0,0,0,0 FROM buckets`, MaxMetricSamples+10, now.Truncate(time.Minute).Unix())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PruneMetricSamples(ctx, now); err != nil {
		t.Fatal(err)
	}
	if got := metricsCount(t, s, "metric_samples"); got != MaxMetricSamples {
		t.Fatalf("got %d buckets", got)
	}
}

func TestMetricsConcurrentFirstWins(t *testing.T) {
	s := metricsTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 13, 1, 2, 0, 0, time.UTC)
	var workers sync.WaitGroup
	for i := 0; i < 32; i++ {
		workers.Add(1)
		go func(value int) {
			defer workers.Done()
			sample := MetricSample{CollectedAt: now.Add(time.Duration(value) * time.Second), CPUPercent: float64(value), Temperatures: []MetricTemperature{{"cpu", float64(value)}}}
			if err := s.RecordMetricSample(ctx, sample); err != nil {
				t.Error(err)
			}
		}(i)
	}
	workers.Wait()
	samples, err := s.MetricSamples(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 || len(samples[0].Temperatures) != 1 || samples[0].CPUPercent != samples[0].Temperatures[0].TemperatureCelsius {
		t.Fatalf("concurrent mixed bucket: %#v", samples)
	}
}
