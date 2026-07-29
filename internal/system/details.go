package system

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
)

type Details struct {
	CollectedAt time.Time       `json:"collectedAt"`
	Warnings    []string        `json:"warnings"`
	Device      DeviceDetails   `json:"device"`
	Hardware    HardwareDetails `json:"hardware"`
	Network     NetworkDetails  `json:"network"`
	Storage     StorageDetails  `json:"storage"`
	Proxy       ProxyDetails    `json:"proxy"`
	Control     ControlDetails  `json:"control"`
}

type DeviceDetails struct {
	Hostname        string `json:"hostname"`
	Model           string `json:"model"`
	OperatingSystem string `json:"operatingSystem"`
	KernelVersion   string `json:"kernelVersion"`
	Architecture    string `json:"architecture"`
	UptimeSeconds   uint64 `json:"uptimeSeconds"`
	CgroupVersion   string `json:"cgroupVersion"`
}

type HardwareDetails struct {
	CPU     CPUDetails         `json:"cpu"`
	Memory  MemoryDetails      `json:"memory"`
	Sensors []TemperatureProbe `json:"sensors"`
}

type CPUDetails struct {
	Model              string  `json:"model"`
	PhysicalCores      int     `json:"physicalCores"`
	LogicalCores       int     `json:"logicalCores"`
	FrequencyMHz       float64 `json:"frequencyMHz"`
	TemperatureCelsius float64 `json:"temperatureCelsius"`
}

type MemoryDetails struct {
	TotalBytes     uint64 `json:"totalBytes"`
	AvailableBytes uint64 `json:"availableBytes"`
}

type TemperatureProbe struct {
	Name               string  `json:"name"`
	TemperatureCelsius float64 `json:"temperatureCelsius"`
}

type NetworkDetails struct {
	Interfaces     []NetworkInterface `json:"interfaces"`
	Gateway        string             `json:"gateway"`
	Routes         []NetworkRoute     `json:"routes"`
	DNSServers     []string           `json:"dnsServers"`
	ListeningPorts []ListeningPort    `json:"listeningPorts"`
}

type NetworkInterface struct {
	Name            string           `json:"name"`
	HardwareAddress string           `json:"hardwareAddress"`
	MTU             int              `json:"mtu"`
	State           string           `json:"state"`
	SpeedMbps       int              `json:"speedMbps"`
	Duplex          string           `json:"duplex"`
	Addresses       []NetworkAddress `json:"addresses"`
}

type NetworkAddress struct {
	Address      string `json:"address"`
	PrefixLength int    `json:"prefixLength"`
	Family       string `json:"family"`
}

type NetworkRoute struct {
	Destination string `json:"destination"`
	Gateway     string `json:"gateway"`
	Interface   string `json:"interface"`
	Metric      int    `json:"metric"`
}

type ListeningPort struct {
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     uint32 `json:"port"`
	PID      int32  `json:"pid"`
}

type StorageDetails struct {
	Mounts []MountDetails        `json:"mounts"`
	Disks  []PhysicalDiskDetails `json:"disks"`
	RAID   []RAIDDetails         `json:"raid"`
}

type MountDetails struct {
	Path           string  `json:"path"`
	Device         string  `json:"device"`
	Filesystem     string  `json:"filesystem"`
	TotalBytes     uint64  `json:"totalBytes"`
	UsedBytes      uint64  `json:"usedBytes"`
	AvailableBytes uint64  `json:"availableBytes"`
	UsedPercent    float64 `json:"usedPercent"`
}

type PhysicalDiskDetails struct {
	Name               string  `json:"name"`
	Model              string  `json:"model"`
	SizeBytes          uint64  `json:"sizeBytes"`
	Rotational         bool    `json:"rotational"`
	Health             string  `json:"health"`
	TemperatureCelsius float64 `json:"temperatureCelsius"`
}

type RAIDDetails struct {
	Name    string   `json:"name"`
	Level   string   `json:"level"`
	State   string   `json:"state"`
	Devices []string `json:"devices"`
}

type ProxyDetails struct {
	Mihomo       ProxyService       `json:"mihomo"`
	System       []ProxyEvidence    `json:"system"`
	Associations []ProxyAssociation `json:"associations"`
}

type ProxyService struct {
	Detected bool   `json:"detected"`
	State    string `json:"state"`
	Detail   string `json:"detail"`
}

type ProxyEvidence struct {
	Source   string `json:"source"`
	Method   string `json:"method"`
	Evidence string `json:"evidence"`
	Address  string `json:"address"`
	Detail   string `json:"detail"`
}

