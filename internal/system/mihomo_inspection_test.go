package system

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLoadMihomoRuntimeConfigReadsOnlySafeNodeFields(t *testing.T) {
	environment := &networkCapabilityEnvironment{files: map[string][]byte{
		"/config/config.yaml": []byte(`
external-controller: 0.0.0.0:9091
secret: controller-secret
mixed-port: 7890
mode: rule
proxies:
  - name: local-node
    type: ss
    server: node.example.test
    port: 443
    password: must-not-leak
proxy-providers:
  provider-a:
    type: file
    path: ./providers/provider-a.yaml
  escaped:
    type: file
    path: ../../outside.yaml
`),
		"/config/providers/provider-a.yaml": []byte(`
proxies:
  - name: provider-node
    type: trojan
    server: 203.0.113.10
    port: 8443
    password: must-not-leak
    uuid: must-not-leak
`),
		"/outside.yaml": []byte(`proxies: [{name: escaped-node, type: ss, server: 198.51.100.1, port: 443}]`),
	}}
	runtime, err := loadMihomoRuntimeConfig(environment, "/config/config.yaml")
	if err != nil {
		t.Fatalf("loadMihomoRuntimeConfig() error = %v", err)
	}
	if runtime.ControllerEndpoint != "http://127.0.0.1:9091" || runtime.LocalProxyAddress != "http://127.0.0.1:7890" || runtime.Mode != "rule" {
		t.Fatalf("runtime endpoints = %#v", runtime)
	}
	if runtime.Nodes["provider-node"].Provider != "provider-a" || runtime.Nodes["escaped-node"].Name != "" {
		t.Fatalf("runtime nodes = %#v", runtime.Nodes)
	}
	encoded, err := json.Marshal(MihomoInspection{
		LocalProxy: MihomoLocalProxy{Address: runtime.LocalProxyAddress, Mode: runtime.Mode},
		Node:       MihomoNodeInspection{Server: runtime.Nodes["provider-node"].Server, Port: runtime.Nodes["provider-node"].Port},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, forbidden := range []string{"controller-secret", "must-not-leak", "uuid", "subscription"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("inspection leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestParseMihomoStrategyResolvesSelectedProviderNode(t *testing.T) {
	nodes := map[string]mihomoRuntimeNode{
		"上海-01": {Name: "上海-01", Type: "trojan", Server: "203.0.113.20", Port: 443, Provider: "provider-a"},
	}
	selection, err := parseMihomoStrategy([]byte(`{"proxies":{"GLOBAL":{"type":"Selector","now":"节点选择"},"节点选择":{"type":"Fallback","now":"上海-01"},"上海-01":{"type":"Trojan","now":""}}}`), nodes)
	if err != nil {
		t.Fatalf("parseMihomoStrategy() error = %v", err)
	}
	if selection.Group != "节点选择" || selection.SelectedNode != "上海-01" || selection.NodeType != "trojan" || selection.Provider != "provider-a" {
		t.Fatalf("selection = %#v", selection)
	}
}

func TestCurrentMihomoRuntimeNodesRefreshesProviderMetadata(t *testing.T) {
	environment := &networkCapabilityEnvironment{files: map[string][]byte{
		"/config/config.yaml": []byte(`
external-controller: 0.0.0.0:9091
mixed-port: 7890
proxy-providers:
  provider-a:
    type: file
    path: ./providers/provider-a.yaml
`),
		"/config/providers/provider-a.yaml": []byte(`
proxies:
  - name: remaining-100
    type: vless
    server: old.example.test
    port: 443
`),
	}}
	cached, err := loadMihomoRuntimeConfig(environment, "/config/config.yaml")
	if err != nil {
		t.Fatalf("loadMihomoRuntimeConfig() error = %v", err)
	}
	inspector := &MihomoInspector{environment: environment, runtime: cached}
	environment.files["/config/providers/provider-a.yaml"] = []byte(`
proxies:
  - name: remaining-99
    type: vless
    server: current.example.test
    port: 8443
`)

	nodes := inspector.currentMihomoRuntimeNodes()
	if nodes["remaining-99"].Server != "current.example.test" || nodes["remaining-99"].Port != 8443 {
		t.Fatalf("current nodes = %#v", nodes)
	}
	if _, exists := nodes["remaining-100"]; exists {
		t.Fatalf("stale provider node remained after refresh: %#v", nodes)
	}
}

func TestLocalMihomoControllerEndpointRejectsMissingPort(t *testing.T) {
	if _, err := localMihomoControllerEndpoint("http://127.0.0.1"); err == nil {
		t.Fatal("controller endpoint without explicit port must fail")
	}
}
