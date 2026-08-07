package system

import (
	"context"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

type fakeUGOSDNSClient struct {
	config   []byte
	setCalls int
}

func (c *fakeUGOSDNSClient) GetGeneralConfig(context.Context) ([]byte, error) {
	return append([]byte(nil), c.config...), nil
}

func (c *fakeUGOSDNSClient) SetGeneralConfig(_ context.Context, config []byte) error {
	c.config = append([]byte(nil), config...)
	c.setCalls++
	return nil
}

func TestUGOSNetworkDNSControllerPreviewConfirmAndRollback(t *testing.T) {
	client := &fakeUGOSDNSClient{config: makeUGOSTestConfig([]string{"192.168.5.1"}, false)}
	backupDir := filepath.Join(t.TempDir(), "dns")
	controller := newUGOSNetworkDNSController(client, backupDir)

	preview, err := controller.Preview(context.Background(), DNSChangeRequest{Nameservers: []string{"223.5.5.5", "1.1.1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Backend != DNSBackendUGOSNetwork || preview.Before.Nameservers[0] != "192.168.5.1" || client.setCalls != 0 {
		t.Fatalf("preview = %#v, set calls = %d", preview, client.setCalls)
	}
	result, err := controller.Confirm(context.Background(), DNSChangeConfirmation{PreviewID: preview.PreviewID, Confirmed: true})
	if err != nil || !result.Applied || !result.RollbackAvailable || client.setCalls != 1 {
		t.Fatalf("confirm = %#v, error = %v, set calls = %d", result, err, client.setCalls)
	}
	nameservers, manual, err := parseUGOSDNSConfig(client.config)
	if err != nil || !manual || !equalStringSlices(nameservers, []string{"223.5.5.5", "1.1.1.1"}) {
		t.Fatalf("applied DNS = %#v, manual = %v, error = %v", nameservers, manual, err)
	}

	restarted := newUGOSNetworkDNSController(client, backupDir)
	rolledBack, err := restarted.Rollback(context.Background(), DNSRollbackRequest{ChangeID: result.ChangeID})
	if err != nil || rolledBack.Applied || rolledBack.RollbackAvailable || client.setCalls != 2 {
		t.Fatalf("rollback = %#v, error = %v, set calls = %d", rolledBack, err, client.setCalls)
	}
	nameservers, manual, err = parseUGOSDNSConfig(client.config)
	if err != nil || manual || !equalStringSlices(nameservers, []string{"192.168.5.1"}) {
		t.Fatalf("rolled back DNS = %#v, manual = %v, error = %v", nameservers, manual, err)
	}
}

func TestUGOSNetworkDNSControllerRejectsConcurrentChanges(t *testing.T) {
	client := &fakeUGOSDNSClient{config: makeUGOSTestConfig([]string{"192.168.5.1"}, true)}
	controller := newUGOSNetworkDNSController(client, t.TempDir())
	preview, err := controller.Preview(context.Background(), DNSChangeRequest{Nameservers: []string{"1.1.1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	client.config = makeUGOSTestConfig([]string{"9.9.9.9"}, true)
	_, err = controller.Confirm(context.Background(), DNSChangeConfirmation{PreviewID: preview.PreviewID, Confirmed: true})
	if err == nil || err.Error() != "DNS_SOURCE_CHANGED" || client.setCalls != 0 {
		t.Fatalf("confirm error = %v, set calls = %d", err, client.setCalls)
	}
}

func TestRewriteUGOSDNSConfigPreservesOtherFields(t *testing.T) {
	before := makeUGOSTestConfig([]string{"192.168.5.1"}, false)
	beforeProxy, ok, err := firstBytesField(before, 2)
	if err != nil || !ok {
		t.Fatal("missing proxy fixture")
	}
	after, err := rewriteUGOSDNSConfig(before, []string{"8.8.8.8"})
	if err != nil {
		t.Fatal(err)
	}
	afterProxy, ok, err := firstBytesField(after, 2)
	if err != nil || !ok || string(afterProxy) != string(beforeProxy) {
		t.Fatalf("proxy changed: before=%x after=%x error=%v", beforeProxy, afterProxy, err)
	}
	nameservers, manual, err := parseUGOSDNSConfig(after)
	if err != nil || !manual || !equalStringSlices(nameservers, []string{"8.8.8.8"}) {
		t.Fatalf("rewritten DNS = %#v, manual = %v, error = %v", nameservers, manual, err)
	}
}

func makeUGOSTestConfig(nameservers []string, manual bool) []byte {
	general := []byte{}
	general = protowire.AppendTag(general, 1, protowire.BytesType)
	general = protowire.AppendBytes(general, []byte("gateway-fixture"))
	for _, nameserver := range nameservers {
		general = protowire.AppendTag(general, 3, protowire.BytesType)
		general = protowire.AppendString(general, nameserver)
	}
	general = protowire.AppendTag(general, 4, protowire.VarintType)
	if manual {
		general = protowire.AppendVarint(general, 1)
	} else {
		general = protowire.AppendVarint(general, 0)
	}
	general = protowire.AppendTag(general, 6, protowire.VarintType)
	general = protowire.AppendVarint(general, 1)

	result := []byte{}
	result = protowire.AppendTag(result, 1, protowire.BytesType)
	result = protowire.AppendBytes(result, general)
	result = protowire.AppendTag(result, 2, protowire.BytesType)
	result = protowire.AppendBytes(result, []byte{0x0a, 0x00, 0x22, 0x00})
	return result
}