type ProxyAssociation struct {
	Subject  string `json:"subject"`
	Kind     string `json:"kind"`
	Method   string `json:"method"`
	Evidence string `json:"evidence"`
	Endpoint string `json:"endpoint"`
	Detail   string `json:"detail"`
}

type ControlDetails struct {
	Nodes []ControlNode `json:"nodes"`
}

type ControlNode struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Detail   string    `json:"detail"`
	Status   string    `json:"status"`
	Version  string    `json:"version"`
	LastSeen time.Time `json:"lastSeen"`
}

type DetailsCollector struct{}

func NewDetailsCollector() *DetailsCollector {
	return &DetailsCollector{}
}

func (c *DetailsCollector) Collect(ctx context.Context) (Details, error) {
	now := time.Now().UTC()
	result := Details{
		CollectedAt: now,
		Warnings:    []string{},
		Hardware:    HardwareDetails{Sensors: []TemperatureProbe{}},
		Network: NetworkDetails{
			Interfaces: []NetworkInterface{}, Routes: []NetworkRoute{},
			DNSServers: []string{}, ListeningPorts: []ListeningPort{},
		},
		Storage: StorageDetails{Mounts: []MountDetails{}, Disks: []PhysicalDiskDetails{}, RAID: []RAIDDetails{}},
		Proxy:   ProxyDetails{System: []ProxyEvidence{}, Associations: []ProxyAssociation{}},
		Control: ControlDetails{Nodes: []ControlNode{{
			ID: "agent", Name: "Root Agent", Detail: "宿主机与 Docker Engine 采集端",
			Status: "ready", LastSeen: now,
		}}},
	}
	if err := ctx.Err(); err != nil {
		return Details{}, err
	}
	c.collectDevice(ctx, &result)
	c.collectHardware(ctx, &result)
	c.collectNetwork(ctx, &result)
	c.collectStorage(ctx, &result)
	c.collectProxy(&result)
	return result, nil
}

func (c *DetailsCollector) collectDevice(ctx context.Context, result *Details) {
	info, err := host.InfoWithContext(ctx)
	if err != nil {
		result.Warnings = append(result.Warnings, "无法读取完整主机信息")
		result.Device.Architecture = runtime.GOARCH
		return
	}
	result.Device = DeviceDetails{
		Hostname:        info.Hostname,
		Model:           firstNonEmpty(readTrimmed("/sys/devices/virtual/dmi/id/product_name"), readTrimmed("/proc/device-tree/model")),
		OperatingSystem: operatingSystemName(info.Platform, info.PlatformVersion),
		KernelVersion:   info.KernelVersion,
		Architecture:    firstNonEmpty(info.KernelArch, runtime.GOARCH),
		UptimeSeconds:   info.Uptime,
		CgroupVersion:   detectCgroupVersion(),
	}
}

func (c *DetailsCollector) collectHardware(ctx context.Context, result *Details) {
	cpuInfo, err := cpu.InfoWithContext(ctx)
	if err != nil {
		result.Warnings = append(result.Warnings, "无法读取 CPU 详细信息")
	}
	physical, _ := cpu.CountsWithContext(ctx, false)
	logical, _ := cpu.CountsWithContext(ctx, true)
	result.Hardware.CPU.PhysicalCores = physical
	result.Hardware.CPU.LogicalCores = logical
	if len(cpuInfo) > 0 {
		result.Hardware.CPU.Model = cpuInfo[0].ModelName
		result.Hardware.CPU.FrequencyMHz = cpuInfo[0].Mhz
	}
	memory, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		result.Warnings = append(result.Warnings, "无法读取内存容量")
	} else {
		result.Hardware.Memory = MemoryDetails{TotalBytes: memory.Total, AvailableBytes: memory.Available}
	}
	result.Hardware.Sensors = collectTemperatures()
	for _, sensor := range result.Hardware.Sensors {
		if result.Hardware.CPU.TemperatureCelsius == 0 && strings.Contains(strings.ToLower(sensor.Name), "cpu") {
			result.Hardware.CPU.TemperatureCelsius = sensor.TemperatureCelsius
		}
	}
}

