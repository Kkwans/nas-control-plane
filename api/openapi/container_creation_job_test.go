package openapi_test

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestContainerCreationJobRouteContract(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	path := doc.Paths.Value("/api/v1/docker/containers/create-jobs")
	if path == nil || path.Post == nil {
		t.Fatal("missing creation job route")
	}
	op := path.Post
	if op.Security == nil || len(*op.Security) != 1 {
		t.Fatal("missing protected operation")
	}
	if _, ok := (*op.Security)[0]["RootSession"]; !ok {
		t.Fatal("missing RootSession")
	}
	if op.RequestBody == nil || !op.RequestBody.Value.Required || op.RequestBody.Value.Content.Get("application/json").Schema.Ref != "#/components/schemas/DockerContainerCreateRequest" {
		t.Fatal("creation job must reuse the synchronous creation request")
	}
	for _, status := range []string{"202", "400", "401", "403", "413", "415", "503"} {
		if op.Responses.Value(status) == nil {
			t.Fatalf("missing %s", status)
		}
	}
	if !strings.Contains(*op.Responses.Value("503").Value.Description, "AGENT_DOCKER_CONTROL_UNAVAILABLE") {
		t.Fatal("missing creation capability rejection")
	}
	for _, budget := range []string{"Server执行预算10分钟", "Agent创建预算2分钟", "失败清理独立预算15秒"} {
		if !strings.Contains(op.Description, budget) {
			t.Errorf("missing execution budget %s", budget)
		}
	}
	if op.Responses.Value("200") != nil || op.Responses.Value("201") != nil || op.Responses.Value("202").Value.Content.Get("application/json").Schema.Ref != "#/components/schemas/JobSnapshot" {
		t.Fatal("creation job must return accepted JobSnapshot")
	}
	if op.Responses.Value("403").Ref != "#/components/responses/CrossOriginRejected" {
		t.Fatal("missing origin protection")
	}
	accepted := *op.Responses.Value("202").Value.Description
	for _, evidence := range []string{"queued", "docker-container-create", "reference", "cancellable=false", "artifactState=unknown", "progress/downloadedBytes/totalBytes/speedBytes", "layers", "createResult"} {
		if !strings.Contains(accepted, evidence) {
			t.Errorf("accepted snapshot contract omits %s", evidence)
		}
	}
	sync := doc.Paths.Value("/api/v1/docker/containers").Post
	if sync.Responses.Value("201") == nil || sync.Responses.Value("202") != nil || sync.Responses.Value("201").Value.Content.Get("application/json").Schema.Ref != "#/components/schemas/DockerContainerCreateResult" {
		t.Fatal("legacy synchronous creation contract changed")
	}
}

func TestContainerCreationJobResultContract(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	result := func(started bool) map[string]any {
		return map[string]any{"containerId": strings.Repeat("a", 64), "created": true, "started": started}
	}
	schema := doc.Components.Schemas["DockerContainerCreateJobResult"].Value
	if len(schema.Properties) != 3 {
		t.Fatal("create result must expose only the minimal receipt")
	}
	for _, started := range []bool{false, true} {
		if err := schema.VisitJSON(result(started)); err != nil {
			t.Fatalf("valid receipt rejected: %v", err)
		}
	}
	for _, field := range []string{"containerId", "created", "started"} {
		value := result(false)
		delete(value, field)
		if schema.VisitJSON(value) == nil {
			t.Errorf("missing %s accepted", field)
		}
	}
	for _, id := range []any{"", "name", "abc", strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("a", 63), strings.Repeat("a", 65), strings.Repeat("a", 64) + "\n", nil} {
		value := result(false)
		value["containerId"] = id
		if schema.VisitJSON(value) == nil {
			t.Errorf("invalid container identity accepted: %v", id)
		}
	}
	for _, invalid := range []any{false, "true", nil} {
		value := result(false)
		value["created"] = invalid
		if schema.VisitJSON(value) == nil {
			t.Errorf("invalid creation acknowledgment accepted: %v", invalid)
		}
	}
	for _, invalid := range []any{"false", float64(0), nil} {
		value := result(false)
		value["started"] = invalid
		if schema.VisitJSON(value) == nil {
			t.Errorf("non-boolean start acknowledgment accepted: %v", invalid)
		}
	}
	for _, field := range []string{"name", "image", "state", "runContainer", "environment", "unexpected"} {
		value := result(false)
		value[field] = "private"
		if schema.VisitJSON(value) == nil {
			t.Errorf("unexpected field %s accepted", field)
		}
	}
	if _, ok := doc.Components.Schemas["JobHistoryEntry"].Value.Properties["createResult"]; ok {
		t.Fatal("history summary must not expose createResult")
	}
	job := doc.Components.Schemas["JobSnapshot"].Value
	for _, kind := range []string{"docker-container-create", "docker-image-pull", "docker-container-start"} {
		for _, status := range []string{"queued", "running", "completed", "failed", "interrupted", "cancelled"} {
			value := map[string]any{
				"id": "job-1", "type": kind, "status": status, "artifactState": "unknown",
				"message": "", "progress": float64(0), "createdAt": "2026-10-16T00:00:00Z", "updatedAt": "2026-10-16T00:00:00Z",
			}
			if err := job.VisitJSON(value); err != nil {
				t.Fatalf("optional receipt became mandatory for %s/%s: %v", kind, status, err)
			}
			value["createResult"] = result(false)
			valid := kind == "docker-container-create" && status == "completed"
			if err := job.VisitJSON(value); (err == nil) != valid {
				t.Errorf("receipt restriction violated for %s/%s: %v", kind, status, err)
			}
		}
	}
}
