package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/auth"
	"github.com/Kkwans/nas-control-plane/internal/system"
)

type transientSessionAuth struct {
	Authenticator
	err error
}

func (*transientSessionAuth) Initialized(context.Context) (bool, error) { return true, nil }
func (a *transientSessionAuth) Authenticate(context.Context, string) (auth.Principal, error) {
	return auth.Principal{ID: 1, Username: "root-admin", Role: auth.RootRole}, a.err
}

func TestTemporaryAuthenticationFailureDoesNotMasqueradeAsLogout(t *testing.T) {
	backend := &transientSessionAuth{err: errors.New("private database failure")}
	handler := NewHandler(Config{Auth: backend, Agent: &fakeAgentClient{}})
	for _, path := range []string{"/api/v1/auth/status", "/api/v1/system/summary"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "test-session"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s returned %d, want 503: %s", path, response.Code, response.Body.String())
		}
		assertErrorCode(t, response, "AUTH_UNAVAILABLE")
		if strings.Contains(response.Body.String(), "private") {
			t.Fatal("authentication response leaked internal error")
		}
		if len(response.Result().Cookies()) != 0 {
			t.Fatal("transient failure must not clear cookies")
		}
	}
	backend.err = nil
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/status", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "test-session"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"authenticated":true`) {
		t.Fatalf("session did not recover: %s", response.Body.String())
	}
}

func TestInvalidCookieStillRequiresAuthentication(t *testing.T) {
	service := openTestAuthService(t)
	_, unauthorized := service.Authenticate(context.Background(), "not-a-valid-session")
	backend := &transientSessionAuth{err: unauthorized}
	handler := NewHandler(Config{Auth: backend})
	for path, want := range map[string]int{"/api/v1/auth/status": 200, "/api/v1/system/summary": 401} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "not-a-valid-session"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s returned %d, want %d", path, response.Code, want)
		}
	}
}

func TestRootBootstrapLoginSessionProtectsControlPlaneAPIs(t *testing.T) {
	authService := openTestAuthService(t)
	agent := &fakeAgentClient{summary: system.Summary{CollectedAt: time.Now().UTC(), Host: system.HostSnapshot{Hostname: "DH4300-PLUS"}}}
	provider := metricsProvider(agent.summary.CollectedAt)
	provider.state.Summary = &agent.summary
	handler := NewHandler(Config{Auth: authService, Agent: agent, SummarySnapshots: provider, RequestID: func() string { return "req-auth" }})

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
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request
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

// 只用内存 fake 验证 HTTP 的分步提交语义，不创建账号、数据库或监听器。
type availabilityAuth struct {
	initialized                                                bool
	bootstrapErr, loginErr, logoutErr                          error
	bootstrapCalls, loginCalls, logoutCalls, authenticateCalls int
	calls                                                      []string
}

func (a *availabilityAuth) Initialized(context.Context) (bool, error) {
	return a.initialized, nil
}
func (a *availabilityAuth) Bootstrap(context.Context, string, string) (auth.Principal, error) {
	a.bootstrapCalls++
	a.calls = append(a.calls, "bootstrap")
	if a.bootstrapErr != nil {
		return auth.Principal{}, a.bootstrapErr
	}
	a.initialized = true
	return auth.Principal{ID: 1, Username: "fixture", Role: auth.RootRole}, nil
}
func (a *availabilityAuth) Login(context.Context, string, string) (auth.Session, error) {
	a.loginCalls++
	a.calls = append(a.calls, "login")
	// 即使失败同时返回了部分结果，也不能把 Token 写入响应。
	return auth.Session{Token: "private-partial-token"}, a.loginErr
}
func (a *availabilityAuth) Authenticate(context.Context, string) (auth.Principal, error) {
	a.authenticateCalls++
	return auth.Principal{ID: 1, Username: "fixture", Role: auth.RootRole}, nil
}
func (a *availabilityAuth) Logout(context.Context, string) error {
	a.logoutCalls++
	return a.logoutErr
}

func assertAuthUnavailable(t *testing.T, response *httptest.ResponseRecorder, password string) {
	t.Helper()
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", response.Code, response.Body.String())
	}
	var body ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "AUTH_UNAVAILABLE" || body.RequestID != "req-availability" || body.Message == "" {
		t.Fatalf("unexpected error envelope: %#v", body)
	}
	if response.Header().Get("X-Request-ID") != body.RequestID {
		t.Fatal("request ID header and body differ")
	}
	if len(response.Header().Values("Set-Cookie")) != 0 {
		t.Fatal("failure must not set or clear a cookie")
	}
	exposed := response.Body.String() + fmt.Sprint(response.Header())
	for _, secret := range []string{"private-backend-detail", "private-partial-token", "private-existing-session", password} {
		if strings.Contains(exposed, secret) {
			t.Fatalf("response leaked fixture secret %q", secret)
		}
	}
}

