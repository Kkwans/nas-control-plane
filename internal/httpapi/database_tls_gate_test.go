package httpapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
)

type tlsHTTPAgent struct {
	connectHTTPAgent
	operationCalls int
	connection     ncpdatabase.Connection
	insert         ncpdatabase.InsertRequest
	update         ncpdatabase.UpdateRequest
	delete         ncpdatabase.DeleteRequest
}

func (a *tlsHTTPAgent) TestDatabaseConnection(ctx context.Context, socket string, input ncpdatabase.Connection) (ncpdatabase.ConnectionDiagnostic, error) {
	a.connection = input
	return a.connectHTTPAgent.TestDatabaseConnection(ctx, socket, input)
}

func (a *tlsHTTPAgent) CatalogDatabase(_ context.Context, _ string, input ncpdatabase.CatalogRequest) (ncpdatabase.Catalog, error) {
	a.operationCalls++
	a.connection = input.Connection
	return ncpdatabase.Catalog{}, nil
}
func (a *tlsHTTPAgent) QueryDatabase(_ context.Context, _ string, input ncpdatabase.QueryRequest) (ncpdatabase.QueryResult, error) {
	a.operationCalls++
	a.connection = input.Connection
	return ncpdatabase.QueryResult{}, nil
}
func (a *tlsHTTPAgent) ReadDatabaseRows(_ context.Context, _ string, input ncpdatabase.RowsRequest) (ncpdatabase.RowsResult, error) {
	a.operationCalls++
	a.connection = input.Connection
	return ncpdatabase.RowsResult{}, nil
}
func (a *tlsHTTPAgent) InsertDatabaseRow(_ context.Context, _ string, input ncpdatabase.InsertRequest) (ncpdatabase.MutationResult, error) {
	a.operationCalls++
	a.connection, a.insert = input.Connection, input
	return ncpdatabase.MutationResult{}, nil
}
func (a *tlsHTTPAgent) UpdateDatabaseRow(_ context.Context, _ string, input ncpdatabase.UpdateRequest) (ncpdatabase.MutationResult, error) {
	a.operationCalls++
	a.connection, a.update = input.Connection, input
	return ncpdatabase.MutationResult{}, nil
}
func (a *tlsHTTPAgent) DeleteDatabaseRow(_ context.Context, _ string, input ncpdatabase.DeleteRequest) (ncpdatabase.MutationResult, error) {
	a.operationCalls++
	a.connection, a.delete = input.Connection, input
	return ncpdatabase.MutationResult{}, nil
}

type tlsHTTPEndpoint struct {
	path   string
	handle func(*handler, http.ResponseWriter, *http.Request)
	extra  string
}

func tlsHTTPEndpoints() []tlsHTTPEndpoint {
	return []tlsHTTPEndpoint{
		{"connect", (*handler).databaseConnect, ""},
		{"test-connection", (*handler).databaseTestConnection, ""},
		{"catalog", (*handler).databaseCatalog, ""},
		{"query", (*handler).databaseQuery, `,"sql":"SELECT 'credentials: {tls: null}'"`},
		{"rows", (*handler).databaseRows, `,"table":"tls"`},
		{"rows/insert", (*handler).databaseInsert, `,"table":"tls","values":{"tls":null,"credentials":{"tls":true},"id":9007199254740993}`},
		{"rows/update", (*handler).databaseUpdate, `,"table":"tls","values":{"tls":"tls"},"keys":{"tls":null,"credentials":{"tls":true},"id":9007199254740993}`},
		{"rows/delete", (*handler).databaseDelete, `,"table":"tls","keys":{"tls":null,"credentials":{"tls":true},"id":9007199254740993}`},
	}
}

func tlsHTTPRecorder(api *handler, endpoint tlsHTTPEndpoint, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/databases/"+endpoint.path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	endpoint.handle(api, response, request)
	return response
}

