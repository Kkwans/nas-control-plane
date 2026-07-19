package caddy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAddPOCRoutePostsFixedRouteToScopedConfigPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/config/apps/http/servers/ncp-p0/routes" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		var route route
		if err := json.NewDecoder(request.Body).Decode(&route); err != nil {
			t.Fatalf("decode route: %v", err)
		}
		if route.ID != pocRouteID || len(route.Match) != 1 || len(route.Match[0].Path) != 1 || route.Match[0].Path[0] != pocRoutePath || len(route.Handle) != 1 || route.Handle[0].Handler != "static_response" || route.Handle[0].Body != pocRouteBody || !route.Terminal {
			t.Fatalf("route = %#v", route)
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL)

	if err := client.AddPOCRoute(context.Background()); err != nil {
		t.Fatalf("add route: %v", err)
	}
}

func TestDeletePOCRouteUsesIDScopedEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodDelete || request.URL.Path != "/id/"+pocRouteID {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL)

	if err := client.DeletePOCRoute(context.Background()); err != nil {
		t.Fatalf("delete route: %v", err)
	}
}

func TestNewClientRejectsNonLoopbackAdminEndpoint(t *testing.T) {
	_, err := NewClient("http://192.168.5.110:2019", http.DefaultClient)

	if ErrorCode(err) != "CADDY_ADMIN_ENDPOINT_INVALID" {
		t.Fatalf("error code = %q", ErrorCode(err))
	}
}

func newTestClient(t *testing.T, endpoint string) *Client {
	t.Helper()
	client, err := NewClient(endpoint, http.DefaultClient)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client
}
