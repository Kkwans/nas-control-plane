package httpapi

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/docker"
)

type siteProbeAgent struct {
	*fakeAgentClient
	probes map[string]agentsocket.WebProbeResult
}

func (agent *siteProbeAgent) ProbeWeb(_ context.Context, _ string, target string) (agentsocket.WebProbeResult, error) {
	for port, result := range agent.probes {
		if strings.Contains(target, ":"+port+"/") {
			return result, nil
		}
	}
	return agentsocket.WebProbeResult{}, errors.New("not a web endpoint")
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
