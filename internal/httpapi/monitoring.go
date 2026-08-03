package httpapi

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
)

const (
	DefaultMonitoringRange = 6 * time.Hour
	MonitoringRetention    = 7 * 24 * time.Hour
)

var (
	ErrMonitoringRangeInvalid     = errors.New("monitoring range is invalid")
	ErrMonitoringStoreUnavailable = errors.New("monitoring store is unavailable")
)

// MonitoringRange 是历史监控查询的闭区间。
type MonitoringRange struct {
	From time.Time
	To   time.Time
}

// MonitoringHistoryStore 是 controlstore 历史查询所需的最小接口。
// 扩展 MetricSample 的字段不会改变该查询接口。
type MonitoringHistoryStore interface {
	MetricSamples(context.Context, time.Time) ([]controlstore.MetricSample, error)
}

// ParseMonitoringRange 保持现有 monitoring/samples 的范围语义：默认查询 6 小时，
// 支持 1h/6h/24h/7d，精确 from/to 必须成对提供且最长 7 天。
func ParseMonitoringRange(values url.Values, now time.Time) (MonitoringRange, error) {
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()

	result := MonitoringRange{From: now.Add(-DefaultMonitoringRange), To: now}
	if values.Has("from") || values.Has("to") {
		if !values.Has("from") || !values.Has("to") {
			return MonitoringRange{}, ErrMonitoringRangeInvalid
		}
		from, fromErr := time.Parse(time.RFC3339, values.Get("from"))
		to, toErr := time.Parse(time.RFC3339, values.Get("to"))
		if fromErr != nil || toErr != nil {
			return MonitoringRange{}, ErrMonitoringRangeInvalid
		}
		result = MonitoringRange{From: from.UTC(), To: to.UTC()}
	} else if duration, ok := monitoringRanges[values.Get("range")]; ok {
		result.From = now.Add(-duration)
	}

	if !result.From.Before(result.To) || result.To.After(now.Add(time.Minute)) || result.To.Sub(result.From) > MonitoringRetention {
		return MonitoringRange{}, ErrMonitoringRangeInvalid
	}
	return result, nil
}

var monitoringRanges = map[string]time.Duration{
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"24h": 24 * time.Hour,
	"7d":  MonitoringRetention,
}

// QueryMonitoringSamples reads and bounds persisted samples without changing their
// one-minute bucket semantics. The store remains responsible for retention cleanup.
func QueryMonitoringSamples(ctx context.Context, store MonitoringHistoryStore, historyRange MonitoringRange) ([]controlstore.MetricSample, error) {
	if ctx == nil {
		return nil, ErrMonitoringRangeInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, ErrMonitoringStoreUnavailable
	}
	if !historyRange.From.Before(historyRange.To) || historyRange.To.Sub(historyRange.From) > MonitoringRetention {
		return nil, ErrMonitoringRangeInvalid
	}

	samples, err := store.MetricSamples(ctx, historyRange.From)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := make([]controlstore.MetricSample, 0, len(samples))
	for _, sample := range samples {
		collectedAt := sample.CollectedAt.UTC()
		if collectedAt.Before(historyRange.From) || collectedAt.After(historyRange.To) {
			continue
		}
		result = append(result, sample)
	}
	return result, nil
}

// Compatibility aliases keep the helper discoverable alongside the existing
// controlstore.MetricSample naming.
type MetricHistoryRange = MonitoringRange
type MetricHistoryStore = MonitoringHistoryStore

var ErrMetricHistoryRangeInvalid = ErrMonitoringRangeInvalid

func ParseMetricHistoryRange(values url.Values, now time.Time) (MetricHistoryRange, error) {
	return ParseMonitoringRange(values, now)
}

func QueryMetricHistory(ctx context.Context, store MetricHistoryStore, historyRange MetricHistoryRange) ([]controlstore.MetricSample, error) {
	return QueryMonitoringSamples(ctx, store, historyRange)
}
