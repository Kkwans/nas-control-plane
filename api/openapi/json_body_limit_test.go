package openapi_test

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestJSONRequestBodyLimitContract(t *testing.T) {
	document, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for path, item := range document.Paths.Map() {
		for method, operation := range item.Operations() {
			jsonBody := operation.RequestBody != nil && operation.RequestBody.Value.Content.Get("application/json") != nil
			response := operation.Responses.Value("413")
			if !jsonBody {
				if response != nil {
					t.Errorf("%s %s: non-JSON route has request-body 413", method, path)
				}
				continue
			}
			if response == nil || response.Ref != "#/components/responses/RequestBodyTooLarge" {
				t.Errorf("%s %s: missing shared 413", method, path)
			}
			limit := "16384"
			if strings.HasPrefix(path, "/api/v1/auth/") || strings.HasPrefix(path, "/api/v1/users") {
				limit = "8192"
			} else if strings.HasPrefix(path, "/api/v1/databases/") && path != "/api/v1/databases/project-preferences" {
				limit = "1048576"
			}
			counts[limit]++
			body := operation.RequestBody.Value
			for _, clause := range []string{"完整请求体最多 " + limit + " 字节（bytes）", "JSON 前后空白", "所有 JSON 值及其他尾部字节", "按字节而非字符计数", "恰好等于上限允许", "超过上限返回 413"} {
				if !strings.Contains(body.Description, clause) {
					t.Errorf("%s %s: missing body limit clause %q", method, path, clause)
				}
			}
			optional := path == "/api/v1/proxy/mihomo/inspect"
			if body.Required == optional {
				t.Errorf("%s %s: required=%v", method, path, body.Required)
			}
		}
	}
	for limit, want := range map[string]int{"8192": 6, "16384": 24, "1048576": 8} {
		if counts[limit] != want {
			t.Errorf("JSON routes with %s-byte limit=%d, want %d", limit, counts[limit], want)
		}
	}
	shared := document.Components.Responses["RequestBodyTooLarge"]
	if shared == nil || shared.Value == nil {
		t.Fatal("missing shared 413 response")
	}
	response := shared.Value
	for _, clause := range []string{"HTTP_REQUEST_BODY_TOO_LARGE", "requestId", "不依赖 Content-Length", "不会执行后续业务操作"} {
		if response.Description == nil || !strings.Contains(*response.Description, clause) {
			t.Errorf("shared 413 missing clause %q", clause)
		}
	}
	media := response.Content.Get("application/json")
	if media == nil || media.Schema == nil || media.Schema.Ref != "#/components/schemas/ErrorResponse" {
		t.Fatal("shared 413 must use ErrorResponse")
	}
	if err := media.Schema.Value.VisitJSON(media.Example); err != nil {
		t.Fatalf("shared 413 example violates ErrorResponse: %v", err)
	}
}