func TestBootstrapAvailabilityPreservesInitializationBoundary(t *testing.T) {
	for _, failBootstrap := range []bool{false, true} {
		name := "login fails after bootstrap commit"
		if failBootstrap {
			name = "bootstrap fails before login"
		}
		t.Run(name, func(t *testing.T) {
			backend := &availabilityAuth{loginErr: errors.New("private-backend-detail")}
			if failBootstrap {
				backend.bootstrapErr = errors.New("private-backend-detail")
			}
			handler := NewHandler(Config{Auth: backend, Agent: &fakeAgentClient{}, RequestID: func() string { return "req-availability" }})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, credentialsRequestFor(t, http.MethodPost, "/api/v1/auth/bootstrap"))
			assertAuthUnavailable(t, response, testLoginPassword(t))
			wantLogin := 1
			wantCalls := "bootstrap,login"
			if failBootstrap {
				wantLogin = 0
				wantCalls = "bootstrap"
			}
			if backend.bootstrapCalls != 1 || backend.loginCalls != wantLogin || strings.Join(backend.calls, ",") != wantCalls {
				t.Fatalf("unexpected write calls: %v", backend.calls)
			}
			status := httptest.NewRecorder()
			handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/v1/auth/status", nil))
			var state AuthStatusResponse
			if status.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", status.Code, status.Body.String())
			}
			if err := json.Unmarshal(status.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			if state.Initialized != !failBootstrap || state.Authenticated || state.User != nil {
				t.Fatalf("initialization must not imply authentication: %#v", state)
			}
			protected := httptest.NewRecorder()
			handler.ServeHTTP(protected, httptest.NewRequest(http.MethodGet, "/api/v1/system/capabilities", nil))
			if protected.Code != http.StatusUnauthorized {
				t.Fatalf("protected status = %d", protected.Code)
			}
			assertErrorCode(t, protected, "AUTH_UNAUTHORIZED")
			if backend.authenticateCalls != 0 || backend.bootstrapCalls != 1 || backend.loginCalls != wantLogin {
				t.Fatal("read-only recovery must not authenticate or replay writes")
			}
		})
	}
}

func TestLoginAndLogoutAvailabilityDoNotClaimSuccess(t *testing.T) {
	for _, endpoint := range []string{"login", "logout"} {
		t.Run(endpoint, func(t *testing.T) {
			backend := &availabilityAuth{initialized: true, loginErr: errors.New("private-backend-detail"), logoutErr: errors.New("private-backend-detail")}
			handler := NewHandler(Config{Auth: backend, Agent: &fakeAgentClient{}, RequestID: func() string { return "req-availability" }})
			request := credentialsRequestFor(t, http.MethodPost, "/api/v1/auth/"+endpoint)
			if endpoint == "logout" {
				request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "private-existing-session"})
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			assertAuthUnavailable(t, response, testLoginPassword(t))
			if backend.bootstrapCalls != 0 {
				t.Fatal("must not bootstrap")
			}
			if endpoint == "login" {
				if backend.loginCalls != 1 || backend.logoutCalls != 0 {
					t.Fatal("unexpected login calls")
				}
			} else {
				if backend.logoutCalls != 1 || backend.loginCalls != 0 {
					t.Fatal("unexpected logout calls")
				}
				// 撤销失败后的原会话仍可由权威状态查询确认，不能虚报退出成功。
				status := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/status", nil)
				request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "private-existing-session"})
				handler.ServeHTTP(status, request)
				var state AuthStatusResponse
				if err := json.Unmarshal(status.Body.Bytes(), &state); err != nil {
					t.Fatal(err)
				}
				if status.Code != http.StatusOK || !state.Initialized || !state.Authenticated || state.User == nil || backend.authenticateCalls != 1 {
					t.Fatalf("failed logout must not imply revocation: %#v", state)
				}
			}
		})
	}
}

func TestNilAuthEndpointsRetainAvailabilitySemantics(t *testing.T) {
	handler := NewHandler(Config{Agent: &fakeAgentClient{}, RequestID: func() string { return "req-availability" }})
	for _, endpoint := range []string{"status", "bootstrap", "login"} {
		method := http.MethodPost
		if endpoint == "status" {
			method = http.MethodGet
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, "/api/v1/auth/"+endpoint, nil))
		assertAuthUnavailable(t, response, testLoginPassword(t))
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil))
	cookies := response.Result().Cookies()
	if response.Code != http.StatusNoContent || len(cookies) != 1 || cookies[0].Name != sessionCookieName || cookies[0].MaxAge != -1 {
		t.Fatal("nil auth logout must retain cookie-clearing behavior")
	}
}
