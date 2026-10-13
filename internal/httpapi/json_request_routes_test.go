package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Kkwans/nas-control-plane/internal/auth"
	"github.com/Kkwans/nas-control-plane/internal/controlstore"
)

type jsonMIMERouteAuth struct {
	calls           atomic.Int32
	authentications atomic.Int32
}

func (f *jsonMIMERouteAuth) Initialized(context.Context) (bool, error) { return true, nil }
func (f *jsonMIMERouteAuth) Authenticate(context.Context, string) (auth.Principal, error) {
	f.authentications.Add(1)
	return auth.Principal{ID: 1, Username: "fixture-root", Role: auth.RootRole}, nil
}
func (f *jsonMIMERouteAuth) Bootstrap(context.Context, string, string) (auth.Principal, error) {
	f.calls.Add(1)
	return auth.Principal{}, nil
}
func (f *jsonMIMERouteAuth) Login(context.Context, string, string) (auth.Session, error) {
	f.calls.Add(1)
	return auth.Session{}, nil
}
func (f *jsonMIMERouteAuth) Logout(context.Context, string) error { f.calls.Add(1); return nil }
func (f *jsonMIMERouteAuth) Users(context.Context) ([]auth.User, error) {
	f.calls.Add(1)
	return nil, nil
}
func (f *jsonMIMERouteAuth) PasswordPolicy(context.Context) (auth.PasswordPolicy, error) {
	f.calls.Add(1)
	return auth.PasswordPolicy{}, nil
}
func (f *jsonMIMERouteAuth) UpdatePasswordPolicy(context.Context, auth.PasswordPolicy) (auth.PasswordPolicy, error) {
	f.calls.Add(1)
	return auth.PasswordPolicy{}, nil
}
func (f *jsonMIMERouteAuth) CreateUser(context.Context, string, string) (auth.User, error) {
	f.calls.Add(1)
	return auth.User{}, nil
}
func (f *jsonMIMERouteAuth) SetUserDisabled(context.Context, int64, int64, bool) (auth.User, error) {
	f.calls.Add(1)
	return auth.User{}, nil
}
func (f *jsonMIMERouteAuth) DeleteUser(context.Context, int64, int64) error {
	f.calls.Add(1)
	return nil
}
func (f *jsonMIMERouteAuth) UpdatePassword(context.Context, int64, int64, string, string) error {
	f.calls.Add(1)
	return nil
}

type jsonMIMERoute struct{ method, path, inputError string }

func jsonMIMERoutes() []jsonMIMERoute {
	return []jsonMIMERoute{
		{"POST", "/auth/bootstrap", "AUTH_INPUT_INVALID"},
		{"POST", "/auth/login", "AUTH_INPUT_INVALID"},
		{"POST", "/users", "AUTH_INPUT_INVALID"},
		{"PUT", "/users/password-policy", "AUTH_INPUT_INVALID"},
		{"PUT", "/users/2/status", "AUTH_INPUT_INVALID"},
		{"PUT", "/users/1/password", "AUTH_INPUT_INVALID"},
		{"POST", "/system/dns/preview", "CONTROL_INPUT_INVALID"},
		{"POST", "/system/dns/confirm", "CONTROL_INPUT_INVALID"},
		{"POST", "/system/dns/rollback", "CONTROL_INPUT_INVALID"},
		{"POST", "/proxy/mihomo/invoke", "CONTROL_INPUT_INVALID"},
		{"POST", "/proxy/mihomo/inspect", "CONTROL_INPUT_INVALID"},
		{"PUT", "/preferences", "CONTROL_INPUT_INVALID"},
		{"PUT", "/preferences/lists/containers", "CONTROL_INPUT_INVALID"},
		{"PUT", "/databases/project-preferences", "CONTROL_INPUT_INVALID"},
		{"POST", "/docker/images/pull", "CONTROL_INPUT_INVALID"},
		{"POST", "/docker/images/remove", "CONTROL_INPUT_INVALID"},
		{"POST", "/docker/images/remove-batch", "CONTROL_INPUT_INVALID"},
		{"POST", "/docker/containers", "CONTROL_INPUT_INVALID"},
		{"POST", "/docker/projects/fixture/actions/start", "CONTROL_INPUT_INVALID"},
		{"POST", "/docker/compose/projects/fixture/actions/start", "CONTROL_INPUT_INVALID"},
		{"DELETE", "/docker/compose/projects/fixture", "CONTROL_INPUT_INVALID"},
		{"POST", "/docker/compose/config/read", "CONTROL_INPUT_INVALID"},
		{"POST", "/docker/compose/config/validate", "CONTROL_INPUT_INVALID"},
		{"POST", "/docker/compose/deploy", "CONTROL_INPUT_INVALID"},
		{"PUT", "/docker/compose/drafts", "CONTROL_INPUT_INVALID"},
		{"POST", "/sites", "CONTROL_INPUT_INVALID"},
		{"PUT", "/sites/fixture", "CONTROL_INPUT_INVALID"},
		{"POST", "/databases/connect", "DATABASE_INPUT_INVALID"},
		{"POST", "/databases/test-connection", "DATABASE_INPUT_INVALID"},
		{"POST", "/databases/catalog", "DATABASE_INPUT_INVALID"},
		{"POST", "/databases/query", "DATABASE_INPUT_INVALID"},
		{"POST", "/databases/rows", "DATABASE_INPUT_INVALID"},
		{"POST", "/databases/rows/insert", "DATABASE_INPUT_INVALID"},
		{"POST", "/databases/rows/update", "DATABASE_INPUT_INVALID"},
		{"POST", "/databases/rows/delete", "DATABASE_INPUT_INVALID"},
	}
}

