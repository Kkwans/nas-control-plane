package docker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/moby/moby/client"
)

type createNetworkRoundTripper func(*http.Request) (*http.Response, error)

func (f createNetworkRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// 所有请求均由内存传输处理，不连接 Docker，也不启动监听器。
func TestContainerCreateNetworkReuseContract(t *testing.T) {
	const matching = `{"Driver":"bridge","IPAM":{"Config":[{"Subnet":"172.30.0.0/24","Gateway":"172.30.0.1"}]}}`
	tests := []struct {
		name, driver, gateway, inspection string
		status                            int
		transportError                    bool
		subnet                            string
		wantError, wantNetworkCreate      bool
	}{
		{name: "matching explicit configuration", driver: "bridge", gateway: "172.30.0.1", inspection: matching},
		{name: "default bridge", inspection: matching},
		{name: "omitted gateway accepts existing", inspection: `{"Driver":"bridge","IPAM":{"Config":[{"Subnet":"172.30.0.0/24","Gateway":"172.30.0.254"}]}}`},
		{name: "explicit driver mismatch", driver: "macvlan", inspection: matching, wantError: true},
		{name: "default driver mismatch", inspection: `{"Driver":"macvlan","IPAM":{"Config":[{"Subnet":"172.30.0.0/24"}]}}`, wantError: true},
		{name: "gateway mismatch", gateway: "172.30.0.254", inspection: matching, wantError: true},
		{name: "gateway must match same subnet entry", gateway: "172.30.0.1", inspection: `{"Driver":"bridge","IPAM":{"Config":[{"Subnet":"172.30.0.0/24","Gateway":"172.30.0.254"},{"Subnet":"172.31.0.0/24","Gateway":"172.30.0.1"}]}}`, wantError: true},
		{name: "subnet mismatch", inspection: `{"Driver":"bridge","IPAM":{"Config":[{"Subnet":"172.31.0.0/24"}]}}`, wantError: true},
		{name: "later matching entry", gateway: "172.30.0.1", inspection: `{"Driver":"bridge","IPAM":{"Config":[{"Subnet":"172.31.0.0/24"},{"Subnet":"172.30.0.0/24","Gateway":"172.30.0.1"}]}}`},
		{name: "not found creates default bridge", status: 404, wantNetworkCreate: true},
		{name: "not found creates explicit settings", status: 404, driver: "macvlan", gateway: "172.30.0.1", wantNetworkCreate: true},
		{name: "forbidden does not create", status: 403, wantError: true},
		{name: "server failure does not create", status: 500, wantError: true},
		{name: "transport failure does not create", transportError: true, wantError: true},
		{name: "invalid inspection does not create", inspection: `{`, wantError: true},
		{name: "name only preserves attachment", subnet: "none", driver: "macvlan"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			var networkBody struct {
				Driver string
				IPAM   struct {
					Config []struct{ Subnet, Gateway string }
				}
			}
			api, err := client.New(client.WithHost("http://docker.invalid"), client.WithAPIVersion("1.55"), client.WithHTTPClient(&http.Client{Transport: createNetworkRoundTripper(func(r *http.Request) (*http.Response, error) {
				path := strings.TrimPrefix(r.URL.Path, "/v1.55")
				calls = append(calls, r.Method+" "+path)
				status, body := 200, ""
				switch r.Method + " " + path {
				case "GET /networks/demo-net":
					if tt.transportError {
						return nil, errors.New("fake transport failure")
					}
					body = tt.inspection
					if tt.status != 0 {
						status = tt.status
						body = `{"message":"fake inspect failure"}`
					}
				case "POST /networks/create":
					if err := json.NewDecoder(r.Body).Decode(&networkBody); err != nil {
						t.Fatal(err)
					}
					status, body = 201, `{"Id":"network-1"}`
				case "POST /containers/create":
					var payload struct {
						NetworkingConfig struct{ EndpointsConfig map[string]json.RawMessage }
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					if len(payload.NetworkingConfig.EndpointsConfig) != 1 || payload.NetworkingConfig.EndpointsConfig["demo-net"] == nil {
						t.Errorf("unexpected endpoint: %#v", payload)
					}
					status, body = 201, `{"Id":"container-1"}`
				case "GET /containers/container-1/json":
					body = `{"Id":"container-1","Name":"/demo","Config":{},"State":{"Running":false}}`
				default:
					t.Errorf("unexpected request %s %s", r.Method, path)
					status, body = 500, `{"message":"unexpected request"}`
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}))
			if err != nil {
				t.Fatal(err)
			}
			defer api.Close()
			subnet := "172.30.0.0/24"
			if tt.subnet == "none" {
				subnet = ""
			}
			spec := ContainerCreateSpec{Name: "demo", Image: "alpine:3.21", Network: &ContainerNetwork{Name: "demo-net", Driver: tt.driver, Subnet: subnet, Gateway: tt.gateway}}
			result, err := (&mobyContainerControlGateway{client: api}).CreateContainer(context.Background(), spec)
			if (err != nil) != tt.wantError {
				t.Fatalf("result=%#v error=%v wantError=%v", result, err, tt.wantError)
			}
			var want []string
			if subnet != "" {
				want = append(want, "GET /networks/demo-net")
			}
			if tt.wantNetworkCreate {
				want = append(want, "POST /networks/create")
			}
			if !tt.wantError {
				want = append(want, "POST /containers/create", "GET /containers/container-1/json")
				if result.ID != "container-1" {
					t.Errorf("result=%#v", result)
				}
			}
			if !reflect.DeepEqual(calls, want) {
				t.Errorf("calls=%v want=%v", calls, want)
			}
			if tt.wantNetworkCreate {
				driver := tt.driver
				if driver == "" {
					driver = "bridge"
				}
				if networkBody.Driver != driver || len(networkBody.IPAM.Config) != 1 {
					t.Fatalf("network body=%#v", networkBody)
				}
				if networkBody.IPAM.Config[0].Subnet != subnet || networkBody.IPAM.Config[0].Gateway != tt.gateway {
					t.Errorf("IPAM=%#v", networkBody.IPAM)
				}
			}
		})
	}
}
