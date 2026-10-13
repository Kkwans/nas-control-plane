package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type jsonMIMECase struct {
	name   string
	values []string
	valid  bool
}

func jsonMIMECases() []jsonMIMECase {
	return []jsonMIMECase{
		{"missing", nil, false},
		{"empty", []string{""}, false},
		{"whitespace", []string{" \t "}, false},
		{"plain", []string{"text/plain"}, false},
		{"form", []string{"application/x-www-form-urlencoded"}, false},
		{"multipart", []string{"multipart/form-data; boundary=fixture"}, false},
		{"text-json", []string{"text/json"}, false},
		{"vendor-json", []string{"application/vnd.ncp+json"}, false},
		{"problem-json", []string{"application/problem+json"}, false},
		{"identical-fields", []string{"application/json", "application/json"}, false},
		{"different-fields", []string{"application/json", "text/plain"}, false},
		{"blank-second-field", []string{"application/json", ""}, false},
		{"comma-list", []string{"application/json, application/json"}, false},
		{"missing-subtype", []string{"application/"}, false},
		{"bare-parameter", []string{"application/json; charset"}, false},
		{"unclosed-quote", []string{`application/json; charset="utf-8`}, false},
		{"conflicting-parameters", []string{"application/json; charset=utf-8; charset=utf-16"}, false},
		{"json", []string{"application/json"}, true},
		{"media-type-case", []string{"Application/JSON"}, true},
		{"utf8-parameter", []string{"application/json; charset=utf-8"}, true},
		{"quoted-parameter", []string{`application/json; charset="utf-8"`}, true},
		{"other-valid-parameter", []string{`application/json; profile="urn:ncp:fixture"`}, true},
		{"identical-parameters", []string{"application/json; charset=utf-8; charset=utf-8"}, true},
		{"trailing-semicolon", []string{"application/json;"}, true},
		{"charset-no-transcoding", []string{"application/json; charset=iso-8859-1"}, true},
	}
}

type jsonMIMEReadSpy struct {
	reader     io.Reader
	calls      int
	bytes      int
	maxRequest int
	closed     bool
}

func (s *jsonMIMEReadSpy) Read(p []byte) (int, error) {
	s.calls++
	if len(p) > s.maxRequest {
		s.maxRequest = len(p)
	}
	n, err := s.reader.Read(p)
	s.bytes += n
	return n, err
}
func (s *jsonMIMEReadSpy) Close() error { s.closed = true; return nil }

func jsonMIMERequest(method, path, body string, values []string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if values != nil {
		request.Header["Content-Type"] = append([]string(nil), values...)
	}
	return request.WithContext(context.WithValue(request.Context(), requestIDContextKey{}, "mime-fixture"))
}

func TestJSONContentTypeMIMEContract(t *testing.T) {
	for _, tc := range jsonMIMECases() {
		t.Run(tc.name, func(t *testing.T) {
			request := jsonMIMERequest(http.MethodPost, "/", `{}`, tc.values)
			spy := &jsonMIMEReadSpy{reader: request.Body}
			request.Body = spy
			err := validateJSONContentType(request)
			if tc.valid && err != nil {
				t.Fatalf("valid MIME rejected: %v", err)
			}
			if !tc.valid && !errors.Is(err, errUnsupportedMediaType) {
				t.Fatalf("error=%v, want unsupported-media sentinel", err)
			}
			if spy.calls != 0 {
				t.Fatalf("MIME validation read body %d times", spy.calls)
			}
		})
	}
}

func TestJSONDecodersMIMEContract(t *testing.T) {
	for _, decoder := range []string{"credentials", "user", "control", "database"} {
		for _, tc := range jsonMIMECases() {
			t.Run(decoder+"/"+tc.name, func(t *testing.T) {
				request := jsonMIMERequest(http.MethodPost, "/", `{"username":"fixture-中文","password":"fixture-password"}`, tc.values)
				spy := &jsonMIMEReadSpy{reader: request.Body}
				request.Body = spy
				before := credentialsRequest{Username: "unchanged", Password: "unchanged"}
				target := before
				api := &handler{newRequestID: func() string { return "mime-decoder" }}
				response := httptest.NewRecorder()
				var err error
				var ok bool
				switch decoder {
				case "credentials":
					target, err = decodeCredentials(request)
					ok = err == nil
				case "user":
					err = decodeUserRequest(request, &target)
					ok = err == nil
				case "control":
					ok = api.decodeControlBody(response, request, &target)
				case "database":
					ok = api.decodeDatabaseBody(response, request, &target)
				}
				if ok != tc.valid {
					t.Fatalf("accepted=%v want=%v err=%v response=%s", ok, tc.valid, err, response.Body.String())
				}
				if tc.valid {
					if target.Username != "fixture-中文" || target.Password != "fixture-password" {
						t.Fatalf("decoded target=%+v", target)
					}
					if spy.calls == 0 {
						t.Fatal("valid MIME did not decode body")
					}
					return
				}
				if spy.calls != 0 {
					t.Fatalf("rejected MIME read body %d times (%d bytes)", spy.calls, spy.bytes)
				}
				if decoder == "credentials" {
					if target != (credentialsRequest{}) {
						t.Fatalf("rejected credentials returned payload: %+v", target)
					}
				} else if target != before {
					t.Fatalf("rejected MIME mutated target: %+v", target)
				}
				if decoder == "credentials" || decoder == "user" {
					if !errors.Is(err, errUnsupportedMediaType) {
						t.Fatalf("error=%v, want unsupported-media sentinel", err)
					}
				} else {
					assertJSONMIMEError(t, response, http.StatusUnsupportedMediaType, "HTTP_UNSUPPORTED_MEDIA_TYPE")
				}
			})
		}
	}
}