func TestJSONRoutesRejectUnsupportedMediaBeforeSideEffects(t *testing.T) {
	routes := jsonMIMERoutes()
	if len(routes) != 35 {
		t.Fatalf("JSON route inventory=%d, want 35", len(routes))
	}
	for _, route := range routes {
		for _, tc := range []jsonMIMECase{
			{"missing", nil, false},
			{"simple-mime", []string{"text/plain; token=mime-header-secret"}, false},
			{"duplicate", []string{"application/json", "application/json"}, false},
		} {
			t.Run(route.method+route.path+"/"+tc.name, func(t *testing.T) {
				agent, store, authenticator := &jsonMIMERouteAgent{}, &jsonMIMERouteStore{}, &jsonMIMERouteAuth{}
				jobs := newJobRegistry(nil)
				var logs bytes.Buffer
				h := NewHandler(Config{Agent: agent, DatabaseAgent: agent, DockerImages: agent, DockerResources: agent, Filesystem: agent, Compose: agent, ControlStore: store, Auth: authenticator, JobEngine: jobs,
					RequestID: func() string { return "mime-route" }, Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
				request := jsonMIMERequest(route.method, "http://nas.test/api/v1"+route.path, `{"password":"mime-body-secret"}`, tc.values)
				request.Header.Set("Origin", "http://nas.test")
				request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "mime-cookie-secret"})
				spy := &jsonMIMEReadSpy{reader: request.Body}
				request.Body = spy
				response := httptest.NewRecorder()
				h.ServeHTTP(response, request)
				result := assertJSONMIMEError(t, response, 415, "HTTP_UNSUPPORTED_MEDIA_TYPE")
				if result.RequestID != "mime-route" || response.Header().Get("X-Request-ID") != result.RequestID {
					t.Fatalf("requestId mismatch: %+v", result)
				}
				if len(response.Result().Cookies()) != 0 {
					t.Fatal("MIME rejection changed session cookie")
				}
				if route.path == "/proxy/mihomo/inspect" {
					if spy.bytes != 1 || spy.maxRequest != 1 {
						t.Fatalf("optional body probe bytes=%d requested=%d, want one", spy.bytes, spy.maxRequest)
					}
				} else if spy.calls != 0 {
					t.Fatalf("required JSON body read before MIME rejection: calls=%d bytes=%d", spy.calls, spy.bytes)
				}
				if agent.calls.Load() != 0 || store.calls.Load() != 0 || authenticator.calls.Load() != 0 || len(jobs.list("")) != 0 {
					t.Fatalf("MIME rejection caused side effects: Agent=%d store=%d auth=%d jobs=%d", agent.calls.Load(), store.calls.Load(), authenticator.calls.Load(), len(jobs.list("")))
				}
				var record map[string]any
				if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &record); err != nil {
					t.Fatalf("structured log: %v", err)
				}
				if record["error_code"] != "HTTP_UNSUPPORTED_MEDIA_TYPE" || record["request_id"] != "mime-route" || record["status"] != float64(415) {
					t.Fatalf("structured error log=%v", record)
				}
				for _, secret := range []string{"mime-header-secret", "mime-body-secret", "mime-cookie-secret"} {
					if strings.Contains(response.Body.String(), secret) || strings.Contains(logs.String(), secret) {
						t.Fatalf("MIME error leaked %s", secret)
					}
				}
			})
		}
	}
}

