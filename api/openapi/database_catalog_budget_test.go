package openapi_test

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestDatabaseCatalogRetainedBudgetContract(t *testing.T) {
	document, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	operation := document.Paths.Value("/api/v1/databases/catalog").Post
	for _, text := range []string{"1 MiB", "保守结果保留预算", "不返回部分目录", "不截断字段", "不自动重试", "保留上次有效目录", "最大长度预留", "取消和预算错误不能当作成功", "不限制驱动读取单个值的峰值内存", "COUNT 查询工作量"} {
		if !strings.Contains(operation.Description, text) {
			t.Fatalf("catalog budget description missing %q", text)
		}
	}
	response := operation.Responses.Value("422")
	if response == nil || response.Ref != "#/components/responses/DatabaseResultTooLarge" {
		t.Fatal("catalog must retain the shared result_too_large response")
	}
}
