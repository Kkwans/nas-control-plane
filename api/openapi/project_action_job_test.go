package openapi_test

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestProjectActionJobContract(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	op := doc.Paths.Value("/api/v1/docker/projects/standalone/action-jobs/{action}").Post
	if op == nil || op.Security == nil || op.RequestBody == nil || op.Responses.Value("202") == nil || op.Responses.Value("200") != nil {
		t.Fatal("missing protected async contract")
	}
	body := op.RequestBody.Value.Content.Get("application/json").Schema.Value
	valid := map[string]any{"containerIds": []any{strings.Repeat("a", 64)}}
	if err := body.VisitJSON(valid); err != nil {
		t.Fatal(err)
	}
	for _, v := range []any{map[string]any{}, map[string]any{"containerIds": []any{}}, map[string]any{"containerIds": []any{"short"}}, map[string]any{"containerIds": []any{strings.Repeat("a", 64), strings.Repeat("a", 64)}}, map[string]any{"containerIds": []any{strings.Repeat("a", 64)}, "action": "start"}} {
		if body.VisitJSON(v) == nil {
			t.Fatal("bad body accepted")
		}
	}
	if _, ok := doc.Components.Schemas["JobHistoryEntry"].Value.Properties["projectActionResult"]; ok {
		t.Fatal("history exposes result")
	}
	result := map[string]any{"projectId": "standalone", "kind": "standalone", "action": "start", "items": []any{map[string]any{"containerId": strings.Repeat("a", 64), "outcome": "succeeded", "state": "running"}}}
	schema := doc.Components.Schemas["DockerProjectActionJobResult"].Value
	if err := schema.VisitJSON(result); err != nil {
		t.Fatal(err)
	}
	result["name"] = "secret"
	if schema.VisitJSON(result) == nil {
		t.Fatal("unsafe extra field")
	}
}
