package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/auth"
)

type admissionClock struct {
	mu    sync.Mutex
	value time.Time
}

func (c *admissionClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.value }
func (c *admissionClock) advance(d time.Duration) {
	c.mu.Lock()
	c.value = c.value.Add(d)
	c.mu.Unlock()
}
func admissionFixture() (*authAdmission, *admissionClock) {
	c := &admissionClock{value: time.Now()}
	return newAuthAdmission(c.now), c
}
func consumeAdmission(t *testing.T, gate *authAdmission, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		release, retry := gate.acquire(context.Background())
		if release == nil {
			t.Fatalf("attempt %d rejected, retry %d", i, retry)
		}
		release()
	}
}
func assertAdmissionDenied(t *testing.T, gate *authAdmission, retry int) {
	t.Helper()
	release, got := gate.acquire(context.Background())
	if release != nil {
		release()
		t.Fatal("unexpected admission")
	}
	if got != retry {
		t.Fatalf("retry=%d, want %d", got, retry)
	}
}
func TestAuthAdmissionBudgetAndClock(t *testing.T) {
	gate, c := admissionFixture()
	consumeAdmission(t, gate, 10)
	assertAdmissionDenied(t, gate, 5)
	c.advance(time.Nanosecond)
	assertAdmissionDenied(t, gate, 5)
	c.advance(time.Second)
	assertAdmissionDenied(t, gate, 4)
	for i := 0; i < 100; i++ {
		assertAdmissionDenied(t, gate, 4)
	}
	c.advance(4*time.Second - 2*time.Nanosecond)
	assertAdmissionDenied(t, gate, 1)
	c.advance(time.Nanosecond)
	consumeAdmission(t, gate, 1)
	assertAdmissionDenied(t, gate, 5)
	c.advance(-time.Hour)
	assertAdmissionDenied(t, gate, 5)
	c.advance(time.Hour + 5*time.Second)
	consumeAdmission(t, gate, 1)
	assertAdmissionDenied(t, gate, 5)
	c.advance(100 * 365 * 24 * time.Hour)
	consumeAdmission(t, gate, 10)
	assertAdmissionDenied(t, gate, 5)
}
func TestAuthAdmissionConcurrencyReleaseAndCancellation(t *testing.T) {
	gate, c := admissionFixture()
	first, _ := gate.acquire(context.Background())
	second, _ := gate.acquire(context.Background())
	for i := 0; i < 20; i++ {
		assertAdmissionDenied(t, gate, 1)
	}
	first()
	first()
	second()
	consumeAdmission(t, gate, 8)
	assertAdmissionDenied(t, gate, 5)
	c.advance(5 * time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	release, retry := gate.acquire(ctx)
	if release != nil || retry != 0 {
		t.Fatal("canceled request admitted or rate-limited")
	}
	consumeAdmission(t, gate, 1)
	// 即使在检查时钟时取消，仍不得消耗预算。
	gate, _ = admissionFixture()
	ctx, cancel = context.WithCancel(context.Background())
	gate.now = func() time.Time { cancel(); return time.Now() }
	release, retry = gate.acquire(ctx)
	if release != nil || retry != 0 {
		t.Fatal("late canceled request admitted")
	}
	if gate.credit != authAttemptBurst*authAttemptInterval || gate.active != 0 {
		t.Fatal("cancel changed budget")
	}
}

type admissionAuth struct {
	Authenticator
	bootstrapCalls    atomic.Int32
	loginCalls        atomic.Int32
	logoutCalls       atomic.Int32
	authenticateCalls atomic.Int32
	bootstrapFn       func(context.Context) error
	loginFn           func(context.Context) error
}

func (a *admissionAuth) Initialized(context.Context) (bool, error) { return true, nil }
func (a *admissionAuth) Bootstrap(ctx context.Context, _, _ string) (auth.Principal, error) {
	a.bootstrapCalls.Add(1)
	if a.bootstrapFn != nil {
		return auth.Principal{}, a.bootstrapFn(ctx)
	}
	return auth.Principal{}, nil
}
func (a *admissionAuth) Login(ctx context.Context, _, _ string) (auth.Session, error) {
	a.loginCalls.Add(1)
	if a.loginFn != nil {
		if err := a.loginFn(ctx); err != nil {
			return auth.Session{}, err
		}
	}
	return auth.Session{Token: "fixture", ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (a *admissionAuth) Authenticate(context.Context, string) (auth.Principal, error) {
	a.authenticateCalls.Add(1)
	return auth.Principal{}, nil
}
func (a *admissionAuth) Logout(context.Context, string) error { a.logoutCalls.Add(1); return nil }
func admissionRequest(path string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/"+path, strings.NewReader(`{"username":"fixture","password":"fixture"}`))
	r.Header.Set("Content-Type", "application/json")
	return r
}
func admissionServe(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func assertRateLimited(t *testing.T, w *httptest.ResponseRecorder, retry string) {
	t.Helper()
	if w.Code != 429 || w.Header().Get("Retry-After") != retry || len(w.Result().Cookies()) != 0 {
		t.Fatalf("invalid rate limit response: %d %v", w.Code, w.Header())
	}
	var body ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "AUTH_RATE_LIMITED" || body.RequestID != "admission-test" || body.Message == "" {
		t.Fatalf("invalid envelope: %+v", body)
	}
}
func admissionHandler(a Authenticator, g *authAdmission) http.Handler {
	return newHandlerWithAuthAdmission(Config{Auth: a, RequestID: func() string { return "admission-test" }}, g)
}

func TestAuthAdmissionSharedBudgetAndReadOnlyPaths(t *testing.T) {
	gate, c := admissionFixture()
	backend := &admissionAuth{}
	h := admissionHandler(backend, gate)
	for i := 0; i < 10; i++ {
		path := "login"
		want := 200
		if i == 9 {
			path = "bootstrap"
			want = 201
		}
		w := admissionServe(h, admissionRequest(path))
		if w.Code != want {
			t.Fatalf("%s %d", path, w.Code)
		}
	}
	if backend.bootstrapCalls.Load() != 1 || backend.loginCalls.Load() != 10 {
		t.Fatal("bootstrap must use one permit for both calls")
	}
	for i := 0; i < 100; i++ {
		path := []string{"login", "bootstrap"}[i%2]
		r := admissionRequest(path)
		r = httptest.NewRequest(http.MethodPost, r.URL.String(), strings.NewReader(fmt.Sprintf(`{"username":"user%d","password":"fixture"}`, i)))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = fmt.Sprintf("192.0.2.%d:99", i)
		for _, header := range []string{"Forwarded", "X-Forwarded-For", "X-Real-IP"} {
			r.Header.Set(header, fmt.Sprintf("192.0.2.%d", i))
		}
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "existing"})
		assertRateLimited(t, admissionServe(h, r), "5")
	}
	if backend.bootstrapCalls.Load() != 1 || backend.loginCalls.Load() != 10 {
		t.Fatal("refused attempts reached backend")
	}
	for _, path := range []string{"/api/v1/auth/status", "/api/v1/auth/logout", "/api/v1/users"} {
		method := http.MethodGet
		if strings.HasSuffix(path, "logout") {
			method = http.MethodPost
		}
		r := httptest.NewRequest(method, path, nil)
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "fixture"})
		w := admissionServe(h, r)
		if w.Code == 429 {
			t.Fatalf("unrelated route throttled: %s", path)
		}
	}
	if backend.logoutCalls.Load() != 1 || backend.authenticateCalls.Load() != 2 {
		t.Fatal("read/logout authentication did not run")
	}
	c.advance(5 * time.Second)
	if w := admissionServe(h, admissionRequest("login")); w.Code != 200 {
		t.Fatalf("refill failed: %d", w.Code)
	}
	independent := admissionHandler(backend, newAuthAdmission(c.now))
	if w := admissionServe(independent, admissionRequest("login")); w.Code != 200 {
		t.Fatal("handlers share budget")
	}
}