func TestJSONDecodersKeepInvalidJSONErrors(t *testing.T) {
	for _, decoder := range []string{"credentials", "user", "control", "database"} {
		for name, payload := range map[string]string{"empty": "", "whitespace": " \r\n\t", "malformed": "{", "unknown-field": `{"unknown":true}`, "multiple-values": `{} {}`} {
			t.Run(decoder+"/"+name, func(t *testing.T) {
				request := jsonMIMERequest(http.MethodPost, "/", payload, []string{"application/json"})
				response := httptest.NewRecorder()
				api := &handler{newRequestID: func() string { return "invalid-json" }}
				var target credentialsRequest
				switch decoder {
				case "credentials":
					_, err := decodeCredentials(request)
					if err == nil || errors.Is(err, errUnsupportedMediaType) {
						t.Fatalf("JSON error=%v", err)
					}
				case "user":
					err := decodeUserRequest(request, &target)
					if err == nil || errors.Is(err, errUnsupportedMediaType) {
						t.Fatalf("JSON error=%v", err)
					}
				case "control":
					if api.decodeControlBody(response, request, &target) {
						t.Fatal("invalid JSON accepted")
					}
					assertJSONMIMEError(t, response, 400, "CONTROL_INPUT_INVALID")
				case "database":
					if api.decodeDatabaseBody(response, request, &target) {
						t.Fatal("invalid JSON accepted")
					}
					assertJSONMIMEError(t, response, 400, "DATABASE_INPUT_INVALID")
				}
			})
		}
	}
}

func TestJSONDatabaseDecoderKeepsExactNumbers(t *testing.T) {
	request := jsonMIMERequest(http.MethodPost, "/", `{"large":9007199254740993,"decimal":12345678901234567890.123456789}`, []string{"application/json; charset=utf-8"})
	var target map[string]any
	response := httptest.NewRecorder()
	if !(&handler{}).decodeDatabaseBody(response, request, &target) {
		t.Fatalf("decode failed: %s", response.Body.String())
	}
	for key, want := range map[string]string{"large": "9007199254740993", "decimal": "12345678901234567890.123456789"} {
		value, ok := target[key].(json.Number)
		if !ok || value.String() != want {
			t.Fatalf("%s=%#v, want exact json.Number %s", key, target[key], want)
		}
	}
}

func assertJSONMIMEError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) ErrorResponse {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status=%d want=%d body=%s", response.Code, status, response.Body.String())
	}
	var result ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if result.Code != code || result.RequestID == "" {
		t.Fatalf("error response=%+v, want %s and requestId", result, code)
	}
	return result
}

type jsonMIMEReaderFunc func([]byte) (int, error)

func (f jsonMIMEReaderFunc) Read(p []byte) (int, error) { return f(p) }

func TestJSONRequestHasBodyBoundedProbe(t *testing.T) {
	for _, length := range []int64{-1, 0, 25} {
		for _, content := range []string{"", `{"force":true}`} {
			request := httptest.NewRequest(http.MethodPost, "/", nil)
			spy := &jsonMIMEReadSpy{reader: strings.NewReader(content)}
			request.Body, request.ContentLength = spy, length
			hasBody, err := requestHasBody(request)
			if err != nil || hasBody != (content != "") {
				t.Fatalf("length=%d content=%q hasBody=%v err=%v", length, content, hasBody, err)
			}
			if spy.bytes > 1 || spy.maxRequest > 1 {
				t.Fatalf("probe read too much: bytes=%d requested=%d", spy.bytes, spy.maxRequest)
			}
			remaining, err := io.ReadAll(request.Body)
			if err != nil || string(remaining) != content {
				t.Fatalf("probe lost body: %q err=%v", remaining, err)
			}
			if spy.closed {
				t.Fatal("probe closed underlying body")
			}
			if err := request.Body.Close(); err != nil || !spy.closed {
				t.Fatalf("close not preserved: err=%v closed=%v", err, spy.closed)
			}
		}
	}
	for _, body := range []io.ReadCloser{nil, http.NoBody} {
		request := httptest.NewRequest(http.MethodPost, "/", nil)
		request.Body, request.ContentLength = body, 20
		if hasBody, err := requestHasBody(request); err != nil || hasBody {
			t.Fatalf("nil/NoBody probe=%v err=%v", hasBody, err)
		}
	}
}

func TestJSONRequestHasBodyShortReadsAndErrors(t *testing.T) {
	t.Run("empty-short-read", func(t *testing.T) {
		calls := 0
		request := httptest.NewRequest(http.MethodPost, "/", nil)
		request.Body = io.NopCloser(jsonMIMEReaderFunc(func(p []byte) (int, error) {
			calls++
			if calls == 1 {
				return 0, nil
			}
			p[0] = '{'
			return 1, io.EOF
		}))
		if hasBody, err := requestHasBody(request); err != nil || !hasBody || calls != 2 {
			t.Fatalf("short-read probe=%v err=%v calls=%d", hasBody, err, calls)
		}
	})
	for _, n := range []int{0, 1} {
		t.Run(string(rune('0'+n))+"-bytes-with-error", func(t *testing.T) {
			failure := errors.New("private body read failure")
			request := httptest.NewRequest(http.MethodPost, "/", nil)
			request.Body = io.NopCloser(jsonMIMEReaderFunc(func(p []byte) (int, error) {
				if n != 0 {
					p[0] = '{'
				}
				return n, failure
			}))
			_, err := requestHasBody(request)
			if !errors.Is(err, failure) {
				t.Fatalf("read error was lost: %v", err)
			}
		})
	}
}
