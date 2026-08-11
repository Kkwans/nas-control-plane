package agentsocket

import (
	"net"
	"net/http"
	"net/url"
	"testing"
)

func TestNewWebProbeServiceBypassesEnvironmentProxy(t *testing.T) {
	service := newWebProbeService()
	transport, ok := service.client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatalf("web probe transport must bypass environment proxies: %#v", service.client.Transport)
	}
}

func TestValidateLocalProbeURLWithAddresses(t *testing.T) {
	localAddresses := []net.IP{net.ParseIP("192.168.5.110"), net.ParseIP("fd00::110")}
	tests := []struct {
		name    string
		target  string
		wantErr bool
	}{
		{name: "loopback", target: "http://127.0.0.1:3000/"},
		{name: "localhost", target: "http://localhost:3000/"},
		{name: "nas ipv4", target: "http://192.168.5.110:3210/"},
		{name: "nas ipv6", target: "http://[fd00::110]:3210/"},
		{name: "remote lan host", target: "http://192.168.5.111:3210/", wantErr: true},
		{name: "wildcard", target: "http://0.0.0.0:3210/", wantErr: true},
		{name: "remote hostname", target: "http://example.test:3210/", wantErr: true},
		{name: "credentials", target: "http://user:secret@127.0.0.1:3210/", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := url.Parse(test.target)
			if err != nil {
				t.Fatal(err)
			}
			err = validateLocalProbeURLWithAddresses(parsed, localAddresses)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateLocalProbeURLWithAddresses(%q) error = %v, wantErr = %v", test.target, err, test.wantErr)
			}
		})
	}
}
