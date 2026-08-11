package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/docker"
)

type siteProbeAgent struct {
	*fakeAgentClient
	mu      sync.Mutex
	probes  map[string]agentsocket.WebProbeResult
	targets []string
}

type siteIconRoundTripFunc func(*http.Request) (*http.Response, error)

func (function siteIconRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func (agent *siteProbeAgent) ProbeWeb(_ context.Context, _ string, target string) (agentsocket.WebProbeResult, error) {
	agent.mu.Lock()
	agent.targets = append(agent.targets, target)
	agent.mu.Unlock()
	for port, result := range agent.probes {
		if strings.Contains(target, ":"+port+"/") {
			return result, nil
		}
	}
	return agentsocket.WebProbeResult{}, errors.New("not a web endpoint")
}

func TestDiscoveredSitesProbesSpecificHostBinding(t *testing.T) {
	inventory := docker.Inventory{
		Projects: []docker.Project{{ID: "compose:agenthub", Name: "agenthub", State: "running"}},
		Containers: []docker.InventoryContainer{{
			ID: "agenthub", ProjectID: "compose:agenthub", ProjectName: "agenthub", State: "running",
			Ports: []docker.PortMapping{{HostIP: "192.168.5.110", PrivatePort: 3210, PublicPort: 3210, Protocol: "tcp"}},
		}},
	}
	agent := &siteProbeAgent{fakeAgentClient: &fakeAgentClient{}, probes: map[string]agentsocket.WebProbeResult{
		"3210": {URL: "http://192.168.5.110:3210/", Title: "AgentHub", ContentType: "text/html", StatusCode: 200},
	}}
	api := &handler{agent: agent, agentSocketPath: "/run/ncp/test.sock"}

	sites := api.discoveredSites(context.Background(), "192.168.5.110", inventory, nil, nil)
	if len(sites) != 1 || sites[0].Name != "AgentHub" || sites[0].Category != "AI 工具" {
		t.Fatalf("sites = %#v", sites)
	}
	if len(agent.targets) != 1 || agent.targets[0] != "http://192.168.5.110:3210/" {
		t.Fatalf("probe targets = %#v", agent.targets)
	}
}

func TestSiteProbeTargetsUseLoopbackForWildcardBinding(t *testing.T) {
	site := Site{ProjectID: "compose:web", Ports: []int{8088}}
	containers := []docker.InventoryContainer{{
		ProjectID: "compose:web",
		Ports:     []docker.PortMapping{{HostIP: "0.0.0.0", PublicPort: 8088, Protocol: "tcp"}},
	}}
	targets := siteProbeTargets(site, containers)
	if len(targets) != 1 || targets[0].URL != "http://127.0.0.1:8088/" {
		t.Fatalf("targets = %#v", targets)
	}
}

func TestDiscoverSitesReportsUnavailableProbe(t *testing.T) {
	inventory := docker.Inventory{
		Projects: []docker.Project{{ID: "compose:web", Name: "web", State: "running"}},
		Containers: []docker.InventoryContainer{{
			ProjectID: "compose:web",
			Ports:     []docker.PortMapping{{HostIP: "0.0.0.0", PublicPort: 8088, Protocol: "tcp"}},
		}},
	}
	api := &handler{agent: &fakeAgentClient{}}

	sites, summary := api.discoverSites(context.Background(), "192.168.5.110", inventory, nil, nil)
	if len(sites) != 0 {
		t.Fatalf("sites = %#v, want no unverified sites", sites)
	}
	if summary.Status != "unavailable" || summary.ProbeAvailable || summary.CandidateCount != 1 || summary.FailedCount != 1 || len(summary.Issues) != 1 {
		t.Fatalf("summary = %#v", summary)
	}
	if summary.Issues[0].ProjectID != "compose:web" || summary.Issues[0].Reason != "站点网页探测暂不可用" {
		t.Fatalf("issue = %#v", summary.Issues[0])
	}
}

func TestDiscoveredSitesCreatesIndependentHostNetworkEntries(t *testing.T) {
	inventory := docker.Inventory{
		Projects: []docker.Project{{ID: "compose:film-forest", Name: "film-forest", State: "running"}},
		Containers: []docker.InventoryContainer{{
			ID: "client-ui", ProjectID: "compose:film-forest", ProjectName: "film-forest", State: "running",
			Labels: map[string]string{"com.ncp.site.name": "影视森林"},
		}},
	}
	hostCandidates := []docker.HostSiteCandidate{
		{ProjectID: "compose:film-forest", ContainerID: "client-ui", Ports: []int{3000}},
		{ProjectID: "compose:film-forest", ContainerID: "admin-ui", Ports: []int{3001}},
		{ProjectID: "compose:film-forest", ContainerID: "client-server", Ports: []int{8080}},
		{ProjectID: "compose:film-forest", ContainerID: "admin-server", Ports: []int{8081}},
	}
	agent := &siteProbeAgent{fakeAgentClient: &fakeAgentClient{}, probes: map[string]agentsocket.WebProbeResult{
		"3000": {URL: "http://127.0.0.1:3000/", Title: "影视森林 - 影视资源聚合平台", ContentType: "text/html", StatusCode: 200},
		"3001": {URL: "http://127.0.0.1:3001/", Title: "影视森林 - 管理后台", ContentType: "text/html", StatusCode: 200},
		"8080": {URL: "http://127.0.0.1:8080/", Title: "Whitelabel Error Page", ContentType: "text/html", StatusCode: 404},
		"8081": {URL: "http://127.0.0.1:8081/", ContentType: "application/json", StatusCode: 200},
	}}
	api := &handler{agent: agent, agentSocketPath: "/run/ncp/test.sock"}

	sites := api.discoveredSites(context.Background(), "192.168.5.110", inventory, nil, hostCandidates)
	if len(sites) != 2 {
		t.Fatalf("sites = %#v, want exactly two verified HTML entries", sites)
	}
	if sites[0].ProjectID != "compose:film-forest" || sites[1].ProjectID != "compose:film-forest" {
		t.Fatalf("project IDs = %q, %q", sites[0].ProjectID, sites[1].ProjectID)
	}
	byID := map[string]Site{sites[0].ID: sites[0], sites[1].ID: sites[1]}
	if site := byID["compose:film-forest@3000"]; site.Name != "影视森林 - 影视资源聚合平台" || site.PrimaryPort != 3000 || site.LaunchURL != "http://192.168.5.110:3000/" {
		t.Fatalf("client site = %#v", site)
	}
	if site := byID["compose:film-forest@3001"]; site.Name != "影视森林 - 管理后台" || site.PrimaryPort != 3001 || site.LaunchURL != "http://192.168.5.110:3001/" {
		t.Fatalf("admin site = %#v", site)
	}
}

func TestDiscoveredSiteUsesSameOriginIconProxy(t *testing.T) {
	inventory := docker.Inventory{
		Projects: []docker.Project{{ID: "compose:agenthub", Name: "agenthub", State: "running"}},
		Containers: []docker.InventoryContainer{{
			ID: "agenthub", ProjectID: "compose:agenthub", ProjectName: "agenthub", State: "running",
			Ports: []docker.PortMapping{{HostIP: "192.168.5.110", PublicPort: 3210, Protocol: "tcp"}},
		}},
	}
	agent := &siteProbeAgent{fakeAgentClient: &fakeAgentClient{}, probes: map[string]agentsocket.WebProbeResult{
		"3210": {URL: "http://192.168.5.110:3210/", Title: "AgentHub", IconURL: "http://192.168.5.110:3210/favicon.svg", ContentType: "text/html", StatusCode: 200},
	}}
	api := &handler{agent: agent}

	sites := api.discoveredSites(context.Background(), "192.168.5.110", inventory, nil, nil)
	if len(sites) != 1 {
		t.Fatalf("sites = %#v", sites)
	}
	parsed, err := url.Parse(sites[0].IconURL)
	if err != nil || parsed.Path != "/api/v1/sites/icon-proxy" || parsed.Query().Get("url") != "http://192.168.5.110:3210/favicon.svg" {
		t.Fatalf("icon URL = %q", sites[0].IconURL)
	}
}

func TestSiteIconProxyServesSameHostImage(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00}
	client := &http.Client{Transport: siteIconRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "http://192.168.5.110:3210/favicon.png" {
			t.Fatalf("upstream URL = %q", request.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"image/png"}},
			Body:       io.NopCloser(bytes.NewReader(png)),
			Request:    request,
		}, nil
	})}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/sites/icon-proxy?url="+url.QueryEscape("http://192.168.5.110:3210/favicon.png"), nil)
	request.Host = "192.168.5.110:8760"
	response := httptest.NewRecorder()

	(&handler{siteIconClient: client}).siteIconProxy(response, request)

	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/png" || !strings.EqualFold(response.Header().Get("X-Content-Type-Options"), "nosniff") {
		t.Fatalf("response = %d %#v %q", response.Code, response.Header(), response.Body.Bytes())
	}
}

func TestSiteIconProxyFallsBackWhenUpstreamFails(t *testing.T) {
	client := &http.Client{Transport: siteIconRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("upstream unavailable")
	})}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/sites/icon-proxy?url="+url.QueryEscape("http://192.168.5.110:3210/favicon.png"), nil)
	request.Host = "192.168.5.110:8760"
	response := httptest.NewRecorder()

	(&handler{siteIconClient: client}).siteIconProxy(response, request)

	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/svg+xml" || response.Header().Get("X-NCP-Icon-Fallback") != "true" {
		t.Fatalf("response = %d %#v %q", response.Code, response.Header(), response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "<svg") {
		t.Fatalf("fallback body = %q", response.Body.String())
	}
}

func TestSiteIconProxyRejectsDifferentHost(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/sites/icon-proxy?url="+url.QueryEscape("http://127.0.0.1:8080/favicon.ico"), nil)
	request.Host = "192.168.5.110:8760"
	response := httptest.NewRecorder()

	(&handler{newRequestID: func() string { return "req-icon" }}).siteIconProxy(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}
