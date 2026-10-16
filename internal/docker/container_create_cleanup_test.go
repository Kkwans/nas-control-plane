package docker

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/moby/moby/client"
)

// 仅使用内存传输和虚拟时钟，不连接 Docker 或启动监听器。
func TestContainerCreateFailureNetworkCleanup(t *testing.T) {
	for _, tt := range []struct {
		name           string
		network        string
		cancelParent   bool
		cleanupFailure bool
		cleanupTimeout bool
	}{
		{name: "new network", network: "new"},
		{name: "cancelled parent", network: "new", cancelParent: true},
		{name: "cleanup failure preserves create error", network: "new", cleanupFailure: true},
		{name: "cleanup timeout preserves create error", network: "new", cleanupTimeout: true},
		{name: "cancelled parent cleanup timeout", network: "new", cancelParent: true, cleanupTimeout: true},
		{name: "existing network", network: "existing"},
		{name: "name only network", network: "name-only"},
		{name: "no network"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				createError := errors.New("original container create failure")
				var calls []string
				var cleanupContext context.Context
				api, err := client.New(client.WithHost("http://docker.invalid"), client.WithAPIVersion("1.55"), client.WithHTTPClient(&http.Client{Transport: createNetworkRoundTripper(func(r *http.Request) (*http.Response, error) {
					call := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/v1.55")
					calls = append(calls, call)
					status, body := 200, ""
					switch call {
					case "GET /networks/demo-net":
						status, body = 404, `{"message":"not found"}`
						if tt.network == "existing" {
							status, body = 200, `{"Driver":"bridge","IPAM":{"Config":[{"Subnet":"172.30.0.0/24"}]}}`
						}
					case "POST /networks/create":
						status, body = 201, `{"Id":"network-1"}`
					case "POST /containers/create":
						if tt.cancelParent {
							cancel()
						}
						return nil, createError
					case "DELETE /networks/network-1":
						cleanupContext = r.Context()
						deadline, ok := cleanupContext.Deadline()
						if !ok || deadline.Sub(time.Now()) != containerCreateCleanupTimeout {
							t.Fatalf("cleanup deadline=%v present=%v, want now + %v", deadline, ok, containerCreateCleanupTimeout)
						}
						if err := cleanupContext.Err(); err != nil {
							t.Fatalf("cleanup inherited parent cancellation: %v", err)
						}
						if r.URL.RawQuery != "" {
							t.Errorf("unexpected cleanup options: %q", r.URL.RawQuery)
						}
						if tt.cleanupTimeout {
							<-cleanupContext.Done()
							return nil, cleanupContext.Err()
						}
						if tt.cleanupFailure {
							status, body = 500, `{"message":"cleanup failed"}`
						} else {
							status = 204
						}
					default:
						t.Fatalf("unexpected request: %s", call)
					}
					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
				})}))
				if err != nil {
					t.Fatal(err)
				}
				defer api.Close()
				spec := ContainerCreateSpec{Name: "demo", Image: "alpine:3.21"}
				var want []string
				if tt.network != "" {
					spec.Network = &ContainerNetwork{Name: "demo-net"}
					if tt.network != "name-only" {
						spec.Network.Subnet = "172.30.0.0/24"
						want = append(want, "GET /networks/demo-net")
					}
				}
				if tt.network == "new" {
					want = append(want, "POST /networks/create")
				}
				want = append(want, "POST /containers/create")
				if tt.network == "new" {
					want = append(want, "DELETE /networks/network-1")
				}
				start := time.Now()
				result, err := (&mobyContainerControlGateway{client: api}).CreateContainer(ctx, spec)
				if !errors.Is(err, createError) {
					t.Errorf("error=%v, want original create error %v", err, createError)
				}
				if !reflect.DeepEqual(result, ContainerSnapshot{}) {
					t.Errorf("unexpected snapshot: %#v", result)
				}
				if !reflect.DeepEqual(calls, want) {
					t.Errorf("calls=%v, want %v", calls, want)
				}
				wantElapsed := time.Duration(0)
				if tt.cleanupTimeout {
					wantElapsed = containerCreateCleanupTimeout
				}
				if elapsed := time.Since(start); elapsed != wantElapsed {
					t.Errorf("elapsed=%v, want %v", elapsed, wantElapsed)
				}
				if cleanupContext != nil && cleanupContext.Err() == nil {
					t.Error("cleanup context was not released")
				}
			})
		})
	}
}
