package system

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const maxMihomoConfigurationBytes = 8 * 1024 * 1024

// MihomoLocalProxy describes only the local listener used by NCP's controlled
// outbound checks. It never contains proxy authentication data.
type MihomoLocalProxy struct {
	Address string `json:"address"`
	Mode    string `json:"mode"`
}

// MihomoStrategySelection separates the selected policy group from the final
// leaf node. Provider is a local name, never a subscription URL.
type MihomoStrategySelection struct {
	Group        string `json:"group"`
	SelectedNode string `json:"selectedNode"`
	NodeType     string `json:"nodeType"`
	Provider     string `json:"provider"`
}

// MihomoNodeInspection contains the minimum non-secret endpoint metadata that
// is useful for explaining the active proxy route.
type MihomoNodeInspection struct {
	Server     string `json:"server"`
	Port       int    `json:"port"`
	ResolvedIP string `json:"resolvedIp"`
	Country    string `json:"country"`
	Region     string `json:"region"`
	ISP        string `json:"isp"`
	ASN        string `json:"asn"`
}

// MihomoInspection is the end-to-end, user-facing proxy chain snapshot.
// Controller credentials, UUIDs, passwords, subscription URLs and full raw
// controller responses are deliberately absent.
type MihomoInspection struct {
	Status       string                  `json:"status"`
	Capability   MihomoCapability        `json:"capability"`
	LocalProxy   MihomoLocalProxy        `json:"localProxy"`
	Strategy     MihomoStrategySelection `json:"strategy"`
	Node         MihomoNodeInspection    `json:"node"`
	PublicEgress PublicEgressResult      `json:"publicEgress"`
	CheckedAt    time.Time               `json:"checkedAt"`
	ExpiresAt    time.Time               `json:"expiresAt"`
	ErrorCode    string                  `json:"errorCode"`
}

type MihomoInspectionRequest struct {
	Force bool `json:"force"`
}

type mihomoRuntimeNode struct {
	Name     string
	Type     string
	Server   string
	Port     int
	Provider string
}

type mihomoRuntimeConfig struct {
	ConfigPath         string
	ControllerEndpoint string
	ControllerToken    string
	LocalProxyAddress  string
	Mode               string
	Nodes              map[string]mihomoRuntimeNode
}

type mihomoYAMLConfig struct {
	ExternalController string                        `yaml:"external-controller"`
	Secret             string                        `yaml:"secret"`
	MixedPort          int                           `yaml:"mixed-port"`
	HTTPPort           int                           `yaml:"port"`
	SOCKSPort          int                           `yaml:"socks-port"`
	Mode               string                        `yaml:"mode"`
	Proxies            []mihomoYAMLProxy             `yaml:"proxies"`
	ProxyProviders     map[string]mihomoYAMLProvider `yaml:"proxy-providers"`
}

type mihomoYAMLProvider struct {
	Type string `yaml:"type"`
	Path string `yaml:"path"`
}

type mihomoYAMLProxy struct {
	Name   string `yaml:"name"`
	Type   string `yaml:"type"`
	Server string `yaml:"server"`
	Port   int    `yaml:"port"`
}

type mihomoYAMLProviderDocument struct {
	Proxies []mihomoYAMLProxy `yaml:"proxies"`
}

