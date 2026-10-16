package agentsocket

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// 直接调用服务并经过真实 protobuf 编解码；不创建连接、监听器或数据库。
func databaseWirePrecisionService(t *testing.T, strict bool) (*databaseService, *tlsRPCProvider) {
	t.Helper()
	provider := &tlsRPCProvider{supports: true}
	if strict {
		service, err := newDatabaseTLSService(provider)
		if err != nil {
			t.Fatal(err)
		}
		return service.databaseService, provider
	}
	return newDatabaseService(provider), provider
}

func databaseWirePrecisionPayload(t *testing.T, strict bool, field string, value *structpb.Value) *structpb.Struct {
	t.Helper()
	payload := tlsRPCPayload(t, ncpdatabase.UpdateRequest{InsertRequest: ncpdatabase.InsertRequest{Connection: tlsRPCFixtureConnection(strict), Table: "fixture", Values: map[string]any{"label": "synthetic"}}, Keys: map[string]any{"id": "1"}})
	payload.Fields[field] = structpb.NewStructValue(&structpb.Struct{Fields: map[string]*structpb.Value{"sensitive-column": value}})
	return payload
}

func databaseWirePrecisionInvoke(t *testing.T, service *databaseService, method string, payload *structpb.Struct) error {
	t.Helper()
	// Insert/Delete 的严格协议不接受另一种 mutation 映射。
	if method == "Insert" {
		delete(payload.Fields, "keys")
	}
	if method == "Delete" {
		delete(payload.Fields, "values")
	}
	wire, err := proto.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	received := new(structpb.Struct)
	if err := proto.Unmarshal(wire, received); err != nil {
		t.Fatal(err)
	}
	switch method {
	case "Insert":
		_, err = service.Insert(context.Background(), received)
	case "Update":
		_, err = service.Update(context.Background(), received)
	case "Delete":
		_, err = service.Delete(context.Background(), received)
	default:
		t.Fatal("unknown fixture method")
	}
	return err
}

func TestDatabaseMutationWireNumbers(t *testing.T) {
	cases := []struct {
		name   string
		value  *structpb.Value
		reject bool
	}{
		{"max safe", structpb.NewNumberValue(1<<53 - 1), false},
		{"min safe", structpb.NewNumberValue(-(1<<53 - 1)), false},
		{"positive boundary", structpb.NewNumberValue(1 << 53), true},
		{"negative boundary", structpb.NewNumberValue(-(1 << 53)), true},
		{"positive beyond", structpb.NewNumberValue(1<<53 + 2), true},
		{"negative beyond", structpb.NewNumberValue(-(1<<53 + 2)), true},
		{"max float", structpb.NewNumberValue(math.MaxFloat64), true},
		{"nan", structpb.NewNumberValue(math.NaN()), true},
		{"positive infinity", structpb.NewNumberValue(math.Inf(1)), true},
		{"negative infinity", structpb.NewNumberValue(math.Inf(-1)), true},
		{"fraction", structpb.NewNumberValue(1.25), false},
		{"negative fraction", structpb.NewNumberValue(-1.25), false},
		{"zero", structpb.NewNumberValue(0), false},
		{"negative zero", structpb.NewNumberValue(math.Copysign(0, -1)), false},
		{"large integer text", structpb.NewStringValue("9007199254740993"), false},
		{"negative integer text", structpb.NewStringValue("-9007199254740993"), false},
		{"decimal text", structpb.NewStringValue("1.234567890123456789002500"), false},
		{"exponent text", structpb.NewStringValue("9.007199254740993e30"), false},
		{"nan text", structpb.NewStringValue("NaN"), false},
		{"bool", structpb.NewBoolValue(true), false},
		{"null", structpb.NewNullValue(), false},
	}
	for _, strict := range []bool{false, true} {
		for _, operation := range []struct{ method, field string }{{"Insert", "values"}, {"Update", "values"}, {"Update", "keys"}, {"Delete", "keys"}} {
			for _, test := range cases {
				t.Run(map[bool]string{false: "legacy", true: "tls"}[strict]+"/"+operation.method+"/"+operation.field+"/"+test.name, func(t *testing.T) {
					service, provider := databaseWirePrecisionService(t, strict)
					payload := databaseWirePrecisionPayload(t, strict, operation.field, test.value)
					err := databaseWirePrecisionInvoke(t, service, operation.method, payload)
					if test.reject {
						if grpcstatus.Code(err) != codes.InvalidArgument || grpcstatus.Convert(err).Message() != string(ncpdatabase.CodeSQLInvalid) || len(provider.calls) != 0 {
							t.Fatalf("rejected request: err=%v calls=%v", err, provider.calls)
						}
						if ErrorCode(databaseRPCError(err)) != string(ncpdatabase.CodeSQLInvalid) {
							t.Fatalf("client code=%v", databaseRPCError(err))
						}
						return
					}
					if err != nil || !reflect.DeepEqual(provider.calls, []string{operation.method}) {
						t.Fatalf("valid request: err=%v calls=%v", err, provider.calls)
					}
					var values map[string]any
					switch request := provider.request.(type) {
					case ncpdatabase.InsertRequest:
						values = request.Values
					case ncpdatabase.UpdateRequest:
						if operation.field == "values" {
							values = request.Values
						} else {
							values = request.Keys
						}
					case ncpdatabase.DeleteRequest:
						values = request.Keys
					}
					got, want := values["sensitive-column"], test.value.AsInterface()
					if number, ok := got.(json.Number); ok {
						converted, err := number.Float64()
						if err != nil {
							t.Fatal(err)
						}
						got = converted
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("value=%#v want=%#v", got, want)
					}
				})
			}
		}
	}
}

