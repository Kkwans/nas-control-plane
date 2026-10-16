package openapi_test

import (
	"github.com/getkin/kin-openapi/openapi3"
	"strings"
	"testing"
)

func TestContainerActionJobContract(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	op := doc.Paths.Value("/api/v1/docker/containers/{containerID}/action-jobs/{action}").Post
	if op == nil || op.RequestBody != nil || op.Security == nil || len(*op.Security) != 1 {
		t.Fatal("missing protected bodyless operation")
	}
	if _, ok := (*op.Security)[0]["RootSession"]; !ok {
		t.Fatal("missing RootSession")
	}
	if op.Responses.Value("200") != nil || op.Responses.Value("202").Value.Content.Get("application/json").Schema.Ref != "#/components/schemas/JobSnapshot" {
		t.Fatal("must return accepted job")
	}
	for _, code := range []string{"400", "401", "403", "503"} {
		if op.Responses.Value(code) == nil {
			t.Errorf("missing %s", code)
		}
	}
	for _, parameter := range op.Parameters {
		p := parameter.Value
		if p.Name == "containerID" {
			if err := p.Schema.Value.VisitJSON(strings.Repeat("a", 64)); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"name", "abc", strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("a", 64) + "\n"} {
				if p.Schema.Value.VisitJSON(id) == nil {
					t.Errorf("unsafe ID %q", id)
				}
			}
		}
		if p.Name == "action" {
			for _, action := range []string{"start", "stop", "restart"} {
				if p.Schema.Value.VisitJSON(action) != nil {
					t.Fatal("valid action rejected")
				}
			}
			for _, action := range []string{"START", " start", "kill"} {
				if p.Schema.Value.VisitJSON(action) == nil {
					t.Fatal("unsafe action accepted")
				}
			}
		}
	}
	if doc.Paths.Value("/api/v1/docker/containers/{containerID}/actions/{action}").Post.Responses.Value("200") == nil {
		t.Fatal("legacy synchronous route changed")
	}
}
