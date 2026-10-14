package agentsocket

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// 共用旧 Agent（306eb1b 之前）线格式与实际 HTTP handler 产物，防止前端
// fixture 只省略字段而漏掉当前 Server DTO 重新编码产生的空枚举与 nil slice。
// 本测试只执行 protobuf/客户端 codec/JSON 编码，不监听、拨号或连接数据库。
func TestDatabaseLegacyReadResponseHTTPFixture(t *testing.T) {
	data, err := os.ReadFile("../../frontend/src/api/fixtures/databaseReadResponse.legacy.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures map[string]struct {
		Wire map[string]any `json:"wire"`
		HTTP map[string]any `json:"http"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name   string
		result any
	}{
		{"discovery", &ncpdatabase.Discovery{}},
		{"catalog", &ncpdatabase.Catalog{}},
		{"rows", &ncpdatabase.RowsResult{}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture, ok := fixtures[testCase.name]
			if !ok {
				t.Fatal("missing legacy fixture")
			}
			response, err := structpb.NewStruct(fixture.Wire)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := proto.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			var received structpb.Struct
			if err := proto.Unmarshal(wire, &received); err != nil {
				t.Fatal(err)
			}
			calls := 0
			err = callDatabaseClient(nil, map[string]any{}, testCase.result, func(_ AgentDatabaseServiceClient, _ *structpb.Struct) (*structpb.Struct, error) {
				calls++
				return &received, nil
			})
			if err != nil || calls != 1 {
				t.Fatalf("codec error = %v, calls = %d", err, calls)
			}
			encoded, err := json.Marshal(testCase.result)
			if err != nil {
				t.Fatal(err)
			}
			var actual map[string]any
			if err := json.Unmarshal(encoded, &actual); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, fixture.HTTP) {
				t.Fatalf("Go HTTP payload = %s, want %#v", encoded, fixture.HTTP)
			}
		})
	}
}
