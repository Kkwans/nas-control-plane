package openapi_test

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestSpecificationDocumentsLiveSessionStreamAdmission(t *testing.T) {
	document, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/system/events", "/api/v1/logs/events", "/api/v1/jobs/{jobID}/events", "/ws/terminal"} {
		operation := document.Paths.Value(path).Get
		if operation.Responses.Status(401) == nil {
			t.Errorf("%s has no 401 admission response", path)
		}
		unavailable := operation.Responses.Status(503)
		if unavailable == nil || unavailable.Value.Description == nil {
			t.Errorf("%s has no 503 admission response", path)
			continue
		}
		for _, code := range []string{"AUTH_STREAM_UNAVAILABLE", "AUTH_STREAM_LIMIT_REACHED"} {
			if !strings.Contains(*unavailable.Value.Description, code) {
				t.Errorf("%s omits %s", path, code)
			}
		}
		if !strings.Contains(operation.Description, "会话") || !strings.Contains(operation.Description, "到期") {
			t.Errorf("%s omits live-session cancellation contract", path)
		}
	}
}