// 八个入口共享同一个 bounded raw 校验；所有用例只用 Recorder/fake。
func TestDatabaseTLSHTTPRejectsInvalidPolicyBeforeDispatchAllEndpoints(t *testing.T) {
	invalid := []struct{ name, fields string }{
		{"null", `"credentials":{"tls":null}`},
		{"empty", `"credentials":{"tls":{}}`},
		{"array", `"credentials":{"tls":[]}`},
		{"boolean", `"credentials":{"tls":false}`},
		{"number", `"credentials":{"tls":1}`},
		{"string", `"credentials":{"tls":"verify-full"}`},
		{"empty mode", `"credentials":{"tls":{"mode":""}}`},
		{"unknown mode", `"credentials":{"tls":{"mode":"prefer"}}`},
		{"mode case", `"credentials":{"tls":{"mode":"VERIFY-FULL"}}`},
		{"missing mode", `"credentials":{"tls":{"serverName":"synthetic.invalid"}}`},
		{"null mode", `"credentials":{"tls":{"mode":null}}`},
		{"nonstring mode", `"credentials":{"tls":{"mode":true}}`},
		{"unknown policy key", `"credentials":{"tls":{"mode":"verify-full","insecureSkipVerify":true}}`},
		{"duplicate mode", `"credentials":{"tls":{"mode":"verify-full","mode":"verify-full"}}`},
		{"mode alias", `"credentials":{"tls":{"mode":"verify-full","Mode":"verify-full"}}`},
		{"escaped duplicate", `"credentials":{"tls":{"mode":"verify-full","\u006dode":"verify-full"}}`},
		{"null CA", `"credentials":{"tls":{"mode":"verify-full","caPEM":null}}`},
		{"nonstring CA", `"credentials":{"tls":{"mode":"verify-full","caPEM":[]}}`},
		{"duplicate CA", `"credentials":{"tls":{"mode":"verify-full","caPEM":"","caPEM":""}}`},
		{"CA alias", `"credentials":{"tls":{"mode":"verify-full","capem":""}}`},
		{"null name", `"credentials":{"tls":{"mode":"verify-full","serverName":null}}`},
		{"nonstring name", `"credentials":{"tls":{"mode":"verify-full","serverName":42}}`},
		{"duplicate name", `"credentials":{"tls":{"mode":"verify-full","serverName":"","serverName":""}}`},
		{"unicode name alias", `"credentials":{"tls":{"mode":"verify-full","ſerverName":"synthetic.invalid"}}`},
		{"credential case", `"Credentials":{"TLS":null}`},
		{"TLS case", `"credentials":{"TlS":{"mode":"verify-full"}}`},
		{"unicode credential alias", `"credentialſ":{"tls":{"mode":"verify-full"}}`},
		{"unicode TLS alias", `"credentials":{"tlſ":{"mode":"verify-full"}}`},
		{"escaped unicode TLS alias", `"credentials":{"tl\u017f":{"mode":"verify-full"}}`},
		{"escaped unicode credential alias", `"credential\u017f":{"tls":{"mode":"verify-full"}}`},
		{"duplicate credentials after TLS", `"credentials":{"tls":{"mode":"verify-full"}},"credentials":{"username":"fixture"}`},
		{"duplicate credentials before TLS", `"credentials":{"username":"fixture"},"credentials":{"tls":{"mode":"verify-full"}}`},
		{"duplicate credentials null", `"credentials":{"tls":{"mode":"verify-full"}},"credentials":null`},
		{"duplicate credentials alias", `"credentials":null,"CREDENTIALS":{"tls":{"mode":"verify-full"}}`},
		{"duplicate TLS null", `"credentials":{"tls":{"mode":"verify-full"},"tls":null}`},
		{"duplicate TLS before valid", `"credentials":{"tls":null,"tls":{"mode":"verify-full"}}`},
		{"duplicate TLS valid", `"credentials":{"tls":{"mode":"verify-full"},"tls":{"mode":"verify-full"}}`},
		{"duplicate username", `"credentials":{"username":"fixture","tls":{"mode":"verify-full"},"username":"replacement"}`},
		{"credential field case", `"credentials":{"Username":"fixture","tls":{"mode":"verify-full"}}`},
		{"unicode credential field alias", `"credentials":{"uſername":"fixture","tls":{"mode":"verify-full"}}`},
		{"unknown credential", `"credentials":{"other":"fixture","tls":{"mode":"verify-full"}}`},
		{"null credential string", `"credentials":{"password":null,"tls":{"mode":"verify-full"}}`},
		{"duplicate source", `"sourceId":"other","credentials":{"tls":{"mode":"verify-full"}}`},
		{"escaped duplicate source", `"\u0073ourceId":"other","credentials":{"tls":{"mode":"verify-full"}}`},
		{"source case", `"SourceID":"fixture","credentials":{"tls":{"mode":"verify-full"}}`},
		{"unicode source alias", `"ſourceId":"fixture","credentials":{"tls":{"mode":"verify-full"}}`},
	}
	for _, name := range []string{"*.invalid", " synthetic.invalid", "synthetic.invalid ", "synthetic..invalid", "synthetic.invalid..", "本机.invalid", "[::1]", "fe80::1%eth0", "127.0.0.999", strings.Repeat("a", 64) + ".invalid", strings.Repeat("a.", 127)} {
		encoded, _ := json.Marshal(name)
		invalid = append(invalid, struct{ name, fields string }{"bad identity " + name, `"credentials":{"tls":{"mode":"verify-full","serverName":` + string(encoded) + `}}`})
	}
	ca := tlsHTTPTestCAPEM(t)
	for _, fixture := range []struct{ name, ca string }{
		{"bad PEM", "raw-private-ca-sentinel"},
		{"over byte budget", ca + strings.Repeat(" ", (64<<10)-len(ca)+1)},
		{"over certificate budget", strings.Repeat(ca, 33)},
		{"hidden private key", ca + "-----BEGIN PRIVATE KEY-----\nraw-private-ca-sentinel\n-----END PRIVATE KEY-----\n"},
	} {
		encoded, _ := json.Marshal(fixture.ca)
		invalid = append(invalid, struct{ name, fields string }{fixture.name, `"credentials":{"tls":{"mode":"verify-full","caPEM":` + string(encoded) + `}}`})
	}
	for _, endpoint := range tlsHTTPEndpoints() {
		t.Run(endpoint.path, func(t *testing.T) {
			for _, fixture := range invalid {
				t.Run(fixture.name, func(t *testing.T) {
					agent := &tlsHTTPAgent{}
					coordinator := &tlsHTTPProtectedCoordinator{}
					api := &handler{databaseAgent: agent, databaseConnections: coordinator}
					response := tlsHTTPRecorder(api, endpoint, `{"sourceId":"fixture",`+fixture.fields+endpoint.extra+`}`)
					var envelope ErrorResponse
					if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
						t.Fatal(err)
					}
					if response.Code != http.StatusBadRequest || envelope.Code != "DATABASE_INPUT_INVALID" {
						t.Fatalf("input response = %d %s", response.Code, response.Body)
					}
					if agent.discoverCalls != 0 || agent.testCalls != 0 || agent.operationCalls != 0 || coordinator.calls != 0 || coordinator.tests != 0 || coordinator.resolves != 0 {
						t.Fatal("invalid TLS input reached discovery, coordinator or Agent")
					}
					for _, secret := range []string{"raw-private-ca-sentinel", "BEGIN", "synthetic.invalid", "replacement"} {
						if strings.Contains(response.Body.String(), secret) {
							t.Fatal("input error exposed raw credential or policy input")
						}
					}
				})
			}
		})
	}
}

