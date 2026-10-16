package openapi_test

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestComposeActionJobContract(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	op := doc.Paths.Value("/api/v1/docker/compose/projects/{projectID}/action-jobs/{action}").Post
	if op == nil || op.Security == nil || op.RequestBody == nil || op.Responses.Value("202") == nil || op.Responses.Value("200") != nil {
		t.Fatal("async contract")
	}
	schema := op.RequestBody.Value.Content.Get("application/json").Schema.Value
	body := map[string]any{"workingDirectory": "/volume1/demo", "configFiles": []any{"compose.yaml"}}
	if schema.VisitJSON(body) != nil {
		t.Fatal("valid body")
	}
	body["action"] = "start"
	if schema.VisitJSON(body) == nil {
		t.Fatal("extra field")
	}
	result := map[string]any{"projectId": "demo", "action": "start", "state": "running", "serviceCount": 1}
	safe := doc.Components.Schemas["ComposeActionJobResult"].Value
	if safe.VisitJSON(result) != nil {
		t.Fatal("valid result")
	}
	result["output"] = "secret"
	if safe.VisitJSON(result) == nil {
		t.Fatal("unsafe result")
	}
	if _, ok := doc.Components.Schemas["JobHistoryEntry"].Value.Properties["composeActionResult"]; ok {
		t.Fatal("history result")
	}
	if doc.Paths.Value("/api/v1/docker/compose/projects/{projectID}/actions/{action}").Post == nil {
		t.Fatal("sync removed")
	}
}