func configuredMihomoConfigPath(environment Environment) string {
	if value, ok := lookupEnvironmentValue(environment, "NCP_MIHOMO_CONFIG_PATH"); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func loadMihomoRuntimeConfig(environment Environment, configPath string) (mihomoRuntimeConfig, error) {
	if environment == nil {
		environment = NewOSEnvironment()
	}
	configPath = filepath.Clean(strings.TrimSpace(configPath))
	if configPath == "." || !filepath.IsAbs(configPath) || configPath == string(filepath.Separator) {
		return mihomoRuntimeConfig{}, errors.New("MIHOMO_CONFIG_PATH_INVALID")
	}
	content, err := environment.ReadFile(configPath)
	if err != nil {
		return mihomoRuntimeConfig{}, errors.New("MIHOMO_CONFIG_UNAVAILABLE")
	}
	if len(content) == 0 || len(content) > maxMihomoConfigurationBytes {
		return mihomoRuntimeConfig{}, errors.New("MIHOMO_CONFIG_INVALID")
	}
	var raw mihomoYAMLConfig
	if err := yaml.Unmarshal(content, &raw); err != nil {
		return mihomoRuntimeConfig{}, errors.New("MIHOMO_CONFIG_INVALID")
	}
	endpoint, err := localMihomoControllerEndpoint(raw.ExternalController)
	if err != nil {
		return mihomoRuntimeConfig{}, err
	}
	result := mihomoRuntimeConfig{
		ConfigPath: configPath, ControllerEndpoint: endpoint,
		ControllerToken: strings.TrimSpace(raw.Secret), Mode: normalizedMihomoMode(raw.Mode),
		Nodes: map[string]mihomoRuntimeNode{},
	}
	result.LocalProxyAddress = localMihomoProxyAddress(raw.MixedPort, raw.HTTPPort, raw.SOCKSPort)
	appendMihomoRuntimeNodes(result.Nodes, raw.Proxies, "")

	providerNames := make([]string, 0, len(raw.ProxyProviders))
	for name := range raw.ProxyProviders {
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)
	for _, providerName := range providerNames {
		provider := raw.ProxyProviders[providerName]
		providerPath, ok := safeMihomoProviderPath(configPath, provider.Path)
		if !ok {
			continue
		}
		providerContent, readErr := environment.ReadFile(providerPath)
		if readErr != nil || len(providerContent) == 0 || len(providerContent) > maxMihomoConfigurationBytes {
			continue
		}
		var document mihomoYAMLProviderDocument
		if yaml.Unmarshal(providerContent, &document) != nil {
			continue
		}
		appendMihomoRuntimeNodes(result.Nodes, document.Proxies, providerName)
	}
	return result, nil
}

func localMihomoControllerEndpoint(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("MIHOMO_CONTROLLER_ENDPOINT_NOT_CONFIGURED")
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		parsed, err := parseControllerEndpoint(value)
		if err != nil {
			return "", errors.New("MIHOMO_CONTROLLER_ENDPOINT_INVALID")
		}
		host := parsed.Hostname()
		if isWildcardMihomoHost(host) {
			host = "127.0.0.1"
		}
		port := parsed.Port()
		if port == "" {
			return "", errors.New("MIHOMO_CONTROLLER_ENDPOINT_INVALID")
		}
		parsed.Host = net.JoinHostPort(host, port)
		parsed.Path = strings.TrimRight(parsed.Path, "/")
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return parsed.String(), nil
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return "", errors.New("MIHOMO_CONTROLLER_ENDPOINT_INVALID")
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return "", errors.New("MIHOMO_CONTROLLER_ENDPOINT_INVALID")
	}
	if isWildcardMihomoHost(host) {
		host = "127.0.0.1"
	}
	return (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, port)}).String(), nil
}

func isWildcardMihomoHost(value string) bool {
	value = strings.Trim(strings.TrimSpace(value), "[]")
	return value == "" || value == "0.0.0.0" || value == "::"
}

func localMihomoProxyAddress(mixedPort, httpPort, socksPort int) string {
	port := mixedPort
	if port <= 0 {
		port = httpPort
	}
	if port <= 0 {
		port = socksPort
	}
	if port <= 0 || port > 65535 {
		return ""
	}
	return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

func normalizedMihomoMode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "rule", "global", "direct":
		return value
	default:
		return "unknown"
	}
}

func safeMihomoProviderPath(configPath, providerPath string) (string, bool) {
	providerPath = strings.TrimSpace(providerPath)
	if providerPath == "" {
		return "", false
	}
	root := filepath.Dir(configPath)
	resolved := providerPath
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(root, resolved)
	}
	resolved = filepath.Clean(resolved)
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	extension := strings.ToLower(filepath.Ext(resolved))
	if extension != ".yaml" && extension != ".yml" {
		return "", false
	}
	return resolved, true
}

func appendMihomoRuntimeNodes(target map[string]mihomoRuntimeNode, values []mihomoYAMLProxy, provider string) {
	for _, value := range values {
		name := strings.TrimSpace(value.Name)
		nodeType := strings.TrimSpace(value.Type)
		server := strings.TrimSpace(value.Server)
		if name == "" || len(name) > 256 || len(nodeType) > 64 || server == "" || len(server) > 253 || value.Port < 0 || value.Port > 65535 {
			continue
		}
		target[name] = mihomoRuntimeNode{Name: name, Type: nodeType, Server: server, Port: value.Port, Provider: provider}
	}
}

