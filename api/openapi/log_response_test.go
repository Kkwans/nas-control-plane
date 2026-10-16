package openapi_test

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestLogResponseOptionalStreamContract(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	schema := doc.Components.Schemas["LogResponse"].Value
	entry := map[string]any{"id": "", "timestamp": "0001-01-01T00:00:00Z", "source": "container", "unit": "", "level": "info", "message": ""}
	response := map[string]any{"collectedAt": "2026-10-16T05:00:00.123456789Z", "entries": []any{entry}, "nextCursor": ""}
	if err := schema.VisitJSON(response); err != nil {
		t.Fatalf("optional stream and empty log text: %v", err)
	}
	for _, stream := range []string{"stdout", "stderr"} {
		entry["stream"] = stream
		if err := schema.VisitJSON(response); err != nil {
			t.Fatalf("stream %s: %v", stream, err)
		}
	}
	entry["stream"] = "unknown"
	if schema.VisitJSON(response) == nil {
		t.Fatal("unknown stream accepted")
	}
}
