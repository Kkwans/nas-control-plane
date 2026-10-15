package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type bodyLimitDecoder struct {
	name  string
	limit int
}

func bodyLimitDecoders() []bodyLimitDecoder {
	return []bodyLimitDecoder{{"credentials", 8192}, {"user", 8192}, {"control", 16384}, {"database", 1048576}}
}

// 所有 decoder 使用相同输入，验证完整读取必须在修改解码目标之前成功。
func checkBodyLimitDecode(t *testing.T, d bodyLimitDecoder, request *http.Request, status int, unchanged bool) {
	t.Helper()
	target := credentialsRequest{Username: "unchanged", Password: "unchanged"}
	before := target
	recorder := httptest.NewRecorder()
	api := &handler{newRequestID: func() string { return "body-limit" }}
	var err error
	var ok bool
	switch d.name {
	case "credentials":
		target, err = decodeCredentials(request)
		ok = err == nil
	case "user":
		err = decodeUserRequest(request, &target)
		ok = err == nil
	case "control":
		ok = api.decodeControlBody(recorder, request, &target)
	case "database":
		ok = api.decodeDatabaseBody(recorder, request, &target)
	}
	if ok != (status == 200) {
		t.Fatalf("accepted=%v want status=%d err=%v response=%s", ok, status, err, recorder.Body.String())
	}
	if status == 200 {
		return
	}
	if unchanged {
		if d.name == "credentials" {
			before = credentialsRequest{}
		}
		if target != before {
			t.Fatalf("failed pre-read mutated target: %+v", target)
		}
	}
	if d.name == "credentials" || d.name == "user" {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) != (status == 413) {
			t.Fatalf("error=%v want status=%d", err, status)
		}
		if tooLarge != nil && tooLarge.Limit != int64(d.limit) {
			t.Fatalf("limit=%d", tooLarge.Limit)
		}
	} else {
		code := "CONTROL_INPUT_INVALID"
		if d.name == "database" {
			code = "DATABASE_INPUT_INVALID"
		}
		if status == 413 {
			code = "HTTP_REQUEST_BODY_TOO_LARGE"
		}
		result := assertJSONMIMEError(t, recorder, status, code)
		if status == 413 && result.Message != "请求体超过大小限制。" {
			t.Fatalf("unstable 413 message: %q", result.Message)
		}
		if strings.Contains(recorder.Body.String(), "private-reader-secret") {
			t.Fatal("leaked read error")
		}
	}
}

type bodyLimitReader struct {
	data            string
	chunk           int
	terminal        error
	withFinal       bool
	calls, consumed int
	cancel          context.CancelFunc
}