func TestDatabaseTLSHTTPAcceptsValidPolicyAllEndpoints(t *testing.T) {
	ca := tlsHTTPTestCAPEM(t)
	for _, policy := range []ncpdatabase.TLSPolicy{
		{Mode: ncpdatabase.TLSVerifyFull},
		{Mode: ncpdatabase.TLSVerifyFull, ServerName: "Synthetic.INVALID."},
		{Mode: ncpdatabase.TLSVerifyFull, CAPEM: ca, ServerName: "2001:db8::1"},
		{Mode: ncpdatabase.TLSVerifyFull, CAPEM: strings.Repeat(ca, 32)},
		{Mode: ncpdatabase.TLSVerifyFull, CAPEM: ca + strings.Repeat(" ", (64<<10)-len(ca))},
	} {
		encoded, err := json.Marshal(policy)
		if err != nil {
			t.Fatal(err)
		}
		for _, endpoint := range tlsHTTPEndpoints() {
			t.Run(endpoint.path, func(t *testing.T) {
				agent := &tlsHTTPAgent{}
				api := &handler{databaseAgent: agent}
				response := tlsHTTPRecorder(api, endpoint, `{"sourceId":"fixture","credentials":{"username":"fixture","password":"","tls":`+string(encoded)+`}`+endpoint.extra+`}`)
				wantStatus := http.StatusOK
				if endpoint.path == "rows/insert" {
					wantStatus = http.StatusCreated
				}
				if response.Code != wantStatus || !reflect.DeepEqual(agent.connection.Credentials.TLS, &policy) {
					t.Fatalf("valid TLS policy did not reach %s intact: %d %s", endpoint.path, response.Code, response.Body)
				}
				if agent.connection.Credentials.Password != "" {
					t.Fatal("empty password changed")
				}
				if strings.Contains(response.Body.String(), "appliedTls") {
					t.Fatal("policy input must not fabricate TLS application proof")
				}
			})
		}
	}
}

func TestDatabaseTLSHTTPInputKeepsLegacyCaseCompatibility(t *testing.T) {
	for _, fixture := range []struct {
		name, body string
		strict     bool
	}{
		{"legacy case", `{"SOURCEID":"fixture","CREDENTIALS":{"USERNAME":"fixture"}}`, false},
		{"legacy unicode", `{"ſourceId":"fixture","credentialſ":{"uſername":"fixture"}}`, false},
		{"legacy duplicate", `{"sourceId":"old","sourceId":"fixture","credentials":{"username":"old"},"credentials":{"username":"fixture"}}`, false},
		{"empty optional strings", `{"sourceId":"fixture","credentials":{"username":"fixture","tls":{"mode":"verify-full","caPEM":"","serverName":""}}}`, true},
		{"escaped canonical fields", `{"\u0073ourceId":"fixture","\u0063redentials":{"username":"fixture","\u0074ls":{"\u006dode":"verify-full"}}}`, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			agent := &tlsHTTPAgent{}
			response := tlsHTTPRecorder(&handler{databaseAgent: agent}, tlsHTTPEndpoints()[2], fixture.body)
			if response.Code != http.StatusOK || agent.operationCalls != 1 || agent.connection.SourceID != "fixture" || agent.connection.Credentials.Username != "fixture" || (agent.connection.Credentials.TLS != nil) != fixture.strict {
				t.Fatalf("decoder compatibility changed: %d %s", response.Code, response.Body)
			}
		})
	}
}