func TestAuthAdmissionValidationPriority(t *testing.T) {
	for _, exhausted := range []bool{false, true} {
		for _, endpoint := range []string{"login", "bootstrap"} {
			t.Run(fmt.Sprintf("%s/exhausted=%t", endpoint, exhausted), func(t *testing.T) {
				gate, _ := admissionFixture()
				backend := &admissionAuth{}
				h := admissionHandler(backend, gate)
				if exhausted {
					consumeAdmission(t, gate, 10)
				}
				for _, tc := range []struct {
					name, body, mime, origin string
					code                     int
				}{{"origin", `{`, "text/plain", "http://evil.invalid", 403}, {"mime", `{`, "text/plain", "", 415}, {"size", strings.Repeat(" ", maxCredentialsBodySize+1), "application/json", "", 413}, {"json", `{`, "application/json", "", 400}, {"trailing", `{} {}`, "application/json", "", 400}, {"unknown", `{"extra":1}`, "application/json", "", 400}} {
					t.Run(tc.name, func(t *testing.T) {
						r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/"+endpoint, strings.NewReader(tc.body))
						r.Header.Set("Content-Type", tc.mime)
						if tc.origin != "" {
							r.Header.Set("Origin", tc.origin)
						}
						w := admissionServe(h, r)
						if w.Code != tc.code || w.Header().Get("Retry-After") != "" {
							t.Fatalf("got %d %v", w.Code, w.Header())
						}
					})
				}
				if backend.bootstrapCalls.Load() != 0 || backend.loginCalls.Load() != 0 {
					t.Fatal("invalid request reached auth")
				}
				if !exhausted {
					consumeAdmission(t, gate, 10)
				} else {
					assertAdmissionDenied(t, gate, 5)
				}
				nilHandler := admissionHandler(nil, gate)
				r := admissionRequest(endpoint)
				r.Header.Set("Content-Type", "text/plain")
				if w := admissionServe(nilHandler, r); w.Code != 503 {
					t.Fatalf("nil auth precedence: %d", w.Code)
				}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				admissionServe(h, admissionRequest(endpoint).WithContext(ctx))
				if backend.loginCalls.Load() != 0 || backend.bootstrapCalls.Load() != 0 {
					t.Fatal("canceled HTTP request reached backend")
				}
			})
		}
	}
}