func (r *bodyLimitReader) Read(p []byte) (int, error) {
	r.calls++
	if len(r.data) == 0 {
		if r.cancel != nil {
			r.cancel()
		}
		return 0, r.terminal
	}
	if r.chunk > 0 && len(p) > r.chunk {
		p = p[:r.chunk]
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	r.consumed += n
	if len(r.data) == 0 && r.withFinal {
		if r.cancel != nil {
			r.cancel()
		}
		return n, r.terminal
	}
	return n, nil
}
func paddedJSON(s string, size int) string {
	return s + strings.Repeat(" \t\r\n", (size-len(s))/4) + strings.Repeat(" ", (size-len(s))%4)
}

func TestJSONBodyLimitDecoderMatrix(t *testing.T) {
	for _, d := range bodyLimitDecoders() {
		exact := paddedJSON(`{"username":"中文","password":"secret"}`, d.limit)
		cases := []struct {
			name, payload string
			status        int
		}{
			{"below", exact[:d.limit-1], 200}, {"exact", exact, 200}, {"space-over", exact + " ", 413},
			{"hidden-value", exact + "{}", 413}, {"hidden-garbage", exact + "!", 413},
			{"whitespace-over", strings.Repeat(" ", d.limit+1), 413}, {"malformed-over", "!" + exact, 413},
			{"truncated-json", `{"username":"` + strings.Repeat("x", d.limit), 413},
			{"utf8-exact", `{"username":"` + strings.Repeat("中", (d.limit-15)/3) + `"}` + strings.Repeat(" ", (d.limit-15)%3), 200},
		}
		for _, tc := range cases {
			for _, chunk := range []int{1, 7, 32768} {
				for _, length := range []int64{-1, 0, int64(len(tc.payload)), int64(d.limit), int64(d.limit + 1), math.MaxInt64} {
					t.Run(fmt.Sprintf("%s/%s/chunk-%d/length-%d", d.name, tc.name, chunk, length), func(t *testing.T) {
						source := &bodyLimitReader{data: tc.payload, chunk: chunk, terminal: io.EOF}
						spy := &jsonMIMEReadSpy{reader: source}
						request := jsonMIMERequest("POST", "/", "", []string{"application/json"})
						request.Body = spy
						request.ContentLength = length
						checkBodyLimitDecode(t, d, request, tc.status, tc.status != 200)
						if spy.bytes > d.limit+1 || spy.closed {
							t.Fatalf("consumed=%d closed=%v", spy.bytes, spy.closed)
						}
						want := len(tc.payload)
						if want > d.limit+1 {
							want = d.limit + 1
						}
						if spy.bytes != want {
							t.Fatalf("consumed=%d want=%d", spy.bytes, want)
						}
					})
				}
			}
		}
	}
}

func TestJSONBodyLimitReadErrorsAndEOF(t *testing.T) {
	failure := errors.New("private-reader-secret")
	for _, d := range bodyLimitDecoders() {
		for _, size := range []int{2, d.limit, d.limit + 1} {
			for _, terminal := range []error{io.EOF, failure} {
				for _, withFinal := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/size-%d/error-%v/final-%v", d.name, size, terminal, withFinal), func(t *testing.T) {
						source := &bodyLimitReader{data: paddedJSON("{}", size), terminal: terminal, withFinal: withFinal}
						request := jsonMIMERequest("POST", "/", "", []string{"application/json"})
						request.Body = io.NopCloser(source)
						status := 200
						if terminal != io.EOF {
							status = 400
						}
						if size > d.limit {
							status = 413
						}
						checkBodyLimitDecode(t, d, request, status, status != 200)
						if source.consumed > d.limit+1 {
							t.Fatalf("over-read=%d", source.consumed)
						}
					})
				}
			}
		}
	}
}

func TestJSONBodyLimitCancellation(t *testing.T) {
	for _, d := range bodyLimitDecoders() {
		for _, mode := range []string{"before", "during", "last-eof", "last-error", "over-limit"} {
			t.Run(d.name+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				request := jsonMIMERequest("POST", "/", "", []string{"application/json"}).WithContext(context.WithValue(ctx, requestIDContextKey{}, "body-limit-cancel"))
				calls := 0
				payload := "{}"
				if mode == "over-limit" {
					payload = paddedJSON("{}", d.limit+1)
				}
				source := &bodyLimitReader{data: payload, terminal: io.EOF, withFinal: true, cancel: cancel}
				request.Body = io.NopCloser(jsonMIMEReaderFunc(func(p []byte) (int, error) {
					calls++
					if mode == "during" {
						p[0] = '{'
						cancel()
						return 1, nil
					}
					if mode == "last-error" {
						source.terminal = errors.New("private-reader-secret")
					}
					return source.Read(p)
				}))
				if mode == "before" {
					cancel()
				}
				status := 400
				if mode == "over-limit" {
					status = 413
				}
				checkBodyLimitDecode(t, d, request, status, true)
				if mode == "before" && calls != 0 {
					t.Fatalf("pre-cancel reads=%d", calls)
				}
				if mode == "during" && calls != 1 {
					t.Fatalf("continued reading after cancel: %d", calls)
				}
			})
		}
	}
}

