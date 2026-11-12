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

// 初始 804d584 与当前 ConnectionDiagnostic 的必填字段相同；仅 code/database
// 为 omitempty。共用真实 codec 的 HTTP 产物，避免前端虚构旧版缺省合同。
// 仅运行 protobuf/客户端 codec/JSON 编码；不监听、拨号或连接任何数据库。
func TestDatabaseConnectionDiagnosticWireFixture(t *testing.T) {
	data, err := os.ReadFile("../../frontend/src/api/fixtures/databaseConnectionResponse.wire.json")
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
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
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
			var result ncpdatabase.ConnectionDiagnostic
			calls := 0
			err = callDatabaseClient(nil, map[string]any{}, &result, func(AgentDatabaseServiceClient, *structpb.Struct) (*structpb.Struct, error) {
				calls++
				return &received, nil
			})
			if err != nil || calls != 1 {
				t.Fatalf("codec error = %v, calls = %d", err, calls)
			}
			encoded, err := json.Marshal(result)
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
