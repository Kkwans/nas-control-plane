package openapi_test

import (
	"maps"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestDockerInventoryRequiredContract(t *testing.T) {
	document := loadDockerInventorySpecification(t)
	for _, test := range []struct {
		schema   string
		required []string
	}{
		{"DockerInventory", []string{"collectedAt", "engine", "containers", "projects"}},
		{"DockerEngineInfo", []string{"serverVersion", "operatingSystem", "architecture", "containers", "containersRunning", "containersStopped", "images"}},
		{"InventoryContainer", []string{"id", "name", "image", "state", "status", "createdAt", "ports", "projectId", "projectName", "serviceName"}},
		{"PortMapping", []string{"hostIp", "privatePort", "publicPort", "protocol"}},
		{"DockerProject", []string{"id", "name", "kind", "state", "workingDirectory", "configFiles", "containerCount", "runningCount"}},
	} {
		t.Run(test.schema, func(t *testing.T) {
			schema := document.Components.Schemas[test.schema].Value
			required := slices.Clone(schema.Required)
			slices.Sort(required)
			slices.Sort(test.required)
			if !slices.Equal(required, test.required) {
				t.Fatalf("required fields = %v, want %v", required, test.required)
			}
			fixture := dockerInventoryFixtures()[test.schema]
			if err := schema.VisitJSON(fixture); err != nil {
				t.Fatalf("valid fixture rejected: %v", err)
			}
			for _, invalid := range []any{nil, true, float64(1), "object", []any{}} {
				if err := schema.VisitJSON(invalid); err == nil {
					t.Errorf("invalid object accepted: %#v", invalid)
				}
			}
			for _, field := range test.required {
				t.Run("missing_"+field, func(t *testing.T) {
					value := maps.Clone(fixture)
					delete(value, field)
					if err := schema.VisitJSON(value); err == nil {
						t.Fatal("missing required field accepted")
					}
				})
				t.Run("invalid_"+field, func(t *testing.T) {
					var invalidValues []any
					switch fixture[field].(type) {
					case string:
						invalidValues = []any{nil, true, float64(1), []any{}, map[string]any{}}
					case float64:
						invalidValues = []any{nil, true, "1", []any{}, map[string]any{}}
					case []any:
						invalidValues = []any{true, "array", float64(1), map[string]any{}}
					case map[string]any:
						invalidValues = []any{nil, true, "object", float64(1), []any{}}
					default:
						t.Fatalf("uncovered fixture field type %T", fixture[field])
					}
					for _, invalid := range invalidValues {
						value := maps.Clone(fixture)
						value[field] = invalid
						if err := schema.VisitJSON(value); err == nil {
							t.Errorf("invalid field accepted: %#v", invalid)
						}
					}
				})
			}
		})
	}
}

func TestDockerInventoryNullableCollectionsContract(t *testing.T) {
	document := loadDockerInventorySpecification(t)
	for _, test := range []struct{ schema, field string }{
		{"DockerInventory", "containers"}, {"DockerInventory", "projects"},
		{"InventoryContainer", "ports"}, {"DockerProject", "configFiles"},
	} {
		t.Run(test.schema+"_"+test.field, func(t *testing.T) {
			schema := document.Components.Schemas[test.schema].Value
			collection := schema.Properties[test.field].Value
			if !collection.Type.Includes("array") || !collection.Type.Includes("null") || len(collection.Type.Slice()) != 2 {
				t.Fatal("collection must use the OpenAPI 3.1 array/null union")
			}
			if !strings.Contains(collection.Description, "归一化为 []") || !strings.Contains(collection.Description, "缺失字段不接受") {
				t.Fatal("missing explicit null normalization / missing-field documentation")
			}
			for _, valid := range []any{nil, []any{}} {
				value := dockerInventoryFixtures()[test.schema]
				value[test.field] = valid
				if err := schema.VisitJSON(value); err != nil {
					t.Fatalf("compatible collection rejected: %v", err)
				}
			}
			// null 仅允许用于集合本身，不允许作为其中的元素。
			for _, invalid := range []any{nil, true, float64(1), []any{}, map[string]any{}} {
				value := dockerInventoryFixtures()[test.schema]
				value[test.field] = []any{invalid}
				if err := schema.VisitJSON(value); err == nil {
					t.Errorf("invalid collection element accepted: %#v", invalid)
				}
			}
		})
	}
}

func TestDockerInventoryNumericContract(t *testing.T) {
	document := loadDockerInventorySpecification(t)
	for _, test := range []struct {
		schema, field string
		port          bool
	}{
		{"DockerEngineInfo", "containers", false}, {"DockerEngineInfo", "containersRunning", false},
		{"DockerEngineInfo", "containersStopped", false}, {"DockerEngineInfo", "images", false},
		{"InventoryContainer", "restartCount", false},
		{"DockerProject", "containerCount", false}, {"DockerProject", "runningCount", false},
		{"PortMapping", "privatePort", true}, {"PortMapping", "publicPort", true},
	} {
		t.Run(test.schema+"_"+test.field, func(t *testing.T) {
			schema := document.Components.Schemas[test.schema].Value
			invalidValues := []any{nil, true, "0", float64(-1), 0.5, math.NaN(), math.Inf(1), math.Inf(-1)}
			if test.port {
				invalidValues = append(invalidValues, float64(65536))
			}
			for _, invalid := range invalidValues {
				value := dockerInventoryFixtures()[test.schema]
				value[test.field] = invalid
				if err := schema.VisitJSON(value); err == nil {
					t.Errorf("invalid number accepted: %#v", invalid)
				}
			}
			for _, valid := range []float64{0, 1, 65535} {
				value := dockerInventoryFixtures()[test.schema]
				value[test.field] = valid
				if err := schema.VisitJSON(value); err != nil {
					t.Errorf("compatible number %v rejected: %v", valid, err)
				}
			}
		})
	}

	exitCode := document.Components.Schemas["InventoryContainer"].Value.Properties["exitCode"].Value
	for _, valid := range []float64{-1, 0, 137} {
		if err := exitCode.VisitJSON(valid); err != nil {
			t.Errorf("exit code %v rejected: %v", valid, err)
		}
	}
	for _, invalid := range []any{nil, true, "0", 0.5, math.NaN(), math.Inf(1)} {
		if err := exitCode.VisitJSON(invalid); err == nil {
			t.Errorf("invalid exit code accepted: %#v", invalid)
		}
	}
}

func TestDockerInventoryCompatibilityContract(t *testing.T) {
	document := loadDockerInventorySpecification(t)
	container := document.Components.Schemas["InventoryContainer"].Value
	for _, field := range []string{"startedAt", "finishedAt", "exitCode", "restartCount", "health"} {
		if container.Properties[field] == nil || slices.Contains(container.Required, field) {
			t.Fatalf("%s must remain present and optional", field)
		}
	}
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"legacy_without_enrichment", func(map[string]any) {}},
		{"explicit_null_collections", func(value map[string]any) {
			value["containers"], value["projects"] = nil, nil
		}},
		{"nested_null_collections", func(value map[string]any) {
			value["containers"].([]any)[0].(map[string]any)["ports"] = nil
			value["projects"].([]any)[0].(map[string]any)["configFiles"] = nil
		}},
		{"enrichment_and_unknown_container_state_protocol", func(value map[string]any) {
			item := value["containers"].([]any)[0].(map[string]any)
			item["state"] = "future-state"
			item["startedAt"], item["finishedAt"] = "0001-01-01T00:00:00Z", "0001-01-01T00:00:00Z"
			item["exitCode"], item["restartCount"], item["health"] = float64(-1), float64(0), "none"
			item["ports"].([]any)[0].(map[string]any)["protocol"] = "future-protocol"
		}},
		{"extra_fields_and_unrelated_counts", func(value map[string]any) {
			value["future"] = true
			engine := value["engine"].(map[string]any)
			engine["containersRunning"], engine["future"] = float64(9), true
			item := value["containers"].([]any)[0].(map[string]any)
			item["future"] = true
			item["ports"].([]any)[0].(map[string]any)["future"] = true
			project := value["projects"].([]any)[0].(map[string]any)
			project["runningCount"], project["future"] = float64(7), true
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := dockerInventoryFixtures()["DockerInventory"]
			test.mutate(value)
			if err := document.Components.Schemas["DockerInventory"].Value.VisitJSON(value, openapi3.EnableFormatValidation()); err != nil {
				t.Fatalf("compatible inventory rejected: %v", err)
			}
		})
	}
	for _, test := range []struct{ schema, field string }{
		{"InventoryContainer", "health"}, {"DockerProject", "kind"}, {"DockerProject", "state"},
	} {
		value := dockerInventoryFixtures()[test.schema]
		value[test.field] = "future-enum"
		if err := document.Components.Schemas[test.schema].Value.VisitJSON(value); err == nil {
			t.Errorf("unknown enum accepted: %s.%s", test.schema, test.field)
		}
	}
	for _, field := range []string{"startedAt", "finishedAt", "health"} {
		for _, invalid := range []any{nil, true, float64(1), []any{}, map[string]any{}} {
			value := dockerInventoryFixtures()["InventoryContainer"]
			value[field] = invalid
			if err := container.VisitJSON(value); err == nil {
				t.Errorf("invalid optional field %s accepted: %#v", field, invalid)
			}
		}
	}
}

