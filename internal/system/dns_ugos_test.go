package system

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

type fakeUGOSDNSClient struct {
	config   []byte
	setCalls int
	setError error
}

func (c *fakeUGOSDNSClient) GetGeneralConfig(context.Context) ([]byte, error) {
	return append([]byte(nil), c.config...), nil
}

func (c *fakeUGOSDNSClient) SetGeneralConfig(_ context.Context, config []byte) error {
	if c.setError != nil {
		c.setCalls++
		return c.setError
	}
	c.config = append([]byte(nil), config...)
	c.setCalls++
	return nil
}

func TestParseUGOSSingleBool(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		want    bool
		wantErr string
	}{
		{name: "reboot required", content: []byte{0x08, 0x01}, want: true},
		{name: "no reboot default", content: nil, want: false},
		{name: "no reboot explicit", content: []byte{0x08, 0x00}, want: false},
		{name: "malformed", content: []byte{0x08}, wantErr: "UGOS_DNS_RESPONSE_INVALID"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseUGOSSingleBool(test.content)
			if test.wantErr != "" {
				if err == nil || err.Error() != test.wantErr {
					t.Fatalf("parseUGOSSingleBool() error = %v", err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("parseUGOSSingleBool() = %v, %v", got, err)
			}
		})
	}
}

func TestValidateUGOSSetGeneralConfigResponseAcceptsBothRebootStates(t *testing.T) {
	for _, content := range [][]byte{nil, {0x08, 0x00}, {0x08, 0x01}} {
		if err := validateUGOSSetGeneralConfigResponse(content); err != nil {
			t.Fatalf("response %x was rejected: %v", content, err)
		}
	}
	if err := validateUGOSSetGeneralConfigResponse([]byte{0x08}); err == nil || err.Error() != "UGOS_DNS_RESPONSE_INVALID" {
		t.Fatalf("malformed response error = %v", err)
	}
}

func TestUGOSNetworkDNSControllerReportsVendorRejection(t *testing.T) {
	client := &fakeUGOSDNSClient{
		config:   makeUGOSTestConfig([]string{"192.168.5.1"}, true),
		setError: errors.New("UGOS_DNS_APPLY_REJECTED"),
	}
	controller := newUGOSNetworkDNSController(client, t.TempDir())
	preview, err := controller.Preview(context.Background(), DNSChangeRequest{Nameservers: []string{"1.1.1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := controller.Confirm(context.Background(), DNSChangeConfirmation{PreviewID: preview.PreviewID, Confirmed: true})
	if err == nil || err.Error() != "UGOS_DNS_APPLY_REJECTED" || result.ErrorCode != "UGOS_DNS_APPLY_REJECTED" || client.setCalls != 1 {
		t.Fatalf("confirm = %#v, error = %v, set calls = %d", result, err, client.setCalls)
	}
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

func TestUGOSNetworkDNSControllerReadsManagedState(t *testing.T) {
	client := &fakeUGOSDNSClient{config: makeUGOSTestConfig([]string{"192.168.5.1", "240c::6666"}, true)}
	controller := newUGOSNetworkDNSController(client, t.TempDir())

	state, err := controller.CurrentDNSState(context.Background())
	if err != nil || !equalStringSlices(state.Nameservers, []string{"192.168.5.1", "240c::6666"}) || len(state.SearchDomains) != 0 {
		t.Fatalf("managed state = %#v, error = %v", state, err)
	}
}

func TestUGOSNetworkDNSControllerRejectsMoreThanTwoServers(t *testing.T) {
	client := &fakeUGOSDNSClient{config: makeUGOSTestConfig([]string{"192.168.5.1"}, true)}
	controller := newUGOSNetworkDNSController(client, t.TempDir())

	preview, err := controller.Preview(context.Background(), DNSChangeRequest{Nameservers: []string{"1.1.1.1", "8.8.8.8", "9.9.9.9"}})
	if err == nil || err.Error() != "UGOS_DNS_SERVER_LIMIT" || preview.ErrorCode != "UGOS_DNS_SERVER_LIMIT" || client.setCalls != 0 {
		t.Fatalf("preview = %#v, error = %v, set calls = %d", preview, err, client.setCalls)
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

func TestParseUGOSDNSConfigUsesVendorDNSModeEnum(t *testing.T) {
	tests := []struct {
		name   string
		mode   uint64
		manual bool
	}{
		{name: "automatic", mode: ugosDNSModeAuto, manual: false},
		{name: "manual", mode: ugosDNSModeManual, manual: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := makeUGOSTestConfigWithMode([]string{"192.168.5.1"}, test.mode)
			_, manual, err := parseUGOSDNSConfig(config)
			if err != nil || manual != test.manual {
				t.Fatalf("manual = %v, want %v, error = %v", manual, test.manual, err)
			}
		})
	}
}

func TestRewriteUGOSDNSConfigWritesManualMode(t *testing.T) {
	after, err := rewriteUGOSDNSConfig(makeUGOSTestConfig([]string{"192.168.5.1"}, false), []string{"1.1.1.1"})
	if err != nil {
		t.Fatal(err)
	}
	general, ok, err := firstBytesField(after, 1)
	if err != nil || !ok {
		t.Fatalf("general config missing: %v", err)
	}
	mode, ok, err := firstVarintField(general, 4)
	if err != nil || !ok || mode != ugosDNSModeManual {
		t.Fatalf("dns mode = %d, want %d, present = %v, error = %v", mode, ugosDNSModeManual, ok, err)
	}
}

func makeUGOSTestConfig(nameservers []string, manual bool) []byte {
	mode := uint64(ugosDNSModeAuto)
	if manual {
		mode = ugosDNSModeManual
	}
	return makeUGOSTestConfigWithMode(nameservers, mode)
}

func makeUGOSTestConfigWithMode(nameservers []string, mode uint64) []byte {
	general := []byte{}
	general = protowire.AppendTag(general, 1, protowire.BytesType)
	general = protowire.AppendBytes(general, []byte("gateway-fixture"))
	for _, nameserver := range nameservers {
		general = protowire.AppendTag(general, 3, protowire.BytesType)
		general = protowire.AppendString(general, nameserver)
	}
	general = protowire.AppendTag(general, 4, protowire.VarintType)
	general = protowire.AppendVarint(general, mode)
	general = protowire.AppendTag(general, 6, protowire.VarintType)
	general = protowire.AppendVarint(general, 1)

	result := []byte{}
	result = protowire.AppendTag(result, 1, protowire.BytesType)
	result = protowire.AppendBytes(result, general)
	result = protowire.AppendTag(result, 2, protowire.BytesType)
	result = protowire.AppendBytes(result, []byte{0x0a, 0x00, 0x22, 0x00})
	return result
}

func firstVarintField(content []byte, target protowire.Number) (uint64, bool, error) {
	for len(content) > 0 {
		number, kind, tagLength := protowire.ConsumeTag(content)
		if tagLength < 0 {
			return 0, false, errors.New("malformed protobuf tag")
		}
		valueLength := protowire.ConsumeFieldValue(number, kind, content[tagLength:])
		if valueLength < 0 {
			return 0, false, errors.New("malformed protobuf value")
		}
		if number == target && kind == protowire.VarintType {
			value, consumed := protowire.ConsumeVarint(content[tagLength : tagLength+valueLength])
			if consumed < 0 {
				return 0, false, errors.New("malformed protobuf varint")
			}
			return value, true, nil
		}
		content = content[tagLength+valueLength:]
	}
	return 0, false, nil
}
