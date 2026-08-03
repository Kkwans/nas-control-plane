package system

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	dockerSocketPath       = "/var/run/docker.sock"
	systemdRuntimePath     = "/run/systemd/system"
	cgroupV2ControllerPath = "/sys/fs/cgroup/cgroup.controllers"
	procCPUInfoPath        = "/proc/cpuinfo"
	sysProbePath           = "/sys/kernel/uevent_seqnum"
	mountsPath             = "/proc/mounts"
	commandTimeout         = 5 * time.Second
)

// Environment 将 Agent 的只读宿主机访问隔离出来，使单元测试无需访问真实 NAS。
type Environment interface {
	Architecture() string
	Hostname() (string, error)
	ReadFile(name string) ([]byte, error)
	PathExists(name string) bool
	LookPath(name string) (string, error)
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
	Glob(pattern string) ([]string, error)
	NetworkInterfaces() ([]string, error)
	EffectiveUID() int
}

// Capabilities 是 OpenAPI 和 Agent RPC 共用的 P0-01 数据模型。
// 对应能力缺失或无法安全查询时，可选字段为 nil。
type Capabilities struct {
	Hostname               string                 `json:"hostname"`
	Architecture           string                 `json:"architecture"`
	OperatingSystem        OperatingSystem        `json:"operatingSystem"`
	DeviceModel            *string                `json:"deviceModel"`
	Docker                 bool                   `json:"docker"`
	DockerAPIVersion       *string                `json:"dockerApiVersion"`
	Compose                bool                   `json:"compose"`
	ComposeVersion         *string                `json:"composeVersion"`
	Systemd                bool                   `json:"systemd"`
	Journald               bool                   `json:"journald"`
	CgroupVersion          int                    `json:"cgroupVersion"`
	ProcReadable           bool                   `json:"procReadable"`
	SysReadable            bool                   `json:"sysReadable"`
	Smartctl               bool                   `json:"smartctl"`
	NvmeCLI                bool                   `json:"nvmeCli"`
	TemperatureSensors     []string               `json:"temperatureSensors"`
	DataVolumes            []string               `json:"dataVolumes"`
	NetworkInterfaces      []string               `json:"networkInterfaces"`
	CanManageSystemUsers   bool                   `json:"canManageSystemUsers"`
	RootFilesystemWritable bool                   `json:"rootFilesystemWritable"`
	HostTerminal           bool                   `json:"hostTerminal"`
	Tailscale              TailscaleCapability    `json:"tailscale"`
	Mihomo                 MihomoCapability       `json:"mihomo"`
	DNS                    DNSCapability          `json:"dns"`
	PublicEgress           PublicEgressCapability `json:"publicEgress"`
	Warnings               []ProbeWarning         `json:"warnings"`
}

type OperatingSystem struct {
	ID         string `json:"id"`
	VersionID  string `json:"versionId"`
	PrettyName string `json:"prettyName"`
}

// ProbeWarning 使用稳定码，调用方无需依赖可能泄露实现细节的宿主机错误文本。
type ProbeWarning struct {
	Code   string `json:"code"`
	Source string `json:"source"`
}

type Probe struct {
	environment Environment
}

func NewProbe(environment Environment) *Probe {
	return &Probe{environment: environment}
}

