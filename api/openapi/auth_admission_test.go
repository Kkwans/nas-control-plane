package openapi_test

import (
	"github.com/getkin/kin-openapi/openapi3"
	"strings"
	"testing"
)

func TestAuthAdmissionContract(t *testing.T) {
	document, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"bootstrap", "login"} {
		response := document.Paths.Value("/api/v1/auth/" + endpoint).Post.Responses.Value("429")
		if response == nil || response.Value == nil {
			t.Fatalf("%s missing 429", endpoint)
		}
		value := response.Value
		media := value.Content.Get("application/json")
		if media == nil || media.Schema == nil || media.Schema.Ref != "#/components/schemas/ErrorResponse" {
			t.Fatal("429 must use ErrorResponse")
		}
		header := value.Headers["Retry-After"]
		if header == nil || header.Value == nil || !header.Value.Required || header.Value.Schema == nil {
			t.Fatal("missing required Retry-After")
		}
		schema := header.Value.Schema.Value
		if !schema.Type.Is("integer") || schema.Min == nil || *schema.Min != 1 {
			t.Fatal("Retry-After must be integer >=1")
		}
		if value.Headers["Set-Cookie"] != nil {
			t.Fatal("429 must not set cookies")
		}
		for _, clause := range []string{"AUTH_RATE_LIMITED", "requestId", "不设置或清除 Cookie", "login/bootstrap", "10", "5 秒", "2 个", "不自动重放"} {
			if value.Description == nil || !strings.Contains(*value.Description, clause) {
				t.Fatalf("missing clause %q", clause)
			}
		}
	}
	for _, endpoint := range []string{"status", "logout"} {
		item := document.Paths.Value("/api/v1/auth/" + endpoint)
		op := item.Post
		if endpoint == "status" {
			op = item.Get
		}
		if op.Responses.Value("429") != nil {
			t.Fatal("unrelated auth route throttled")
		}
	}
}
