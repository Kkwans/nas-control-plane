package openapi_test

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestImageRemoveJobContract(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	op := doc.Paths.Value("/api/v1/docker/images/remove-job").Post
	if op == nil || op.Responses.Value("200") != nil || op.Responses.Value("202").Value.Content.Get("application/json").Schema.Ref != "#/components/schemas/JobSnapshot" {
		t.Fatal("removal must return accepted JobSnapshot")
	}
	for _, code := range []string{"400", "401", "403", "413", "415", "503"} {
		if op.Responses.Value(code) == nil {
			t.Errorf("missing %s", code)
		}
	}
	schema := op.RequestBody.Value.Content.Get("application/json").Schema.Value
	id := "sha256:" + strings.Repeat("a", 64)
	if err := schema.VisitJSON(map[string]any{"imageId": id}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"alpine:latest", "sha256:abc", strings.ToUpper(id), " " + id, id + "\n", id + "a", "sha256:" + strings.Repeat("g", 64)} {
		if schema.VisitJSON(map[string]any{"imageId": bad}) == nil {
			t.Errorf("unsafe image ID accepted: %q", bad)
		}
	}
	if schema.VisitJSON(map[string]any{}) == nil {
		t.Fatal("missing imageId accepted")
	}
	for _, path := range []string{"/api/v1/docker/images/remove", "/api/v1/docker/images/remove-batch"} {
		if doc.Paths.Value(path).Post.Responses.Value("200") == nil {
			t.Errorf("legacy endpoint changed: %s", path)
		}
	}
}
