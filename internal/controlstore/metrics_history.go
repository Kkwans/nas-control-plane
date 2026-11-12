package controlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"
	"unicode/utf8"
)

const (
	// 单次历史响应预算独立于写入及七天保留策略。七天每分钟六个温度通常可读，
	// 更多传感器、较长名称或较大数字可能需缩短范围；超限不返回部分结果。
	MaxMetricHistoryTemperatures  = 65_536
	MaxMetricHistoryResponseBytes = 8 << 20 // 完整 JSON 数组及末尾换行。
)

var (
	ErrMetricHistoryLimitExceeded = errors.New("MONITORING_HISTORY_LIMIT_EXCEEDED")
	ErrInvalidMetricHistory       = errors.New("MONITORING_HISTORY_INVALID")
)

// MetricSamplesInRange 在同一个只读事务中读取闭区间内的父样本及其温度。
// 预算限制应用层保留的数据和编码体积，不保证 SQLite 驱动或查询工作区的峰值内存。
// 仍在保留期的旧版超限数据不会被修改；无法完整返回时显式报错。
func (s *Store) MetricSamplesInRange(ctx context.Context, from, to time.Time) ([]MetricSample, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if from.After(to) {
		return nil, ErrInvalidMetricHistory
	}
	lower, upper := from.Unix(), to.Unix()
	// 库中时间为整数秒；下界向上取整，上界向下取整，绝不扩大精确范围。
	if from.Nanosecond() != 0 {
		if lower == math.MaxInt64 {
			return []MetricSample{}, nil
		}
		lower++
	}
	if lower > upper {
		return []MetricSample{}, nil
	}

	tx, err := s.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// SQLite 的列亲和性允许旧数据存入任意文本；先检查类型，避免在 Scan 失败前加载巨大异常字段。
	rows, err := tx.QueryContext(ctx, `SELECT collected_at_unix,
 CASE WHEN typeof(cpu_percent) IN ('integer', 'real') THEN cpu_percent END,
 CASE WHEN typeof(memory_percent) IN ('integer', 'real') THEN memory_percent END,
 CASE WHEN typeof(load1) IN ('integer', 'real') THEN load1 END,
 CASE WHEN typeof(disk_percent) IN ('integer', 'real') THEN disk_percent END,
 CASE WHEN typeof(network_receive_bytes) = 'integer' THEN network_receive_bytes END,
 CASE WHEN typeof(network_transmit_bytes) = 'integer' THEN network_transmit_bytes END,
 CASE WHEN typeof(disk_read_bytes) = 'integer' THEN disk_read_bytes END,
 CASE WHEN typeof(disk_write_bytes) = 'integer' THEN disk_write_bytes END
 FROM metric_samples WHERE collected_at_unix >= ? AND collected_at_unix <= ?
 ORDER BY collected_at_unix LIMIT ?`, lower, upper, MaxMetricSamples+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	samples := make([]MetricSample, 0)
	byBucket := make(map[int64]int)
	budget := newMetricHistoryBudget()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(samples) == MaxMetricSamples {
			return nil, ErrMetricHistoryLimitExceeded
		}
		var sample MetricSample
		var bucket int64
		if err := rows.Scan(&bucket, &sample.CPUPercent, &sample.MemoryPercent, &sample.Load1, &sample.DiskPercent,
			&sample.NetworkReceive, &sample.NetworkTransmit, &sample.DiskReadBytes, &sample.DiskWriteBytes); err != nil {
			return nil, metricHistoryScanError(ctx)
		}
		sample.CollectedAt = time.Unix(bucket, 0).UTC()
		if err := budget.addSample(sample); err != nil {
			return nil, err
		}
		byBucket[bucket] = len(samples)
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Open 只提供一个 SQL 连接；必须在子查询前关闭父 rows，且使用同一 tx。
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(samples) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return samples, nil
	}

	// 相同快照、相同父选择：只加载已选父桶，排除孤儿及区间外温度。
	// 巨大旧名称先在 SQL 中检查字节数，避免将任意长字段载入应用；
	// BLOB 长度包含 NUL 字节，不使用 SQLite 的字符长度。
	temperatureRows, err := tx.QueryContext(ctx, `WITH selected_parents AS (
 SELECT collected_at_unix FROM metric_samples
 WHERE collected_at_unix >= ? AND collected_at_unix <= ?
 ORDER BY collected_at_unix LIMIT ?
 )
 SELECT t.collected_at_unix, length(CAST(t.sensor_name AS BLOB)),
 CASE WHEN length(CAST(t.sensor_name AS BLOB)) <= ? THEN t.sensor_name ELSE NULL END,
 CASE WHEN typeof(t.temperature_celsius) IN ('integer', 'real') THEN t.temperature_celsius END
 FROM selected_parents p JOIN metric_temperature_samples t ON t.collected_at_unix = p.collected_at_unix
 ORDER BY t.collected_at_unix, t.sensor_name LIMIT ?`,
		lower, upper, MaxMetricSamples, MaxMetricHistoryResponseBytes, MaxMetricHistoryTemperatures+1)
	if err != nil {
		return nil, err
	}
	defer temperatureRows.Close()
	for temperatureRows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if budget.temperatures == MaxMetricHistoryTemperatures {
			return nil, ErrMetricHistoryLimitExceeded
		}
		var bucket, nameBytes int64
		var name sql.NullString
		var temperature MetricTemperature
		if err := temperatureRows.Scan(&bucket, &nameBytes, &name, &temperature.TemperatureCelsius); err != nil {
			return nil, metricHistoryScanError(ctx)
		}
		if nameBytes > MaxMetricHistoryResponseBytes {
			return nil, ErrMetricHistoryLimitExceeded
		}
		if !name.Valid {
			return nil, ErrInvalidMetricHistory
		}
		temperature.Name = name.String
		index, ok := byBucket[bucket]
		if !ok {
			return nil, ErrInvalidMetricHistory
		}
		if err := budget.addTemperature(temperature, len(samples[index].Temperatures) == 0); err != nil {
			return nil, err
		}
		samples[index].Temperatures = append(samples[index].Temperatures, temperature)
	}
	if err := temperatureRows.Err(); err != nil {
		return nil, err
	}
	if err := temperatureRows.Close(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return samples, nil
}

