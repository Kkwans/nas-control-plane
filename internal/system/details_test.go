package system

import (
	"context"
	"strings"
	"testing"
)

func TestDetailsCollectorReturnsStableCollections(t *testing.T) {
	details, err := NewDetailsCollector().Collect(context.Background())
	if err != nil {
		t.Fatalf("collect details: %v", err)
	}
	if details.CollectedAt.IsZero() {
		t.Fatal("collectedAt must be populated")
	}
	if details.Warnings == nil ||
		details.Hardware.Sensors == nil ||
		details.Network.Interfaces == nil ||
		details.Network.Routes == nil ||
		details.Network.DNSServers == nil ||
		details.Network.ListeningPorts == nil ||
		details.Storage.Mounts == nil ||
		details.Storage.Disks == nil ||
		details.Storage.RAID == nil ||
		details.Proxy.System == nil ||
		details.Proxy.Associations == nil ||
		details.Proxy.Mihomo.Operations == nil ||
		details.Proxy.MihomoCapability.Evidence == nil ||
		details.Proxy.MihomoCapability.Warnings == nil ||
		details.Proxy.MihomoCapability.Controller.Operations == nil ||
		details.Tailscale.OverlayIPs == nil ||
		details.Tailscale.Evidence == nil ||
		details.Tailscale.Warnings == nil ||
		details.DNS.Nameservers == nil ||
		details.Control.Nodes == nil {
		t.Fatal("collection fields must serialize as arrays instead of null")
	}
}

func TestRedactProxyAddressRemovesCredentials(t *testing.T) {
	got := redactProxyAddress("http://alice:secret@127.0.0.1:7890")
	if strings.Contains(got, "alice") || strings.Contains(got, "secret") {
		t.Fatalf("credentials leaked in %q", got)
	}
	if got != "http://%2A%2A%2A@127.0.0.1:7890" {
		t.Fatalf("unexpected redacted address %q", got)
	}
}

func TestDecodeIPv4HexUsesLinuxRouteByteOrder(t *testing.T) {
	if got := decodeIPv4Hex("0105A8C0"); got != "192.168.5.1" {
		t.Fatalf("unexpected decoded gateway %q", got)
	}
}
