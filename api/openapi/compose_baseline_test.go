package openapi_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestComposeBaselineContract(t *testing.T) {
	document, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ schema, field string }{{"ComposeFileSnapshot", "contentHash"}, {"ComposeDeployInput", "baseContentHash"}} {
		schema := document.Components.Schemas[test.schema].Value
		if !slices.Contains(schema.Required, test.field) {
			t.Fatalf("%s.%s must be required", test.schema, test.field)
		}
		property := schema.Properties[test.field].Value
		if !property.Type.Is("string") || property.Pattern != "^[0-9a-f]{64}$" || property.MinLength != 64 || property.MaxLength == nil || *property.MaxLength != 64 {
			t.Fatalf("invalid hash contract: %+v", property)
		}
	}
	operation := document.Paths.Value("/api/v1/docker/compose/deploy").Post
	if operation.Responses.Status(400) == nil || operation.Responses.Status(202) == nil {
		t.Fatal("missing admission/validation responses")
	}
	for _, expected := range []string{"COMPOSE_BASELINE_INVALID", "COMPOSE_BASELINE_UNSUPPORTED", "COMPOSE_CONFIG_CONFLICT", "COMPOSE_CONFIG_UNVERIFIED", "COMPOSE_DEPLOY_POSTCHECK_CONFLICT", "COMPOSE_DEPLOY_POSTCHECK_UNVERIFIED", "DeployConfigChecked", "TOCTOU", "不是跨进程原子 CAS"} {
		if !strings.Contains(operation.Description, expected) {
			t.Fatalf("missing deployment guarantee/limit %s", expected)
		}
	}
}
