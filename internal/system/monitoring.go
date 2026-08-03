package system

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/sensors"
)

var errDiskIOCountersUnavailable = errors.New("disk I/O counters unavailable")

// MetricSample 是可持久化的监控样本契约。
// 其中磁盘和网络字段都是宿主机累计字节计数器，不是当前采样周期的速率。
// Temperatures 使用独立子样本，传感器缺失时保持为空，不用 0 或其他值填充。
type MetricSample struct {
	CollectedAt     time.Time           `json:"collectedAt"`
	CPUPercent      float64             `json:"cpuPercent"`
	MemoryPercent   float64             `json:"memoryPercent"`
	Load1           float64             `json:"load1"`
	DiskPercent     float64             `json:"diskPercent"`
	NetworkReceive  uint64              `json:"networkReceiveBytes"`
	NetworkTransmit uint64              `json:"networkTransmitBytes"`
	DiskReadBytes   uint64              `json:"diskReadBytes"`
	DiskWriteBytes  uint64              `json:"diskWriteBytes"`
	Temperatures    []TemperatureSample `json:"temperatures"`
}

// TemperatureSample 是和一个采样时间关联的单个传感器读数。
// Name 使用 gopsutil 的 SensorKey，不能读取的传感器不生成子样本。
type TemperatureSample struct {
	Name               string  `json:"name"`
	TemperatureCelsius float64 `json:"temperatureCelsius"`
}

// MetricSampleFromSummary 将实时快照转换为持久化样本，同时保留已有的标量字段。
func MetricSampleFromSummary(summary Summary) MetricSample {
	var storageTotal, storageUsed, networkReceive, networkTransmit uint64
	for _, disk := range summary.Storage {
		storageTotal += disk.TotalBytes
		storageUsed += disk.UsedBytes
	}
	for _, item := range summary.Network {
		networkReceive += item.ReceiveBytes
		networkTransmit += item.TransmitBytes
	}

	memoryPercent, diskPercent := 0.0, 0.0
	if summary.Memory.TotalBytes > 0 {
		memoryPercent = float64(summary.Memory.UsedBytes) / float64(summary.Memory.TotalBytes) * 100
	}
	if storageTotal > 0 {
		diskPercent = float64(storageUsed) / float64(storageTotal) * 100
	}

	temperatures := make([]TemperatureSample, 0, len(summary.Sensors))
	for _, sensor := range summary.Sensors {
		temperatures = append(temperatures, TemperatureSample{
			Name:               sensor.Name,
			TemperatureCelsius: sensor.TemperatureCelsius,
		})
	}

	return MetricSample{
		CollectedAt:     summary.CollectedAt.UTC(),
		CPUPercent:      summary.CPU.UsagePercent,
		MemoryPercent:   memoryPercent,
		Load1:           summary.CPU.Load1,
		DiskPercent:     diskPercent,
		NetworkReceive:  networkReceive,
		NetworkTransmit: networkTransmit,
		DiskReadBytes:   summary.DiskIO.ReadBytes,
		DiskWriteBytes:  summary.DiskIO.WriteBytes,
		Temperatures:    temperatures,
	}
}

// MetricSample returns the persistence representation of a live summary.
func (summary Summary) MetricSample() MetricSample {
	return MetricSampleFromSummary(summary)
}

func collectDiskIOWithContext(ctx context.Context) (DiskIOStats, error) {
	if err := ctx.Err(); err != nil {
		return DiskIOStats{}, err
	}

	counters, err := disk.IOCountersWithContext(ctx)
	if err != nil {
		return DiskIOStats{}, err
	}
	result := DiskIOStats{}
	found := false
	for name, counter := range counters {
		if strings.TrimSpace(name) == "" && strings.TrimSpace(counter.Name) == "" {
			continue
		}
		found = true
		result.ReadBytes = addUint64Saturating(result.ReadBytes, counter.ReadBytes)
		result.WriteBytes = addUint64Saturating(result.WriteBytes, counter.WriteBytes)
	}
	if !found {
		return DiskIOStats{}, errDiskIOCountersUnavailable
	}
	if err := ctx.Err(); err != nil {
		return DiskIOStats{}, err
	}
	return result, nil
}

func collectTemperaturesWithContext(ctx context.Context) ([]TemperatureProbe, error) {
	if err := ctx.Err(); err != nil {
		return []TemperatureProbe{}, err
	}

	readings, err := sensors.TemperaturesWithContext(ctx)
	result := make([]TemperatureProbe, 0, len(readings))
	for _, reading := range readings {
		name := strings.TrimSpace(reading.SensorKey)
		if name == "" || math.IsNaN(reading.Temperature) || math.IsInf(reading.Temperature, 0) {
			continue
		}
		result = append(result, TemperatureProbe{
			Name:               name,
			TemperatureCelsius: reading.Temperature,
		})
	}
	sort.SliceStable(result, func(left, right int) bool {
		return result[left].Name < result[right].Name
	})
	if contextErr := ctx.Err(); contextErr != nil {
		return result, contextErr
	}
	return result, err
}

func addUint64Saturating(current, value uint64) uint64 {
	if ^uint64(0)-current < value {
		return ^uint64(0)
	}
	return current + value
}