func (c *DetailsCollector) collectNetwork(ctx context.Context, result *Details) {
	interfaces, err := gnet.InterfacesWithContext(ctx)
	if err != nil {
		result.Warnings = append(result.Warnings, "无法读取网络接口")
	} else {
		for _, item := range interfaces {
			current := NetworkInterface{
				Name: item.Name, HardwareAddress: item.HardwareAddr, MTU: int(item.MTU),
				State:     firstNonEmpty(readTrimmed(filepath.Join("/sys/class/net", item.Name, "operstate")), "unknown"),
				SpeedMbps: parseInt(readTrimmed(filepath.Join("/sys/class/net", item.Name, "speed"))),
				Duplex:    readTrimmed(filepath.Join("/sys/class/net", item.Name, "duplex")),
				Addresses: []NetworkAddress{},
			}
			for _, address := range item.Addrs {
				ip, network, parseErr := net.ParseCIDR(address.Addr)
				if parseErr != nil {
					continue
				}
				prefix, _ := network.Mask.Size()
				family := "ipv6"
				if ip.To4() != nil {
					family = "ipv4"
				}
				current.Addresses = append(current.Addresses, NetworkAddress{
					Address: ip.String(), PrefixLength: prefix, Family: family,
				})
			}
			result.Network.Interfaces = append(result.Network.Interfaces, current)
		}
	}
	result.Network.Routes, result.Network.Gateway = collectRoutes()
	result.Network.DNSServers = collectDNS()
	connections, err := gnet.ConnectionsWithContext(ctx, "inet")
	if err != nil {
		result.Warnings = append(result.Warnings, "监听端口信息不可用")
		return
	}
	seen := map[string]bool{}
	for _, connection := range connections {
		if connection.Status != "LISTEN" && connection.Type != 2 {
			continue
		}
		protocol := "tcp"
		if connection.Type == 2 {
			protocol = "udp"
		}
		key := fmt.Sprintf("%s:%s:%d", protocol, connection.Laddr.IP, connection.Laddr.Port)
		if seen[key] {
			continue
		}
		seen[key] = true
		result.Network.ListeningPorts = append(result.Network.ListeningPorts, ListeningPort{
			Protocol: protocol, Address: connection.Laddr.IP,
			Port: connection.Laddr.Port, PID: connection.Pid,
		})
	}
	sort.Slice(result.Network.ListeningPorts, func(i, j int) bool {
		return result.Network.ListeningPorts[i].Port < result.Network.ListeningPorts[j].Port
	})
}

func (c *DetailsCollector) collectStorage(ctx context.Context, result *Details) {
	partitions, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		result.Warnings = append(result.Warnings, "无法读取挂载点")
	} else {
		seen := map[string]bool{}
		for _, partition := range partitions {
			if seen[partition.Mountpoint] {
				continue
			}
			usage, usageErr := disk.UsageWithContext(ctx, partition.Mountpoint)
			if usageErr != nil || usage.Total == 0 {
				continue
			}
			seen[partition.Mountpoint] = true
			result.Storage.Mounts = append(result.Storage.Mounts, MountDetails{
				Path: partition.Mountpoint, Device: partition.Device, Filesystem: partition.Fstype,
				TotalBytes: usage.Total, UsedBytes: usage.Used,
				AvailableBytes: usage.Free, UsedPercent: usage.UsedPercent,
			})
		}
	}
	result.Storage.Disks = collectPhysicalDisks()
	result.Storage.RAID = collectRAID()
}

func (c *DetailsCollector) collectProxy(result *Details) {
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"} {
		value := firstNonEmpty(os.Getenv(key), os.Getenv(strings.ToLower(key)))
		if value == "" {
			continue
		}
		result.Proxy.System = append(result.Proxy.System, ProxyEvidence{
			Source: "environment", Method: proxyMethod(value), Evidence: "confirmed",
			Address: redactProxyAddress(value), Detail: key,
		})
	}
	for _, item := range result.Network.Interfaces {
		name := strings.ToLower(item.Name)
		if strings.Contains(name, "tun") || strings.Contains(name, "mihomo") || strings.Contains(name, "clash") {
			result.Proxy.System = append(result.Proxy.System, ProxyEvidence{
				Source: "network-interface", Method: "tun", Evidence: "confirmed",
				Address: item.Name, Detail: "检测到代理相关虚拟网卡",
			})
		}
	}
	processes, _ := filepath.Glob("/proc/[0-9]*/comm")
	for _, path := range processes {
		name := strings.ToLower(readTrimmed(path))
		if name != "mihomo" && name != "clash" && name != "clash-meta" {
			continue
		}
		result.Proxy.Mihomo = ProxyService{Detected: true, State: "running", Detail: "检测到 " + name + " 进程"}
		result.Proxy.Associations = append(result.Proxy.Associations, ProxyAssociation{
			Subject: name, Kind: "process", Method: "unknown",
			Evidence: "confirmed", Detail: "运行中的代理核心进程",
		})
		break
	}
	if !result.Proxy.Mihomo.Detected {
		result.Proxy.Mihomo = ProxyService{
			Detected: false, State: "not-found", Detail: "未发现运行中的 Mihomo/Clash 进程",
		}
	}
}

