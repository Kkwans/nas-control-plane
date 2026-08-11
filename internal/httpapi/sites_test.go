package httpapi

import (
	"context"
	"errors"
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
