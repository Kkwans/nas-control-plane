package docker

import (
	"net/netip"
	"testing"

	mobynetwork "github.com/moby/moby/api/types/network"
)

func TestNetworkResourceExposesStableSelectionFields(t *testing.T) {
	summary := mobynetwork.Summary{Network: mobynetwork.Network{
		ID: "network-id", Name: "media-net", Driver: "bridge", Scope: "local", Internal: true,
		IPAM: mobynetwork.IPAM{Config: []mobynetwork.IPAMConfig{
			{Subnet: netip.MustParsePrefix("172.30.0.0/24"), Gateway: netip.MustParseAddr("172.30.0.1")},
		}},
	}}
	result := networkResource(summary)
	if result.ID != "network-id" || result.Name != "media-net" || !result.Internal {
		t.Fatalf("network = %#v", result)
	}
	if len(result.Subnets) != 1 || result.Subnets[0] != "172.30.0.0/24" || len(result.Gateways) != 1 || result.Gateways[0] != "172.30.0.1" {
		t.Fatalf("IPAM = %#v", result)
	}
}