func parseMihomoStrategy(content []byte, nodes map[string]mihomoRuntimeNode) (MihomoStrategySelection, error) {
	var document struct {
		Proxies map[string]struct {
			Type string `json:"type"`
			Now  string `json:"now"`
		} `json:"proxies"`
	}
	if json.Unmarshal(content, &document) != nil || len(document.Proxies) == 0 {
		return MihomoStrategySelection{}, errors.New("MIHOMO_PROXIES_RESPONSE_INVALID")
	}
	current := "GLOBAL"
	if _, ok := document.Proxies[current]; !ok {
		keys := make([]string, 0, len(document.Proxies))
		for name, item := range document.Proxies {
			if strings.TrimSpace(item.Now) != "" {
				keys = append(keys, name)
			}
		}
		sort.Strings(keys)
		if len(keys) == 0 {
			return MihomoStrategySelection{}, errors.New("MIHOMO_STRATEGY_UNAVAILABLE")
		}
		current = keys[0]
	}
	selection := MihomoStrategySelection{}
	visited := map[string]struct{}{}
	for depth := 0; depth < 12; depth++ {
		if _, exists := visited[current]; exists {
			return MihomoStrategySelection{}, errors.New("MIHOMO_STRATEGY_CYCLE")
		}
		visited[current] = struct{}{}
		item, ok := document.Proxies[current]
		if !ok || strings.TrimSpace(item.Now) == "" {
			selection.SelectedNode = current
			selection.NodeType = strings.TrimSpace(item.Type)
			if node, exists := nodes[current]; exists {
				selection.NodeType = firstNonEmpty(node.Type, selection.NodeType)
				selection.Provider = node.Provider
			}
			return selection, nil
		}
		selection.Group = current
		current = strings.TrimSpace(item.Now)
	}
	return MihomoStrategySelection{}, errors.New("MIHOMO_STRATEGY_DEPTH_EXCEEDED")
}

type MihomoInspector struct {
	environment Environment
	runtime     mihomoRuntimeConfig
	controller  *MihomoControllerClient
	egress      *PublicEgressDetector

	cacheMu sync.Mutex
	cached  MihomoInspection
}

func NewMihomoInspector(environment Environment, configPath, endpointOverride, tokenOverride, egressEndpoint, outboundProxy string) (*MihomoInspector, error) {
	if environment == nil {
		environment = NewOSEnvironment()
	}
	runtime, err := loadMihomoRuntimeConfig(environment, configPath)
	if err != nil {
		return nil, err
	}
	if value := strings.TrimSpace(endpointOverride); value != "" {
		if _, err := parseControllerEndpoint(value); err != nil {
			return nil, errors.New("MIHOMO_CONTROLLER_ENDPOINT_INVALID")
		}
		runtime.ControllerEndpoint = strings.TrimRight(value, "/")
	}
	if value := strings.TrimSpace(tokenOverride); value != "" {
		runtime.ControllerToken = value
	}
	controller, err := NewMihomoControllerClient(runtime.ControllerEndpoint, runtime.ControllerToken)
	if err != nil {
		return nil, err
	}
	egress, err := NewPublicEgressDetectorWithProxy(egressEndpoint, firstNonEmpty(strings.TrimSpace(outboundProxy), runtime.LocalProxyAddress))
	if err != nil {
		return nil, err
	}
	return &MihomoInspector{environment: environment, runtime: runtime, controller: controller, egress: egress}, nil
}

func (i *MihomoInspector) Probe(ctx context.Context) MihomoCapability {
	if i == nil {
		return MihomoCapability{State: CapabilityStateUnavailable, Controller: MihomoControllerCapability{Operations: []string{}}, Evidence: []CapabilityEvidence{}, Warnings: []ProbeWarning{{Code: "MIHOMO_INSPECTOR_UNAVAILABLE", Source: "mihomo"}}}
	}
	return probeMihomoWithToken(ctx, i.environment, i.runtime.ControllerToken, i.runtime.ControllerEndpoint)
}

func (i *MihomoInspector) Invoke(ctx context.Context, request MihomoInvokeRequest) (MihomoInvokeResult, error) {
	if i == nil || i.controller == nil {
		return MihomoInvokeResult{}, errors.New("MIHOMO_CONTROLLER_UNAVAILABLE")
	}
	return i.controller.Invoke(ctx, request)
}

