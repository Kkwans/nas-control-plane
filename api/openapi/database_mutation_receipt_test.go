package openapi_test

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestDatabaseMutationReceiptContract(t *testing.T) {
	document, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	count := document.Components.Schemas["DatabaseMutationResult"].Value.Properties["rowsAffected"].Value
	if !count.Type.Is("integer") || count.Format != "int64" || count.Min == nil || *count.Min != 0 || count.Max == nil || *count.Max != 9007199254740991 {
		t.Fatal("mutation receipt must be a safe nonnegative integer")
	}
	for _, value := range []float64{0, 1, 9007199254740991} {
		if err := count.VisitJSON(value); err != nil {
			t.Fatalf("valid %v: %v", value, err)
		}
	}
	for _, value := range []float64{-1, 1.5, 9007199254740992} {
		if err := count.VisitJSON(value); err == nil {
			t.Fatalf("invalid %v accepted", value)
		}
	}
	for _, operation := range []string{"insert", "update", "delete"} {
		responses := document.Paths.Value("/api/v1/databases/rows/" + operation).Post.Responses
		receipt := responses.Status(502)
		if receipt == nil || receipt.Value.Description == nil {
			t.Fatalf("%s missing receipt error", operation)
		}
		description := *receipt.Value.Description
		for _, text := range []string{"mutation_receipt_invalid", "可能已生效", "不自动重试", "只读核对"} {
			if !strings.Contains(description, text) {
				t.Fatalf("%s missing %q", operation, text)
			}
		}
		status := 200
		if operation == "insert" {
			status = 201
		}
		if responses.Status(status) == nil {
			t.Fatalf("%s lost success status", operation)
		}
	}
}