func TestJSONBodyLimitNoProgressAndNil(t *testing.T) {
	for _, d := range bodyLimitDecoders() {
		for _, body := range []io.ReadCloser{nil, http.NoBody} {
			request := jsonMIMERequest("POST", "/", "", []string{"application/json"})
			request.Body = body
			checkBodyLimitDecode(t, d, request, 400, false)
		}
		for _, stall := range []int{3, 99, 100} {
			t.Run(fmt.Sprintf("%s/empty-reads-%d", d.name, stall), func(t *testing.T) {
				calls := 0
				remaining := "{}"
				request := jsonMIMERequest("POST", "/", "", []string{"application/json"})
				request.Body = io.NopCloser(jsonMIMEReaderFunc(func(p []byte) (int, error) {
					calls++
					if calls > 105 {
						t.Fatal("unbounded no-progress reads")
					}
					if calls <= stall {
						return 0, nil
					}
					n := copy(p, remaining)
					remaining = remaining[n:]
					return n, io.EOF
				}))
				status := 200
				if stall == 100 {
					status = 400
				}
				checkBodyLimitDecode(t, d, request, status, status != 200)
				if stall == 100 && calls != 100 {
					t.Fatalf("no-progress calls=%d", calls)
				}
			})
		}
	}
	t.Run("empty-read-counter-resets", func(t *testing.T) {
		request := jsonMIMERequest("POST", "/", "", []string{"application/json"})
		calls := 0
		request.Body = io.NopCloser(jsonMIMEReaderFunc(func(p []byte) (int, error) {
			calls++
			if calls > 201 {
				t.Fatal("no progress")
			}
			if calls == 100 {
				p[0] = '{'
				return 1, nil
			}
			if calls == 200 {
				p[0] = '}'
				return 1, io.EOF
			}
			return 0, nil
		}))
		checkBodyLimitDecode(t, bodyLimitDecoders()[0], request, 200, false)
	})
}

func TestJSONBodyLimitRoutesRejectBeforeSideEffects(t *testing.T) {
	routes := jsonMIMERoutes()
	if len(routes) != 36 {
		t.Fatalf("route inventory=%d", len(routes))
	}
	for _, route := range routes {
		t.Run(route.method+route.path, func(t *testing.T) {
			limit := 16384
			if route.inputError == "AUTH_INPUT_INVALID" {
				limit = 8192
			}
			if route.inputError == "DATABASE_INPUT_INVALID" {
				limit = 1048576
			}
			agent, store, authenticator := &jsonMIMERouteAgent{}, &jsonMIMERouteStore{}, &jsonMIMERouteAuth{}
			jobs := newJobRegistry(nil)
			var logs bytes.Buffer
			h := NewHandler(Config{Agent: agent, DatabaseAgent: agent, DockerImages: agent, DockerResources: agent, Filesystem: agent, Compose: agent, ControlStore: store, Auth: authenticator, JobEngine: jobs, RequestID: func() string { return "body-limit-route" }, Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
			request := jsonMIMERequest(route.method, "http://nas.test/api/v1"+route.path, paddedJSON(`{"password":"body-limit-secret"}`, limit)+" {}", []string{"application/json"})
			request.Header.Set("Origin", "http://nas.test")
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "body-limit-cookie-secret"})
			spy := &jsonMIMEReadSpy{reader: request.Body}
			request.Body = spy
			request.ContentLength = -1
			recorder := httptest.NewRecorder()
			h.ServeHTTP(recorder, request)
			result := assertJSONMIMEError(t, recorder, 413, "HTTP_REQUEST_BODY_TOO_LARGE")
			if result.Message != "请求体超过大小限制。" || result.RequestID != "body-limit-route" || recorder.Header().Get("X-Request-ID") != result.RequestID {
				t.Fatalf("unstable error envelope=%+v", result)
			}
			if len(recorder.Result().Cookies()) != 0 || agent.calls.Load() != 0 || store.calls.Load() != 0 || authenticator.calls.Load() != 0 || len(jobs.list("")) != 0 {
				t.Fatal("oversize rejection caused business side effects")
			}
			if spy.bytes != limit+1 || spy.closed {
				t.Fatalf("consumed=%d closed=%v", spy.bytes, spy.closed)
			}
			for _, secret := range []string{"body-limit-secret", "body-limit-cookie-secret"} {
				if strings.Contains(recorder.Body.String(), secret) || strings.Contains(logs.String(), secret) {
					t.Fatalf("leaked %s", secret)
				}
			}
		})
	}
}