func collectTemperatures() []TemperatureProbe {
	paths, _ := filepath.Glob("/sys/class/thermal/thermal_zone*/temp")
	result := make([]TemperatureProbe, 0, len(paths))
	for _, path := range paths {
		value, err := strconv.ParseFloat(readTrimmed(path), 64)
		if err != nil {
			continue
		}
		if value > 1000 {
			value /= 1000
		}
		name := firstNonEmpty(readTrimmed(filepath.Join(filepath.Dir(path), "type")), filepath.Base(filepath.Dir(path)))
		result = append(result, TemperatureProbe{Name: name, TemperatureCelsius: value})
	}
	return result
}

func collectRoutes() ([]NetworkRoute, string) {
	file, err := os.Open("/proc/net/route")
	if err != nil {
		return []NetworkRoute{}, ""
	}
	defer file.Close()
	result := []NetworkRoute{}
	gateway := ""
	scanner := bufio.NewScanner(file)
	first := true
	for scanner.Scan() {
		if first {
			first = false
			continue
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 8 {
			continue
		}
		current := NetworkRoute{
			Interface: fields[0], Destination: decodeIPv4Hex(fields[1]),
			Gateway: decodeIPv4Hex(fields[2]), Metric: parseInt(fields[6]),
		}
		if fields[1] == "00000000" {
			current.Destination = "0.0.0.0/0"
			if gateway == "" {
				gateway = current.Gateway
			}
		}
		result = append(result, current)
	}
	return result, gateway
}

func collectDNS() []string {
	file, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return []string{}
	}
	defer file.Close()
	result := []string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[0] == "nameserver" {
			result = append(result, fields[1])
		}
	}
	return result
}

func collectPhysicalDisks() []PhysicalDiskDetails {
	paths, _ := filepath.Glob("/sys/block/*")
	result := []PhysicalDiskDetails{}
	for _, path := range paths {
		name := filepath.Base(path)
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "dm-") {
			continue
		}
		sectors, _ := strconv.ParseUint(readTrimmed(filepath.Join(path, "size")), 10, 64)
		result = append(result, PhysicalDiskDetails{
			Name: name, Model: readTrimmed(filepath.Join(path, "device/model")),
			SizeBytes:  sectors * 512,
			Rotational: readTrimmed(filepath.Join(path, "queue/rotational")) == "1",
			Health:     "unknown",
		})
	}
	return result
}

func collectRAID() []RAIDDetails {
	file, err := os.Open("/proc/mdstat")
	if err != nil {
		return []RAIDDetails{}
	}
	defer file.Close()
	result := []RAIDDetails{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || !strings.HasPrefix(fields[0], "md") || fields[1] != ":" {
			continue
		}
		item := RAIDDetails{Name: fields[0], State: fields[2], Devices: []string{}}
		if len(fields) > 3 {
			item.Level = fields[3]
		}
		for _, field := range fields[4:] {
			item.Devices = append(item.Devices, strings.Split(field, "[")[0])
		}
		result = append(result, item)
	}
	return result
}

func detectCgroupVersion() string {
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err == nil {
		return "v2"
	}
	if _, err := os.Stat("/sys/fs/cgroup"); err == nil {
		return "v1"
	}
	return "unknown"
}

func readTrimmed(path string) string {
	value, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimRight(string(value), "\x00"))
}

func parseInt(value string) int {
	number, _ := strconv.Atoi(strings.TrimSpace(value))
	return number
}

func decodeIPv4Hex(value string) string {
	if len(value) != 8 {
		return value
	}
	bytes := make([]byte, 4)
	for index := 0; index < 4; index++ {
		part, err := strconv.ParseUint(value[index*2:index*2+2], 16, 8)
		if err != nil {
			return value
		}
		bytes[3-index] = byte(part)
	}
	return net.IP(bytes).String()
}

func redactProxyAddress(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" {
		return value
	}
	if parsed.User != nil {
		parsed.User = url.User("***")
	}
	return parsed.String()
}

func proxyMethod(value string) string {
	switch strings.ToLower(strings.SplitN(value, ":", 2)[0]) {
	case "http", "https":
		return "http"
	case "socks", "socks5", "socks5h":
		return "socks"
	default:
		return "unknown"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