func TestJSONRoutesKeepCorrectMIMEInvalidBodyErrors(t *testing.T) {
	for _, route := range jsonMIMERoutes() {
		for name, payload := range map[string]string{"malformed": "{", "unknown-field": `{"notARealField":true}`, "multiple-values": `{} {}`, "empty": ""} {
			if route.path == "/proxy/mihomo/inspect" && payload == "" {
				continue
			}
			t.Run(route.method+route.path+"/"+name, func(t *testing.T) {
				agent, store, authenticator := &jsonMIMERouteAgent{}, &jsonMIMERouteStore{}, &jsonMIMERouteAuth{}
				jobs := newJobRegistry(nil)
				h := NewHandler(Config{Agent: agent, DatabaseAgent: agent, DockerImages: agent, Compose: agent, ControlStore: store, Auth: authenticator, JobEngine: jobs, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
				request := jsonMIMERequest(route.method, "/api/v1"+route.path, payload, []string{"application/json"})
				request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "fixture"})
				response := httptest.NewRecorder()
				h.ServeHTTP(response, request)
				assertJSONMIMEError(t, response, 400, route.inputError)
				if agent.calls.Load() != 0 || store.calls.Load() != 0 || authenticator.calls.Load() != 0 || len(jobs.list("")) != 0 {
					t.Fatal("invalid JSON caused business side effects")
				}
			})
		}
	}
}