func TestJSONBodyLimitOptionalInspect(t *testing.T) {
	for _, size := range []int{16384, 16385} {
		for _, length := range []int64{-1, 0, math.MaxInt64} {
			t.Run(fmt.Sprintf("size-%d/length-%d", size, length), func(t *testing.T) {
				agent := &systemHTTPAgent{fakeAgentClient: &fakeAgentClient{}}
				h := NewHandler(Config{Agent: agent, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
				request := jsonMIMERequest("POST", "/api/v1/proxy/mihomo/inspect", "", []string{"application/json"})
				spy := &jsonMIMEReadSpy{reader: &bodyLimitReader{data: paddedJSON(`{"force":true}`, size), chunk: 7, terminal: io.EOF}}
				request.Body = spy
				request.ContentLength = length
				recorder := httptest.NewRecorder()
				h.ServeHTTP(recorder, request)
				if size == 16384 {
					if recorder.Code != 200 || !agent.inspectionCalled || !agent.inspectionForce {
						t.Fatalf("exact inspect=%d called=%v force=%v", recorder.Code, agent.inspectionCalled, agent.inspectionForce)
					}
				} else {
					assertJSONMIMEError(t, recorder, 413, "HTTP_REQUEST_BODY_TOO_LARGE")
					if agent.inspectionCalled {
						t.Fatal("oversize called Agent")
					}
				}
				if spy.bytes != size || spy.closed {
					t.Fatalf("prefix consumption=%d want=%d closed=%v", spy.bytes, size, spy.closed)
				}
				if err := request.Body.Close(); err != nil || !spy.closed {
					t.Fatal("Close chain not preserved")
				}
			})
		}
	}
	for _, mode := range []string{"before-nil", "before-empty", "probe-empty", "probe-byte"} {
		t.Run(mode, func(t *testing.T) {
			agent := &systemHTTPAgent{fakeAgentClient: &fakeAgentClient{}}
			h := NewHandler(Config{Agent: agent, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := jsonMIMERequest("POST", "/api/v1/proxy/mihomo/inspect", "", []string{"application/json"}).WithContext(context.WithValue(ctx, requestIDContextKey{}, "body-limit-cancel"))
			calls := 0
			request.Body = io.NopCloser(jsonMIMEReaderFunc(func(p []byte) (int, error) {
				calls++
				cancel()
				if mode == "probe-byte" {
					p[0] = '{'
					return 1, nil
				}
				return 0, io.EOF
			}))
			if strings.HasPrefix(mode, "before") {
				cancel()
			}
			if mode == "before-nil" {
				request.Body = nil
			}
			recorder := httptest.NewRecorder()
			h.ServeHTTP(recorder, request)
			assertJSONMIMEError(t, recorder, 400, "CONTROL_INPUT_INVALID")
			if agent.inspectionCalled {
				t.Fatal("cancelled optional inspect called Agent")
			}
			if strings.HasPrefix(mode, "before") && calls != 0 {
				t.Fatal("pre-cancel probe read body")
			}
		})
	}
}

type bodyLimitCancelJSON struct{ cancel context.CancelFunc }

func (v *bodyLimitCancelJSON) UnmarshalJSON([]byte) error { v.cancel(); return nil }

func TestJSONBodyLimitCancellationDuringDecode(t *testing.T) {
	for _, name := range []string{"user", "control", "database"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := jsonMIMERequest("POST", "/", "{}", []string{"application/json"}).WithContext(context.WithValue(ctx, requestIDContextKey{}, "body-limit-cancel"))
			target := &bodyLimitCancelJSON{cancel: cancel}
			recorder := httptest.NewRecorder()
			api := &handler{}
			switch name {
			case "user":
				if err := decodeUserRequest(request, target); !errors.Is(err, context.Canceled) {
					t.Fatalf("decode cancellation=%v", err)
				}
			case "control":
				if api.decodeControlBody(recorder, request, target) {
					t.Fatal("cancelled decode succeeded")
				}
				assertJSONMIMEError(t, recorder, 400, "CONTROL_INPUT_INVALID")
			case "database":
				if api.decodeDatabaseBody(recorder, request, target) {
					t.Fatal("cancelled decode succeeded")
				}
				assertJSONMIMEError(t, recorder, 400, "DATABASE_INPUT_INVALID")
			}
		})
	}
}

func TestJSONBodyLimitPreservesGuardOrder(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, origin, mime, code string
		status                                 int
		cookie, unavailable                    bool
	}{
		{"origin", "PUT", "/preferences", "http://other.test", "application/json", "HTTP_CROSS_ORIGIN_REJECTED", 403, true, false},
		{"authentication", "PUT", "/preferences", "http://nas.test", "application/json", "AUTH_UNAUTHORIZED", 401, false, false},
		{"media-type", "PUT", "/preferences", "http://nas.test", "text/plain", "HTTP_UNSUPPORTED_MEDIA_TYPE", 415, true, false},
		{"invalid-user-id", "PUT", "/users/not-an-id/status", "http://nas.test", "application/json", "AUTH_INPUT_INVALID", 400, true, false},
		{"auth-unavailable", "POST", "/auth/login", "", "application/json", "AUTH_UNAVAILABLE", 503, false, true},
		{"store-unavailable", "PUT", "/preferences", "", "application/json", "PREFERENCES_UNAVAILABLE", 503, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent, store, authenticator := &jsonMIMERouteAgent{}, &jsonMIMERouteStore{}, &jsonMIMERouteAuth{}
			config := Config{Agent: agent, ControlStore: store, Auth: authenticator, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			if tc.unavailable {
				config.Auth = nil
				config.ControlStore = nil
			}
			h := NewHandler(config)
			request := jsonMIMERequest(tc.method, "http://nas.test/api/v1"+tc.path, strings.Repeat("!", 1048577), []string{tc.mime})
			if tc.origin != "" {
				request.Header.Set("Origin", tc.origin)
			}
			if tc.cookie {
				request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "fixture"})
			}
			spy := &jsonMIMEReadSpy{reader: request.Body}
			request.Body = spy
			recorder := httptest.NewRecorder()
			h.ServeHTTP(recorder, request)
			assertJSONMIMEError(t, recorder, tc.status, tc.code)
			if spy.calls != 0 || agent.calls.Load() != 0 || store.calls.Load() != 0 || authenticator.calls.Load() != 0 {
				t.Fatal("guard read body or caused side effects")
			}
			if tc.status == 403 && authenticator.authentications.Load() != 0 {
				t.Fatal("origin guard ran after authentication")
			}
		})
	}
}

