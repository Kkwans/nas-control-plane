package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestBrowserWriteOriginMatrix(t *testing.T) {
	cases := []struct {
		name, method, host string
		origin             []string
		metadata           string
		allow              bool
	}{
		{"CLI", "POST", "nas:8760", nil, "", true},
		{"LAN fallback", "POST", "nas:8760", []string{"http://nas:8760"}, "", true},
		{"different port", "POST", "nas:8760", []string{"http://nas:9999"}, "", false},
		{"cross site", "POST", "nas:8760", []string{"https://attacker.test"}, "", false},
		{"same origin metadata", "PUT", "nas:8760", []string{"http://nas:8760"}, "same-origin", true},
		{"none metadata", "DELETE", "nas:8760", nil, "none", true},
		{"same site rejected", "POST", "nas:8760", []string{"http://nas:8760"}, "same-site", false},
		{"cross site metadata", "POST", "nas:8760", nil, "cross-site", false},
		{"unknown metadata", "PATCH", "nas:8760", nil, "unexpected", false},
		{"null", "POST", "nas:8760", []string{"null"}, "same-origin", false},
		{"empty", "POST", "nas:8760", []string{""}, "none", false},
		{"duplicate", "POST", "nas:8760", []string{"http://nas:8760", "http://nas:8760"}, "same-origin", false},
		{"ipv6", "POST", "[::1]:8760", []string{"http://[::1]:8760"}, "", true},
		{"ipv6 wrong port", "POST", "[::1]:8760", []string{"http://[::1]:9999"}, "", false},
		{"lost port", "POST", "nas", []string{"http://nas:8760"}, "", false},
		{"explicit default port", "POST", "nas:80", []string{"http://nas"}, "", false},
		{"case exact", "POST", "NAS:8760", []string{"http://nas:8760"}, "", false},
		{"scheme is not compared", "POST", "nas:8760", []string{"https://nas:8760"}, "", true},
		{"native metadata precedence", "POST", "nas:8760", []string{"https://other.test"}, "same-origin", true},
	}
	for _, method := range []string{"GET", "HEAD", "OPTIONS"} {
		cases = append(cases, struct {
			name, method, host string
			origin             []string
			metadata           string
			allow              bool
		}{method, method, "nas:8760", []string{"null"}, "cross-site", true})
	}
	for _, origin := range []string{"http://nas:8760/", "http://nas:8760?", "http://nas:8760#", "http://u@nas:8760", "http://nas:8760,http://nas:8760", "http://nas:8760 http://nas:8760", "http://nas:", "http://nas:65536", "http://nas:-1", "ftp://nas:8760", "http://[broken]", "http://[::1%25eth0]", "http://nas\\evil", "http://nas:1:2"} {
		cases = append(cases, struct {
			name, method, host string
			origin             []string
			metadata           string
			allow              bool
		}{origin, "POST", "nas:8760", []string{origin}, "same-origin", false})
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			api := &handler{}
			h := api.protectBrowserWrites(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) }))
			request := httptest.NewRequest(tt.method, "http://"+tt.host+"/api/v1/example", nil)
			if tt.origin != nil {
				request.Header["Origin"] = tt.origin
			}
			request.Header.Set("Sec-Fetch-Site", tt.metadata)
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if called != tt.allow {
				t.Fatalf("called=%v status=%d", called, response.Code)
			}
			if !tt.allow {
				assertErrorCode(t, response, "HTTP_CROSS_ORIGIN_REJECTED")
			}
		})
	}
}

type untouchedOriginAuth struct{ Authenticator }
type unreadOriginBody struct{ t *testing.T }

func (b unreadOriginBody) Read([]byte) (int, error) {
	b.t.Fatal("rejected request body was read")
	return 0, io.EOF
}
func (unreadOriginBody) Close() error { return nil }

