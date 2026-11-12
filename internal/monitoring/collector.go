// Package monitoring 管理独立于浏览器请求的后台历史采集。
package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/Kkwans/nas-control-plane/internal/system"
)

type Source interface {
	CollectSystemSummary(context.Context) (system.Summary, error)
}
type Store interface {
	RecordMetricSample(context.Context, controlstore.MetricSample) error
	PruneMetricSamples(context.Context, time.Time) error
}
type Ticker interface {
	C() <-chan time.Time
	Stop()
}
type Clock interface {
	Now() time.Time
	NewTicker(time.Duration) Ticker
}
type realClock struct{}

func (realClock) Now() time.Time                   { return time.Now() }
func (realClock) NewTicker(d time.Duration) Ticker { return realTicker{time.NewTicker(d)} }

type realTicker struct{ *time.Ticker }

func (t realTicker) C() <-chan time.Time { return t.Ticker.C }

type Options struct {
	ResourceAlerts                                            bool
	Interval, SourceTimeout, StoreTimeout, MaxAge, FutureSkew time.Duration
	Clock                                                     Clock
	Logger                                                    *slog.Logger
}
type State struct {
	Summary                                                              *system.Summary
	LastAttemptAt, LastSuccessAt, LastPersistedAt                        time.Time
	LastErrorCode, PersistenceErrorCode, PruneErrorCode                  string
	SourceErrors, WriteErrors, PruneErrors, DroppedPending, SkippedTicks uint64
}
type Collector struct {
	source                                     Source
	store                                      Store
	options                                    Options
	mu                                         sync.RWMutex
	started                                    bool
	state                                      State
	alerts                                     AlertProcessor
	done                                       chan struct{}
	pending                                    *controlstore.MetricSample
	persistedBucket, lastSampleAt, lastPruneAt time.Time
}

func NewCollector(source Source, store Store, options Options) (*Collector, error) {
	if source == nil || store == nil {
		return nil, errors.New("monitoring source and store are required")
	}
	if options.Interval == 0 {
		options.Interval = 5 * time.Second
	}
	if options.SourceTimeout == 0 {
		options.SourceTimeout = 5 * time.Second
	}
	if options.StoreTimeout == 0 {
		options.StoreTimeout = 2 * time.Second
	}
	if options.MaxAge == 0 {
		options.MaxAge = 15 * time.Second
	}
	if options.FutureSkew == 0 {
		options.FutureSkew = 5 * time.Second
	}
	if options.Interval < 0 || options.SourceTimeout < 0 || options.StoreTimeout < 0 || options.MaxAge < 0 || options.FutureSkew < 0 {
		return nil, errors.New("monitoring durations must be positive")
	}
	if options.Clock == nil {
		options.Clock = realClock{}
	}
	return &Collector{source: source, store: store, options: options, alerts: NewAlertProcessor(options.ResourceAlerts, options.Clock.Now()), done: make(chan struct{})}, nil
}
func (c *Collector) Done() <-chan struct{} { return c.done }

// LatestAlerts 只读投影，不采样、不写入也不推进持续时间。
func (c *Collector) LatestAlerts(now time.Time) AlertsSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.alerts.Snapshot(now)
}
func (c *Collector) Latest() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	state := c.state
	if state.Summary != nil {
		s := cloneSummary(*state.Summary)
		state.Summary = &s
	}
	return state
}
func cloneSummary(s system.Summary) system.Summary {
	s.Sensors = slices.Clone(s.Sensors)
	s.Storage = slices.Clone(s.Storage)
	s.Network = slices.Clone(s.Network)
	s.Warnings = slices.Clone(s.Warnings)
	return s
}

