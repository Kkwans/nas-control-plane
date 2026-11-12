package openapi_test

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestJSONRequestMediaTypeContract(t *testing.T) {
	document, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for path, item := range document.Paths.Map() {
		for method, operation := range item.Operations() {
			jsonBody := operation.RequestBody != nil && operation.RequestBody.Value.Content.Get("application/json") != nil
			response := operation.Responses.Value("415")
			if !jsonBody {
				if response != nil {
					t.Errorf("%s %s: non-JSON route has 415", method, path)
				}
				continue
			}
			count++
			if response == nil || response.Ref != "#/components/responses/UnsupportedMediaType" {
				t.Errorf("%s %s: missing shared 415", method, path)
			}
			body := operation.RequestBody.Value
			if !strings.Contains(body.Description, "application/json") {
				t.Errorf("%s %s: missing MIME contract", method, path)
			}
			optional := path == "/api/v1/proxy/mihomo/inspect"
			if body.Required == optional {
				t.Errorf("%s %s: required=%v", method, path, body.Required)
			}
		}
	}
	if count != 39 {
		t.Fatalf("JSON route count=%d, want 39", count)
	}
	response := document.Components.Responses["UnsupportedMediaType"].Value
	if response.Description == nil || !strings.Contains(*response.Description, "HTTP_UNSUPPORTED_MEDIA_TYPE") || !strings.Contains(*response.Description, "requestId") {
		t.Fatal("shared 415 must describe stable error code and requestId")
	}
	if response.Content.Get("application/json").Schema.Ref != "#/components/schemas/ErrorResponse" {
		t.Fatal("shared 415 must use ErrorResponse")
	}
}