func TestDockerInventoryNestedRequiredContract(t *testing.T) {
	schema := loadDockerInventorySpecification(t).Components.Schemas["DockerInventory"].Value
	for _, field := range []string{"engine", "container", "port", "project"} {
		t.Run(field, func(t *testing.T) {
			value := dockerInventoryFixtures()["DockerInventory"]
			switch field {
			case "engine":
				delete(value["engine"].(map[string]any), "images")
			case "container":
				delete(value["containers"].([]any)[0].(map[string]any), "state")
			case "port":
				delete(value["containers"].([]any)[0].(map[string]any)["ports"].([]any)[0].(map[string]any), "protocol")
			case "project":
				delete(value["projects"].([]any)[0].(map[string]any), "configFiles")
			}
			if err := schema.VisitJSON(value); err == nil {
				t.Fatal("incomplete nested inventory object accepted")
			}
		})
	}
}

func loadDockerInventorySpecification(t *testing.T) *openapi3.T {
	t.Helper()
	document, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatalf("load OpenAPI specification: %v", err)
	}
	return document
}

func dockerInventoryFixtures() map[string]map[string]any {
	// 与 Go JSON 字段一致；保留非时间空字符串、零时间、零计数和未发布端口。
	engine := map[string]any{
		"serverVersion": "", "operatingSystem": "", "architecture": "",
		"containers": float64(0), "containersRunning": float64(0), "containersStopped": float64(0), "images": float64(0),
	}
	port := map[string]any{"hostIp": "", "privatePort": float64(0), "publicPort": float64(0), "protocol": ""}
	container := map[string]any{
		"id": "", "name": "", "image": "", "state": "", "status": "", "createdAt": "0001-01-01T00:00:00Z",
		"ports": []any{port}, "projectId": "", "projectName": "", "serviceName": "",
	}
	project := map[string]any{
		"id": "", "name": "", "kind": "standalone", "state": "stopped", "workingDirectory": "",
		"configFiles": []any{""}, "containerCount": float64(0), "runningCount": float64(0),
	}
	return map[string]map[string]any{
		"DockerInventory": {
			"collectedAt": "2026-10-13T20:00:00Z", "engine": engine,
			"containers": []any{container}, "projects": []any{project},
		},
		"DockerEngineInfo": engine, "InventoryContainer": container, "PortMapping": port, "DockerProject": project,
	}
}