func metricHistoryScanError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// 不将含历史字段内容的 Scan 错误泄露到 API。
	return ErrInvalidMetricHistory
}

// MarshalMetricHistory 在写 HTTP 200 前完成验证及有界编码，避免中途截断成功响应。
// 先计数再 Marshal，使 json.Encoder 的内部缓冲也受完整响应预算约束。
func MarshalMetricHistory(ctx context.Context, samples []MetricSample) ([]byte, error) {
	budget := newMetricHistoryBudget()
	for _, sample := range samples {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := budget.addSample(sample); err != nil {
			return nil, err
		}
		for index, temperature := range sample.Temperatures {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := budget.addTemperature(temperature, index == 0); err != nil {
				return nil, err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if samples == nil {
		samples = []MetricSample{}
	}
	encoded, err := json.Marshal(samples)
	if err != nil {
		return nil, ErrInvalidMetricHistory
	}
	if len(encoded)+1 > MaxMetricHistoryResponseBytes {
		return nil, ErrMetricHistoryLimitExceeded
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

type metricHistoryBudget struct {
	bytes        int
	samples      int
	temperatures int
}

func newMetricHistoryBudget() metricHistoryBudget {
	return metricHistoryBudget{bytes: len("[]\n")}
}

func (b *metricHistoryBudget) addSample(sample MetricSample) error {
	if b.samples == MaxMetricSamples {
		return ErrMetricHistoryLimitExceeded
	}
	// 温度单独逐行计费，不能先编码任意大的温度集合。
	sample.Temperatures = nil
	encoded, err := json.Marshal(sample)
	if err != nil {
		return ErrInvalidMetricHistory
	}
	size := len(encoded)
	if b.samples > 0 {
		size++ // 数组逗号。
	}
	if err := b.addBytes(size); err != nil {
		return err
	}
	b.samples++
	return nil
}

func (b *metricHistoryBudget) addTemperature(temperature MetricTemperature, first bool) error {
	if b.temperatures == MaxMetricHistoryTemperatures || len(temperature.Name) > MaxMetricHistoryResponseBytes {
		return ErrMetricHistoryLimitExceeded
	}
	if !utf8.ValidString(temperature.Name) {
		return ErrInvalidMetricHistory
	}
	name := temperature.Name
	temperature.Name = ""
	encoded, err := json.Marshal(temperature)
	if err != nil {
		return ErrInvalidMetricHistory
	}
	size := len(encoded) + metricHistoryEscapedNameBytes(name)
	if first {
		size += len(`,"temperatures":[]`) // 原样本中 omitempty 省略的字段。
	} else {
		size++
	}
	if err := b.addBytes(size); err != nil {
		return err
	}
	b.temperatures++
	return nil
}

func (b *metricHistoryBudget) addBytes(size int) error {
	if size > MaxMetricHistoryResponseBytes-b.bytes {
		return ErrMetricHistoryLimitExceeded
	}
	b.bytes += size
	return nil
}

// 与 encoding/json 默认 HTML 转义一致；只计名称内容，不包含两端引号。
// 先计数避免长名称转义后产生未受预算约束的临时编码缓冲。
func metricHistoryEscapedNameBytes(name string) int {
	size := 0
	for _, r := range name {
		switch r {
		case '"', '\\', '\b', '\f', '\n', '\r', '\t':
			size += 2
		case '<', '>', '&', '\u2028', '\u2029':
			size += 6
		default:
			if r < 0x20 {
				size += 6
			} else {
				size += utf8.RuneLen(r)
			}
		}
	}
	return size
}
