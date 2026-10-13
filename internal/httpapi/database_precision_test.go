package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
)

func TestDecodeDatabaseMutationBodyPreservesNumberLexemes(t *testing.T) {
	const fields = `{"large":9007199254740993,"decimal":12345678901234567890.1234500,"exponent":1.234500e+40,"overflow":1e400,"integerString":"9007199254740993","decimalString":"123.4500","enabled":true,"empty":null,"text":"unchanged"}`
	want := map[string]any{
		"large": json.Number("9007199254740993"), "decimal": json.Number("12345678901234567890.1234500"),
		"exponent": json.Number("1.234500e+40"), "overflow": json.Number("1e400"),
		"integerString": "9007199254740993", "decimalString": "123.4500",
		"enabled": true, "empty": nil, "text": "unchanged",
	}
	cases := []struct {
		name string
		body string
		read func(*testing.T, *handler, http.ResponseWriter, *http.Request) []map[string]any
	}{
		{
			name: "insert", body: `{"sourceId":"fixture","table":"items","values":` + fields + `}`,
			read: func(t *testing.T, api *handler, w http.ResponseWriter, r *http.Request) []map[string]any {
				var input ncpdatabase.InsertRequest
				if !api.decodeDatabaseBody(w, r, &input) {
					t.Fatal("insert body rejected")
				}
				return []map[string]any{input.Values}
			},
		},
		{
			name: "update", body: `{"sourceId":"fixture","table":"items","values":` + fields + `,"keys":` + fields + `}`,
			read: func(t *testing.T, api *handler, w http.ResponseWriter, r *http.Request) []map[string]any {
				var input ncpdatabase.UpdateRequest
				if !api.decodeDatabaseBody(w, r, &input) {
					t.Fatal("update body rejected")
				}
				return []map[string]any{input.Values, input.Keys}
			},
		},
		{
			name: "delete", body: `{"sourceId":"fixture","table":"items","keys":` + fields + `}`,
			read: func(t *testing.T, api *handler, w http.ResponseWriter, r *http.Request) []map[string]any {
				var input ncpdatabase.DeleteRequest
				if !api.decodeDatabaseBody(w, r, &input) {
					t.Fatal("delete body rejected")
				}
				return []map[string]any{input.Keys}
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/databases/"+testCase.name, strings.NewReader(testCase.body))
			request.Header.Set("Content-Type", "application/json")
			for _, fields := range testCase.read(t, &handler{}, response, request) {
				if !reflect.DeepEqual(fields, want) {
					t.Fatalf("decoded fields = %#v, want %#v", fields, want)
				}
			}
		})
	}
}

func TestDecodeDatabaseBodyPreservesNumberRange(t *testing.T) {
	for _, literal := range []string{
		"0", "-0.00", "9007199254740991", "9007199254740992", "9007199254740993",
		"-9007199254740991", "-9007199254740992", "-9007199254740993",
		"9223372036854775807", "-9223372036854775808", "18446744073709551615",
		"1.234567890123456789002500", "1.234500e+40", "1e400",
	} {
		t.Run(literal, func(t *testing.T) {
			body := `{"table":"items","values":{"value":` + literal + `},"keys":{"id":` + literal + `}}`
			request := httptest.NewRequest(http.MethodPost, "/api/v1/databases/update", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			var input ncpdatabase.UpdateRequest
			if !(&handler{}).decodeDatabaseBody(httptest.NewRecorder(), request, &input) {
				t.Fatal("valid number rejected")
			}
			if input.Values["value"] != json.Number(literal) || input.Keys["id"] != json.Number(literal) {
				t.Fatalf("number %s changed: values=%#v keys=%#v", literal, input.Values, input.Keys)
			}
		})
	}
}

func TestDecodeDatabaseBodyKeepsProtocolIntegersAndValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
		ok   bool
	}{
		{name: "pagination", body: `{"sourceId":"fixture","table":"items","limit":50,"offset":100}`, ok: true},
		{name: "quoted pagination", body: `{"table":"items","limit":"50"}`},
		{name: "fractional pagination", body: `{"table":"items","offset":1.5}`},
		{name: "unknown field", body: `{"table":"items","unknown":1}`},
		{name: "trailing object", body: `{"table":"items"} {}`},
		{name: "invalid number", body: `{"table":"items","limit":01}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var input ncpdatabase.RowsRequest
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/databases/rows", strings.NewReader(testCase.body))
			request.Header.Set("Content-Type", "application/json")
			if ok := (&handler{}).decodeDatabaseBody(response, request, &input); ok != testCase.ok {
				t.Fatalf("decode = %v, want %v; response = %s", ok, testCase.ok, response.Body.String())
			}
			if testCase.ok {
				if input.Limit != 50 || input.Offset != 100 {
					t.Fatalf("pagination = %d/%d", input.Limit, input.Offset)
				}
			} else if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "DATABASE_INPUT_INVALID") {
				t.Fatalf("invalid input response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}
