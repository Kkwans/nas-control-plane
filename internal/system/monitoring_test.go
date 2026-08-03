package system

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMetricSampleFromSummaryPreservesExistingFieldsAndAddsMonitoringData(t *testing.T) {
	collectedAt := time.Date(2026, time.July, 23, 8, 0, 0, 123_000_000, time.FixedZone("CST", 8*60*60))
	sample := MetricSampleFromSummary(Summary{
		CollectedAt: collectedAt,
		CPU:         CPUStats{UsagePercent: 27.4, Load1: 0.72},
		Memory:      MemoryStats{TotalBytes: 1_000, UsedBytes: 250},
		Storage: []DiskStats{
			{Mountpoint: "/volume1", TotalBytes: 2_000, UsedBytes: 500},
			{Mountpoint: "/volume2", TotalBytes: 3_000, UsedBytes: 1_000},
		},
		DiskIO: DiskIOStats{ReadBytes: 12_345, WriteBytes: 67_890},
		Network: []NetworkStats{
			{Name: "bond0", ReceiveBytes: 101, TransmitBytes: 202},
			{Name: "eth0", ReceiveBytes: 303, TransmitBytes: 404},
		},
		Sensors: []TemperatureProbe{
			{Name: "soc-thermal", TemperatureCelsius: 38.8},
		},
	})

	if !sample.CollectedAt.Equal(collectedAt.UTC()) {
		t.Fatalf("CollectedAt = %s, want %s", sample.CollectedAt, collectedAt.UTC())
	}
	if sample.CPUPercent != 27.4 || sample.MemoryPercent != 25 || sample.Load1 != 0.72 {
		t.Fatalf("scalar sample = %#v", sample)
	}
	if sample.DiskPercent != 30 || sample.NetworkReceive != 404 || sample.NetworkTransmit != 606 {
		t.Fatalf("storage/network sample = %#v", sample)
	}
	if sample.DiskReadBytes != 12_345 || sample.DiskWriteBytes != 67_890 {
		t.Fatalf("disk I/O sample = %#v", sample)
	}
	if len(sample.Temperatures) != 1 || sample.Temperatures[0].Name != "soc-thermal" || sample.Temperatures[0].TemperatureCelsius != 38.8 {
		t.Fatalf("temperature samples = %#v", sample.Temperatures)
	}
}

func TestMetricSampleFromSummaryUsesEmptyTemperatureSamplesWhenSensorsMissing(t *testing.T) {
	sample := MetricSampleFromSummary(Summary{})
	if sample.Temperatures == nil || len(sample.Temperatures) != 0 {
		t.Fatalf("temperatures = %#v, want non-nil empty slice", sample.Temperatures)
	}
	if sample.DiskReadBytes != 0 || sample.DiskWriteBytes != 0 {
		t.Fatalf("missing disk I/O must remain empty zero values: %#v", sample)
	}
}

func TestGopsutilMonitoringSourcesHonorCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := collectDiskIOWithContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("collectDiskIOWithContext() error = %v, want context.Canceled", err)
	}
	if _, err := collectTemperaturesWithContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("collectTemperaturesWithContext() error = %v, want context.Canceled", err)
	}
}
