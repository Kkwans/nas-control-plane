package openapi_test

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestDockerContainerCreateResultContract(t *testing.T) {
	document, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	schema := document.Components.Schemas["DockerContainerCreateResult"].Value
	result := func(run bool) map[string]any {
		return map[string]any{
			"containerId": "exact-container-id", "name": "", "image": "redis:8-alpine", "state": "stopped",
			"created": true, "started": run, "runContainer": run,
		}
	}
	// 启动成功后进程仍可能退出；匿名容器名称也可以为空。
	for _, run := range []bool{false, true} {
		if err := schema.VisitJSON(result(run)); err != nil {
			t.Fatalf("valid create result rejected: %v", err)
		}
	}
	for _, field := range []string{"containerId", "name", "image", "state", "created", "started", "runContainer"} {
		t.Run("missing_"+field, func(t *testing.T) {
			value := result(false)
			delete(value, field)
			if schema.VisitJSON(value) == nil {
				t.Fatalf("missing %s accepted", field)
			}
		})
	}
	for _, field := range []string{"containerId", "image", "state"} {
		for _, invalid := range []any{"", " \t\r\n ", nil, float64(42)} {
			value := result(false)
			value[field] = invalid
			if schema.VisitJSON(value) == nil {
				t.Errorf("invalid %s accepted", field)
			}
		}
	}
	value := result(false)
	value["created"] = false
	if schema.VisitJSON(value) == nil {
		t.Fatal("created=false accepted as a successful result")
	}
	// 请求意图是跨请求信息；该关联由客户端运行时核验，不假设 schema 能表达。
}
