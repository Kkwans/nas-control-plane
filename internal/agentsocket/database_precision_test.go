package agentsocket

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestDatabaseMutationRPCPreservesExactNumbers(t *testing.T) {
	fields := map[string]any{
		"safe": 42, "safeBoundary": int64(1<<53 - 1), "large": int64(1<<53 + 1),
		"negative": int64(-1<<53 - 1), "signedMax": int64(1<<63 - 1), "unsignedMax": ^uint64(0),
		"decimal": json.Number("12345678901234567890.1234500"), "exponent": json.Number("1.234500e+40"),
		"overflow": json.Number("1e400"), "zero": json.Number("-0.00"), "float": 1.25,
		"integerString": "9007199254740993", "decimalString": "123.4500",
		"enabled": true, "disabled": false, "empty": nil, "text": "unchanged",
	}
	want := map[string]any{
		"safe": "42", "safeBoundary": "9007199254740991", "large": "9007199254740993",
		"negative": "-9007199254740993", "signedMax": "9223372036854775807", "unsignedMax": "18446744073709551615",
		"decimal": "12345678901234567890.1234500", "exponent": "1.234500e+40",
		"overflow": "1e400", "zero": "-0.00", "float": "1.25",
		"integerString": "9007199254740993", "decimalString": "123.4500",
		"enabled": true, "disabled": false, "empty": nil, "text": "unchanged",
	}
	connection := ncpdatabase.Connection{SourceID: "fixture"}
	insert := ncpdatabase.InsertRequest{Connection: connection, Schema: "main", Table: "items", Values: fields}
	t.Run("insert", func(t *testing.T) {
		var decoded ncpdatabase.InsertRequest
		roundTripDatabasePrecisionRequest(t, insert, &decoded)
		if decoded.SourceID != "fixture" || decoded.Schema != "main" || decoded.Table != "items" || !reflect.DeepEqual(decoded.Values, want) {
			t.Fatalf("decoded insert = %#v", decoded)
		}
	})
	t.Run("update", func(t *testing.T) {
		var decoded ncpdatabase.UpdateRequest
		roundTripDatabasePrecisionRequest(t, ncpdatabase.UpdateRequest{InsertRequest: insert, Keys: fields}, &decoded)
		if !reflect.DeepEqual(decoded.Values, want) || !reflect.DeepEqual(decoded.Keys, want) {
			t.Fatalf("decoded update = %#v", decoded)
		}
	})
	t.Run("delete", func(t *testing.T) {
		var decoded ncpdatabase.DeleteRequest
		roundTripDatabasePrecisionRequest(t, ncpdatabase.DeleteRequest{Connection: connection, Table: "items", Keys: fields}, &decoded)
		if !reflect.DeepEqual(decoded.Keys, want) {
			t.Fatalf("decoded delete keys = %#v", decoded.Keys)
		}
	})
	if fields["large"] != int64(1<<53+1) || fields["decimal"] != json.Number("12345678901234567890.1234500") {
		t.Fatal("RPC encoding modified the caller's input")
	}
}

func TestDatabaseRPCKeepsProtocolNumbers(t *testing.T) {
	input := ncpdatabase.RowsRequest{
		Connection: ncpdatabase.Connection{SourceID: "fixture"},
		Table:      "items", Limit: 50, Offset: 100, SortColumn: "id", SortDirection: "asc",
	}
	var decoded ncpdatabase.RowsRequest
	roundTripDatabasePrecisionRequest(t, input, &decoded)
	if !reflect.DeepEqual(decoded, input) {
		t.Fatalf("decoded rows request = %#v, want %#v", decoded, input)
	}
}

func TestDatabaseRPCRejectsInvalidNumber(t *testing.T) {
	request := ncpdatabase.InsertRequest{Values: map[string]any{"id": json.Number("not-a-number")}}
	err := callDatabase(context.Background(), "/unused-database-test.sock", request, &ncpdatabase.MutationResult{}, func(AgentDatabaseServiceClient, *structpb.Struct) (*structpb.Struct, error) {
		t.Fatal("invalid request reached RPC callback")
		return nil, nil
	})
	if ErrorCode(err) != "AGENT_RPC_REQUEST_INVALID" {
		t.Fatalf("invalid number error = %v", err)
	}
}

// 使用真实客户端编码、protobuf 线格式及 Agent 解码，不监听或连接任何套接字。
func roundTripDatabasePrecisionRequest(t *testing.T, request, destination any) {
	t.Helper()
	var result ncpdatabase.MutationResult
	err := callDatabase(context.Background(), "/unused-database-test.sock", request, &result, func(_ AgentDatabaseServiceClient, payload *structpb.Struct) (*structpb.Struct, error) {
		wire, err := proto.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		var received structpb.Struct
		if err := proto.Unmarshal(wire, &received); err != nil {
			t.Fatal(err)
		}
		if err := decodeDatabaseRequest(&received, destination); err != nil {
			t.Fatal(err)
		}
		return databaseResponse(ncpdatabase.MutationResult{RowsAffected: 1}, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RowsAffected != 1 {
		t.Fatalf("mutation result = %#v", result)
	}
}
