package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/auth"
	"github.com/Kkwans/nas-control-plane/internal/system"
)

func TestRootBootstrapLoginSessionProtectsControlPlaneAPIs(t *testing.T) {
	authService := openTestAuthService(t)
	agent := &fakeAgentClient{summary: system.Summary{CollectedAt: time.Now().UTC(), Host: system.HostSnapshot{Hostname: "DH4300-PLUS"}}}
	handler := NewHandler(Config{Auth: authService, Agent: agent, RequestID: func() string { return "req-auth" }})

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/system/summary", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated summary status = %d", unauthorized.Code)
	}
	assertErrorCode(t, unauthorized, "AUTH_UNAUTHORIZED")

	bootstrap := httptest.NewRecorder()
	handler.ServeHTTP(bootstrap, credentialsRequestFor(t, http.MethodPost, "/api/v1/auth/bootstrap"))
	if bootstrap.Code != http.StatusCreated {
		t.Fatalf("bootstrap status = %d: %s", bootstrap.Code, bootstrap.Body.String())
	}
	cookies := bootstrap.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookieName || !cookies[0].HttpOnly {
		t.Fatalf("session cookie = %#v", cookies)
	}
	var session AuthSessionResponse
	if err := json.NewDecoder(bootstrap.Body).Decode(&session); err != nil {
		t.Fatalf("decode bootstrap response: %v", err)
	}
	if session.User.Role != auth.RootRole || session.User.Username != "root-admin" {
		t.Fatalf("session = %#v", session)
	}

	protected := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/system/summary", nil)
	request.AddCookie(cookies[0])
	handler.ServeHTTP(protected, request)
	if protected.Code != http.StatusOK {
		t.Fatalf("authenticated summary status = %d: %s", protected.Code, protected.Body.String())
	}

	logout := httptest.NewRecorder()
	logoutRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutRequest.AddCookie(cookies[0])
	handler.ServeHTTP(logout, logoutRequest)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d", logout.Code)
	}

	afterLogout := httptest.NewRecorder()
	afterLogoutRequest := httptest.NewRequest(http.MethodGet, "/api/v1/system/summary", nil)
	afterLogoutRequest.AddCookie(cookies[0])
	handler.ServeHTTP(afterLogout, afterLogoutRequest)
	if afterLogout.Code != http.StatusUnauthorized {
		t.Fatalf("post-logout summary status = %d", afterLogout.Code)
	}
}

func TestAuthStatusAndBootstrapConflictUseStableResponses(t *testing.T) {
	authService := openTestAuthService(t)
	handler := NewHandler(Config{Auth: authService, RequestID: func() string { return "req-auth-status" }})

	status := httptest.NewRecorder()
	handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/v1/auth/status", nil))
	if status.Code != http.StatusOK {
		t.Fatalf("status response = %d", status.Code)
	}
	var body AuthStatusResponse
	if err := json.NewDecoder(status.Body).Decode(&body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if body.Initialized || body.Authenticated || body.User != nil {
		t.Fatalf("initial status = %#v", body)
	}

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, credentialsRequestFor(t, http.MethodPost, "/api/v1/auth/bootstrap"))
	if first.Code != http.StatusCreated {
		t.Fatalf("first bootstrap status = %d", first.Code)
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, credentialsRequestFor(t, http.MethodPost, "/api/v1/auth/bootstrap"))
	if second.Code != http.StatusConflict {
		t.Fatalf("second bootstrap status = %d", second.Code)
	}
	assertErrorCode(t, second, "AUTH_ALREADY_INITIALIZED")
}

func openTestAuthService(t *testing.T) *auth.Service {
	t.Helper()
	service, err := auth.Open(filepath.Join(t.TempDir(), "ncp.sqlite"), auth.Options{})
	if err != nil {
		t.Fatalf("auth.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return service
}

func credentialsRequestFor(t *testing.T, method, target string) *http.Request {
	t.Helper()
	body, err := json.Marshal(credentialsRequest{Username: "root-admin", Password: testLoginPassword(t)})
	if err != nil {
		t.Fatalf("marshal credentials: %v", err)
	}
	return httptest.NewRequest(method, target, bytes.NewReader(body))
}

func testLoginPassword(t *testing.T) string {
	t.Helper()
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s/%d", t.Name(), 20260723)))
	return fmt.Sprintf("%x", sum[:])
}

func assertErrorCode(t *testing.T, response *httptest.ResponseRecorder, want string) {
	t.Helper()
	var body ErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Code != want {
		t.Fatalf("error code = %q, want %q", body.Code, want)
	}
}