func (i *MihomoInspector) Inspect(ctx context.Context, force bool) MihomoInspection {
	now := time.Now().UTC()
	if i == nil {
		return MihomoInspection{Status: CapabilityStateUnavailable, CheckedAt: now, ExpiresAt: now, ErrorCode: "MIHOMO_INSPECTOR_UNAVAILABLE"}
	}
	i.cacheMu.Lock()
	if !force && !i.cached.CheckedAt.IsZero() && now.Before(i.cached.ExpiresAt) {
		cached := i.cached
		i.cacheMu.Unlock()
		return cached
	}
	i.cacheMu.Unlock()

	result := MihomoInspection{
		Status:     CapabilityStateDegraded,
		LocalProxy: MihomoLocalProxy{Address: i.runtime.LocalProxyAddress, Mode: i.runtime.Mode},
		CheckedAt:  now, ExpiresAt: now.Add(time.Minute),
		PublicEgress: PublicEgressResult{Status: CapabilityStateUnavailable, DetectionSource: "deployment-config", ErrorCode: "PUBLIC_EGRESS_NOT_CHECKED"},
	}
	result.Capability = i.Probe(ctx)
	if !result.Capability.Controller.Reachable || (result.Capability.Controller.AuthRequired && !result.Capability.Controller.TokenConfigured) {
		result.ErrorCode = "MIHOMO_CONTROLLER_UNAVAILABLE"
		return i.storeMihomoInspection(result)
	}
	proxies, err := i.Invoke(ctx, MihomoInvokeRequest{Operation: MihomoOperationProxies})
	if err != nil {
		result.ErrorCode = "MIHOMO_PROXIES_UNAVAILABLE"
		return i.storeMihomoInspection(result)
	}
	// Proxy providers are refreshed by Mihomo independently from the Agent.
	// Re-read only the safe node metadata before resolving the live selection so
	// renamed quota/status nodes do not outlive the Agent's startup snapshot.
	runtimeNodes := i.currentMihomoRuntimeNodes()
	selection, err := parseMihomoStrategy(proxies.Data, runtimeNodes)
	if err != nil {
		result.ErrorCode = err.Error()
		return i.storeMihomoInspection(result)
	}
	result.Strategy = selection
	node, nodeFound := runtimeNodes[selection.SelectedNode]
	if nodeFound {
		result.Node.Server = node.Server
		result.Node.Port = node.Port
		result.Node.ResolvedIP = resolveMihomoServer(ctx, node.Server)
		result.Strategy.NodeType = firstNonEmpty(node.Type, result.Strategy.NodeType)
		result.Strategy.Provider = node.Provider
	}

	type metadataResult struct{ value PublicEgressResult }
	metadataChannel := make(chan metadataResult, 1)
	egressChannel := make(chan PublicEgressResult, 1)
	if result.Node.ResolvedIP != "" {
		go func(address string) { metadataChannel <- metadataResult{value: i.egress.LookupAddress(ctx, address)} }(result.Node.ResolvedIP)
	} else {
		metadataChannel <- metadataResult{value: PublicEgressResult{Status: CapabilityStateUnavailable, ErrorCode: "MIHOMO_NODE_ADDRESS_UNRESOLVED"}}
	}
	go func() { egressChannel <- i.egress.Detect(ctx) }()
	metadata := (<-metadataChannel).value
	result.PublicEgress = <-egressChannel
	if metadata.Status == CapabilityStateAvailable {
		result.Node.Country = metadata.Country
		result.Node.Region = metadata.Region
		result.Node.ISP = metadata.ISP
		result.Node.ASN = metadata.ASN
	}
	if result.PublicEgress.Status == CapabilityStateAvailable {
		result.Status = CapabilityStateAvailable
		result.ErrorCode = ""
	} else {
		result.ErrorCode = firstNonEmpty(result.PublicEgress.ErrorCode, "PUBLIC_EGRESS_UNAVAILABLE")
	}
	return i.storeMihomoInspection(result)
}

func (i *MihomoInspector) currentMihomoRuntimeNodes() map[string]mihomoRuntimeNode {
	if i == nil {
		return nil
	}
	refreshed, err := loadMihomoRuntimeConfig(i.environment, i.runtime.ConfigPath)
	if err == nil {
		return refreshed.Nodes
	}
	return i.runtime.Nodes
}

func (i *MihomoInspector) storeMihomoInspection(value MihomoInspection) MihomoInspection {
	i.cacheMu.Lock()
	i.cached = value
	i.cacheMu.Unlock()
	return value
}

func resolveMihomoServer(ctx context.Context, server string) string {
	server = strings.TrimSpace(server)
	if ip := net.ParseIP(server); ip != nil {
		return ip.String()
	}
	lookupContext, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(lookupContext, server)
	if err != nil {
		return ""
	}
	for _, address := range addresses {
		if address.IP != nil {
			return address.IP.String()
		}
	}
	return ""
}
