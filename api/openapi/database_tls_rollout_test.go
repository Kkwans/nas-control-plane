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
	if len(credentials.Properties) != 5 || credentials.Properties["tls"] == nil || credentials.Properties["tls"].Ref != "#/components/schemas/DatabaseTLSPolicy" {
		t.Fatal("all database requests must share the TLS credential schema")
	}
	policy := document.Components.Schemas["DatabaseTLSPolicy"].Value
	if !policy.Type.Is("object") || policy.Nullable || policy.AdditionalProperties.Has == nil || *policy.AdditionalProperties.Has || len(policy.Properties) != 3 || len(policy.Required) != 1 || policy.Required[0] != "mode" {
		t.Fatal("TLS policy must be a closed, non-null object with required mode")
	}
	mode := policy.Properties["mode"].Value
	if !mode.Type.Is("string") || mode.Nullable || len(mode.Enum) != 1 || mode.Enum[0] != "verify-full" {
		t.Fatal("TLS policy must only accept verify-full")
	}
	for _, name := range []string{"caPEM", "serverName"} {
		field := policy.Properties[name].Value
		if !field.Type.Is("string") || field.Nullable {
			t.Fatalf("%s must be an optional non-null string", name)
		}
	}
	for _, text := range []string{"65536", "字节", "32", "不合并系统根", "Root Agent"} {
		if !strings.Contains(policy.Properties["caPEM"].Value.Description, text) {
			t.Fatalf("CA contract missing %q", text)
		}
	}
	for _, value := range []any{
		map[string]any{"mode": "verify-full"},
		map[string]any{"mode": "verify-full", "caPEM": "", "serverName": ""},
	} {
		if err := policy.VisitJSON(value); err != nil {
			t.Fatalf("valid TLS schema rejected: %v", err)
		}
	}
	for _, value := range []any{
		nil, map[string]any{}, map[string]any{"mode": nil}, map[string]any{"mode": "prefer"},
		map[string]any{"mode": "verify-full", "caPEM": nil},
		map[string]any{"mode": "verify-full", "serverName": nil},
		map[string]any{"mode": "verify-full", "caPEM": false},
		map[string]any{"mode": "verify-full", "serverName": float64(1)},
		map[string]any{"mode": "verify-full", "Mode": "verify-full"},
		map[string]any{"mode": "verify-full", "insecureSkipVerify": true},
	} {
		if err := policy.VisitJSON(value); err == nil {
			t.Fatalf("invalid TLS schema accepted: %#v", value)
		}
	}
	if err := credentials.VisitJSON(map[string]any{"tls": nil}); err == nil {
		t.Fatal("explicit tls:null must not select the legacy path")
	}
	for _, name := range []string{"connect", "test-connection", "catalog", "query", "rows", "rows/insert", "rows/update", "rows/delete"} {
		t.Run(name, func(t *testing.T) {
			operation := document.Paths.Value("/api/v1/databases/" + name).Post
			for _, requirement := range []string{"credentials.tls", "verify-full", "null", "重复", "大小写", "Unicode", "DATABASE_INPUT_INVALID", "继承已保存", "不表示关闭", "不回退重试"} {
				if !strings.Contains(operation.RequestBody.Value.Description, requirement) {
					t.Fatalf("TLS input contract missing %q", requirement)
				}
			}
			if !databaseRequestUsesSharedCredentials(operation.RequestBody.Value.Content.Get("application/json").Schema) {
				t.Fatal("endpoint does not use the shared credentials/TLS schema")
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

func databaseRequestUsesSharedCredentials(schema *openapi3.SchemaRef) bool {
	if schema == nil || schema.Value == nil {
		return false
	}
	if credentials := schema.Value.Properties["credentials"]; credentials != nil && credentials.Ref == "#/components/schemas/DatabaseCredentials" {
		return true
	}
	for _, child := range schema.Value.AllOf {
		if databaseRequestUsesSharedCredentials(child) {
			return true
		}
	}
	return false
}
