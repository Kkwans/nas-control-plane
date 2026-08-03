package system

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSummaryCollectorBuildsLiveHostSnapshot(t *testing.T) {
	now := time.Date(2026, time.July, 23, 8, 0, 0, 0, time.UTC)
	collector := NewSummaryCollector(fakeSummarySource{
		host: HostSnapshot{
			Hostname:        "DH4300-PLUS",
			OperatingSystem: "Debian GNU/Linux 12",
			KernelVersion:   "6.1.0",
			Architecture:    "arm64",
			UptimeSeconds:   86400,
			ProcessCount:    132,
		},
		cpu: CPUStats{UsagePercent: 27.4, LogicalCores: 8, Load1: 0.72, Load5: 0.64, Load15: 0.51},
		memory: MemoryStats{
			TotalBytes:     8 * 1024 * 1024 * 1024,
			UsedBytes:      3 * 1024 * 1024 * 1024,
			AvailableBytes: 5 * 1024 * 1024 * 1024,
		},
		disks:   []DiskStats{{Mountpoint: "/volume2", TotalBytes: 4_000, UsedBytes: 1_200, FreeBytes: 2_800}},
		network: []NetworkStats{{Name: "bond0", ReceiveBytes: 101, TransmitBytes: 202}},
	})
	collector.now = func() time.Time { return now }

	summary, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if !summary.CollectedAt.Equal(now) {
		t.Fatalf("CollectedAt = %s, want %s", summary.CollectedAt, now)
	}
	if summary.Host.Hostname != "DH4300-PLUS" || summary.Host.Architecture != "arm64" {
		t.Fatalf("host = %#v", summary.Host)
	}
	if summary.CPU.LogicalCores != 8 || summary.CPU.UsagePercent != 27.4 || summary.CPU.Load1 != 0.72 {
		t.Fatalf("cpu = %#v", summary.CPU)
	}
	if summary.Memory.AvailableBytes != 5*1024*1024*1024 {
		t.Fatalf("memory = %#v", summary.Memory)
	}
	if len(summary.Storage) != 1 || summary.Storage[0].Mountpoint != "/volume2" || summary.Storage[0].UsedBytes != 1_200 {
		t.Fatalf("storage = %#v", summary.Storage)
	}
	if len(summary.Network) != 1 || summary.Network[0].Name != "bond0" || summary.Network[0].TransmitBytes != 202 {
		t.Fatalf("network = %#v", summary.Network)
	}
	if len(summary.Warnings) != 0 {
		t.Fatalf("warnings = %#v", summary.Warnings)
	}
}

func TestSummaryCollectorIncludesTemperatureSensorsWhenSourceSupportsThem(t *testing.T) {
	collector := NewSummaryCollector(fakeTemperatureSummarySource{
		fakeSummarySource: fakeSummarySource{network: []NetworkStats{}},
		sensors:           []TemperatureProbe{{Name: "soc-thermal", TemperatureCelsius: 38.8}},
	})

	summary, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(summary.Sensors) != 1 || summary.Sensors[0].Name != "soc-thermal" || summary.Sensors[0].TemperatureCelsius != 38.8 {
		t.Fatalf("sensors = %#v", summary.Sensors)
	}
}

func TestSummaryCollectorIncludesDiskIOCountersWhenSourceSupportsThem(t *testing.T) {
	collector := NewSummaryCollector(fakeMonitoringSummarySource{
		fakeSummarySource: fakeSummarySource{network: []NetworkStats{}},
		diskIO:            DiskIOStats{ReadBytes: 12_345, WriteBytes: 67_890},
	})

	summary, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if summary.DiskIO != (DiskIOStats{ReadBytes: 12_345, WriteBytes: 67_890}) {
		t.Fatalf("diskIO = %#v", summary.DiskIO)
	}
}