func (p *Probe) Collect(ctx context.Context) (Capabilities, error) {
	if err := ctx.Err(); err != nil {
		return Capabilities{}, err
	}

	capabilities := Capabilities{
		Architecture:       p.environment.Architecture(),
		TemperatureSensors: make([]string, 0),
		DataVolumes:        make([]string, 0),
		NetworkInterfaces:  make([]string, 0),
		Tailscale: TailscaleCapability{
			OverlayIPs: make([]string, 0), Evidence: make([]CapabilityEvidence, 0), Warnings: make([]ProbeWarning, 0),
		},
		Mihomo: MihomoCapability{
			Controller: MihomoControllerCapability{Operations: make([]string, 0)},
			Evidence:   make([]CapabilityEvidence, 0), Warnings: make([]ProbeWarning, 0),
		},
		DNS:      DNSCapability{Backend: DNSBackendUnknown, Nameservers: make([]string, 0)},
		Warnings: make([]ProbeWarning, 0),
	}

	if hostname, err := p.environment.Hostname(); err == nil {
		capabilities.Hostname = hostname
	} else {
		addWarning(&capabilities, "PROBE_SOURCE_UNAVAILABLE", "hostname")
	}

	if content, err := p.environment.ReadFile("/etc/os-release"); err == nil {
		capabilities.OperatingSystem = parseOSRelease(string(content))
	} else {
		addWarning(&capabilities, "PROBE_SOURCE_UNAVAILABLE", "/etc/os-release")
	}

	for _, path := range []string{"/proc/device-tree/model", "/sys/class/dmi/id/product_name"} {
		content, err := p.environment.ReadFile(path)
		if err != nil {
			continue
		}
		if model := strings.TrimSpace(strings.Trim(string(content), "\x00")); model != "" {
			capabilities.DeviceModel = stringPointer(model)
			break
		}
	}

	capabilities.Docker = p.environment.PathExists(dockerSocketPath)
	if capabilities.Docker {
		p.collectDockerCapabilities(ctx, &capabilities)
	}

	capabilities.Systemd = p.environment.PathExists(systemdRuntimePath)
	capabilities.Journald = p.commandAvailable("journalctl")
	capabilities.Smartctl = p.commandAvailable("smartctl")
	capabilities.NvmeCLI = p.commandAvailable("nvme")
	capabilities.CanManageSystemUsers = p.environment.EffectiveUID() == 0 && p.commandAvailable("useradd")
	capabilities.HostTerminal = p.environment.PathExists("/dev/ptmx") && p.environment.PathExists("/bin/bash")

	if _, err := p.environment.ReadFile(procCPUInfoPath); err == nil {
		capabilities.ProcReadable = true
	} else {
		addWarning(&capabilities, "PROBE_SOURCE_UNAVAILABLE", procCPUInfoPath)
	}
	if _, err := p.environment.ReadFile(sysProbePath); err == nil {
		capabilities.SysReadable = true
	} else {
		addWarning(&capabilities, "PROBE_SOURCE_UNAVAILABLE", sysProbePath)
	}

	capabilities.CgroupVersion = p.detectCgroupVersion()
	capabilities.TemperatureSensors = p.collectTemperatureSensors(&capabilities)
	p.collectMountCapabilities(&capabilities)
	if interfaces, err := p.environment.NetworkInterfaces(); err == nil {
		capabilities.NetworkInterfaces = sortedUnique(interfaces)
	} else {
		addWarning(&capabilities, "PROBE_SOURCE_UNAVAILABLE", "network-interfaces")
	}

	capabilities.Tailscale = ProbeTailscale(ctx, p.environment, interfaceSnapshots(p.environment))
	capabilities.Mihomo = ProbeMihomo(ctx, p.environment)
	capabilities.DNS = ProbeDNS(ctx, p.environment)
	capabilities.PublicEgress = NewPublicEgressCapability(os.Getenv("NCP_PUBLIC_EGRESS_ENDPOINT"))

	return capabilities, nil
}

func (p *Probe) collectDockerCapabilities(ctx context.Context, capabilities *Capabilities) {
	if !p.commandAvailable("docker") {
		if p.commandAvailable("docker-compose") {
			p.collectComposeVersion(ctx, capabilities, "docker-compose", "version", "--short")
		}
		return
	}

	if version, err := p.commandOutput(ctx, "docker", "version", "--format", "{{.Server.APIVersion}}"); err == nil && version != "" {
		capabilities.DockerAPIVersion = stringPointer(version)
	} else if err != nil {
		addWarning(capabilities, "PROBE_COMMAND_FAILED", "docker version")
	}
	p.collectComposeVersion(ctx, capabilities, "docker", "compose", "version", "--short")
}

func (p *Probe) collectComposeVersion(ctx context.Context, capabilities *Capabilities, name string, args ...string) {
	version, err := p.commandOutput(ctx, name, args...)
	if err != nil {
		addWarning(capabilities, "PROBE_COMMAND_FAILED", strings.Join(append([]string{name}, args...), " "))
		return
	}
	if version == "" {
		return
	}
	capabilities.Compose = true
	capabilities.ComposeVersion = stringPointer(version)
}

func (p *Probe) detectCgroupVersion() int {
	if p.environment.PathExists(cgroupV2ControllerPath) {
		return 2
	}

	content, err := p.environment.ReadFile("/proc/cgroups")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(strings.Fields(line)) >= 4 {
			return 1
		}
	}
	return 0
}

