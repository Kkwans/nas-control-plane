package openapi_test

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSpecificationParses(t *testing.T) {
	content, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatalf("read OpenAPI specification: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(content, &document); err != nil {
		t.Fatalf("parse OpenAPI specification: %v", err)
	}
	if document["openapi"] == nil || document["paths"] == nil || document["components"] == nil {
		t.Fatalf("OpenAPI specification is missing required top-level sections")
	}
}
