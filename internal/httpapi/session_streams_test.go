package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Kkwans/nas-control-plane/internal/auth"
	"github.com/Kkwans/nas-control-plane/internal/terminal"
)

var liveSessionPaths = []string{"/ws/terminal?target=host", "/api/v1/system/events?interval=300", "/api/v1/logs/events?interval=300", "/api/v1/jobs/fake/events"}

type streamAdmissionAuth struct {
	*auth.Service
	authenticate func(context.Context, string) (auth.Principal, error)
	acquire      func(context.Context, string) (*auth.StreamLease, error)
}

func (a *streamAdmissionAuth) Authenticate(ctx context.Context, token string) (auth.Principal, error) {
	if a.authenticate != nil {
		return a.authenticate(ctx, token)
	}
	return a.Service.Authenticate(ctx, token)
}
func (a *streamAdmissionAuth) AcquireStream(ctx context.Context, token string) (*auth.StreamLease, error) {
	if a.acquire != nil {
		return a.acquire(ctx, token)
	}
	return a.Service.AcquireStream(ctx, token)
}
func streamTestSession(t *testing.T, s *auth.Service) auth.Session {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Bootstrap(ctx, "root-stream", testLoginPassword(t)); err != nil {
		t.Fatal(err)
	}
	session, err := s.Login(ctx, "root-stream", testLoginPassword(t))
	if err != nil {
		t.Fatal(err)
	}
	return session
}
func streamTestRequest(path, token string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	return r
}

func TestLiveSessionRoutesRejectRevocationAfterOrdinaryAuthentication(t *testing.T) {
	for _, path := range liveSessionPaths {
		t.Run(path, func(t *testing.T) {
			s := openTestAuthService(t)
			session := streamTestSession(t, s)
			var once sync.Once
			provider := &streamAdmissionAuth{Service: s, authenticate: func(ctx context.Context, token string) (auth.Principal, error) {
				p, err := s.Authenticate(ctx, token)
				once.Do(func() {
					if err := s.Logout(ctx, token); err != nil {
						t.Error(err)
					}
				})
				return p, err
			}}
			h := NewHandler(Config{Auth: provider, Agent: &fakeAgentClient{}, TerminalEnabled: true})
			response := httptest.NewRecorder()
			h.ServeHTTP(response, streamTestRequest(path, session.Token))
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertErrorCode(t, response, "AUTH_UNAUTHORIZED")
			if response.Header().Get("Upgrade") != "" || response.Header().Get("Content-Type") == "text/event-stream" {
				t.Fatal("stream headers committed before live admission")
			}
		})
	}
}

func TestLiveSessionRoutesFailClosedForUnsupportedProvider(t *testing.T) {
	provider := &transientSessionAuth{}
	h := NewHandler(Config{Auth: provider, Agent: &fakeAgentClient{}, TerminalEnabled: true})
	for _, path := range liveSessionPaths {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			h.ServeHTTP(response, streamTestRequest(path, "fake"))
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d", response.Code)
			}
			assertErrorCode(t, response, "AUTH_STREAM_UNAVAILABLE")
		})
	}
}