func TestSummaryCollectorKeepsReadableTemperaturesWithSourceWarning(t *testing.T) {
	collector := NewSummaryCollector(fakeTemperatureSummarySource{
		fakeSummarySource: fakeSummarySource{network: []NetworkStats{}},
		sensors:           []TemperatureProbe{{Name: "soc-thermal", TemperatureCelsius: 38.8}},
		temperatureErr:    errors.New("one sensor unavailable"),
	})

	summary, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(summary.Sensors) != 1 || summary.Sensors[0].Name != "soc-thermal" {
		t.Fatalf("sensors = %#v", summary.Sensors)
	}
	if !hasSummaryWarning(summary.Warnings, "SYSTEM_SUMMARY_SOURCE_UNAVAILABLE", "temperature") {
		t.Fatalf("warnings = %#v, want temperature warning", summary.Warnings)
	}
}

func TestSummaryCollectorNormalizesMissingTemperatureSensorsToEmptySlice(t *testing.T) {
	collector := NewSummaryCollector(fakeTemperatureSummarySource{
		fakeSummarySource: fakeSummarySource{network: []NetworkStats{}},
	})

	summary, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if summary.Sensors == nil || len(summary.Sensors) != 0 {
		t.Fatalf("sensors = %#v, want non-nil empty slice", summary.Sensors)
	}
}

func TestSummaryCollectorReturnsPartialDataWithStableWarnings(t *testing.T) {
	collector := NewSummaryCollector(fakeSummarySource{
		host:     HostSnapshot{Hostname: "DH4300-PLUS"},
		cpuErr:   errors.New("cpu unavailable"),
		disksErr: errors.New("disk unavailable"),
		network:  []NetworkStats{},
		memory:   MemoryStats{TotalBytes: 1},
	})

	summary, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if summary.Host.Hostname != "DH4300-PLUS" || summary.Memory.TotalBytes != 1 {
		t.Fatalf("partial summary = %#v", summary)
	}
	if !hasSummaryWarning(summary.Warnings, "SYSTEM_SUMMARY_SOURCE_UNAVAILABLE", "cpu") {
		t.Fatalf("warnings = %#v, want cpu warning", summary.Warnings)
	}
	if !hasSummaryWarning(summary.Warnings, "SYSTEM_SUMMARY_SOURCE_UNAVAILABLE", "storage") {
		t.Fatalf("warnings = %#v, want storage warning", summary.Warnings)
	}
}

func TestSummaryCollectorHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewSummaryCollector(fakeSummarySource{}).Collect(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Collect() error = %v, want context.Canceled", err)
	}
}

type fakeSummarySource struct {
	host       HostSnapshot
	hostErr    error
	cpu        CPUStats
	cpuErr     error
	memory     MemoryStats
	memoryErr  error
	disks      []DiskStats
	disksErr   error
	network    []NetworkStats
	networkErr error
}

type fakeTemperatureSummarySource struct {
	fakeSummarySource
	sensors        []TemperatureProbe
	temperatureErr error
}

type fakeMonitoringSummarySource struct {
	fakeSummarySource
	diskIO DiskIOStats
}

func (f fakeSummarySource) Host(context.Context) (HostSnapshot, error) { return f.host, f.hostErr }
func (f fakeSummarySource) CPU(context.Context) (CPUStats, error)      { return f.cpu, f.cpuErr }
func (f fakeSummarySource) Memory(context.Context) (MemoryStats, error) {
	return f.memory, f.memoryErr
}
func (f fakeSummarySource) Storage(context.Context) ([]DiskStats, error) { return f.disks, f.disksErr }
func (f fakeSummarySource) Network(context.Context) ([]NetworkStats, error) {
	return f.network, f.networkErr
}

func (f fakeTemperatureSummarySource) Temperatures(context.Context) ([]TemperatureProbe, error) {
	return f.sensors, f.temperatureErr
}

func (f fakeMonitoringSummarySource) DiskIO(context.Context) (DiskIOStats, error) {
	return f.diskIO, nil
}

func hasSummaryWarning(warnings []SummaryWarning, code, source string) bool {
	for _, warning := range warnings {
		if warning.Code == code && warning.Source == source {
			return true
		}
	}
	return false
}
