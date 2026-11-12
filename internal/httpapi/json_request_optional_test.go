package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/Kkwans/nas-control-plane/internal/system"
)

func TestJSONMIMEOptionalInspectUsesActualBody(t *testing.T) {
	for _, tc := range []struct {
		name, payload, contentType string
		length                     int64
		status                     int
		force                      bool
	}{
		{"empty-unknown-length", "", "", -1, 200, false},
		{"empty-zero-length", "", "", 0, 200, false},
		{"empty-positive-length", "", "text/plain", 100, 200, false},
		{"nonempty-zero-length-force", `{"force":true}`, "application/json", 0, 200, true},
		{"nonempty-unknown-length-force", `{"force":true}`, "application/json", -1, 200, true},
		{"nonempty-known-length", `{"force":false}`, "application/json", 15, 200, false},
		{"nonempty-zero-length-missing-mime", `{"force":true}`, "", 0, 415, false},
		{"nonempty-unknown-length-missing-mime", `{"force":true}`, "", -1, 415, false},
		{"nonempty-known-length-plain", `{"force":true}`, "text/plain", 14, 415, false},
		{"whitespace-not-empty", " \t\n", "application/json", 0, 400, false},
		{"whitespace-missing-mime", " ", "", 0, 415, false},
		{"malformed-not-empty", "{", "application/json", -1, 400, false},
		{"unknown-field", `{"extra":true}`, "application/json", 0, 400, false},
		{"multiple-values", `{} {}`, "application/json", -1, 400, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent := &systemHTTPAgent{fakeAgentClient: &fakeAgentClient{}}
			h := NewHandler(Config{Agent: agent, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			request := httptest.NewRequest("POST", "/api/v1/proxy/mihomo/inspect", nil)
			spy := &jsonMIMEReadSpy{reader: strings.NewReader(tc.payload)}
			request.Body, request.ContentLength = spy, tc.length
			if tc.contentType != "" {
				request.Header.Set("Content-Type", tc.contentType)
			}
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if tc.status == 200 {
				if response.Code != 200 || !agent.inspectionCalled || agent.inspectionForce != tc.force {
					t.Fatalf("inspect status=%d called=%v force=%v body=%s", response.Code, agent.inspectionCalled, agent.inspectionForce, response.Body.String())
				}
			} else {
				code := "CONTROL_INPUT_INVALID"
				if tc.status == 415 {
					code = "HTTP_UNSUPPORTED_MEDIA_TYPE"
				}
				assertJSONMIMEError(t, response, tc.status, code)
				if agent.inspectionCalled {
					t.Fatal("invalid inspect body called Agent")
				}
			}
			if tc.status == 415 && (spy.bytes != 1 || spy.maxRequest != 1) {
				t.Fatalf("MIME rejection probe bytes=%d requested=%d", spy.bytes, spy.maxRequest)
			}
			if spy.closed {
				t.Fatal("inspect unexpectedly closed request body")
			}
			if err := request.Body.Close(); err != nil || !spy.closed {
				t.Fatal("inspect did not preserve underlying body Close")
			}
		})
	}
	for _, body := range []io.ReadCloser{nil, http.NoBody} {
		agent := &systemHTTPAgent{fakeAgentClient: &fakeAgentClient{}}
		h := NewHandler(Config{Agent: agent, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
		request := httptest.NewRequest("POST", "/api/v1/proxy/mihomo/inspect", nil)
		request.Body, request.ContentLength = body, 100
		request.Header.Set("Content-Type", "text/plain")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != 200 || !agent.inspectionCalled || agent.inspectionForce {
			t.Fatalf("nil/NoBody inspect=%d called=%v force=%v", response.Code, agent.inspectionCalled, agent.inspectionForce)
		}
	}
}

func TestJSONMIMEOptionalInspectReadFailureDoesNotCallAgent(t *testing.T) {
	for _, n := range []int{0, 1} {
		t.Run(string(rune('0'+n))+"-byte-read-error", func(t *testing.T) {
			agent := &systemHTTPAgent{fakeAgentClient: &fakeAgentClient{}}
			h := NewHandler(Config{Agent: agent, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			request := httptest.NewRequest("POST", "/api/v1/proxy/mihomo/inspect", nil)
			request.Header.Set("Content-Type", "application/json")
			request.Body = io.NopCloser(jsonMIMEReaderFunc(func(p []byte) (int, error) {
				if n > 0 {
					p[0] = '{'
				}
				return n, errors.New("private-read-error")
			}))
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			assertJSONMIMEError(t, response, 400, "CONTROL_INPUT_INVALID")
			if agent.inspectionCalled || strings.Contains(response.Body.String(), "private-read-error") {
				t.Fatal("failed inspect read called Agent or leaked underlying error")
			}
		})
	}
	t.Run("bounded-no-progress", func(t *testing.T) {
		request := httptest.NewRequest("POST", "/", nil)
		calls := 0
		request.Body = io.NopCloser(jsonMIMEReaderFunc(func(p []byte) (int, error) {
			calls++
			if calls > 1000 {
				t.Fatal("unbounded no-progress reads")
			}
			return 0, nil
		}))
		if _, err := requestHasBody(request); !errors.Is(err, io.ErrNoProgress) {
			t.Fatalf("no-progress error=%v", err)
		}
	})
}

type jsonMIMENoBodyStore struct{ *iconSecurityStore }

func (*jsonMIMENoBodyStore) DeleteSiteProfile(context.Context, string) error    { return nil }
func (*jsonMIMENoBodyStore) SetSiteIgnored(context.Context, string, bool) error { return nil }
func (*jsonMIMENoBodyStore) RecordSiteVisit(context.Context, string) (time.Time, error) {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), nil
}

func TestJSONMIMELeavesElevenBodylessRoutesUnchanged(t *testing.T) {
	routes := []struct {
		method, path string
		status       int
	}{
		{"POST", "/auth/logout", 204},
		{"DELETE", "/users/2", 204},
		{"POST", "/system/public-egress/detect", 200},
		{"DELETE", "/jobs/missing", 404},
		{"POST", "/jobs/missing/retry", 404},
		{"POST", "/jobs/missing/cancel", 404},
		{"DELETE", "/sites/fixture", 204},
		{"DELETE", "/sites/fixture/icon", 204},
		{"POST", "/sites/fixture/restore", 200},
		{"POST", "/sites/fixture/visit", 200},
		{"POST", "/docker/containers/fixture/actions/start", 200},
	}
	if len(routes) != 11 {
		t.Fatalf("bodyless route inventory=%d", len(routes))
	}
	for _, route := range routes {
		for _, tc := range []struct {
			name, body string
			types      []string
		}{
			{"no-body", "", nil},
			{"extra-body-no-type", "extra body remains ignored", nil},
			{"extra-body-plain", "extra body remains ignored", []string{"text/plain"}},
			{"duplicate-type", "extra body remains ignored", []string{"application/json", "application/json"}},
		} {
			t.Run(route.method+route.path+"/"+tc.name, func(t *testing.T) {
				agent := &systemHTTPAgent{fakeAgentClient: &fakeAgentClient{}, egress: system.PublicEgressResult{Status: system.CapabilityStateAvailable}}
				store := &jsonMIMENoBodyStore{iconSecurityStore: &iconSecurityStore{profile: controlstore.SiteProfile{ProjectID: "fixture", Source: "manual"}}}
				h := NewHandler(Config{Agent: agent, Auth: &jsonMIMERouteAuth{}, ControlStore: store, JobEngine: newJobRegistry(nil), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
				request := jsonMIMERequest(route.method, "/api/v1"+route.path, tc.body, tc.types)
				request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "fixture"})
				spy := &jsonMIMEReadSpy{reader: request.Body}
				request.Body = spy
				response := httptest.NewRecorder()
				h.ServeHTTP(response, request)
				if response.Code != route.status {
					t.Fatalf("bodyless route=%d want=%d body=%s", response.Code, route.status, response.Body.String())
				}
				if spy.calls != 0 {
					t.Fatalf("bodyless route read ignored body %d times", spy.calls)
				}
				if route.status == 404 {
					assertJSONMIMEError(t, response, 404, "JOB_NOT_FOUND")
				}
			})
		}
	}
}

func TestJSONMIMELeavesMultipartIconUploadAndSecurityHeadersUnchanged(t *testing.T) {
	store := &iconSecurityStore{profile: controlstore.SiteProfile{ProjectID: "fixture", Source: "manual"}}
	h := NewHandler(Config{Agent: &jsonMIMERouteAgent{}, Auth: &jsonMIMERouteAuth{}, ControlStore: store, JobEngine: newJobRegistry(nil), SiteAssetsDirectory: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	content := `<svg xmlns="http://www.w3.org/2000/svg"><rect width="16" height="16"/></svg>`
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("icon", "fixture.svg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(part, content); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/api/v1/sites/fixture/icon", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "fixture"})
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != 200 || store.profile.LocalIconName == "" {
		t.Fatalf("multipart icon upload=%d body=%s", response.Code, response.Body.String())
	}
	read := httptest.NewRequest("GET", "/api/v1/sites/fixture/icon", nil)
	read.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "fixture"})
	response = httptest.NewRecorder()
	h.ServeHTTP(response, read)
	if response.Code != 200 || response.Body.String() != content {
		t.Fatalf("uploaded SVG read=%d body=%s", response.Code, response.Body.String())
	}
	assertIconPolicy(t, response)
	for _, contentType := range []string{"text/plain", "application/json", "multipart/form-data"} {
		request := jsonMIMERequest("POST", "/api/v1/sites/fixture/icon", `{}`, []string{contentType})
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "fixture"})
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		assertJSONMIMEError(t, response, 400, "SITE_ICON_INVALID")
	}
}
