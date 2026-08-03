package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	CapabilityStateAvailable   = "available"
	CapabilityStateUnavailable = "unavailable"
	CapabilityStateDegraded    = "degraded"
	CapabilityStateNotFound    = "not-found"
	CapabilityStateReadOnly    = "read-only"
	CapabilityStateUnknown     = "unknown"

	TailscaleBackendRunning = "running"
	TailscaleBackendStopped = "stopped"

	MihomoOperationVersion     MihomoOperation = "version"
	MihomoOperationTraffic     MihomoOperation = "traffic"
	MihomoOperationConnections MihomoOperation = "connections"
	MihomoOperationProxies     MihomoOperation = "proxies"
	MihomoOperationSelectProxy MihomoOperation = "select-proxy"
	MihomoOperationHealth      MihomoOperation = "health"
	MihomoOperationUnavailable MihomoOperation = "unavailable"

	DNSBackendSystemdResolved = "systemd-resolved"
	DNSBackendNetworkManager  = "networkmanager"
	DNSBackendStaticResolv    = "static-resolv-conf"
	DNSBackendUnknown         = "unknown"
)

type MihomoOperation string

// CapabilityEvidence 只保留探测来源和无敏感信息的摘要；不会携带命令原文、Token 或配置内容。
type CapabilityEvidence struct {
	Source string `json:"source"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// InterfaceSnapshot 是 Tailscale 判断链路与 overlay 地址时使用的最小网卡快照。
type InterfaceSnapshot struct {
	Name      string   `json:"name"`
	State     string   `json:"state"`
	Addresses []string `json:"addresses"`
}

type tailscaleStatusSnapshot struct {
	BackendState string
	Version      string
	Online       *bool
	OverlayIPs   []string
}

// TailscaleCapability 明确拆分控制面、overlay、链路和心跳证据。
// overlay 地址只用于判断 Tailscale，永远不会被当作公网出口地址。
type TailscaleCapability struct {
	Detected       bool                 `json:"detected"`
	State          string               `json:"state"`
	BackendState   string               `json:"backendState"`
	Version        string               `json:"version"`
	Interface      string               `json:"interface"`
	OverlayIPs     []string             `json:"overlayIps"`
	Online         bool                 `json:"online"`
	LinkState      string               `json:"linkState"`
	HeartbeatState string               `json:"heartbeatState"`
	Reachable      bool                 `json:"reachable"`
	Evidence       []CapabilityEvidence `json:"evidence"`
	Warnings       []ProbeWarning       `json:"warnings"`
}

// MihomoControllerCapability 暴露控制器是否可用及其受控能力，不暴露认证值。
type MihomoControllerCapability struct {
	Detected        bool     `json:"detected"`
	Endpoint        string   `json:"endpoint"`
	Reachable       bool     `json:"reachable"`
	AuthRequired    bool     `json:"authRequired"`
	TokenConfigured bool     `json:"tokenConfigured"`
	Operations      []string `json:"operations"`
	DetectionSource string   `json:"detectionSource"`
}

type MihomoCapability struct {
	Detected    bool                       `json:"detected"`
	State       string                     `json:"state"`
	ProcessName string                     `json:"processName"`
	Executable  string                     `json:"executable"`
	Version     string                     `json:"version"`
	Controller  MihomoControllerCapability `json:"controller"`
	Evidence    []CapabilityEvidence       `json:"evidence"`
	Warnings    []ProbeWarning             `json:"warnings"`
}

// HTTPProbeResult 是只读控制器探测的最小结果。Body 只在 Agent 内部解析，不向浏览器透传。
type HTTPProbeResult struct {
	StatusCode int
	Body       []byte
}

type httpProbeEnvironment interface {
	HTTPGet(context.Context, string) (HTTPProbeResult, error)
}

type interfaceSnapshotEnvironment interface {
	NetworkInterfaceSnapshots(context.Context) ([]InterfaceSnapshot, error)
}

type tailscaleAPIEnvironment interface {
	TailscaleLocalAPI(context.Context) ([]byte, error)
}

// ProbeTailscale 综合 CLI/API、overlay 地址、链路和心跳；单一 operstate 不足以判定 connected。
func ProbeTailscale(ctx context.Context, environment Environment, interfaces []InterfaceSnapshot) TailscaleCapability {
	result := TailscaleCapability{
		State:          CapabilityStateNotFound,
		LinkState:      "unknown",
		HeartbeatState: "unknown",
		OverlayIPs:     []string{},
		Evidence:       []CapabilityEvidence{},
		Warnings:       []ProbeWarning{},
	}
	if environment == nil {
		environment = NewOSEnvironment()
	}
	if err := ctx.Err(); err != nil {
		result.State = CapabilityStateUnavailable
		result.Warnings = append(result.Warnings, ProbeWarning{Code: "PROBE_CONTEXT_CANCELED", Source: "tailscale"})
		return result
	}

	for _, item := range interfaces {
		if !isTailscaleInterface(item.Name) {
			continue
		}
		if result.Interface == "" {
			result.Interface = item.Name
		}
		if item.State != "" {
			result.LinkState = strings.ToLower(item.State)
		}
		for _, address := range item.Addresses {
			if isTailscaleOverlayIP(address) {
				result.OverlayIPs = append(result.OverlayIPs, net.ParseIP(address).String())
			}
		}
	}
	result.OverlayIPs = sortedUnique(result.OverlayIPs)
	if result.Interface != "" {
		result.Detected = true
		result.Evidence = append(result.Evidence, CapabilityEvidence{
			Source: "overlay-interface", Status: "detected", Detail: result.Interface,
		})
	}
	if result.LinkState == "up" || result.LinkState == "unknown" && result.Interface != "" {
		result.Evidence = append(result.Evidence, CapabilityEvidence{
			Source: "link", Status: result.LinkState, Detail: result.Interface,
		})
	} else if result.Interface != "" {
		result.Evidence = append(result.Evidence, CapabilityEvidence{
			Source: "link", Status: "down", Detail: result.Interface,
		})
	}
	if len(result.OverlayIPs) > 0 {
		result.Evidence = append(result.Evidence, CapabilityEvidence{
			Source: "overlay-ip", Status: "confirmed", Detail: fmt.Sprintf("%d address(es)", len(result.OverlayIPs)),
		})
	}

	statusFound := false
	if environment.LookPath("tailscale") == nil {
		result.Detected = true
		output, err := runWithTimeout(ctx, environment, "tailscale", "status", "--json")
		if err == nil {
			status, parseErr := parseTailscaleStatus(output)
			if parseErr == nil {
				mergeTailscaleStatus(&result, status)
				statusFound = true
				result.Evidence = append(result.Evidence, CapabilityEvidence{
					Source: "tailscale-cli", Status: "confirmed", Detail: "status-json",
				})
			} else {
				result.Warnings = append(result.Warnings, ProbeWarning{Code: "PROBE_RESPONSE_INVALID", Source: "tailscale status"})
			}
		} else {
			result.Warnings = append(result.Warnings, ProbeWarning{Code: "PROBE_COMMAND_FAILED", Source: "tailscale status"})
		}
	} else {
		result.Evidence = append(result.Evidence, CapabilityEvidence{
			Source: "tailscale-cli", Status: "unavailable", Detail: "command-not-found",
		})
	}
	if !statusFound {
		if source, ok := environment.(tailscaleAPIEnvironment); ok {
			if output, err := source.TailscaleLocalAPI(ctx); err == nil {
				if status, parseErr := parseTailscaleStatus(output); parseErr == nil {
					mergeTailscaleStatus(&result, status)
					statusFound = true
					result.Detected = true
					result.Evidence = append(result.Evidence, CapabilityEvidence{
						Source: "tailscale-local-api", Status: "confirmed", Detail: "localapi-status",
					})
				}
			}
		}
	}

	if result.HeartbeatState == "unknown" {
		if result.BackendState == "Running" || strings.EqualFold(result.BackendState, TailscaleBackendRunning) {
			result.HeartbeatState = "running-unknown"
		} else if result.BackendState != "" {
			result.HeartbeatState = strings.ToLower(result.BackendState)
		}
	}
	// unknown 链路状态不能冒充已连通；必须有明确的 up 证据。
	result.Reachable = result.Online && len(result.OverlayIPs) > 0 && result.LinkState == "up"
	result.State = tailscaleState(result)
	return result
}

func mergeTailscaleStatus(result *TailscaleCapability, status tailscaleStatusSnapshot) {
	if result == nil {
		return
	}
	result.BackendState = status.BackendState
	result.Version = status.Version
	if status.Online != nil {
		result.Online = *status.Online
		if result.Online {
			result.HeartbeatState = "online"
		} else {
			result.HeartbeatState = "offline"
		}
	}
	for _, address := range status.OverlayIPs {
		if isTailscaleOverlayIP(address) {
			result.OverlayIPs = append(result.OverlayIPs, net.ParseIP(address).String())
		}
	}
	result.OverlayIPs = sortedUnique(result.OverlayIPs)
}

func tailscaleState(result TailscaleCapability) string {
	if !result.Detected {
		return CapabilityStateNotFound
	}
	if result.Reachable {
		return CapabilityStateAvailable
	}
	if strings.EqualFold(result.BackendState, "Stopped") || strings.EqualFold(result.BackendState, "NeedsLogin") || strings.EqualFold(result.BackendState, "NoState") {
		return CapabilityStateDegraded
	}
	if result.Interface != "" || result.BackendState != "" || len(result.OverlayIPs) > 0 {
		return CapabilityStateDegraded
	}
	return CapabilityStateUnknown
}

func parseTailscaleStatus(content []byte) (tailscaleStatusSnapshot, error) {
	var raw struct {
		BackendState string `json:"BackendState"`
		Version      string `json:"Version"`
		Self         *struct {
			Online       *bool    `json:"Online"`
			TailscaleIPs []string `json:"TailscaleIPs"`
		} `json:"Self"`
	}
	if err := json.Unmarshal(content, &raw); err != nil {
		return tailscaleStatusSnapshot{}, err
	}
	if raw.BackendState == "" && raw.Self == nil {
		return tailscaleStatusSnapshot{}, errors.New("tailscale status does not contain a backend or self record")
	}
	result := tailscaleStatusSnapshot{BackendState: raw.BackendState, Version: raw.Version, OverlayIPs: []string{}}
	if raw.Self != nil {
		result.Online = raw.Self.Online
		result.OverlayIPs = append(result.OverlayIPs, raw.Self.TailscaleIPs...)
	}
	return result, nil
}

func isTailscaleInterface(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "tailscale0" || strings.HasPrefix(name, "tailscale")
}

func isTailscaleOverlayIP(address string) bool {
	ip := net.ParseIP(strings.TrimSpace(address))
	if ip == nil {
		if parsed, _, err := net.ParseCIDR(strings.TrimSpace(address)); err == nil {
			ip = parsed
		}
	}
	if ip == nil {
		return false
	}
	_, ipv4Overlay, _ := net.ParseCIDR("100.64.0.0/10")
	_, ipv6Overlay, _ := net.ParseCIDR("fd7a:115c:a1e0::/48")
	return ipv4Overlay.Contains(ip) || ipv6Overlay.Contains(ip)
}

// ProbeMihomo 只探测进程和无认证 /version 控制器端点，不读取配置文件，也不记录密钥。
func ProbeMihomo(ctx context.Context, environment Environment, endpoints ...string) MihomoCapability {
	result := MihomoCapability{
		State:      CapabilityStateNotFound,
		Controller: MihomoControllerCapability{Operations: []string{}},
		Evidence:   []CapabilityEvidence{},
		Warnings:   []ProbeWarning{},
	}
	if environment == nil {
		environment = NewOSEnvironment()
	}
	if err := ctx.Err(); err != nil {
		result.State = CapabilityStateUnavailable
		result.Warnings = append(result.Warnings, ProbeWarning{Code: "PROBE_CONTEXT_CANCELED", Source: "mihomo"})
		return result
	}

	for _, path := range globSafe(environment, "/proc/[0-9]*/comm") {
		name := strings.ToLower(strings.TrimSpace(string(readFileSafe(environment, path))))
		if name != "mihomo" && name != "clash" && name != "clash-meta" {
			continue
		}
		result.Detected = true
		result.State = CapabilityStateAvailable
		result.ProcessName = name
		result.Evidence = append(result.Evidence, CapabilityEvidence{
			Source: "process", Status: "confirmed", Detail: name,
		})
		break
	}
	if !result.Detected {
		result.Evidence = append(result.Evidence, CapabilityEvidence{
			Source: "process", Status: "not-found", Detail: "mihomo-or-clash",
		})
		return result
	}

	for _, endpoint := range normalizedMihomoEndpoints(endpoints) {
		probe, err := probeHTTP(ctx, environment, joinControllerPath(endpoint, "/version"))
		if err != nil {
			continue
		}
		result.Controller.Endpoint = redactControllerEndpoint(endpoint)
		result.Controller.Detected = true
		result.Controller.DetectionSource = "controller-version"
		result.Controller.Reachable = probe.StatusCode >= 200 && probe.StatusCode < 300
		if probe.StatusCode == http.StatusUnauthorized || probe.StatusCode == http.StatusForbidden {
			result.Controller.AuthRequired = true
			result.Controller.Reachable = true
		}
		result.Controller.TokenConfigured = controllerTokenConfigured()
		result.Controller.Operations = mihomoOperations(result.Controller.Reachable)
		if version := parseMihomoVersion(probe.Body); version != "" {
			result.Version = version
		}
		result.Evidence = append(result.Evidence, CapabilityEvidence{
			Source: "controller-api", Status: httpStatusEvidence(probe.StatusCode), Detail: "/version",
		})
		break
	}
	if result.Controller.Detected {
		return result
	}
	result.State = CapabilityStateDegraded
	result.Warnings = append(result.Warnings, ProbeWarning{Code: "PROXY_CONTROLLER_UNAVAILABLE", Source: "mihomo-controller"})
	return result
}

func mihomoOperations(reachable bool) []string {
	if !reachable {
		return []string{}
	}
	return []string{
		string(MihomoOperationVersion),
		string(MihomoOperationHealth),
		string(MihomoOperationTraffic),
		string(MihomoOperationConnections),
		string(MihomoOperationProxies),
		string(MihomoOperationSelectProxy),
	}
}

func parseMihomoVersion(content []byte) string {
	var value struct {
		Version string `json:"version"`
		Meta    bool   `json:"meta"`
	}
	if json.Unmarshal(content, &value) == nil && value.Version != "" {
		return value.Version
	}
	return ""
}

func normalizedMihomoEndpoints(endpoints []string) []string {
	if len(endpoints) == 0 {
		endpoints = append(endpoints, configuredMihomoEndpoint())
		endpoints = append(endpoints,
			"http://127.0.0.1:9090",
			"http://127.0.0.1:9097",
			"http://127.0.0.1:9095",
		)
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		endpoint = strings.TrimSpace(endpoint)
		if endpoint == "" {
			continue
		}
		if _, ok := seen[endpoint]; ok {
			continue
		}
		if _, err := parseControllerEndpoint(endpoint); err != nil {
			continue
		}
		seen[endpoint] = struct{}{}
		result = append(result, strings.TrimRight(endpoint, "/"))
	}
	return result
}

func configuredMihomoEndpoint() string {
	for _, key := range []string{"NCP_MIHOMO_CONTROLLER_ENDPOINT", "MIHOMO_CONTROLLER_ENDPOINT", "CLASH_CONTROLLER_ENDPOINT"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func controllerTokenConfigured() bool {
	// 只返回布尔能力，不返回环境变量名和值；值不会写入日志、RPC 或 HTTP 响应。
	for _, key := range []string{"NCP_MIHOMO_CONTROLLER_TOKEN", "MIHOMO_CONTROLLER_TOKEN", "CLASH_SECRET"} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true
		}
	}
	return false
}

func parseControllerEndpoint(endpoint string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("controller endpoint must be an http(s) URL without credentials")
	}
	return parsed, nil
}

func redactControllerEndpoint(endpoint string) string {
	parsed, err := parseControllerEndpoint(endpoint)
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

func joinControllerPath(endpoint, path string) string {
	parsed, err := parseControllerEndpoint(endpoint)
	if err != nil {
		return ""
	}
	base := strings.TrimRight(parsed.Path, "/")
	parsed.Path = base + "/" + strings.TrimLeft(path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func httpStatusEvidence(status int) string {
	switch {
	case status >= 200 && status < 300:
		return "reachable"
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "auth-required"
	default:
		return "unavailable"
	}
}

func probeHTTP(ctx context.Context, environment Environment, endpoint string) (HTTPProbeResult, error) {
	if endpoint == "" {
		return HTTPProbeResult{}, errors.New("endpoint is invalid")
	}
	if source, ok := environment.(httpProbeEnvironment); ok {
		return source.HTTPGet(ctx, endpoint)
	}
	return HTTPProbeResult{}, errors.New("http probe source is unavailable")
}

func runWithTimeout(ctx context.Context, environment Environment, name string, args ...string) ([]byte, error) {
	commandContext, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	return environment.Run(commandContext, name, args...)
}

func globSafe(environment Environment, pattern string) []string {
	paths, err := environment.Glob(pattern)
	if err != nil {
		return []string{}
	}
	sort.Strings(paths)
	return paths
}

func readFileSafe(environment Environment, path string) []byte {
	content, err := environment.ReadFile(path)
	if err != nil {
		return nil
	}
	return content
}

// DNSCapability 描述可读取、可预览、可确认和可回滚的能力边界。
// static-resolv-conf 永远 ReadOnly，CanPreview/CanConfirm/CanRollback 均为 false。
type DNSCapability struct {
	Backend         string   `json:"backend"`
	Detected        bool     `json:"detected"`
	State           string   `json:"state"`
	ReadOnly        bool     `json:"readOnly"`
	CanRead         bool     `json:"canRead"`
	CanPreview      bool     `json:"canPreview"`
	CanConfirm      bool     `json:"canConfirm"`
	CanRollback     bool     `json:"canRollback"`
	Nameservers     []string `json:"nameservers"`
	DetectionSource string   `json:"detectionSource"`
	ErrorCode       string   `json:"errorCode"`
}

func ProbeDNS(ctx context.Context, environment Environment) DNSCapability {
	result := DNSCapability{
		Backend:         DNSBackendUnknown,
		State:           CapabilityStateUnavailable,
		Nameservers:     []string{},
		DetectionSource: "resolv.conf",
	}
	if environment == nil {
		environment = NewOSEnvironment()
	}
	if err := ctx.Err(); err != nil {
		result.ErrorCode = "DNS_PROBE_CANCELED"
		return result
	}
	content, err := environment.ReadFile("/etc/resolv.conf")
	if err == nil {
		result.Nameservers = parseNameservers(string(content))
		result.CanRead = true
	} else {
		result.ErrorCode = "DNS_SOURCE_UNAVAILABLE"
	}

	if environment.LookPath("resolvectl") == nil {
		if output, commandErr := runWithTimeout(ctx, environment, "resolvectl", "status"); commandErr == nil && len(output) > 0 {
			result.Backend = DNSBackendSystemdResolved
			result.Detected = true
			result.State = CapabilityStateAvailable
			result.CanPreview = true
			result.CanConfirm = true
			result.CanRollback = true
			result.DetectionSource = "resolvectl"
			result.ErrorCode = ""
			return result
		}
	}
	if environment.LookPath("nmcli") == nil {
		if output, commandErr := runWithTimeout(ctx, environment, "nmcli", "general", "status"); commandErr == nil && len(output) > 0 {
			result.Backend = DNSBackendNetworkManager
			result.Detected = true
			result.State = CapabilityStateAvailable
			result.CanPreview = true
			result.CanConfirm = true
			result.CanRollback = true
			result.DetectionSource = "nmcli"
			result.ErrorCode = ""
			return result
		}
	}
	if err == nil {
		result.Backend = DNSBackendStaticResolv
		result.Detected = true
		result.State = CapabilityStateReadOnly
		result.ReadOnly = true
		result.ErrorCode = "DNS_BACKEND_READ_ONLY"
		return result
	}
	return result
}

func parseNameservers(content string) []string {
	result := []string{}
	seen := map[string]struct{}{}
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "nameserver" {
			continue
		}
		if net.ParseIP(fields[1]) == nil {
			continue
		}
		if _, ok := seen[fields[1]]; ok {
			continue
		}
		seen[fields[1]] = struct{}{}
		result = append(result, fields[1])
	}
	return result
}

type DNSChangeRequest struct {
	Interface     string   `json:"interface"`
	ConnectionID  string   `json:"connectionId"`
	Nameservers   []string `json:"nameservers"`
	SearchDomains []string `json:"searchDomains"`
}

type DNSChangePreview struct {
	PreviewID         string    `json:"previewId"`
	Backend           string    `json:"backend"`
	Before            DNSState  `json:"before"`
	After             DNSState  `json:"after"`
	RequiresConfirm   bool      `json:"requiresConfirm"`
	RollbackAvailable bool      `json:"rollbackAvailable"`
	ExpiresAt         time.Time `json:"expiresAt"`
	ErrorCode         string    `json:"errorCode"`
}

type DNSState struct {
	Interface     string   `json:"interface"`
	ConnectionID  string   `json:"connectionId"`
	Nameservers   []string `json:"nameservers"`
	SearchDomains []string `json:"searchDomains"`
}

type DNSChangeConfirmation struct {
	PreviewID string `json:"previewId"`
	Confirmed bool   `json:"confirmed"`
}

type DNSChangeResult struct {
	ChangeID          string    `json:"changeId"`
	Backend           string    `json:"backend"`
	Applied           bool      `json:"applied"`
	RollbackAvailable bool      `json:"rollbackAvailable"`
	AppliedAt         time.Time `json:"appliedAt"`
	ErrorCode         string    `json:"errorCode"`
}

type DNSRollbackRequest struct {
	ChangeID string `json:"changeId"`
}

type DNSChangeController interface {
	Preview(context.Context, DNSChangeRequest) (DNSChangePreview, error)
	Confirm(context.Context, DNSChangeConfirmation) (DNSChangeResult, error)
	Rollback(context.Context, DNSRollbackRequest) (DNSChangeResult, error)
}

// ReadOnlyDNSController is the safe default when no managed DNS adapter was injected.
// It keeps the write lifecycle explicit without touching /etc/resolv.conf.
type ReadOnlyDNSController struct {
	Capability DNSCapability
}

func NewReadOnlyDNSController(capability DNSCapability) *ReadOnlyDNSController {
	return &ReadOnlyDNSController{Capability: capability}
}

func (c *ReadOnlyDNSController) Preview(context.Context, DNSChangeRequest) (DNSChangePreview, error) {
	code := "DNS_WRITE_ADAPTER_UNAVAILABLE"
	if c != nil && c.Capability.ReadOnly {
		code = "DNS_BACKEND_READ_ONLY"
	}
	backend := ""
	if c != nil {
		backend = c.Capability.Backend
	}
	return DNSChangePreview{Backend: backend, ErrorCode: code}, errors.New(code)
}

func (c *ReadOnlyDNSController) Confirm(context.Context, DNSChangeConfirmation) (DNSChangeResult, error) {
	code := "DNS_WRITE_ADAPTER_UNAVAILABLE"
	if c != nil && c.Capability.ReadOnly {
		code = "DNS_BACKEND_READ_ONLY"
	}
	backend := ""
	if c != nil {
		backend = c.Capability.Backend
	}
	return DNSChangeResult{Backend: backend, ErrorCode: code}, errors.New(code)
}

func (c *ReadOnlyDNSController) Rollback(context.Context, DNSRollbackRequest) (DNSChangeResult, error) {
	code := "DNS_WRITE_ADAPTER_UNAVAILABLE"
	if c != nil && c.Capability.ReadOnly {
		code = "DNS_BACKEND_READ_ONLY"
	}
	backend := ""
	if c != nil {
		backend = c.Capability.Backend
	}
	return DNSChangeResult{Backend: backend, ErrorCode: code}, errors.New(code)
}

// PublicEgressCapability 表示是否配置了部署端点；实际检测只能由用户显式触发。
type PublicEgressCapability struct {
	Configured         bool   `json:"configured"`
	Status             string `json:"status"`
	Endpoint           string `json:"endpoint"`
	RequiresUserAction bool   `json:"requiresUserAction"`
	DetectionSource    string `json:"detectionSource"`
	ErrorCode          string `json:"errorCode"`
}

type PublicEgressResult struct {
	Status          string    `json:"status"`
	Address         string    `json:"address"`
	Country         string    `json:"country,omitempty"`
	Region          string    `json:"region,omitempty"`
	ISP             string    `json:"isp,omitempty"`
	CheckedAt       time.Time `json:"checkedAt"`
	DetectionSource string    `json:"detectionSource"`
	ErrorCode       string    `json:"errorCode"`
}

type PublicEgressDetector struct {
	Endpoint string
	Client   *http.Client
}

func NewPublicEgressDetector(endpoint string) *PublicEgressDetector {
	return &PublicEgressDetector{Endpoint: strings.TrimSpace(endpoint), Client: &http.Client{Timeout: commandTimeout}}
}

func NewPublicEgressCapability(endpoint string) PublicEgressCapability {
	endpoint = strings.TrimSpace(endpoint)
	if _, err := parseControllerEndpoint(endpoint); err != nil {
		return PublicEgressCapability{
			Status:             CapabilityStateUnavailable,
			RequiresUserAction: true,
			DetectionSource:    "deployment-config",
			ErrorCode:          "PUBLIC_EGRESS_ENDPOINT_NOT_CONFIGURED",
		}
	}
	return PublicEgressCapability{
		Configured:         true,
		Status:             "not-checked",
		Endpoint:           redactControllerEndpoint(endpoint),
		RequiresUserAction: true,
		DetectionSource:    "deployment-config",
	}
}

func (d *PublicEgressDetector) Detect(ctx context.Context) PublicEgressResult {
	result := PublicEgressResult{Status: CapabilityStateUnavailable, DetectionSource: "deployment-config"}
	if d == nil || strings.TrimSpace(d.Endpoint) == "" {
		result.ErrorCode = "PUBLIC_EGRESS_ENDPOINT_NOT_CONFIGURED"
		return result
	}
	endpoint, err := parseControllerEndpoint(d.Endpoint)
	if err != nil {
		result.ErrorCode = "PUBLIC_EGRESS_ENDPOINT_INVALID"
		return result
	}
	if err := ctx.Err(); err != nil {
		result.ErrorCode = "PUBLIC_EGRESS_CHECK_CANCELED"
		return result
	}
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: commandTimeout}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		result.ErrorCode = "PUBLIC_EGRESS_ENDPOINT_INVALID"
		return result
	}
	response, err := client.Do(request)
	if err != nil {
		result.ErrorCode = "PUBLIC_EGRESS_ENDPOINT_UNAVAILABLE"
		return result
	}
	defer response.Body.Close()
	content, readErr := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if readErr != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		result.ErrorCode = "PUBLIC_EGRESS_ENDPOINT_UNAVAILABLE"
		return result
	}
	address, country, region, isp := parsePublicEgressResponse(content)
	if address == "" {
		result.ErrorCode = "PUBLIC_EGRESS_RESPONSE_INVALID"
		return result
	}
	result.Status = CapabilityStateAvailable
	result.Address = address
	result.Country = country
	result.Region = region
	result.ISP = isp
	result.CheckedAt = time.Now().UTC()
	return result
}

func parsePublicEgressAddress(content []byte) string {
	address, _, _, _ := parsePublicEgressResponse(content)
	return address
}

func parsePublicEgressResponse(content []byte) (address, country, region, isp string) {
	trimmed := strings.TrimSpace(string(content))
	if ip := net.ParseIP(strings.Trim(trimmed, "\"")); isPublicUnicast(ip) {
		return ip.String(), "", "", ""
	}
	var value map[string]any
	if json.Unmarshal(content, &value) != nil {
		return "", "", "", ""
	}
	for _, key := range []string{"ip", "address", "publicIp", "publicIP", "出口地址"} {
		candidate, _ := value[key].(string)
		if ip := net.ParseIP(strings.TrimSpace(candidate)); isPublicUnicast(ip) {
			country = firstPublicEgressMetadata(value, "country", "countryCode", "国家", "国家代码")
			region = firstPublicEgressMetadata(value, "region", "regionName", "地区", "区域")
			isp = firstPublicEgressMetadata(value, "isp", "org", "provider", "运营商")
			return ip.String(), country, region, isp
		}
	}
	return "", "", "", ""
}

func firstPublicEgressMetadata(value map[string]any, keys ...string) string {
	for _, key := range keys {
		candidate, _ := value[key].(string)
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			if len(candidate) > 128 {
				return candidate[:128]
			}
			return candidate
		}
	}
	return ""
}

func isPublicUnicast(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
		return false
	}
	reserved := []string{
		"100.64.0.0/10", // CGNAT / Tailscale IPv4 overlay range.
		"198.18.0.0/15",
		"192.0.0.0/24",
		"192.0.2.0/24",
		"198.51.100.0/24",
		"203.0.113.0/24",
		"240.0.0.0/4",
		"fd7a:115c:a1e0::/48", // Tailscale IPv6 overlay.
	}
	for _, block := range reserved {
		_, network, err := net.ParseCIDR(block)
		if err == nil && network.Contains(ip) {
			return false
		}
	}
	return true
}

// NetworkInterfaceSnapshots 在 OSEnvironment 上提供地址和链路快照；它是可选扩展接口，
// 因而不破坏已有 Environment 测试替身和调用方。
func (OSEnvironment) NetworkInterfaceSnapshots(context.Context) ([]InterfaceSnapshot, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	result := make([]InterfaceSnapshot, 0, len(interfaces))
	for _, item := range interfaces {
		addresses, _ := item.Addrs()
		current := InterfaceSnapshot{Name: item.Name, State: readTrimmed(filepath.Join("/sys/class/net", item.Name, "operstate")), Addresses: []string{}}
		for _, address := range addresses {
			value, _, parseErr := net.ParseCIDR(address.String())
			if parseErr == nil {
				current.Addresses = append(current.Addresses, value.String())
			}
		}
		result = append(result, current)
	}
	return result, nil
}

func (OSEnvironment) HTTPGet(ctx context.Context, endpoint string) (HTTPProbeResult, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return HTTPProbeResult{}, err
	}
	response, err := (&http.Client{Timeout: commandTimeout}).Do(request)
	if err != nil {
		return HTTPProbeResult{}, err
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if readErr != nil {
		return HTTPProbeResult{}, readErr
	}
	return HTTPProbeResult{StatusCode: response.StatusCode, Body: body}, nil
}

func (OSEnvironment) TailscaleLocalAPI(ctx context.Context) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://local-tailscale.invalid/localapi/v0/status", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout: commandTimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				dialer := &net.Dialer{Timeout: commandTimeout}
				return dialer.DialContext(ctx, "unix", "/var/run/tailscale/tailscaled.sock")
			},
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("tailscale local api status %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, 256*1024))
}

func interfaceSnapshots(environment Environment) []InterfaceSnapshot {
	if source, ok := environment.(interfaceSnapshotEnvironment); ok {
		result, err := source.NetworkInterfaceSnapshots(context.Background())
		if err == nil {
			return result
		}
	}
	result := []InterfaceSnapshot{}
	names, err := environment.NetworkInterfaces()
	if err != nil {
		return result
	}
	for _, name := range names {
		result = append(result, InterfaceSnapshot{
			Name:      name,
			State:     firstNonEmpty(readTrimmed(filepath.Join("/sys/class/net", name, "operstate")), "unknown"),
			Addresses: []string{},
		})
	}
	return result
}
