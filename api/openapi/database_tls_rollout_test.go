package openapi_test

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestDatabaseTLSRolloutHTTPContract(t *testing.T) {
	document, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	credentials := document.Components.Schemas["DatabaseCredentials"].Value
	if len(credentials.Properties) != 4 || credentials.Properties["tls"] != nil {
		t.Fatal("T2 must not publish a TLS input schema")
	}
	for _, name := range []string{"connect", "test-connection", "catalog", "query", "rows", "rows/insert", "rows/update", "rows/delete"} {
		t.Run(name, func(t *testing.T) {
			operation := document.Paths.Value("/api/v1/databases/" + name).Post
			for _, requirement := range []string{"credentials.tls", "null", "重复", "大小写", "DATABASE_INPUT_INVALID"} {
				if !strings.Contains(operation.RequestBody.Value.Description, requirement) {
					t.Fatalf("rollout rejection missing %q", requirement)
				}
			}
			response := operation.Responses.Value("400")
			if response == nil || response.Value == nil || response.Value.Description == nil {
				t.Fatal("TLS errors must be documented as HTTP 400")
			}
			for _, code := range []string{"tls_configuration_invalid", "tls_policy_unsupported", "tls_verification_failed"} {
				if !strings.Contains(*response.Value.Description, code) {
					t.Fatalf("missing visible TLS code %q", code)
				}
			}
			if !strings.Contains(*response.Value.Description, "不保证 SQL 未执行或本次凭据未保存") {
				t.Fatal("TLS errors must not become a generic execution/save guarantee")
			}
			media := response.Value.Content.Get("application/json")
			if media == nil || media.Schema == nil || media.Schema.Ref != "#/components/schemas/ErrorResponse" {
				t.Fatal("TLS errors must retain the normal ErrorResponse envelope")
			}
			unavailable := operation.Responses.Value("503")
			if unavailable == nil || unavailable.Value == nil || unavailable.Value.Description == nil || !strings.Contains(*unavailable.Value.Description, "credential_store_unavailable") {
				t.Fatal("configured missing protected capability must fail closed")
			}
		})
	}
	description := document.Paths.Value("/api/v1/databases/test-connection").Post.Description
	for _, requirement := range []string{"继承已保存的 TLS 策略", "不保存或替换凭据", "不绕过"} {
		if !strings.Contains(description, requirement) {
			t.Fatalf("test-only policy contract missing %q", requirement)
		}
	}
}
