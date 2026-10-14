package openapi_test

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestDatabaseRowCountPreservesEngineEstimate(t *testing.T) {
	document, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	properties := document.Components.Schemas["DatabaseTable"].Value.Properties
	rowCount := properties["rowCount"].Value
	if !rowCount.Type.Is("integer") || rowCount.Format != "int64" || rowCount.Min != nil {
		t.Fatal("rowCount must preserve the engine's signed int64 estimate")
	}
	if err := rowCount.VisitJSON(float64(-1)); err != nil {
		t.Fatalf("PostgreSQL unanalyzed estimate must be accepted: %v", err)
	}
	if err := rowCount.VisitJSON(1.5); err == nil {
		t.Fatal("fractional row count must not be accepted")
	}
	if !strings.Contains(rowCount.Description, "PostgreSQL") || !strings.Contains(rowCount.Description, "-1") {
		t.Fatal("rowCount must explain the unknown estimate")
	}
	sizeBytes := properties["sizeBytes"].Value
	if sizeBytes.Min == nil || *sizeBytes.Min != 0 {
		t.Fatal("unrelated sizeBytes nonnegative contract changed")
	}
}