func TestJSONBodyLimitHelperSmallLimits(t *testing.T) {
	for _, limit := range []int64{-1, 0, 1, math.MaxInt64} {
		for _, payload := range []string{"", "x", "xy"} {
			t.Run(fmt.Sprintf("limit-%d/bytes-%d", limit, len(payload)), func(t *testing.T) {
				request := jsonMIMERequest("POST", "/", payload, []string{"application/json"})
				original := request.Body
				data, err := readBoundedJSONBody(request, limit)
				effective := limit
				if effective < 0 {
					effective = 0
				}
				var tooLarge *http.MaxBytesError
				if int64(len(payload)) > effective {
					if !errors.As(err, &tooLarge) || data != nil {
						t.Fatalf("data=%q err=%v", data, err)
					}
				} else if err != nil || string(data) != payload {
					t.Fatalf("data=%q err=%v", data, err)
				}
				if request.Body != original {
					t.Fatal("helper replaced original body")
				}
			})
		}
	}
	request := jsonMIMERequest("POST", "/", "", []string{"application/json"})
	calls := 0
	request.Body = io.NopCloser(jsonMIMEReaderFunc(func([]byte) (int, error) {
		calls++
		if calls > 100 {
			t.Fatal("unbounded read")
		}
		return 0, nil
	}))
	if data, err := readBoundedJSONBody(request, 10); !errors.Is(err, io.ErrNoProgress) || data != nil {
		t.Fatalf("data=%q err=%v", data, err)
	}
}