func TestDatabaseMutationWirePolicyPriorityAndAliases(t *testing.T) {
	for _, strict := range []bool{false, true} {
		for _, operation := range []struct{ method, field, alias string }{{"Insert", "values", "VALUES"}, {"Update", "values", "valueſ"}, {"Update", "keys", "KEYS"}, {"Delete", "keys", "keyſ"}} {
			for _, alias := range []bool{false, true} {
				service, provider := databaseWirePrecisionService(t, strict)
				payload := databaseWirePrecisionPayload(t, strict, operation.field, structpb.NewNumberValue(math.Inf(1)))
				want := string(ncpdatabase.CodeSQLInvalid)
				if alias {
					payload.Fields[operation.alias] = payload.Fields[operation.field]
					delete(payload.Fields, operation.field)
					if strict {
						want = "数据库请求格式无效"
					}
				} else if strict {
					delete(payload.Fields, "credentials")
					want = string(ncpdatabase.CodeTLSConfigurationInvalid)
				} else {
					payload.Fields["credentials"] = tlsRPCPayload(t, tlsRPCFixtureConnection(true)).Fields["credentials"]
					want = string(ncpdatabase.CodeTLSPolicyUnsupported)
				}
				err := databaseWirePrecisionInvoke(t, service, operation.method, payload)
				if grpcstatus.Code(err) != codes.InvalidArgument || grpcstatus.Convert(err).Message() != want || len(provider.calls) != 0 {
					t.Fatalf("strict=%v operation=%v alias=%v: err=%v calls=%v", strict, operation, alias, err, provider.calls)
				}
			}
		}
	}
}

func TestDatabaseMutationWireGuardScope(t *testing.T) {
	for _, strict := range []bool{false, true} {
		service, provider := databaseWirePrecisionService(t, strict)
		payload := tlsRPCPayload(t, ncpdatabase.RowsRequest{Connection: tlsRPCFixtureConnection(strict), Table: "fixture", Limit: 1 << 53, Offset: 1 << 53})
		if _, err := service.Rows(context.Background(), payload); err != nil || !reflect.DeepEqual(provider.calls, []string{"Rows"}) {
			t.Fatalf("pagination scope: err=%v calls=%v", err, provider.calls)
		}
		service, provider = databaseWirePrecisionService(t, strict)
		nested := structpb.NewStructValue(&structpb.Struct{Fields: map[string]*structpb.Value{"nested": structpb.NewNumberValue(1 << 53)}})
		payload = databaseWirePrecisionPayload(t, strict, "values", nested)
		if err := databaseWirePrecisionInvoke(t, service, "Insert", payload); err != nil || !reflect.DeepEqual(provider.calls, []string{"Insert"}) {
			t.Fatalf("nested scope: err=%v calls=%v", err, provider.calls)
		}
	}
}

func TestDatabaseMutationWireModernExactNumbers(t *testing.T) {
	for _, strict := range []bool{false, true} {
		for _, operation := range []databaseConnectionOperation{databaseOperationInsert, databaseOperationUpdate, databaseOperationDelete} {
			provider := &tlsRPCProvider{supports: true}
			services := tlsRPCRegistrar{}
			registerDatabaseServices(services, provider)
			transport := &tlsRPCTransport{services: services}
			connection := tlsRPCFixtureConnection(strict)
			fields := map[string]any{"integer": json.Number("9007199254740993"), "decimal": json.Number("1.234567890123456789002500"), "negative": json.Number("-9007199254740993"), "exponent": json.Number("1e400")}
			insert := ncpdatabase.InsertRequest{Connection: connection, Table: "fixture", Values: fields}
			var request any = insert
			if operation == databaseOperationUpdate {
				request = ncpdatabase.UpdateRequest{InsertRequest: insert, Keys: fields}
			}
			if operation == databaseOperationDelete {
				request = ncpdatabase.DeleteRequest{Connection: connection, Table: "fixture", Keys: fields}
			}
			err := callDatabaseConnectionClient(context.Background(), transport, connection, request, new(ncpdatabase.MutationResult), operation)
			if err != nil || !reflect.DeepEqual(provider.calls, []string{string(operation)}) || len(transport.calls) != 1 {
				t.Fatalf("modern strict=%v op=%s err=%v provider=%v transport=%v", strict, operation, err, provider.calls, transport.calls)
			}
			var maps []map[string]any
			switch decoded := provider.request.(type) {
			case ncpdatabase.InsertRequest:
				maps = append(maps, decoded.Values)
			case ncpdatabase.UpdateRequest:
				maps = append(maps, decoded.Values, decoded.Keys)
			case ncpdatabase.DeleteRequest:
				maps = append(maps, decoded.Keys)
			}
			for _, values := range maps {
				for key, value := range fields {
					if values[key] != value.(json.Number).String() {
						t.Fatalf("modern %s precision lost: %#v", key, values[key])
					}
				}
			}
		}
	}
}