func (p *Probe) collectTemperatureSensors(capabilities *Capabilities) []string {
	paths := make([]string, 0)
	for _, pattern := range []string{
		"/sys/class/thermal/thermal_zone*/temp",
		"/sys/class/hwmon/hwmon*/temp*_input",
	} {
		matches, err := p.environment.Glob(pattern)
		if err != nil {
			addWarning(capabilities, "PROBE_SOURCE_UNAVAILABLE", pattern)
			continue
		}
		paths = append(paths, matches...)
	}

	sensors := make([]string, 0, len(paths))
	for _, path := range paths {
		sensors = append(sensors, temperatureSensorName(path))
	}
	return sortedUnique(sensors)
}

func (p *Probe) collectMountCapabilities(capabilities *Capabilities) {
	content, err := p.environment.ReadFile(mountsPath)
	if err != nil {
		addWarning(capabilities, "PROBE_SOURCE_UNAVAILABLE", mountsPath)
		return
	}

	rootWritable, dataVolumes := parseMounts(string(content))
	capabilities.RootFilesystemWritable = rootWritable
	capabilities.DataVolumes = dataVolumes
}

func (p *Probe) commandAvailable(name string) bool {
	_, err := p.environment.LookPath(name)
	return err == nil
}

func (p *Probe) commandOutput(ctx context.Context, name string, args ...string) (string, error) {
	commandContext, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	output, err := p.environment.Run(commandContext, name, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func parseOSRelease(content string) OperatingSystem {
	operatingSystem := OperatingSystem{}
	for _, line := range strings.Split(content, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		switch key {
		case "ID":
			operatingSystem.ID = unquoteOSReleaseValue(value)
		case "VERSION_ID":
			operatingSystem.VersionID = unquoteOSReleaseValue(value)
		case "PRETTY_NAME":
			operatingSystem.PrettyName = unquoteOSReleaseValue(value)
		}
	}
	return operatingSystem
}

func unquoteOSReleaseValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return value
	}

	unquoted, err := strconv.Unquote(value)
	if err != nil {
		return strings.Trim(value, "\"")
	}
	return unquoted
}

func parseMounts(content string) (bool, []string) {
	rootWritable := false
	volumes := make([]string, 0)
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		mountPoint := unescapeMountPath(fields[1])
		if mountPoint == "/" {
			rootWritable = mountOptionsContain(fields[3], "rw")
		}
		if volumeRoot, ok := dataVolumeRoot(mountPoint); ok {
			volumes = append(volumes, volumeRoot)
		}
	}
	return rootWritable, sortedUnique(volumes)
}

func unescapeMountPath(path string) string {
	replacer := strings.NewReplacer(
		"\\040", " ",
		"\\011", "\t",
		"\\012", "\n",
		"\\134", "\\",
	)
	return replacer.Replace(path)
}

func dataVolumeRoot(mountPoint string) (string, bool) {
	parts := strings.Split(strings.TrimPrefix(filepath.ToSlash(mountPoint), "/"), "/")
	if len(parts) == 0 {
		return "", false
	}

	name := parts[0]
	suffix, found := strings.CutPrefix(name, "volume")
	if !found || suffix == "" {
		return "", false
	}
	for _, character := range suffix {
		if character < '0' || character > '9' {
			return "", false
		}
	}
	return "/" + name, true
}

func mountOptionsContain(options, option string) bool {
	for _, candidate := range strings.Split(options, ",") {
		if candidate == option {
			return true
		}
	}
	return false
}

func temperatureSensorName(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, part := range parts {
		if strings.HasPrefix(part, "thermal_zone") {
			return part
		}
	}
	return strings.TrimPrefix(filepath.ToSlash(path), "/sys/class/")
}

func sortedUnique(values []string) []string {
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			unique[value] = struct{}{}
		}
	}

	result := make([]string, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func stringPointer(value string) *string {
	return &value
}

func addWarning(capabilities *Capabilities, code, source string) {
	for _, warning := range capabilities.Warnings {
		if warning.Code == code && warning.Source == source {
			return
		}
	}
	capabilities.Warnings = append(capabilities.Warnings, ProbeWarning{Code: code, Source: source})
}