func TestAllUnsafeAPIRoutesRejectBeforeAuthAndBusiness(t *testing.T) {
	for _, authBackend := range []Authenticator{nil, &untouchedOriginAuth{}} {
		var logs bytes.Buffer
		h := NewHandler(Config{Auth: authBackend, RequestID: func() string { return "req-origin" }, Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
		checked := 0
		err := chi.Walk(h.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			if !strings.HasPrefix(route, "/api/v1/") || method == "GET" || method == "HEAD" || method == "OPTIONS" {
				return nil
			}
			checked++
			request := httptest.NewRequest(method, "http://nas:8760"+route, unreadOriginBody{t})
			request.Header.Set("Origin", "http://nas:9999")
			request.Header.Set("Content-Type", "multipart/form-data; boundary=test")
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "private-cookie"})
			request.Header.Set("Authorization", "private-authorization")
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if response.Code != 403 {
				t.Fatalf("%s %s = %d", method, route, response.Code)
			}
			var body ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code != "HTTP_CROSS_ORIGIN_REJECTED" || body.RequestID != "req-origin" || response.Header().Get("X-Request-ID") != "req-origin" || !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
				t.Fatalf("invalid contract: %+v", body)
			}
			if len(response.Result().Cookies()) != 0 {
				t.Fatal("rejection changed cookie")
			}
			for _, secret := range []string{"private-cookie", "private-authorization", "nas:9999"} {
				if strings.Contains(response.Body.String()+logs.String(), secret) {
					t.Fatal("rejection leaked request data")
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if checked < 47 {
			t.Fatalf("only %d routes covered", checked)
		}
		if !strings.Contains(logs.String(), `"error_code":"HTTP_CROSS_ORIGIN_REJECTED"`) {
			t.Fatal("missing correlated error log")
		}
	}
}

func TestBrowserOriginAuthenticationCompatibility(t *testing.T) {
	for _, origin := range []string{"", "http://example.com"} {
		t.Run("origin="+origin, func(t *testing.T) {
			service := openTestAuthService(t)
			agent := &fakeAgentClient{}
			h := NewHandler(Config{Auth: service, Agent: agent})
			perform := func(request *http.Request) *httptest.ResponseRecorder {
				if origin != "" {
					request.Header.Set("Origin", origin)
				}
				response := httptest.NewRecorder()
				h.ServeHTTP(response, request)
				return response
			}
			// 跨源 bootstrap 不得创建账号。
			denied := credentialsRequestFor(t, "POST", "/api/v1/auth/bootstrap")
			denied.Header.Set("Origin", "http://attacker.test")
			deniedResponse := httptest.NewRecorder()
			h.ServeHTTP(deniedResponse, denied)
			initialized, err := service.Initialized(context.Background())
			if err != nil || initialized || deniedResponse.Code != 403 {
				t.Fatal("rejected bootstrap changed state")
			}
			bootstrap := perform(credentialsRequestFor(t, "POST", "/api/v1/auth/bootstrap"))
			if bootstrap.Code != 201 {
				t.Fatal(bootstrap.Body.String())
			}
			login := perform(credentialsRequestFor(t, "POST", "/api/v1/auth/login"))
			if login.Code != 200 {
				t.Fatal(login.Body.String())
			}
			cookie := login.Result().Cookies()[0]
			deniedLogout := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
			deniedLogout.AddCookie(cookie)
			deniedLogout.Header.Set("Origin", "http://attacker.test")
			rejected := httptest.NewRecorder()
			h.ServeHTTP(rejected, deniedLogout)
			if rejected.Code != 403 || len(rejected.Result().Cookies()) != 0 {
				t.Fatal("rejected logout mutated cookie")
			}
			action := httptest.NewRequest("POST", "/api/v1/docker/containers/abc123/actions/restart", nil)
			action.AddCookie(cookie)
			if response := perform(action); response.Code != 200 {
				t.Fatalf("write status %d: %s", response.Code, response.Body.String())
			}
			logout := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
			logout.AddCookie(cookie)
			if response := perform(logout); response.Code != 204 {
				t.Fatal(response.Body.String())
			}
			if response := perform(action); response.Code != 401 {
				t.Fatalf("revoked session got %d", response.Code)
			}
			anonymous := httptest.NewRequest("POST", "/api/v1/docker/containers/abc123/actions/restart", nil)
			if response := perform(anonymous); response.Code != 401 {
				t.Fatalf("anonymous write got %d", response.Code)
			}
		})
	}
}

type originRoundTripper func(*http.Request) (*http.Response, error)

func (f originRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOriginProxyPreservesExternalAuthority(t *testing.T) {
	for _, preserve := range []bool{true, false} {
		api := &handler{}
		upstream := api.protectBrowserWrites(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
		target, _ := url.Parse("http://server:8750")
		proxy := httputil.NewSingleHostReverseProxy(target)
		director := proxy.Director
		proxy.Director = func(r *http.Request) {
			director(r)
			if !preserve {
				r.Host = "server:8750"
			}
		}
		proxy.Transport = originRoundTripper(func(r *http.Request) (*http.Response, error) {
			response := httptest.NewRecorder()
			upstream.ServeHTTP(response, r)
			return response.Result(), nil
		})
		request := httptest.NewRequest("POST", "http://nas:8760/api/v1/auth/logout", nil)
		request.Header.Set("Origin", "http://nas:8760")
		request.Header.Set("Forwarded", "host=attacker.test;proto=https")
		request.Header.Set("X-Forwarded-Host", "attacker.test")
		request.Header.Set("X-Forwarded-Proto", "https")
		request.Header.Set("X-Forwarded-Port", "9999")
		response := httptest.NewRecorder()
		proxy.ServeHTTP(response, request)
		want := 403
		if preserve {
			want = 204
		}
		if response.Code != want {
			t.Fatalf("preserve=%v got %d", preserve, response.Code)
		}
	}
	for file, want := range map[string]string{"../../deploy/console/nginx.conf": "proxy_set_header Host $http_host;", "../../frontend/vite.config.ts": "changeOrigin: false"} {
		content, err := os.ReadFile(file)
		if file == "../../deploy/console/nginx.conf" {
			_, apiBlock, found := strings.Cut(string(content), "location /api/ {")
			if !found {
				t.Fatal("missing API proxy block")
			}
			apiBlock, _, _ = strings.Cut(apiBlock, "}")
			content = []byte(apiBlock)
		}
		if err != nil || !strings.Contains(string(content), want) {
			t.Fatalf("external authority configuration %s: %v", file, err)
		}
	}
}

func TestOriginProtectionLeavesSafeRoutesAndBodiesUnchanged(t *testing.T) {
	h := NewHandler(Config{Agent: &fakeAgentClient{}, Auth: openTestAuthService(t)})
	for _, path := range []string{"/healthz", "/readyz", "/api/v1/auth/status"} {
		request := httptest.NewRequest("GET", path, nil)
		request.Header.Set("Origin", "null")
		request.Header.Set("Sec-Fetch-Site", "cross-site")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatalf("safe route %s = %d: %s", path, response.Code, response.Body.String())
		}
	}
	for _, contentType := range []string{"", "application/json", "multipart/form-data; boundary=test", "text/plain"} {
		request := httptest.NewRequest("POST", "http://nas:8760/api/v1/test", strings.NewReader("unchanged body"))
		request.Header.Set("Origin", "http://nas:8760")
		request.Header.Set("Content-Type", contentType)
		api := &handler{}
		response := httptest.NewRecorder()
		api.protectBrowserWrites(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != "unchanged body" || r.Header.Get("Content-Type") != contentType {
				t.Fatal("origin guard changed body or media type")
			}
			w.WriteHeader(204)
		})).ServeHTTP(response, request)
		if response.Code != 204 {
			t.Fatalf("content type %q got %d", contentType, response.Code)
		}
	}
}