func TestLiveSessionRoutesReportSharedLeaseLimit(t *testing.T) {
	s, err := auth.Open(filepath.Join(t.TempDir(), "auth.sqlite"), auth.Options{StreamLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	session := streamTestSession(t, s)
	held, err := s.AcquireStream(context.Background(), session.Token)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	h := NewHandler(Config{Auth: s, Agent: &fakeAgentClient{}, TerminalEnabled: true})
	for _, path := range liveSessionPaths {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			h.ServeHTTP(response, streamTestRequest(path, session.Token))
			if response.Code != 503 {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertErrorCode(t, response, "AUTH_STREAM_LIMIT_REACHED")
		})
	}
	held.Release()
	lease, err := s.AcquireStream(context.Background(), session.Token)
	if err != nil {
		t.Fatalf("rejected streams consumed slots: %v", err)
	}
	lease.Release()
}

func TestLiveSessionWrapperRefreshesPrincipalAndReleasesLease(t *testing.T) {
	s := openTestAuthService(t)
	session := streamTestSession(t, s)
	provider := &streamAdmissionAuth{Service: s, authenticate: func(context.Context, string) (auth.Principal, error) {
		return auth.Principal{ID: 999, Username: "stale", Role: auth.RootRole}, nil
	}}
	api := &handler{auth: provider}
	metadata := &requestMetadata{}
	r := streamTestRequest("/stream", session.Token)
	r = r.WithContext(context.WithValue(r.Context(), requestMetadataContextKey{}, metadata))
	var streamContext context.Context
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		streamContext = r.Context()
		p, _ := r.Context().Value(principalContextKey{}).(auth.Principal)
		if p != session.Principal {
			t.Fatalf("principal=%+v", p)
		}
		if metadata.actor != session.Principal.Username {
			t.Fatalf("actor=%s", metadata.actor)
		}
		w.WriteHeader(204)
	})
	response := httptest.NewRecorder()
	api.requireAuthentication(api.requireLiveSession(next)).ServeHTTP(response, r)
	if response.Code != 204 || streamContext == nil || streamContext.Err() == nil {
		t.Fatal("lease not released after handler return")
	}
}

func TestLiveSessionWrapperLogoutCancelsActiveRequest(t *testing.T) {
	s := openTestAuthService(t)
	session := streamTestSession(t, s)
	api := &handler{auth: s}
	entered, done := make(chan struct{}), make(chan struct{})
	response := httptest.NewRecorder()
	h := api.requireLiveSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() }))
	go func() { defer close(done); h.ServeHTTP(response, streamTestRequest("/stream", session.Token)) }()
	awaitProxySignal(t, entered, "live request")
	if err := s.Logout(context.Background(), session.Token); err != nil {
		t.Fatal(err)
	}
	awaitProxySignal(t, done, "revoked request exit")
}

func TestLiveSessionWrapperRejectsCanceledLeaseBeforeHandler(t *testing.T) {
	s := openTestAuthService(t)
	session := streamTestSession(t, s)
	provider := &streamAdmissionAuth{Service: s, acquire: func(ctx context.Context, token string) (*auth.StreamLease, error) {
		lease, err := s.AcquireStream(ctx, token)
		if err == nil {
			lease.Release()
		}
		return lease, err
	}}
	api := &handler{auth: provider}
	response := httptest.NewRecorder()
	api.requireLiveSession(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("entered handler with canceled lease") })).ServeHTTP(response, streamTestRequest("/stream", session.Token))
	if response.Code != 401 {
		t.Fatalf("status=%d", response.Code)
	}
	assertErrorCode(t, response, "AUTH_UNAUTHORIZED")
}

func TestLiveSessionLogoutClosesTerminalProxyAndReleasesAfterPumps(t *testing.T) {
	s := openTestAuthService(t)
	session := streamTestSession(t, s)
	api := &handler{auth: s}
	c, stream := newProxyTestConnection(), newProxyTestStream()
	ready, done := make(chan struct{}), make(chan struct{})
	stream.recv = func(n int32) (terminal.Message, error) {
		if n == 1 {
			return terminal.Message{Type: terminal.MessageStarted, SessionID: "lease-terminal"}, nil
		}
		close(ready)
		<-stream.closed
		return terminal.Message{}, io.EOF
	}
	h := api.requireLiveSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runTerminalProxy(r.Context(), c, proxyTestClient{open: func(context.Context, string) (TerminalStream, error) { return stream, nil }}, "fake", terminal.Message{Type: terminal.MessageStart})
	}))
	go func() {
		defer close(done)
		h.ServeHTTP(httptest.NewRecorder(), streamTestRequest("/terminal", session.Token))
	}()
	awaitProxySignal(t, ready, "active terminal output pump")
	if err := s.Logout(context.Background(), session.Token); err != nil {
		t.Fatal(err)
	}
	awaitProxySignal(t, done, "terminal revocation cleanup")
	assertProxyClosed(t, c, stream)
}