func TestJSONMIMEPreservesOriginAuthenticationAndAvailabilityOrder(t *testing.T) {
	for _, tc := range []struct {
		name, origin                   string
		authenticated, unavailableAuth bool
		status                         int
		code                           string
	}{
		{"cross-origin-anonymous", "http://other.test", false, false, 403, "HTTP_CROSS_ORIGIN_REJECTED"},
		{"cross-origin-authenticated", "http://other.test", true, false, 403, "HTTP_CROSS_ORIGIN_REJECTED"},
		{"same-origin-anonymous", "http://nas.test", false, false, 401, "AUTH_UNAUTHORIZED"},
		{"same-origin-authenticated", "http://nas.test", true, false, 415, "HTTP_UNSUPPORTED_MEDIA_TYPE"},
		{"cli-authenticated", "", true, false, 415, "HTTP_UNSUPPORTED_MEDIA_TYPE"},
		{"auth-nil-business-fixture", "", false, true, 415, "HTTP_UNSUPPORTED_MEDIA_TYPE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &jsonMIMERouteAuth{}
			config := Config{Agent: &jsonMIMERouteAgent{}, ControlStore: &jsonMIMERouteStore{}, Auth: a, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			if tc.unavailableAuth {
				config.Auth = nil
			}
			h := NewHandler(config)
			request := jsonMIMERequest("PUT", "http://nas.test/api/v1/preferences", `{}`, []string{"text/plain"})
			if tc.origin != "" {
				request.Header.Set("Origin", tc.origin)
			}
			if tc.authenticated {
				request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "fixture"})
			}
			spy := &jsonMIMEReadSpy{reader: request.Body}
			request.Body = spy
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			assertJSONMIMEError(t, response, tc.status, tc.code)
			if spy.calls != 0 || a.calls.Load() != 0 {
				t.Fatal("rejection read body or mutated auth")
			}
			if tc.status == 403 && a.authentications.Load() != 0 {
				t.Fatal("authentication ran before Origin guard")
			}
		})
	}
	for _, route := range []struct{ method, path, code string }{
		{"POST", "/auth/bootstrap", "AUTH_UNAVAILABLE"}, {"POST", "/auth/login", "AUTH_UNAVAILABLE"},
		{"PUT", "/preferences", "PREFERENCES_UNAVAILABLE"}, {"POST", "/system/dns/preview", "SYSTEM_DNS_UNAVAILABLE"},
	} {
		t.Run("unavailable"+route.path, func(t *testing.T) {
			h := NewHandler(Config{Agent: &fakeAgentClient{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			response := httptest.NewRecorder()
			h.ServeHTTP(response, jsonMIMERequest(route.method, "/api/v1"+route.path, `{}`, []string{"text/plain"}))
			assertJSONMIMEError(t, response, 503, route.code)
		})
	}
}

func TestJSONMIMEAuthSQLiteAndCLILifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.sqlite")
	service, err := auth.Open(path, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store, err := controlstore.Open(filepath.Join(t.TempDir(), "control.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	agent := &jsonMIMERouteAgent{}
	h := NewHandler(Config{Agent: agent, Auth: service, ControlStore: store, JobEngine: newJobRegistry(nil), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	payload := `{"username":"fixture-root","password":"Fixture-only-password-2026"}`
	count := func(table string) int {
		t.Helper()
		var value int
		if err := database.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	for _, endpoint := range []string{"bootstrap", "login"} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, jsonMIMERequest("POST", "/api/v1/auth/"+endpoint, payload, []string{"text/plain"}))
		assertJSONMIMEError(t, response, 415, "HTTP_UNSUPPORTED_MEDIA_TYPE")
		if count("users") != 0 || count("sessions") != 0 || len(response.Result().Cookies()) != 0 {
			t.Fatal("rejected auth request created user/session/cookie")
		}
	}
	bootstrap := httptest.NewRecorder()
	h.ServeHTTP(bootstrap, jsonMIMERequest("POST", "/api/v1/auth/bootstrap", payload, []string{"application/json"}))
	if bootstrap.Code != 201 || count("users") != 1 || count("sessions") != 1 {
		t.Fatalf("bootstrap=%d body=%s", bootstrap.Code, bootstrap.Body.String())
	}
	rejectedLogin := httptest.NewRecorder()
	h.ServeHTTP(rejectedLogin, jsonMIMERequest("POST", "/api/v1/auth/login", payload, nil))
	assertJSONMIMEError(t, rejectedLogin, 415, "HTTP_UNSUPPORTED_MEDIA_TYPE")
	if count("sessions") != 1 || len(rejectedLogin.Result().Cookies()) != 0 {
		t.Fatal("rejected login established a session")
	}
	login := httptest.NewRecorder()
	h.ServeHTTP(login, jsonMIMERequest("POST", "/api/v1/auth/login", payload, []string{"application/json; charset=utf-8"}))
	if login.Code != 200 || len(login.Result().Cookies()) != 1 || count("sessions") != 2 {
		t.Fatalf("CLI login=%d body=%s", login.Code, login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	inputPreferences := controlstore.DefaultPreferences()
	inputPreferences.RefreshIntervalSeconds = 7
	preferencePayload, err := json.Marshal(inputPreferences)
	if err != nil {
		t.Fatal(err)
	}
	write := jsonMIMERequest("PUT", "/api/v1/preferences", string(preferencePayload), []string{"application/json"})
	write.AddCookie(cookie)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, write)
	if response.Code != 200 {
		t.Fatalf("CLI JSON write=%d body=%s", response.Code, response.Body.String())
	}
	var preferences controlstore.Preferences
	if err := json.Unmarshal(response.Body.Bytes(), &preferences); err != nil {
		t.Fatal(err)
	}
	persisted, err := store.Preferences(context.Background(), 1)
	if err != nil || !reflect.DeepEqual(persisted, preferences) {
		t.Fatalf("preferences not persisted: %+v err=%v", persisted, err)
	}
	logout := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	logout.AddCookie(cookie)
	response = httptest.NewRecorder()
	h.ServeHTTP(response, logout)
	if response.Code != 204 {
		t.Fatalf("bodyless CLI logout=%d", response.Code)
	}
	read := httptest.NewRequest("GET", "/api/v1/preferences", nil)
	read.AddCookie(cookie)
	response = httptest.NewRecorder()
	h.ServeHTTP(response, read)
	assertJSONMIMEError(t, response, 401, "AUTH_UNAUTHORIZED")
	if agent.calls.Load() != 0 {
		t.Fatal("auth/preferences fixture called Agent")
	}
}