func tlsHTTPTestCAPEM(t *testing.T) string {
	t.Helper()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x3b}, ed25519.SeedSize))
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic HTTP fixture CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestDatabaseTLSHTTPInputPreservesDataAndLegacyPaths(t *testing.T) {
	for _, endpoint := range tlsHTTPEndpoints() {
		t.Run(endpoint.path, func(t *testing.T) {
			agent := &tlsHTTPAgent{}
			api := &handler{databaseAgent: agent}
			response := tlsHTTPRecorder(api, endpoint, `{"sourceId":"fixture","credentials":{"username":"{\"credentials\":{\"tls\":null}}","password":"tls"}`+endpoint.extra+`}`)
			wantStatus := http.StatusOK
			if endpoint.path == "rows/insert" {
				wantStatus = http.StatusCreated
			}
			if response.Code != wantStatus {
				t.Fatalf("row data or string rejected: %d %s", response.Code, response.Body)
			}
			var numbers map[string]any
			switch endpoint.path {
			case "rows/insert":
				numbers = agent.insert.Values
			case "rows/update":
				numbers = agent.update.Keys
			case "rows/delete":
				numbers = agent.delete.Keys
			}
			if numbers != nil {
				if number, ok := numbers["id"].(json.Number); !ok || string(number) != "9007199254740993" {
					t.Fatal("TLS input validation lost exact UseNumber decoding")
				}
			}
			if endpoint.path == "connect" {
				if agent.discoverCalls != 1 || agent.testCalls != 1 || agent.operationCalls != 0 {
					t.Fatal("nil coordinator changed legacy connect route")
				}
			} else if endpoint.path == "test-connection" {
				if agent.discoverCalls != 0 || agent.testCalls != 1 || agent.operationCalls != 0 {
					t.Fatal("nil coordinator changed legacy diagnostic route")
				}
			} else if agent.discoverCalls != 0 || agent.testCalls != 0 || agent.operationCalls != 1 {
				t.Fatal("nil coordinator changed legacy operation route")
			}
		})
	}
}

func TestDatabaseTLSHTTPDecoderPreservesGuards(t *testing.T) {
	valid := `{"sourceId":"fixture"}`
	for _, testCase := range []struct {
		name, body string
		headers    []string
		cancelled  bool
		status     int
	}{
		{"exact limit", valid + strings.Repeat(" ", maxDatabaseBodySize-len(valid)), []string{"application/json; charset=utf-8"}, false, http.StatusOK},
		{"over limit", valid + strings.Repeat(" ", maxDatabaseBodySize-len(valid)+1), []string{"application/json"}, false, http.StatusRequestEntityTooLarge},
		{"missing type", valid, nil, false, http.StatusUnsupportedMediaType},
		{"duplicate type", valid, []string{"application/json", "application/json"}, false, http.StatusUnsupportedMediaType},
		{"wrong type", valid, []string{"text/plain"}, false, http.StatusUnsupportedMediaType},
		{"unknown field", `{"sourceId":"fixture","other":true}`, []string{"application/json"}, false, http.StatusBadRequest},
		{"unknown credential", `{"credentials":{"other":true}}`, []string{"application/json"}, false, http.StatusBadRequest},
		{"trailing object", valid + `{}`, []string{"application/json"}, false, http.StatusBadRequest},
		{"trailing bytes", valid + `invalid`, []string{"application/json"}, false, http.StatusBadRequest},
		{"invalid JSON", `{"credentials":`, []string{"application/json"}, false, http.StatusBadRequest},
		{"cancelled", valid, []string{"application/json"}, true, http.StatusBadRequest},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/databases/query", strings.NewReader(testCase.body))
			for _, contentType := range testCase.headers {
				request.Header.Add("Content-Type", contentType)
			}
			if testCase.cancelled {
				ctx, cancel := context.WithCancel(request.Context())
				cancel()
				request = request.WithContext(ctx)
			}
			response := httptest.NewRecorder()
			var input ncpdatabase.Connection
			ok := (&handler{}).decodeDatabaseBody(response, request, &input)
			if ok != (testCase.status == http.StatusOK) || response.Code != testCase.status {
				t.Fatalf("decode = %t, %d %s", ok, response.Code, response.Body)
			}
		})
	}
}
