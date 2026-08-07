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
	"strconv"
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
	Name         string   `json:"name"`
	State        string   `json:"state"`
	LowerUp      bool     `json:"lowerUp"`
	LowerUpKnown bool     `json:"lowerUpKnown"`
	Addresses    []string `json:"addresses"`
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

// TailscaleContainerCLIResult 是容器内 tailscale CLI 的只读证据。Status 只在 CLI
// 成功返回 JSON 时填充；容器名称和错误码用于解释证据边界，不包含命令原文或密钥。
type TailscaleContainerCLIResult struct {
	ContainerID   string
	ContainerName string
	Status        []byte
	ErrorCode     string
}

type tailscaleContainerCLIEnvironment interface {
	TailscaleContainerCLI(context.Context) ([]TailscaleContainerCLIResult, error)
}

type tailscaleContainerCandidate struct {
	ID    string
	Name  string
	Image string
}

type interfaceLinkState struct {
	OperState    string
	LowerUp      bool
	LowerUpKnown bool
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

	interfaceLinkUp := false
	interfaceLinkKnown := false
	for _, item := range interfaces {
		if !isTailscaleInterface(item.Name) {
			continue
		}
		if result.Interface == "" {
			result.Interface = item.Name
		}
		linkState, linkUp, linkKnown := tailscaleInterfaceLinkState(item)
		if linkState != "unknown" || result.LinkState == "unknown" {
			result.LinkState = linkState
		}
		interfaceLinkUp = interfaceLinkUp || linkUp
		interfaceLinkKnown = interfaceLinkKnown || linkKnown
		for _, address := range item.Addresses {
			if normalized := normalizeIP(address); isTailscaleOverlayIP(normalized) {
				result.OverlayIPs = append(result.OverlayIPs, normalized)
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
	for _, item := range interfaces {
		if !isTailscaleInterface(item.Name) {
			continue
		}
		if item.LowerUpKnown {
			status := "down"
			if item.LowerUp {
				status = "up"
			}
			result.Evidence = append(result.Evidence, CapabilityEvidence{
				Source: "link-lower-up", Status: status, Detail: item.Name,
			})
		}
		if state := strings.ToLower(strings.TrimSpace(item.State)); state != "" {
			result.Evidence = append(result.Evidence, CapabilityEvidence{
				Source: "link-operstate", Status: state, Detail: item.Name,
			})
		}
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
	statusFound := false
	containerStatusConfirmed := false
	if source, ok := environment.(tailscaleContainerCLIEnvironment); ok {
		if evidence, err := source.TailscaleContainerCLI(ctx); err == nil {
			containerStatusConfirmed = mergeTailscaleContainerEvidence(&result, evidence)
			statusFound = containerStatusConfirmed || statusFound
		} else {
			result.Evidence = append(result.Evidence, CapabilityEvidence{
				Source: "docker-cli", Status: "unavailable", Detail: "container-cli-unavailable",
			})
		}
	} else if result.Interface != "" {
		// 只有已发现 Tailscale 网卡时才在兼容旧 Environment 的路径上调用 Docker CLI，
		// 避免普通无 Tailscale 主机额外产生探测命令；容器专用替身可覆盖上述扩展。
		if evidence, err := collectTailscaleContainerCLI(ctx, environment); err == nil {
			containerStatusConfirmed = mergeTailscaleContainerEvidence(&result, evidence)
			statusFound = containerStatusConfirmed || statusFound
		}
	}
	if _, lookPathErr := environment.LookPath("tailscale"); lookPathErr == nil {
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
	if len(result.OverlayIPs) > 0 {
		result.Evidence = append(result.Evidence, CapabilityEvidence{
			Source: "overlay-ip", Status: "confirmed", Detail: fmt.Sprintf("%d address(es)", len(result.OverlayIPs)),
		})
	}

	if result.HeartbeatState == "unknown" {
		if result.BackendState == "Running" || strings.EqualFold(result.BackendState, TailscaleBackendRunning) {
			result.HeartbeatState = "running-unknown"
		} else if result.BackendState != "" {
			result.HeartbeatState = strings.ToLower(result.BackendState)
		}
	}
	// unknown 链路状态不能冒充已连通；必须有明确的 up 证据。
	if !interfaceLinkKnown && result.LinkState == "up" {
		interfaceLinkUp = true
	}
	linkConfirmed := interfaceLinkUp || (result.Interface == "" && containerStatusConfirmed)
	result.Reachable = result.Online && len(result.OverlayIPs) > 0 && linkConfirmed
	result.State = tailscaleState(result)
	return result
}

func tailscaleInterfaceLinkState(item InterfaceSnapshot) (state string, linkUp, known bool) {
	operState := strings.ToLower(strings.TrimSpace(item.State))
	if operState == "" {
		operState = "unknown"
	}
	operKnown := operState != "unknown"
	if item.LowerUpKnown {
		known = true
		if !item.LowerUp || operState == "down" || operState == "lowerlayerdown" {
			return "down", false, true
		}
		if operKnown && operState != "up" {
			return operState, false, true
		}
		return "up", true, true
	}
	if operState == "up" {
		return "up", true, true
	}
	if operKnown {
		return operState, false, true
	}
	return "unknown", false, false
}

func mergeTailscaleContainerEvidence(result *TailscaleCapability, evidence []TailscaleContainerCLIResult) bool {
	if result == nil {
		return false
	}
	statusFound := false
	for _, item := range evidence {
		container := firstNonEmpty(item.ContainerName, item.ContainerID, "tailscale container")
		result.Detected = true
		result.Evidence = append(result.Evidence, CapabilityEvidence{
			Source: "docker-cli", Status: "detected", Detail: container,
		})
		if len(item.Status) == 0 {
			if item.ErrorCode != "" {
				result.Evidence = append(result.Evidence, CapabilityEvidence{
					Source: "tailscale-container-cli", Status: "unavailable", Detail: item.ErrorCode,
				})
			}
			continue
		}
		status, err := parseTailscaleStatus(item.Status)
		if err != nil {
			result.Warnings = append(result.Warnings, ProbeWarning{Code: "PROBE_RESPONSE_INVALID", Source: "tailscale container status"})
			continue
		}
		mergeTailscaleStatus(result, status)
		statusFound = true
		result.Evidence = append(result.Evidence, CapabilityEvidence{
			Source: "tailscale-container-cli", Status: "confirmed", Detail: "status-json",
		})
	}
	return statusFound
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
		if normalized := normalizeIP(address); isTailscaleOverlayIP(normalized) {
			result.OverlayIPs = append(result.OverlayIPs, normalized)
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

func normalizeIP(address string) string {
	trimmed := strings.TrimSpace(address)
	if ip := net.ParseIP(trimmed); ip != nil {
		return ip.String()
	}
	if parsed, _, err := net.ParseCIDR(trimmed); err == nil {
		return parsed.String()
	}
	return ""
}

// ProbeMihomo 只探测进程和控制器的 allowlisted 只读端点，不读取配置文件，也不记录密钥。
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
		probe, err := probeMihomoHTTP(ctx, environment, joinControllerPath(endpoint, "/version"))
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
		result.Controller.TokenConfigured = controllerTokenConfiguredFor(environment)
		result.Controller.Operations = confirmedMihomoOperations(result.Controller.Reachable, result.Controller.AuthRequired)
		if version := parseMihomoVersion(probe.Body); version != "" {
			result.Version = version
		}
		result.Evidence = append(result.Evidence, CapabilityEvidence{
			Source: "controller-api", Status: httpStatusEvidence(probe.StatusCode), Detail: "/version",
		})
		if result.Controller.Reachable && !result.Controller.AuthRequired {
			probeMihomoControllerOperations(ctx, environment, endpoint, &result)
		}
		break
	}
	if result.Controller.Detected {
		return result
	}
	result.State = CapabilityStateDegraded
	result.Warnings = append(result.Warnings, ProbeWarning{Code: "PROXY_CONTROLLER_UNAVAILABLE", Source: "mihomo-controller"})
	return result
}

func confirmedMihomoOperations(reachable, authRequired bool) []string {
	if !reachable || authRequired {
		return []string{}
	}
	return []string{string(MihomoOperationVersion), string(MihomoOperationHealth)}
}

func probeMihomoControllerOperations(ctx context.Context, environment Environment, endpoint string, result *MihomoCapability) {
	if result == nil {
		return
	}
	for _, item := range []struct {
		operation MihomoOperation
		path      string
	}{
		{operation: MihomoOperationTraffic, path: "/traffic"},
		{operation: MihomoOperationConnections, path: "/connections"},
		{operation: MihomoOperationProxies, path: "/proxies"},
	} {
		probe, err := probeMihomoHTTP(ctx, environment, joinControllerPath(endpoint, item.path))
		if err != nil {
			continue
		}
		result.Evidence = append(result.Evidence, CapabilityEvidence{
			Source: "controller-api", Status: httpStatusEvidence(probe.StatusCode), Detail: item.path,
		})
		if probe.StatusCode >= 200 && probe.StatusCode < 300 {
			result.Controller.Operations = appendUniqueOperation(result.Controller.Operations, item.operation)
		}
	}
	// /rules is evidence only. There is no safe read/write/backup/rollback
	// contract in the current controller client, so it is never an operation.
	if probe, err := probeMihomoHTTP(ctx, environment, joinControllerPath(endpoint, "/rules")); err == nil {
		result.Evidence = append(result.Evidence, CapabilityEvidence{
			Source: "controller-api", Status: httpStatusEvidence(probe.StatusCode), Detail: "/rules",
		})
	}
}

func appendUniqueOperation(values []string, operation MihomoOperation) []string {
	for _, value := range values {
		if value == string(operation) {
			return values
		}
	}
	return append(values, string(operation))
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
	return controllerTokenConfiguredFor(NewOSEnvironment())
}

func controllerTokenConfiguredFor(environment Environment) bool {
	// 只返回布尔能力，不返回环境变量名和值；值不会写入日志、RPC 或 HTTP 响应。
	for _, key := range []string{"NCP_MIHOMO_CONTROLLER_TOKEN", "MIHOMO_CONTROLLER_TOKEN", "CLASH_SECRET"} {
		if value, ok := lookupEnvironmentValue(environment, key); ok && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func configuredMihomoToken(environment Environment) string {
	for _, key := range []string{"NCP_MIHOMO_CONTROLLER_TOKEN", "MIHOMO_CONTROLLER_TOKEN", "CLASH_SECRET"} {
		if value, ok := lookupEnvironmentValue(environment, key); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func lookupEnvironmentValue(environment Environment, key string) (string, bool) {
	if source, ok := environment.(lookupEnvironment); ok {
		return source.LookupEnv(key)
	}
	return os.LookupEnv(key)
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

type authenticatedHTTPProbeEnvironment interface {
	HTTPGetWithToken(context.Context, string, string) (HTTPProbeResult, error)
}

func probeMihomoHTTP(ctx context.Context, environment Environment, endpoint string) (HTTPProbeResult, error) {
	if source, ok := environment.(authenticatedHTTPProbeEnvironment); ok {
		return source.HTTPGetWithToken(ctx, endpoint, configuredMihomoToken(environment))
	}
	return probeHTTP(ctx, environment, endpoint)
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

	if _, lookPathErr := environment.LookPath("resolvectl"); lookPathErr == nil {
		if output, commandErr := runWithTimeout(ctx, environment, "resolvectl", "status"); commandErr == nil && len(output) > 0 {
			result.Backend = DNSBackendSystemdResolved
			result.Detected = true
			result.State = CapabilityStateReadOnly
			result.ReadOnly = true
			result.DetectionSource = "resolvectl"
			result.ErrorCode = "DNS_WRITE_ADAPTER_UNAVAILABLE"
			return result
		}
	}
	if _, lookPathErr := environment.LookPath("nmcli"); lookPathErr == nil {
		if output, commandErr := runWithTimeout(ctx, environment, "nmcli", "general", "status"); commandErr == nil && len(output) > 0 {
			result.Backend = DNSBackendNetworkManager
			result.Detected = true
			result.State = CapabilityStateReadOnly
			result.ReadOnly = true
			result.DetectionSource = "nmcli"
			result.ErrorCode = "DNS_WRITE_ADAPTER_UNAVAILABLE"
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
	ASN             string    `json:"asn,omitempty"`
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
	result.ASN = parsePublicEgressASN(content)
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
			if isp == "" {
				isp = nestedPublicEgressMetadata(value, "connection", "isp", "org")
			}
			return ip.String(), country, region, isp
		}
	}
	return "", "", "", ""
}

func parsePublicEgressASN(content []byte) string {
	var value map[string]any
	if json.Unmarshal(content, &value) != nil {
		return ""
	}
	if asn := firstPublicEgressMetadata(value, "asn", "ASN", "asNumber", "as", "自治系统", "自治系统号"); asn != "" {
		return asn
	}
	return nestedPublicEgressMetadata(value, "connection", "asn")
}

func nestedPublicEgressMetadata(value map[string]any, objectKey string, keys ...string) string {
	nested, ok := value[objectKey].(map[string]any)
	if !ok {
		return ""
	}
	return firstPublicEgressMetadata(nested, keys...)
}

func firstPublicEgressMetadata(value map[string]any, keys ...string) string {
	for _, key := range keys {
		candidate := ""
		switch typed := value[key].(type) {
		case string:
			candidate = typed
		case float64:
			if typed == float64(int64(typed)) {
				candidate = strconv.FormatInt(int64(typed), 10)
			}
		case json.Number:
			candidate = typed.String()
		}
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
		link := readInterfaceLinkState(NewOSEnvironment(), item.Name)
		current := InterfaceSnapshot{
			Name: item.Name, State: link.OperState, LowerUp: link.LowerUp,
			LowerUpKnown: link.LowerUpKnown, Addresses: []string{},
		}
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

// TailscaleContainerCLI reads only container metadata and tailscale status JSON. The
// command sequence is fixed; no user input is interpolated into a shell command.
func (OSEnvironment) TailscaleContainerCLI(ctx context.Context) ([]TailscaleContainerCLIResult, error) {
	return collectTailscaleContainerCLI(ctx, NewOSEnvironment())
}

func (OSEnvironment) HTTPGet(ctx context.Context, endpoint string) (HTTPProbeResult, error) {
	return (OSEnvironment{}).httpGet(ctx, endpoint, "")
}

func (OSEnvironment) HTTPGetWithToken(ctx context.Context, endpoint, token string) (HTTPProbeResult, error) {
	return (OSEnvironment{}).httpGet(ctx, endpoint, token)
}

func (OSEnvironment) httpGet(ctx context.Context, endpoint, token string) (HTTPProbeResult, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return HTTPProbeResult{}, err
	}
	request.Header.Set("Accept", "application/json")
	if strings.TrimSpace(token) != "" {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
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
		link := readInterfaceLinkState(environment, name)
		result = append(result, InterfaceSnapshot{
			Name: name, State: link.OperState, LowerUp: link.LowerUp,
			LowerUpKnown: link.LowerUpKnown, Addresses: []string{},
		})
	}
	return result
}

func readInterfaceLinkState(environment Environment, name string) interfaceLinkState {
	if environment == nil {
		environment = NewOSEnvironment()
	}
	operState := firstNonEmpty(readEnvironmentTrimmed(environment, filepath.Join("/sys/class/net", name, "operstate")), "unknown")
	flags, err := environment.ReadFile(filepath.Join("/sys/class/net", name, "flags"))
	if err != nil {
		return interfaceLinkState{OperState: strings.ToLower(operState)}
	}
	value, parseErr := parseInterfaceFlags(string(flags))
	if parseErr != nil {
		return interfaceLinkState{OperState: strings.ToLower(operState)}
	}
	return interfaceLinkState{
		OperState: strings.ToLower(operState), LowerUp: value&0x10000 != 0, LowerUpKnown: true,
	}
}

func parseInterfaceFlags(value string) (uint64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("interface flags are empty")
	}
	if parsed, err := strconv.ParseUint(value, 0, 32); err == nil {
		return parsed, nil
	}
	return strconv.ParseUint(strings.TrimPrefix(strings.ToLower(value), "0x"), 16, 32)
}

func readEnvironmentTrimmed(environment Environment, path string) string {
	if environment == nil {
		return ""
	}
	content, err := environment.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimRight(string(content), "\x00"))
}

func collectTailscaleContainerCLI(ctx context.Context, environment Environment) ([]TailscaleContainerCLIResult, error) {
	if environment == nil {
		return nil, errors.New("docker environment is unavailable")
	}
	if _, err := environment.LookPath("docker"); err != nil {
		return []TailscaleContainerCLIResult{}, nil
	}
	output, err := runWithTimeout(ctx, environment, "docker", "ps", "--no-trunc", "--format", "{{.ID}}\t{{.Names}}\t{{.Image}}")
	if err != nil {
		return nil, err
	}
	candidates := parseTailscaleContainerCandidates(output)
	result := make([]TailscaleContainerCLIResult, 0, len(candidates))
	for _, candidate := range candidates {
		item := TailscaleContainerCLIResult{ContainerID: candidate.ID, ContainerName: candidate.Name}
		status, execErr := runWithTimeout(ctx, environment, "docker", "exec", candidate.ID, "tailscale", "status", "--json")
		if execErr != nil {
			item.ErrorCode = "TAILSCALE_CONTAINER_CLI_UNAVAILABLE"
		} else {
			item.Status = status
		}
		result = append(result, item)
	}
	return result, nil
}

func parseTailscaleContainerCandidates(content []byte) []tailscaleContainerCandidate {
	result := make([]tailscaleContainerCandidate, 0)
	seen := map[string]struct{}{}
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		candidate := tailscaleContainerCandidate{
			ID: strings.TrimSpace(fields[0]), Name: strings.TrimSpace(fields[1]),
		}
		if len(fields) > 2 {
			candidate.Image = strings.TrimSpace(fields[2])
		}
		if candidate.ID == "" {
			continue
		}
		identifier := strings.ToLower(candidate.Name + " " + candidate.Image)
		if !strings.Contains(identifier, "tailscale") {
			continue
		}
		if _, ok := seen[candidate.ID]; ok {
			continue
		}
		seen[candidate.ID] = struct{}{}
		result = append(result, candidate)
	}
	return result
}
