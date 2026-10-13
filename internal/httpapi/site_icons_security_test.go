package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/go-chi/chi/v5"
)

type iconSecurityStore struct {
	ControlStore
	profile controlstore.SiteProfile
}

func (s *iconSecurityStore) SiteProfiles(context.Context) ([]controlstore.SiteProfile, error) {
	return []controlstore.SiteProfile{s.profile}, nil
}

func TestLocalSiteIconSecurityHeadersAcrossResponses(t *testing.T) {
	dir := t.TempDir()
	// 无害标记用于证明原始 SVG 保持兼容；浏览器执行阻断需独立浏览器验收。
	content := `<svg xmlns="http://www.w3.org/2000/svg" onload="window.ncpIconExecuted=true"><script>window.ncpIconExecuted=true</script><rect width="10" height="10"/></svg>`
	path := filepath.Join(dir, "fixture.svg")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	api := &handler{siteAssetsDirectory: dir, controlStore: &iconSecurityStore{profile: controlstore.SiteProfile{ProjectID: "fixture", LocalIconName: "fixture.svg"}}}
	for _, tt := range []struct {
		name, method, header, value string
		status                      int
	}{
		{"document", http.MethodGet, "", "", 200},
		{"head", http.MethodHead, "", "", 200},
		{"cached", http.MethodGet, "If-Modified-Since", stamp.Format(http.TimeFormat), 304},
		{"range", http.MethodGet, "Range", "bytes=0-9", 206},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "http://ncp.test/api/v1/sites/fixture/icon", nil)
			rc := chi.NewRouteContext()
			rc.URLParams.Add("siteID", "fixture")
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
			if tt.header != "" {
				req.Header.Set(tt.header, tt.value)
			}
			res := httptest.NewRecorder()
			api.siteIcon(res, req)
			if res.Code != tt.status {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			assertIconPolicy(t, res)
			if (tt.method == http.MethodHead || tt.status == 304) && res.Body.Len() != 0 {
				t.Fatal("unexpected response body")
			}
			if tt.status == 206 && (res.Body.String() != content[:10] || !strings.HasPrefix(res.Header().Get("Content-Range"), "bytes 0-9/")) {
				t.Fatal("range response changed")
			}
			if tt.status == 200 && tt.method == http.MethodGet {
				if res.Body.String() != content {
					t.Fatal("SVG content changed")
				}
				if !strings.HasPrefix(res.Header().Get("Content-Type"), "image/svg+xml") {
					t.Fatal("wrong SVG content type")
				}
			}
		})
	}
}

func TestFallbackSiteIconUsesSameSecurityPolicy(t *testing.T) {
	res := httptest.NewRecorder()
	writeSiteIconFallback(res)
	assertIconPolicy(t, res)
	if res.Code != http.StatusOK || res.Header().Get("X-NCP-Icon-Fallback") != "true" {
		t.Fatal("fallback changed")
	}
}

func assertIconPolicy(t *testing.T, res *httptest.ResponseRecorder) {
	t.Helper()
	if got := res.Header().Get("Content-Security-Policy"); got != "default-src 'none'; style-src 'unsafe-inline'; sandbox" {
		t.Fatalf("unsafe CSP: %q", got)
	}
	if got := res.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("nosniff=%q", got)
	}
}

func TestProxySiteIconPolicyForSVGAndRaster(t *testing.T) {
	for _, ct := range []string{"image/svg+xml", "image/png"} {
		t.Run(ct, func(t *testing.T) {
			content := []byte("fixture")
			if ct == "image/svg+xml" {
				content = []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>window.ncpIconExecuted=true</script></svg>`)
			}
			client := &http.Client{Transport: siteIconRoundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{ct}}, Body: io.NopCloser(bytes.NewReader(content))}, nil
			})}
			api := &handler{siteIconClient: client}
			res := httptest.NewRecorder()
			api.siteIconProxy(res, httptest.NewRequest(http.MethodGet, "http://ncp.test/api/v1/sites/icon-proxy?url=http%3A%2F%2Fncp.test%3A8080%2Ficon", nil))
			if res.Code != 200 || res.Body.String() != string(content) {
				t.Fatal("proxy response changed")
			}
			assertIconPolicy(t, res)
		})
	}
}

func (s *iconSecurityStore) UpsertSiteProfile(_ context.Context, profile controlstore.SiteProfile) (controlstore.SiteProfile, error) {
	s.profile = profile
	return profile, nil
}

// Chromium 重放此契约；这里使用真实上传/读取 handler 验证契约，不启动网络服务。
func TestUploadedSiteIconMatchesBrowserSecurityContract(t *testing.T) {
	var fixture struct {
		Headers   map[string]string `json:"headers"`
		SVG       string            `json:"svg"`
		PNGBase64 string            `json:"pngBase64"`
	}
	raw, err := os.ReadFile("../../frontend/e2e/fixtures/site-icon-security.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	png, err := base64.StdEncoding.DecodeString(fixture.PNGBase64)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, contentType string
		body              []byte
	}{
		{"svg", "image/svg+xml", []byte(fixture.SVG)},
		{"png", "image/png", png},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &iconSecurityStore{profile: controlstore.SiteProfile{ProjectID: "fixture"}}
			api := &handler{siteAssetsDirectory: t.TempDir(), controlStore: store}
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			part, err := writer.CreateFormFile("icon", "fixture."+tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write(tc.body); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			routeRequest := func(method string, body io.Reader) *http.Request {
				req := httptest.NewRequest(method, "http://ncp.test/api/v1/sites/fixture/icon", body)
				rc := chi.NewRouteContext()
				rc.URLParams.Add("siteID", "fixture")
				return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
			}
			upload := routeRequest(http.MethodPost, &body)
			upload.Header.Set("Content-Type", writer.FormDataContentType())
			uploaded := httptest.NewRecorder()
			api.uploadSiteIcon(uploaded, upload)
			if uploaded.Code != http.StatusOK {
				t.Fatalf("upload status=%d body=%s", uploaded.Code, uploaded.Body.String())
			}
			if !strings.HasSuffix(store.profile.LocalIconName, "."+tc.name) {
				t.Fatalf("stored icon=%q", store.profile.LocalIconName)
			}
			res := httptest.NewRecorder()
			api.siteIcon(res, routeRequest(http.MethodGet, nil))
			if res.Code != http.StatusOK || !bytes.Equal(res.Body.Bytes(), tc.body) {
				t.Fatal("uploaded icon bytes changed")
			}
			if got := res.Header().Get("Content-Type"); got != tc.contentType {
				t.Fatalf("content type=%q", got)
			}
			assertIconPolicy(t, res)
			for key, want := range fixture.Headers {
				if got := res.Header().Get(key); got != want {
					t.Fatalf("browser contract %s=%q, handler=%q", key, want, got)
				}
			}
			for _, key := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Cache-Control"} {
				if fixture.Headers[key] == "" {
					t.Fatalf("browser contract missing %s", key)
				}
			}
		})
	}
}