// Run 只可启动一次；所有 RPC/SQL 同步执行，取消时不遗留后台调用。
func (c *Collector) Run(ctx context.Context) error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return errors.New("monitoring collector already started")
	}
	c.started = true
	c.mu.Unlock()
	defer close(c.done)
	ticker := c.options.Clock.NewTicker(c.options.Interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		c.step(ctx)
		// 慢调用错过的节拍直接丢弃，不并行补采或紧接着追赶。
	drain:
		for {
			select {
			case <-ticker.C():
				c.mu.Lock()
				c.state.SkippedTicks++
				c.mu.Unlock()
			default:
				break drain
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C():
		}
	}
}
func (c *Collector) change(fn func(*State)) {
	c.mu.Lock()
	before := c.state
	fn(&c.state)
	after := c.state
	c.mu.Unlock()
	if c.options.Logger != nil && (before.LastErrorCode != after.LastErrorCode || before.PersistenceErrorCode != after.PersistenceErrorCode || before.PruneErrorCode != after.PruneErrorCode) {
		c.options.Logger.Info("监控采集状态变化", "source_code", after.LastErrorCode, "persistence_code", after.PersistenceErrorCode, "prune_code", after.PruneErrorCode)
	}
}
func (c *Collector) step(ctx context.Context) {
	now := c.options.Clock.Now()
	if c.lastPruneAt.IsZero() || now.Sub(c.lastPruneAt) >= time.Minute || now.Before(c.lastPruneAt) {
		request, cancel := context.WithTimeout(ctx, c.options.StoreTimeout)
		err := c.store.PruneMetricSamples(request, now)
		cancel()
		c.lastPruneAt = now
		if c.options.Logger != nil {
			state := c.Latest()
			if state.SourceErrors+state.WriteErrors+state.PruneErrors+state.DroppedPending+state.SkippedTicks > 0 {
				c.options.Logger.Info("监控采集累计诊断", "source_errors", state.SourceErrors, "write_errors", state.WriteErrors, "prune_errors", state.PruneErrors, "dropped_pending", state.DroppedPending, "skipped_ticks", state.SkippedTicks)
			}
		}
		c.change(func(s *State) {
			s.PruneErrorCode = ""
			if err != nil {
				s.PruneErrorCode = "MONITORING_PRUNE_FAILED"
				s.PruneErrors++
			}
		})
	}
	if ctx.Err() != nil {
		return
	}
	c.change(func(s *State) { s.LastAttemptAt = now })
	request, cancel := context.WithTimeout(ctx, c.options.SourceTimeout)
	summary, err := c.source.CollectSystemSummary(request)
	if err == nil {
		err = request.Err()
	}
	cancel()
	received := c.options.Clock.Now()
	if ctx.Err() != nil {
		return
	}
	code := ""
	if err != nil {
		code = "SYSTEM_SUMMARY_UNAVAILABLE"
	} else {
		code = c.validateSummary(summary, received)
	}
	if code != "" {
		c.change(func(s *State) {
			c.alerts = c.alerts.Process(received, summary, code)
			s.LastErrorCode = code
			s.SourceErrors++
		})
	} else {
		c.lastSampleAt = summary.CollectedAt
		copy := cloneSummary(summary)
		c.change(func(s *State) {
			c.alerts = c.alerts.Process(received, summary, "")
			s.Summary = &copy
			s.LastSuccessAt = received
			s.LastErrorCode = ""
		})
	}
	// 待写缓存最多一个桶，过期后即使 RPC 失败也释放。
	bucket := received.UTC().Truncate(time.Minute)
	if c.pending != nil && c.pending.CollectedAt.UTC().Truncate(time.Minute).Before(bucket) {
		c.pending = nil
		c.change(func(s *State) { s.DroppedPending++ })
	}
	if code == "" {
		sample, invalid := historySample(summary)
		if invalid != "" {
			c.change(func(s *State) { s.PersistenceErrorCode = invalid })
		} else if c.pending == nil {
			if sample.CollectedAt.UTC().Truncate(time.Minute).After(c.persistedBucket) {
				c.pending = &sample
			} else {
				// 已成功持久化的桶无需再写。合格快照可解除候选校验错误；
				// 真正的写入失败仍须等一次成功写入才解除。
				c.change(func(s *State) {
					switch s.PersistenceErrorCode {
					case "MONITORING_SAMPLE_INCOMPLETE", "MONITORING_SAMPLE_INVALID", "MONITORING_SAMPLE_LIMIT_EXCEEDED":
						s.PersistenceErrorCode = ""
					}
				})
			}
		}
	}
	if c.pending == nil {
		return
	}
	request, cancel = context.WithTimeout(ctx, c.options.StoreTimeout)
	err = c.store.RecordMetricSample(request, *c.pending)
	cancel()
	if err != nil {
		c.change(func(s *State) { s.PersistenceErrorCode = "MONITORING_WRITE_FAILED"; s.WriteErrors++ })
		return
	}
	c.persistedBucket = c.pending.CollectedAt.UTC().Truncate(time.Minute)
	at := c.pending.CollectedAt
	c.pending = nil
	c.change(func(s *State) { s.LastPersistedAt = at; s.PersistenceErrorCode = "" })
}
func (c *Collector) validateSummary(s system.Summary, now time.Time) string {
	if s.CollectedAt.IsZero() || s.CollectedAt.After(now.Add(c.options.FutureSkew)) || now.Sub(s.CollectedAt) > c.options.MaxAge || (!c.lastSampleAt.IsZero() && !s.CollectedAt.After(c.lastSampleAt)) {
		return "MONITORING_INVALID_TIMESTAMP"
	}
	// JSON 编码同时拒绝任何浮点 NaN/Inf；整个快照受独立内存预算约束。
	data, err := json.Marshal(s)
	if err != nil {
		return "MONITORING_SAMPLE_INVALID"
	}
	if len(data) > 1<<20 {
		return "MONITORING_SAMPLE_LIMIT_EXCEEDED"
	}
	return ""
}
func historySample(s system.Summary) (controlstore.MetricSample, string) {
	invalid := func(code string) (controlstore.MetricSample, string) { return controlstore.MetricSample{}, code }
	for _, w := range s.Warnings {
		switch w.Source {
		case "cpu", "memory", "storage", "network", "disk-io":
			return invalid("MONITORING_SAMPLE_INCOMPLETE")
		}
	}
	if s.CPU.UsagePercent < 0 || s.CPU.UsagePercent > 100 || s.CPU.Load1 < 0 || s.Memory.TotalBytes == 0 || s.Memory.UsedBytes > s.Memory.TotalBytes || len(s.Storage) == 0 || len(s.Network) == 0 {
		return invalid("MONITORING_SAMPLE_INVALID")
	}
	var total, used, rx, tx uint64
	add := func(sum *uint64, v uint64) bool {
		if v > math.MaxInt64 || *sum > math.MaxInt64-v {
			return false
		}
		*sum += v
		return true
	}
	for _, d := range s.Storage {
		if d.UsedBytes > d.TotalBytes || !add(&total, d.TotalBytes) || !add(&used, d.UsedBytes) {
			return invalid("MONITORING_SAMPLE_INVALID")
		}
	}
	for _, n := range s.Network {
		if !add(&rx, n.ReceiveBytes) || !add(&tx, n.TransmitBytes) {
			return invalid("MONITORING_SAMPLE_INVALID")
		}
	}
	if total == 0 || s.DiskIO.ReadBytes > math.MaxInt64 || s.DiskIO.WriteBytes > math.MaxInt64 {
		return invalid("MONITORING_SAMPLE_INVALID")
	}
	names := map[string]bool{}
	temperatures := make([]controlstore.MetricTemperature, 0)
	for _, sensor := range s.Sensors {
		if strings.TrimSpace(sensor.Name) == "" || !utf8.ValidString(sensor.Name) {
			return invalid("MONITORING_SAMPLE_INVALID")
		}
		if len(sensor.Name) > controlstore.MaxMetricSensorNameBytes {
			return invalid("MONITORING_SAMPLE_LIMIT_EXCEEDED")
		}
		if names[sensor.Name] {
			continue
		}
		names[sensor.Name] = true
		if len(names) > controlstore.MaxMetricTemperatures {
			return invalid("MONITORING_SAMPLE_LIMIT_EXCEEDED")
		}
		temperatures = append(temperatures, controlstore.MetricTemperature{Name: sensor.Name, TemperatureCelsius: sensor.TemperatureCelsius})
	}
	sort.Slice(temperatures, func(i, j int) bool { return temperatures[i].Name < temperatures[j].Name })
	m := s.MetricSample()
	return controlstore.MetricSample{CollectedAt: m.CollectedAt, CPUPercent: m.CPUPercent, MemoryPercent: m.MemoryPercent, Load1: m.Load1, DiskPercent: m.DiskPercent, NetworkReceive: rx, NetworkTransmit: tx, DiskReadBytes: m.DiskReadBytes, DiskWriteBytes: m.DiskWriteBytes, Temperatures: temperatures}, ""
}
