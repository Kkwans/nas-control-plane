package openapi_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

func TestSpecificationParses(t *testing.T) {
	content, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatalf("read OpenAPI specification: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(content, &document); err != nil {
		t.Fatalf("parse OpenAPI specification: %v", err)
	}
	if document["openapi"] == nil || document["paths"] == nil || document["components"] == nil {
		t.Fatalf("OpenAPI specification is missing required top-level sections")
	}
}

func TestSpecificationValidatesFully(t *testing.T) {
	loader := openapi3.NewLoader()
	document, err := loader.LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatalf("load OpenAPI specification: %v", err)
	}
	if err := document.Validate(context.Background()); err != nil {
		t.Fatalf("validate OpenAPI specification: %v", err)
	}
}

func TestSpecificationMatchesHTTPRouteDeclarations(t *testing.T) {
	loader := openapi3.NewLoader()
	document, err := loader.LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatalf("load OpenAPI specification: %v", err)
	}

	specified := make(map[string]bool)
	for path, item := range document.Paths.Map() {
		for _, method := range []string{
			http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete,
			http.MethodPatch, http.MethodHead, http.MethodOptions,
		} {
			if item.GetOperation(method) != nil {
				specified[method+" "+path] = true
			}
		}
	}

	declared := parseHTTPRouteDeclarations(t)
	var missing, undocumented []string
	for route := range declared {
		if !specified[route] {
			undocumented = append(undocumented, route)
		}
	}
	for route := range specified {
		if !declared[route] {
			missing = append(missing, route)
		}
	}
	sort.Strings(undocumented)
	sort.Strings(missing)
	if len(undocumented) > 0 || len(missing) > 0 {
		t.Fatalf("OpenAPI/HTTP route drift: undocumented=%v missing=%v", undocumented, missing)
	}
}

func parseHTTPRouteDeclarations(t *testing.T) map[string]bool {
	t.Helper()
	filename := filepath.Join("..", "..", "internal", "httpapi", "handler.go")
	file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
	if err != nil {
		t.Fatalf("parse HTTP handler routes: %v", err)
	}

	routes := make(map[string]bool)
	methods := map[string]bool{
		"Connect": true, "Delete": true, "Get": true, "Head": true,
		"Options": true, "Patch": true, "Post": true, "Put": true,
		"Trace": true,
	}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !methods[selector.Sel.Name] {
			return true
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		path, err := strconv.Unquote(literal.Value)
		if err != nil || !strings.HasPrefix(path, "/") {
			return true
		}
		if path != "/healthz" && path != "/readyz" && path != "/ws/terminal" {
			path = "/api/v1" + path
		}
		routes[strings.ToUpper(selector.Sel.Name)+" "+path] = true
		return true
	})
	return routes
}

func TestJobLifecycleCompatibilityContract(t *testing.T) {
	loader := openapi3.NewLoader()
	document, err := loader.LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	job := document.Components.Schemas["JobSnapshot"].Value
	if len(job.Properties["status"].Value.Enum) != 6 {
		t.Fatal("v1 Job status must retain six values")
	}
	for _, field := range []string{"cancellationStatus", "persistenceStatus", "cancellable", "errorCode", "completedAt"} {
		if job.Properties[field] == nil {
			t.Fatalf("missing optional lifecycle property %s", field)
		}
		for _, required := range job.Required {
			if required == field {
				t.Fatalf("lifecycle property %s must remain optional", field)
			}
		}
	}
	for _, path := range []string{"/api/v1/docker/images/pull", "/api/v1/docker/compose/deploy", "/api/v1/jobs/{jobID}/retry"} {
		response := document.Paths.Value(path).Post.Responses.Value("503")
		if response == nil {
			t.Fatalf("%s missing 503", path)
		}
		for _, code := range []string{"JOB_QUEUE_FULL", "JOB_PERSIST_FAILED", "JOB_ENGINE_UNAVAILABLE"} {
			if response.Value.Description == nil || !strings.Contains(*response.Value.Description, code) {
				t.Fatalf("%s missing %s", path, code)
			}
		}
	}
	cancel := document.Paths.Value("/api/v1/jobs/{jobID}/cancel").Post
	for _, status := range []string{"202", "404", "409", "503"} {
		if cancel.Responses.Value(status) == nil {
			t.Fatalf("cancel missing %s", status)
		}
	}
	if strings.Contains(*cancel.Responses.Value("202").Value.Description, "任务已停止") {
		t.Fatal("cancel request is not proof of remote stop")
	}
}

func TestReadinessDocumentsJobStorageDegradation(t *testing.T) {
	loader := openapi3.NewLoader()
	document, err := loader.LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	checks := document.Components.Schemas["ReadinessResponse"].Value.Properties["checks"].Value
	jobs := checks.Properties["jobs"]
	if jobs == nil {
		t.Fatal("readiness missing jobs check")
	}
	for _, state := range []string{"ready", "degraded", "unavailable"} {
		found := false
		for _, value := range jobs.Value.Enum {
			if value == state {
				found = true
			}
		}
		if !found {
			t.Fatalf("jobs readiness missing %s", state)
		}
	}
}
