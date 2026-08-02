package system

import (
	"context"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gopsnet "github.com/shirou/gopsutil/v4/net"
)

const cpuSampleInterval = 200 * time.Millisecond

// Summary 是首页和系统详情页共用的实时宿主机快照。
// 指标采集委托给 gopsutil，避免在业务层重复实现 Linux /proc、statfs 与网卡计数器解析。
type Summary struct {
	CollectedAt time.Time          `json:"collectedAt"`
	Host        HostSnapshot       `json:"host"`
	CPU         CPUStats           `json:"cpu"`
	Memory      MemoryStats        `json:"memory"`
	Sensors     []TemperatureProbe `json:"sensors"`
	Storage     []DiskStats        `json:"storage"`
	Network     []NetworkStats     `json:"network"`
	Warnings    []SummaryWarning   `json:"warnings"`
}

type HostSnapshot struct {
	Hostname        string `json:"hostname"`
	OperatingSystem string `json:"operatingSystem"`
	KernelVersion   string `json:"kernelVersion"`
	Architecture    string `json:"architecture"`
	UptimeSeconds   uint64 `json:"uptimeSeconds"`
	ProcessCount    uint64 `json:"processCount"`
}

type CPUStats struct {
	UsagePercent float64 `json:"usagePercent"`
	LogicalCores int     `json:"logicalCores"`
	Load1        float64 `json:"load1"`
	Load5        float64 `json:"load5"`
	Load15       float64 `json:"load15"`
}

type MemoryStats struct {
	TotalBytes     uint64 `json:"totalBytes"`
	UsedBytes      uint64 `json:"usedBytes"`
	AvailableBytes uint64 `json:"availableBytes"`
}

type DiskStats struct {
	Mountpoint string `json:"mountpoint"`
	TotalBytes uint64 `json:"totalBytes"`
	UsedBytes  uint64 `json:"usedBytes"`
	FreeBytes  uint64 `json:"freeBytes"`
}

type NetworkStats struct {
	Name          string `json:"name"`
	ReceiveBytes  uint64 `json:"receiveBytes"`
	TransmitBytes uint64 `json:"transmitBytes"`
}

type SummaryWarning struct {
	Code   string `json:"code"`
	Source string `json:"source"`
}

type SummarySource interface {
	Host(context.Context) (HostSnapshot, error)
	CPU(context.Context) (CPUStats, error)
	Memory(context.Context) (MemoryStats, error)
	Storage(context.Context) ([]DiskStats, error)
	Network(context.Context) ([]NetworkStats, error)
}

type temperatureSummarySource interface {
	Temperatures(context.Context) ([]TemperatureProbe, error)
}

type SummaryCollector struct {
	source SummarySource
	now    func() time.Time
}

func NewSummaryCollector(source SummarySource) *SummaryCollector {
	if source == nil {
		source = gopsutilSummarySource{}
	}
	return &SummaryCollector{source: source, now: time.Now}
}

func NewLiveSummaryCollector() *SummaryCollector {
	return NewSummaryCollector(gopsutilSummarySource{})
}

func (c *SummaryCollector) Collect(ctx context.Context) (Summary, error) {
	if err := ctx.Err(); err != nil {
		return Summary{}, err
	}

	summary := Summary{
		CollectedAt: c.now().UTC(),
		Sensors:     make([]TemperatureProbe, 0),
		Storage:     make([]DiskStats, 0),
		Network:     make([]NetworkStats, 0),
		Warnings:    make([]SummaryWarning, 0),
	}
	if value, err := c.source.Host(ctx); err != nil {
		addSummaryWarning(&summary, "SYSTEM_SUMMARY_SOURCE_UNAVAILABLE", "host")
	} else {
		summary.Host = value
	}
	if err := ctx.Err(); err != nil {
		return Summary{}, err
	}
	if value, err := c.source.CPU(ctx); err != nil {
		addSummaryWarning(&summary, "SYSTEM_SUMMARY_SOURCE_UNAVAILABLE", "cpu")
	} else {
		summary.CPU = value
	}
	if err := ctx.Err(); err != nil {
		return Summary{}, err
	}
	if value, err := c.source.Memory(ctx); err != nil {
		addSummaryWarning(&summary, "SYSTEM_SUMMARY_SOURCE_UNAVAILABLE", "memory")
	} else {
		summary.Memory = value
	}
	if err := ctx.Err(); err != nil {
		return Summary{}, err
	}
	if value, err := c.source.Storage(ctx); err != nil {
		addSummaryWarning(&summary, "SYSTEM_SUMMARY_SOURCE_UNAVAILABLE", "storage")
	} else {
		summary.Storage = value
	}
	if err := ctx.Err(); err != nil {
		return Summary{}, err
	}
	if value, err := c.source.Network(ctx); err != nil {
		addSummaryWarning(&summary, "SYSTEM_SUMMARY_SOURCE_UNAVAILABLE", "network")
	} else {
		summary.Network = value
	}
	if source, ok := c.source.(temperatureSummarySource); ok {
		if value, err := source.Temperatures(ctx); err == nil {
			summary.Sensors = value
		} else {
			addSummaryWarning(&summary, "SYSTEM_SUMMARY_SOURCE_UNAVAILABLE", "temperature")
		}
	}
	return summary, nil
}

