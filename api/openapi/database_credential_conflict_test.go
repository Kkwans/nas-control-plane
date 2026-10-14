package openapi_test

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestDatabaseConnectCredentialConflictContract(t *testing.T) {
	document, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	operation := document.Paths.Value("/api/v1/databases/connect").Post
	response := operation.Responses.Value("409")
	if response == nil || response.Value == nil || response.Value.Description == nil {
		t.Fatal("connect must document credential conflicts as HTTP 409")
	}
	description := operation.Description + *response.Value.Description
	for _, requirement := range []string{"credential_conflict", "本次尝试没有写入凭据", "不能据此判断测试成功", "手动重试", "不自动重发", "其他请求", "constraint_failed"} {
		if !strings.Contains(description, requirement) {
			t.Fatalf("connect conflict contract missing %q", requirement)
		}
	}
	media := response.Value.Content.Get("application/json")
	if media == nil || media.Schema == nil || media.Schema.Ref != "#/components/schemas/ErrorResponse" {
		t.Fatal("connect conflict must retain the standard ErrorResponse envelope")
	}
	example := media.Examples["credentialConflict"]
	if example == nil || example.Value == nil {
		t.Fatal("connect conflict must document its stable code")
	}
	value, ok := example.Value.Value.(map[string]any)
	if !ok || len(value) != 3 || value["code"] != "credential_conflict" || value["requestId"] != "req-example" {
		t.Fatalf("unexpected credential conflict example: %#v", example.Value.Value)
	}
	if value["message"] != "凭据已被其他请求更改，本次凭据未保存，请重新确认后手动重试。" {
		t.Fatal("connect example must identify this attempt without claiming test success")
	}
	if err := media.Schema.Value.VisitJSON(value); err != nil {
		t.Fatalf("connect conflict example violates ErrorResponse: %v", err)
	}
}
