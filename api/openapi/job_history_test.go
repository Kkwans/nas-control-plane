package openapi_test

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestJobHistoryReadOnlyContract(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	path := doc.Paths.Value("/api/v1/job-history")
	if path == nil || path.Get == nil || path.Post != nil || path.Delete != nil || path.Put != nil {
		t.Fatal("history must be additive GET only")
	}
	op := path.Get
	if op.Security == nil || len(*op.Security) == 0 {
		t.Fatal("missing auth")
	}
	for _, status := range []string{"200", "400", "401", "501", "503"} {
		if op.Responses.Value(status) == nil {
			t.Errorf("missing %s", status)
		}
	}
	if op.Responses.Value("200").Value.Content.Get("application/json").Schema.Ref != "#/components/schemas/JobHistoryPage" {
		t.Fatal("wrong page schema")
	}
	page := doc.Components.Schemas["JobHistoryPage"].Value
	if page.Properties["items"].Value.MaxItems == nil || *page.Properties["items"].Value.MaxItems != 100 {
		t.Fatal("unbounded items")
	}
	if page.Properties["source"].Value.Enum[0] != "persisted" {
		t.Fatal("missing provenance")
	}
	entry := doc.Components.Schemas["JobHistoryEntry"].Value
	for _, name := range []string{"type", "status"} {
		if len(entry.Properties[name].Value.Enum) != 0 {
			t.Fatalf("future %s blocked", name)
		}
	}
	for _, name := range []string{"cancellable", "persistenceStatus", "artifactState", "layers", "speedBytes", "downloadedBytes", "totalBytes"} {
		if _, ok := entry.Properties[name]; ok {
			t.Fatalf("action/live field %s leaked", name)
		}
	}
	for _, path := range []string{"/api/v1/jobs", "/api/v1/jobs/{jobID}", "/api/v1/jobs/{jobID}/events"} {
		if doc.Paths.Value(path).Get == nil {
			t.Fatalf("legacy %s changed", path)
		}
	}
	good := map[string]any{"source": "persisted", "items": []any{map[string]any{"id": "legacy", "type": "future-task", "status": "future-state", "message": "", "createdAt": "2026-10-15T00:00:00Z", "updatedAt": "2026-10-15T00:00:00Z"}}}
	if err := page.VisitJSON(good); err != nil {
		t.Fatal(err)
	}
	good["source"] = "live"
	if page.VisitJSON(good) == nil {
		t.Fatal("live source accepted")
	}
}
