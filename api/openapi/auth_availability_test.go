package openapi_test

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestAuthAvailabilityContract(t *testing.T) {
	document, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"status", "bootstrap", "login", "logout"} {
		t.Run(endpoint, func(t *testing.T) {
			item := document.Paths.Value("/api/v1/auth/" + endpoint)
			operation := item.Post
			if endpoint == "status" {
				operation = item.Get
			}
			response := operation.Responses.Value("503")
			if response == nil || response.Value == nil {
				t.Fatal("missing authentication unavailable response")
			}
			media := response.Value.Content.Get("application/json")
			if media == nil || media.Schema == nil || media.Schema.Ref != "#/components/schemas/ErrorResponse" {
				t.Fatal("503 must use ErrorResponse")
			}
			var clauses []string
			if endpoint != "status" {
				clauses = append(clauses, "AUTH_UNAVAILABLE", "requestId", "Cookie")
			}
			if endpoint == "bootstrap" {
				clauses = append(clauses, "账号可能已经创建", "会话未建立", "/api/v1/auth/status", "不得自动重放")
			}
			if endpoint == "logout" {
				clauses = append(clauses, "会话撤销未获确认")
			}
			for _, clause := range clauses {
				if response.Value.Description == nil || !strings.Contains(*response.Value.Description, clause) {
					t.Errorf("503 missing clause %q", clause)
				}
			}
		})
	}
}