type gopsutilSummarySource struct{}

func (gopsutilSummarySource) Host(ctx context.Context) (HostSnapshot, error) {
	info, err := host.InfoWithContext(ctx)
	if err != nil {
		return HostSnapshot{}, err
	}
	return HostSnapshot{
		Hostname:        info.Hostname,
		OperatingSystem: operatingSystemName(info.Platform, info.PlatformVersion),
		KernelVersion:   info.KernelVersion,
		Architecture:    runtime.GOARCH,
		UptimeSeconds:   info.Uptime,
		ProcessCount:    info.Procs,
	}, nil
}

func (gopsutilSummarySource) CPU(ctx context.Context) (CPUStats, error) {
	usage, err := cpu.PercentWithContext(ctx, cpuSampleInterval, false)
	if err != nil {
		return CPUStats{}, err
	}
	loads, err := load.AvgWithContext(ctx)
	if err != nil {
		return CPUStats{}, err
	}
	cores, err := cpu.CountsWithContext(ctx, true)
	if err != nil {
		return CPUStats{}, err
	}
	value := 0.0
	if len(usage) > 0 {
		value = usage[0]
	}
	return CPUStats{UsagePercent: value, LogicalCores: cores, Load1: loads.Load1, Load5: loads.Load5, Load15: loads.Load15}, nil
}

func (gopsutilSummarySource) Memory(ctx context.Context) (MemoryStats, error) {
	stats, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return MemoryStats{}, err
	}
	return MemoryStats{TotalBytes: stats.Total, UsedBytes: stats.Used, AvailableBytes: stats.Available}, nil
}

func (gopsutilSummarySource) Storage(ctx context.Context) ([]DiskStats, error) {
	partitions, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		return nil, err
	}
	result := make([]DiskStats, 0, len(partitions))
	for _, partition := range partitions {
		if !persistentMountpoint(partition.Mountpoint) {
			continue
		}
		usage, err := disk.UsageWithContext(ctx, partition.Mountpoint)
		if err != nil {
			continue
		}
		result = append(result, DiskStats{
			Mountpoint: partition.Mountpoint,
			TotalBytes: usage.Total,
			UsedBytes:  usage.Used,
			FreeBytes:  usage.Free,
		})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Mountpoint < result[right].Mountpoint })
	return result, nil
}

func (gopsutilSummarySource) Network(ctx context.Context) ([]NetworkStats, error) {
	counters, err := gopsnet.IOCountersWithContext(ctx, true)
	if err != nil {
		return nil, err
	}
	result := make([]NetworkStats, 0, len(counters))
	for _, counter := range counters {
		if counter.Name == "" || counter.Name == "lo" {
			continue
		}
		result = append(result, NetworkStats{
			Name:          counter.Name,
			ReceiveBytes:  counter.BytesRecv,
			TransmitBytes: counter.BytesSent,
		})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Name < result[right].Name })
	return result, nil
}

func (gopsutilSummarySource) Temperatures(context.Context) ([]TemperatureProbe, error) {
	return collectTemperatures(), nil
}

func persistentMountpoint(mountpoint string) bool {
	return mountpoint == "/" || strings.HasPrefix(mountpoint, "/volume")
}

func operatingSystemName(platform, version string) string {
	platform = strings.TrimSpace(platform)
	version = strings.TrimSpace(version)
	if platform == "" {
		return version
	}
	if version == "" {
		return platform
	}
	return platform + " " + version
}

func addSummaryWarning(summary *Summary, code, source string) {
	for _, warning := range summary.Warnings {
		if warning.Code == code && warning.Source == source {
			return
		}
	}
	summary.Warnings = append(summary.Warnings, SummaryWarning{Code: code, Source: source})
}