func TestAuthAdmissionWholeChainConcurrency(t *testing.T) {
	gate, _ := admissionFixture()
	backend := &admissionAuth{}
	entered := make(chan string, 4)
	allowBootstrap := make(chan struct{})
	allowLogin := make(chan struct{})
	backend.bootstrapFn = func(context.Context) error { entered <- "bootstrap"; <-allowBootstrap; return nil }
	backend.loginFn = func(context.Context) error { entered <- "login"; <-allowLogin; return nil }
	h := admissionHandler(backend, gate)
	done := make(chan *httptest.ResponseRecorder, 2)
	for i := 0; i < 2; i++ {
		go func() { done <- admissionServe(h, admissionRequest("bootstrap")) }()
	}
	for i := 0; i < 2; i++ {
		select {
		case stage := <-entered:
			if stage != "bootstrap" {
				t.Fatal(stage)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("backend did not enter")
		}
	}
	assertRateLimited(t, admissionServe(h, admissionRequest("login")), "1")
	close(allowBootstrap)
	for i := 0; i < 2; i++ {
		select {
		case stage := <-entered:
			if stage != "login" {
				t.Fatal(stage)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("login did not enter")
		}
	}
	assertRateLimited(t, admissionServe(h, admissionRequest("bootstrap")), "1")
	close(allowLogin)
	for i := 0; i < 2; i++ {
		if w := <-done; w.Code != 201 {
			t.Fatal(w.Code)
		}
	}
	consumeAdmission(t, gate, 8)
	assertAdmissionDenied(t, gate, 5)
}

func TestAuthAdmissionReleaseOnFailuresAndPanic(t *testing.T) {
	for _, kind := range []string{"bootstrap-error", "login-error", "cancellation", "panic"} {
		t.Run(kind, func(t *testing.T) {
			gate, _ := admissionFixture()
			backend := &admissionAuth{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fail := func(context.Context) error {
				if kind == "panic" {
					panic("fixture")
				}
				if kind == "cancellation" {
					cancel()
					return ctx.Err()
				}
				return errors.New("fixture")
			}
			if kind == "bootstrap-error" {
				backend.bootstrapFn = fail
			} else {
				backend.loginFn = fail
			}
			h := admissionHandler(backend, gate)
			func() {
				defer func() {
					value := recover()
					if (value != nil) != (kind == "panic") {
						t.Fatalf("panic=%v", value)
					}
				}()
				w := admissionServe(h, admissionRequest("bootstrap").WithContext(ctx))
				if w.Code != 503 {
					t.Fatal(w.Code)
				}
				if len(w.Result().Cookies()) != 0 {
					t.Fatal("failure set cookie")
				}
			}()
			if gate.active != 0 {
				t.Fatal("permit leaked")
			}
			consumeAdmission(t, gate, 9)
			assertAdmissionDenied(t, gate, 5)
		})
	}
}

func TestAuthAdmissionCanceledAfterDecode(t *testing.T) {
	for _, endpoint := range []string{"login", "bootstrap"} {
		t.Run(endpoint, func(t *testing.T) {
			gate, _ := admissionFixture()
			backend := &admissionAuth{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			gate.now = func() time.Time { cancel(); return time.Now() }
			w := admissionServe(admissionHandler(backend, gate), admissionRequest(endpoint).WithContext(ctx))
			if w.Code != 503 || w.Header().Get("Retry-After") != "" || len(w.Result().Cookies()) != 0 {
				t.Fatalf("invalid canceled response: %d %v", w.Code, w.Header())
			}
			assertErrorCode(t, w, "AUTH_UNAVAILABLE")
			if backend.bootstrapCalls.Load() != 0 || backend.loginCalls.Load() != 0 || gate.active != 0 || gate.credit != authAttemptBurst*authAttemptInterval {
				t.Fatal("canceled request used credentials or budget")
			}
		})
	}
}

func TestAuthAdmissionCancellationHoldsRunningPermit(t *testing.T) {
	gate, _ := admissionFixture()
	backend := &admissionAuth{}
	entered := make(chan struct{}, 2)
	finish := make(chan struct{})
	backend.loginFn = func(ctx context.Context) error { entered <- struct{}{}; <-finish; return ctx.Err() }
	h := admissionHandler(backend, gate)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan *httptest.ResponseRecorder, 2)
	for i := 0; i < 2; i++ {
		go func() { done <- admissionServe(h, admissionRequest("login").WithContext(ctx)) }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("backend did not enter")
		}
	}
	cancel()
	assertRateLimited(t, admissionServe(h, admissionRequest("bootstrap")), "1")
	if backend.bootstrapCalls.Load() != 0 {
		t.Fatal("cancellation released running backend permit")
	}
	close(finish)
	for i := 0; i < 2; i++ {
		if w := <-done; w.Code != 503 {
			t.Fatalf("canceled backend response %d", w.Code)
		}
	}
	consumeAdmission(t, gate, 8)
	assertAdmissionDenied(t, gate, 5)
}
