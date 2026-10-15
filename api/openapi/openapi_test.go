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
	for _, field := range []string{"cancellationStatus", "persistenceStatus", "cancellable", "errorCode", "completedAt", "progressLimited"} {
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

func TestUnsafeAPIOperationsDeclareOriginRejection(t *testing.T) {
	document, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for path, item := range document.Paths.Map() {
		if !strings.HasPrefix(path, "/api/v1/") {
			continue
		}
		for method, operation := range item.Operations() {
			if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
				continue
			}
			response := operation.Responses.Value("403")
			if response == nil || response.Ref != "#/components/responses/CrossOriginRejected" {
				t.Errorf("%s %s lacks shared 403 response", method, path)
				continue
			}
			media := response.Value.Content.Get("application/json")
			if media == nil || media.Schema.Ref != "#/components/schemas/ErrorResponse" {
				t.Errorf("%s %s has invalid 403 schema", method, path)
			}
		}
	}
}

func TestDatabaseResultBudgetCompatibilityContract(t *testing.T) {
	document, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	result := document.Components.Schemas["DatabaseQueryResult"].Value
	reason := result.Properties["truncationReason"]
	if reason == nil || reason.Value == nil {
		t.Fatal("query result missing truncationReason")
	}
	if len(reason.Value.Enum) != 2 || reason.Value.Enum[0] != "row_limit" || reason.Value.Enum[1] != "byte_limit" {
		t.Fatalf("unexpected truncation reasons: %v", reason.Value.Enum)
	}
	for _, name := range result.Required {
		if name == "truncationReason" || name == "rowsAffectedKnown" {
			t.Fatalf("%s must remain optional for older responses", name)
		}
	}
	for _, fixture := range []struct {
		name   string
		reason string
		rows   []any
	}{
		{name: "legacy truncated result", rows: []any{[]any{"9007199254740993", "1.234567890123456789002500"}}},
		{name: "row prefix", reason: "row_limit", rows: []any{[]any{"9007199254740993", "1.234567890123456789002500"}}},
		{name: "byte prefix", reason: "byte_limit", rows: []any{[]any{"9007199254740993", "1.234567890123456789002500"}}},
		{name: "first oversized row", reason: "byte_limit", rows: []any{}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			value := map[string]any{
				"columns": []any{"id", "amount"}, "rows": fixture.rows,
				"rowsAffected": float64(0), "truncated": true, "durationMs": float64(1),
			}
			if fixture.reason != "" {
				value["truncationReason"] = fixture.reason
				value["rowsAffectedKnown"] = false
			}
			if err := result.VisitJSON(value); err != nil {
				t.Fatalf("compatible budget response rejected: %v", err)
			}
		})
	}
	if err := reason.Value.VisitJSON("unknown_limit"); err == nil {
		t.Fatal("truncationReason must retain its documented enum")
	}
	for _, endpoint := range []string{"discovery", "connect", "test-connection", "catalog", "query", "rows", "rows/insert", "rows/update", "rows/delete"} {
		path := "/api/v1/databases/" + endpoint
		operation := document.Paths.Value(path).Post
		if endpoint == "discovery" {
			operation = document.Paths.Value(path).Get
		}
		response := operation.Responses.Value("422")
		if response == nil || response.Ref != "#/components/responses/DatabaseResultTooLarge" {
			t.Fatalf("%s lacks shared result_too_large 422 response", path)
		}
		media := response.Value.Content.Get("application/json")
		if media == nil || media.Schema.Ref != "#/components/schemas/ErrorResponse" {
			t.Fatalf("%s has invalid budget error schema", path)
		}
		for _, clause := range []string{"result_too_large", "1 MiB", "不得自动重试 SQL"} {
			if response.Value.Description == nil || !strings.Contains(*response.Value.Description, clause) {
				t.Fatalf("%s missing budget error clause %q", path, clause)
			}
		}
	}
	if !strings.Contains(document.Components.Schemas["DatabaseRowsResult"].Value.Description, "客户端不得当作空页或推进 offset") {
		t.Fatal("table page budget failure must not silently advance pagination")
	}
}

func TestDatabaseOperationBusyContract(t *testing.T) {
	loader := openapi3.NewLoader()
	document, err := loader.LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"connect", "test-connection", "catalog", "query", "rows", "rows/insert", "rows/update", "rows/delete"} {
		path := "/api/v1/databases/" + endpoint
		response := document.Paths.Value(path).Post.Responses.Value("503")
		if response == nil || response.Value == nil || response.Value.Description == nil {
			t.Fatalf("%s lacks a 503 description", path)
		}
		for _, clause := range []string{"operation_busy", "固定 4 个", "不排队", "不自动重试", "直到池关闭才释放", "独立来源发现、Server 端连接解析及凭据服务不在预算内", "不保证整条请求没有副作用", "不保证凭据未保存"} {
			if !strings.Contains(*response.Value.Description, clause) {
				t.Fatalf("%s missing busy clause %q", path, clause)
			}
		}
		media := response.Value.Content.Get("application/json")
		if media == nil || media.Schema.Ref != "#/components/schemas/ErrorResponse" {
			t.Fatalf("%s has invalid busy error schema", path)
		}
	}
	response := document.Paths.Value("/api/v1/databases/discovery").Get.Responses.Value("503")
	if response == nil || response.Value.Description == nil || strings.Contains(*response.Value.Description, "operation_busy") {
		t.Fatal("standalone discovery must retain its unrelated unavailable response")
	}
}

func TestJobProgressLimitedCompatibilityContract(t *testing.T) {
	loader := openapi3.NewLoader()
	document, err := loader.LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	job := document.Components.Schemas["JobSnapshot"].Value
	limited := job.Properties["progressLimited"].Value
	if !limited.Type.Is("boolean") || limited.Nullable {
		t.Fatal("progressLimited must be an optional non-null boolean")
	}
	for _, text := range []string{"单调", "不保证进度完整", "终态"} {
		if !strings.Contains(limited.Description, text) {
			t.Fatalf("progressLimited description missing %s", text)
		}
	}
	layers := job.Properties["layers"].Value
	if layers.MaxProps != nil || layers.AdditionalProperties.Has == nil || !*layers.AdditionalProperties.Has || layers.AdditionalProperties.Schema != nil {
		t.Fatal("legacy layer snapshots must remain readable without new global schema limits")
	}
	for _, text := range []string{"新接纳", "128 层", "128 UTF-8", "256 UTF-8", "32 KiB", "2 × ID", "旧持久化快照"} {
		if !strings.Contains(layers.Description, text) {
			t.Fatalf("layer admission limits description missing %s", text)
		}
	}
}
