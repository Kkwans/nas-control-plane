package controlstore

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MetricRetention          = 7 * 24 * time.Hour
	MaxMetricSamples         = 7*24*60 + 1
	MaxMetricTemperatures    = 128
	MaxMetricSensorNameBytes = 256
)

var (
	ErrInvalidMetricSample       = errors.New("MONITORING_SAMPLE_INVALID")
	ErrMetricSampleLimitExceeded = errors.New("MONITORING_SAMPLE_LIMIT_EXCEEDED")
)

// normalizeMetricTemperatures 保持输入不变，同名传感器取首值并稳定排序。
func normalizeMetricTemperatures(sample MetricSample) ([]MetricTemperature, error) {
	if sample.CollectedAt.IsZero() {
		return nil, ErrInvalidMetricSample
	}
	for _, value := range []float64{sample.CPUPercent, sample.MemoryPercent, sample.Load1, sample.DiskPercent} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, ErrInvalidMetricSample
		}
	}
	for _, value := range []uint64{sample.NetworkReceive, sample.NetworkTransmit, sample.DiskReadBytes, sample.DiskWriteBytes} {
		if value > math.MaxInt64 {
			return nil, ErrInvalidMetricSample
		}
	}
	byName := make(map[string]MetricTemperature)
	for _, temperature := range sample.Temperatures {
		if math.IsNaN(temperature.TemperatureCelsius) || math.IsInf(temperature.TemperatureCelsius, 0) || !utf8.ValidString(temperature.Name) {
			return nil, ErrInvalidMetricSample
		}
		if len(temperature.Name) > MaxMetricSensorNameBytes {
			return nil, ErrMetricSampleLimitExceeded
		}
		// 与既有存储兼容：空名称不持久化，不修改其余名称。
		if strings.TrimSpace(temperature.Name) == "" {
			continue
		}
		if _, exists := byName[temperature.Name]; !exists {
			byName[temperature.Name] = temperature
		}
		if len(byName) > MaxMetricTemperatures {
			return nil, ErrMetricSampleLimitExceeded
		}
	}
	temperatures := make([]MetricTemperature, 0, len(byName))
	for _, temperature := range byName {
		temperatures = append(temperatures, temperature)
	}
	sort.Slice(temperatures, func(i, j int) bool { return temperatures[i].Name < temperatures[j].Name })
	return temperatures, nil
}

// RecordMetricSample 主指标及全部温度原子提交；同分钟首次完整成功记录获胜。
func (s *Store) RecordMetricSample(ctx context.Context, sample MetricSample) error {
	temperatures, err := normalizeMetricTemperatures(sample)
	if err != nil {
		return err
	}
	bucket := sample.CollectedAt.UTC().Truncate(time.Minute).Unix()
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
  INSERT INTO metric_samples (
   collected_at_unix, cpu_percent, memory_percent, load1, disk_percent,
   network_receive_bytes, network_transmit_bytes, disk_read_bytes, disk_write_bytes
  ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
  ON CONFLICT(collected_at_unix) DO NOTHING
 `, bucket, sample.CPUPercent, sample.MemoryPercent, sample.Load1, sample.DiskPercent,
		sample.NetworkReceive, sample.NetworkTransmit, sample.DiskReadBytes, sample.DiskWriteBytes)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return tx.Commit()
	}
	// 旧版本可能留下同桶孤儿；新完整样本不能混入旧温度。
	if _, err := tx.ExecContext(ctx, `DELETE FROM metric_temperature_samples WHERE collected_at_unix = ?`, bucket); err != nil {
		return err
	}
	for _, temperature := range temperatures {
		if _, err := tx.ExecContext(ctx, `INSERT INTO metric_temperature_samples
   (collected_at_unix, sensor_name, temperature_celsius) VALUES (?, ?, ?)`, bucket, temperature.Name, temperature.TemperatureCelsius); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PruneMetricSamples 使用服务时钟独立清理，不依赖采集或写入成功。
// 七天含分钟边界，正常新样本最多 MaxMetricSamples 桶；保留仍在时间窗内的旧版数据，
// 不因新增传感器上限或旧版未来时间戳删除历史，也不执行 VACUUM。
func (s *Store) PruneMetricSamples(ctx context.Context, now time.Time) error {
	if now.IsZero() {
		return ErrInvalidMetricSample
	}
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cutoff := now.UTC().Truncate(time.Minute).Add(-MetricRetention).Unix()
	if _, err := tx.ExecContext(ctx, `DELETE FROM metric_samples WHERE collected_at_unix < ?`, cutoff); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM metric_temperature_samples WHERE NOT EXISTS
  (SELECT 1 FROM metric_samples WHERE metric_samples.collected_at_unix = metric_temperature_samples.collected_at_unix)`); err != nil {
		return err
	}
	return tx.Commit()
}

// MetricSamples 保留旧调用者的单下界语义；HTTP 历史必须使用 MetricSamplesInRange。
func (s *Store) MetricSamples(ctx context.Context, since time.Time) ([]MetricSample, error) {
	rows, err := s.database.QueryContext(ctx, `
		SELECT collected_at_unix, cpu_percent, memory_percent, load1, disk_percent,
			network_receive_bytes, network_transmit_bytes, disk_read_bytes, disk_write_bytes
		FROM metric_samples
		WHERE collected_at_unix >= ?
		ORDER BY collected_at_unix
	`, since.UTC().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	samples := make([]MetricSample, 0)
	for rows.Next() {
		var sample MetricSample
		var collectedAtUnix int64
		if err := rows.Scan(&collectedAtUnix, &sample.CPUPercent, &sample.MemoryPercent, &sample.Load1, &sample.DiskPercent,
			&sample.NetworkReceive, &sample.NetworkTransmit, &sample.DiskReadBytes, &sample.DiskWriteBytes); err != nil {
			return nil, err
		}
		sample.CollectedAt = time.Unix(collectedAtUnix, 0).UTC()
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(samples) == 0 {
		return samples, nil
	}
	temperatureRows, err := s.database.QueryContext(ctx, `
		SELECT collected_at_unix, sensor_name, temperature_celsius
		FROM metric_temperature_samples
		WHERE collected_at_unix >= ?
		ORDER BY collected_at_unix, sensor_name
	`, since.UTC().Unix())
	if err != nil {
		return nil, err
	}
	defer temperatureRows.Close()
	byBucket := make(map[int64][]MetricTemperature)
	for temperatureRows.Next() {
		var collectedAtUnix int64
		var temperature MetricTemperature
		if err := temperatureRows.Scan(&collectedAtUnix, &temperature.Name, &temperature.TemperatureCelsius); err != nil {
			return nil, err
		}
		byBucket[collectedAtUnix] = append(byBucket[collectedAtUnix], temperature)
	}
	if err := temperatureRows.Err(); err != nil {
		return nil, err
	}
	for index := range samples {
		samples[index].Temperatures = byBucket[samples[index].CollectedAt.Unix()]
	}
	return samples, nil
}
