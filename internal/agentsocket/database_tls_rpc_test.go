package agentsocket

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// 全部测试只调用纯内存描述符/Invoke fake；不创建 listener、ClientConn 或数据库。
type tlsRPCProvider struct {
	supports    bool
	calls       []string
	connection  ncpdatabase.Connection
	request     any
	queryResult ncpdatabase.QueryResult
	err         error
}

func (p *tlsRPCProvider) SupportsDatabaseTLSPolicy() bool { return p.supports }
func (p *tlsRPCProvider) record(method string, connection ncpdatabase.Connection, request any) {
	p.calls = append(p.calls, method)
	p.connection = connection
	p.request = request
}
func (p *tlsRPCProvider) Discover(context.Context) (ncpdatabase.Discovery, error) {
	p.calls = append(p.calls, "Discover")
	return ncpdatabase.Discovery{}, p.err
}
func (p *tlsRPCProvider) TestConnection(_ context.Context, request ncpdatabase.Connection) (ncpdatabase.ConnectionDiagnostic, error) {
	p.record("TestConnection", request, request)
	return ncpdatabase.ConnectionDiagnostic{Connected: true}, p.err
}
func (p *tlsRPCProvider) Catalog(_ context.Context, request ncpdatabase.CatalogRequest) (ncpdatabase.Catalog, error) {
	p.record("Catalog", request.Connection, request)
	return ncpdatabase.Catalog{}, p.err
}
func (p *tlsRPCProvider) Query(_ context.Context, request ncpdatabase.QueryRequest) (ncpdatabase.QueryResult, error) {
	p.record("Query", request.Connection, request)
	return p.queryResult, p.err
}
func (p *tlsRPCProvider) QueryWithOptions(_ context.Context, request ncpdatabase.QueryRequest) (ncpdatabase.QueryResult, error) {
	p.record("QueryWithOptions", request.Connection, request)
	return p.queryResult, p.err
}
func (p *tlsRPCProvider) Rows(_ context.Context, request ncpdatabase.RowsRequest) (ncpdatabase.RowsResult, error) {
	p.record("Rows", request.Connection, request)
	return ncpdatabase.RowsResult{}, p.err
}
func (p *tlsRPCProvider) Insert(_ context.Context, request ncpdatabase.InsertRequest) (ncpdatabase.MutationResult, error) {
	p.record("Insert", request.Connection, request)
	return ncpdatabase.MutationResult{RowsAffected: 1}, p.err
}
func (p *tlsRPCProvider) Update(_ context.Context, request ncpdatabase.UpdateRequest) (ncpdatabase.MutationResult, error) {
	p.record("Update", request.Connection, request)
	return ncpdatabase.MutationResult{RowsAffected: 1}, p.err
}
func (p *tlsRPCProvider) Delete(_ context.Context, request ncpdatabase.DeleteRequest) (ncpdatabase.MutationResult, error) {
	p.record("Delete", request.Connection, request)
	return ncpdatabase.MutationResult{RowsAffected: 1}, p.err
}

type tlsRPCRegistered struct {
	description    *grpc.ServiceDesc
	implementation any
}
type tlsRPCRegistrar map[string]tlsRPCRegistered

func (r tlsRPCRegistrar) RegisterService(description *grpc.ServiceDesc, implementation any) {
	r[description.ServiceName] = tlsRPCRegistered{description, implementation}
}

type tlsRPCTransport struct {
	services    tlsRPCRegistrar
	calls       []string
	payloads    []*structpb.Struct
	err         error
	response    *structpb.Struct
	interceptor grpc.UnaryServerInterceptor
}

func (c *tlsRPCTransport) Invoke(ctx context.Context, method string, input, output any, _ ...grpc.CallOption) error {
	c.calls = append(c.calls, method)
	wire, err := proto.Marshal(input.(*structpb.Struct))
	if err != nil {
		return err
	}
	payload := new(structpb.Struct)
	if err := proto.Unmarshal(wire, payload); err != nil {
		return err
	}
	c.payloads = append(c.payloads, payload)
	if c.err != nil {
		return c.err
	}
	response := c.response
	if response == nil {
		parts := strings.Split(strings.TrimPrefix(method, "/"), "/")
		if len(parts) != 2 {
			return grpcstatus.Error(codes.Unimplemented, "method unavailable")
		}
		registered, ok := c.services[parts[0]]
		if !ok {
			return grpcstatus.Error(codes.Unimplemented, "service unavailable")
		}
		found := false
		for _, descriptor := range registered.description.Methods {
			if descriptor.MethodName != parts[1] {
				continue
			}
			found = true
			value, err := descriptor.Handler(registered.implementation, ctx, func(target any) error { return proto.Unmarshal(wire, target.(*structpb.Struct)) }, c.interceptor)
			if err != nil {
				return err
			}
			response = value.(*structpb.Struct)
			break
		}
		if !found {
			return grpcstatus.Error(codes.Unimplemented, "method unavailable")
		}
	}
	wire, err = proto.Marshal(response)
	if err != nil {
		return err
	}
	return proto.Unmarshal(wire, output.(*structpb.Struct))
}
func (*tlsRPCTransport) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	return nil, errors.New("stream not supported by pure RPC fake")
}

func tlsRPCFixtureConnection(strict bool) ncpdatabase.Connection {
	connection := ncpdatabase.Connection{SourceID: "fixture", Credentials: ncpdatabase.Credentials{Username: "fixture-user", Password: "synthetic-password"}}
	if strict {
		connection.Credentials.TLS = &ncpdatabase.TLSPolicy{Mode: ncpdatabase.TLSVerifyFull, ServerName: "db.example.test"}
	}
	return connection
}
func tlsRPCOperations() []databaseConnectionOperation {
	return []databaseConnectionOperation{databaseOperationTestConnection, databaseOperationCatalog, databaseOperationQuery, databaseOperationQueryWithOptions, databaseOperationRows, databaseOperationInsert, databaseOperationUpdate, databaseOperationDelete}
}
func tlsRPCFixtureRequest(operation databaseConnectionOperation, connection ncpdatabase.Connection) any {
	switch operation {
	case databaseOperationTestConnection:
		return connection
	case databaseOperationCatalog:
		return ncpdatabase.CatalogRequest{Connection: connection}
	case databaseOperationQuery:
		return ncpdatabase.QueryRequest{Connection: connection, SQL: "SELECT 1"}
	case databaseOperationQueryWithOptions:
		return ncpdatabase.QueryRequest{Connection: connection, SQL: "UPDATE fixture SET value=1 RETURNING id", ResultMode: ncpdatabase.QueryResultModeRows}
	case databaseOperationRows:
		return ncpdatabase.RowsRequest{Connection: connection, Table: "fixture", Limit: 25, Offset: 3}
	case databaseOperationInsert:
		return ncpdatabase.InsertRequest{Connection: connection, Table: "fixture", Values: map[string]any{"value": "9007199254740993", "decimal": "1234567890.123456789"}}
	case databaseOperationUpdate:
		return ncpdatabase.UpdateRequest{InsertRequest: ncpdatabase.InsertRequest{Connection: connection, Table: "fixture", Values: map[string]any{"value": "9007199254740993"}}, Keys: map[string]any{"id": "9007199254740995"}}
	case databaseOperationDelete:
		return ncpdatabase.DeleteRequest{Connection: connection, Table: "fixture", Keys: map[string]any{"id": "9007199254740995"}}
	default:
		panic("invalid fixture operation")
	}
}
func tlsRPCPayload(t *testing.T, request any) *structpb.Struct {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		t.Fatal(err)
	}
	payload, err := structpb.NewStruct(values)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

type tlsRPCIncompleteProvider struct {
	DatabaseProvider
	DatabaseConnectionProvider
}

func (*tlsRPCIncompleteProvider) SupportsDatabaseTLSPolicy() bool { return true }

type tlsRPCMissingConnectionProvider struct {
	DatabaseProvider
	DatabaseQueryOptionsProvider
}

func (*tlsRPCMissingConnectionProvider) SupportsDatabaseTLSPolicy() bool { return true }

func TestDatabaseTLSRPCRegistrationRequiresCompleteOptIn(t *testing.T) {
	complete := &tlsRPCProvider{supports: true}
	for _, test := range []struct {
		name     string
		provider DatabaseProvider
		strict   bool
	}{
		{"complete", complete, true}, {"false marker", &tlsRPCProvider{}, false},
		{"legacy", struct{ DatabaseProvider }{complete}, false},
		{"missing query options", &tlsRPCIncompleteProvider{complete, complete}, false},
		{"missing test connection", &tlsRPCMissingConnectionProvider{complete, complete}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			registrar := tlsRPCRegistrar{}
			registerDatabaseServices(registrar, test.provider)
			if _, ok := registrar[agentDatabaseServiceName]; !ok {
				t.Fatal("legacy service missing")
			}
			_, strict := registrar[agentDatabaseTLSServiceName]
			if strict != test.strict {
				t.Fatalf("strict registered=%v", strict)
			}
			want := 1
			if test.strict {
				want++
			}
			if len(registrar) != want {
				t.Fatalf("services=%d", len(registrar))
			}
		})
	}
	if _, err := newDatabaseTLSService(&tlsRPCProvider{}); ncpdatabase.ErrorCodeOf(err) != ncpdatabase.CodeTLSPolicyUnsupported {
		t.Fatalf("false marker=%v", err)
	}
	if _, ok := any(newDatabaseService(complete)).(AgentDatabaseTLSServiceServer); ok {
		t.Fatal("legacy service acquired strict registration marker")
	}
	if len(agentDatabaseTLSServiceDescription.Methods) != 8 {
		t.Fatalf("strict methods=%d", len(agentDatabaseTLSServiceDescription.Methods))
	}
	for _, method := range agentDatabaseTLSServiceDescription.Methods {
		if method.MethodName == "Discover" {
			t.Fatal("strict Discover must not exist")
		}
	}
}

func tlsRPCFixtureCA(t *testing.T) string {
	t.Helper()
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Synthetic RPC CA"},
		NotBefore: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestDatabaseTLSRPCClientRoutesAllEightAndPreservesPayload(t *testing.T) {
	caPEM := tlsRPCFixtureCA(t)
	for _, strict := range []bool{false, true} {
		for _, operation := range tlsRPCOperations() {
			t.Run(string(operation)+map[bool]string{false: "/legacy", true: "/strict"}[strict], func(t *testing.T) {
				provider := &tlsRPCProvider{supports: true, queryResult: ncpdatabase.QueryResult{Columns: []string{"id"}, Rows: [][]any{{"9007199254740993"}}, RowsAffectedKnown: true}}
				services := tlsRPCRegistrar{}
				registerDatabaseServices(services, provider)
				transport := &tlsRPCTransport{services: services}
				connection := tlsRPCFixtureConnection(strict)
				if strict {
					connection.Credentials.TLS.CAPEM = caPEM
				}
				var result map[string]any
				err := callDatabaseConnectionClient(context.Background(), transport, connection, tlsRPCFixtureRequest(operation, connection), &result, operation)
				if err != nil {
					t.Fatal(err)
				}
				prefix := agentDatabaseServiceName
				if strict {
					prefix = agentDatabaseTLSServiceName
				}
				want := "/" + prefix + "/" + string(operation)
				if !reflect.DeepEqual(transport.calls, []string{want}) || !reflect.DeepEqual(provider.calls, []string{string(operation)}) {
					t.Fatalf("transport=%v provider=%v", transport.calls, provider.calls)
				}
				if !reflect.DeepEqual(provider.connection, connection) {
					t.Fatalf("connection lost: %#v", provider.connection)
				}
				for _, field := range []string{"values", "keys"} {
					row, ok := transport.payloads[0].AsMap()[field].(map[string]any)
					if !ok {
						continue
					}
					for key, value := range row {
						if _, ok := value.(string); !ok {
							t.Fatalf("%s.%s rounded/type changed: %T", field, key, value)
						}
					}
				}
				switch request := provider.request.(type) {
				case ncpdatabase.InsertRequest:
					if request.Values["value"] != "9007199254740993" || request.Values["decimal"] != "1234567890.123456789" {
						t.Fatalf("insert numeric precision=%v", request.Values)
					}
				case ncpdatabase.UpdateRequest:
					if request.Values["value"] != "9007199254740993" || request.Keys["id"] != "9007199254740995" {
						t.Fatalf("update numeric precision=%v/%v", request.Values, request.Keys)
					}
				case ncpdatabase.DeleteRequest:
					if request.Keys["id"] != "9007199254740995" {
						t.Fatalf("delete numeric precision=%v", request.Keys)
					}
				case ncpdatabase.RowsRequest:
					if request.Limit != 25 || request.Offset != 3 {
						t.Fatalf("pagination changed: %#v", request)
					}
				}
			})
		}
	}
}

func TestDatabaseTLSRPCHandlersRequirePolicyBeforeProvider(t *testing.T) {
	fixtures := []struct {
		name        string
		credentials any
		omit        bool
		valid       bool
	}{
		{name: "missing credentials", omit: true}, {name: "null credentials", credentials: nil},
		{name: "missing tls", credentials: map[string]any{}}, {name: "null tls", credentials: map[string]any{"tls": nil}},
		{name: "empty tls", credentials: map[string]any{"tls": map[string]any{}}},
		{name: "empty mode", credentials: map[string]any{"tls": map[string]any{"mode": ""}}},
		{name: "unknown mode", credentials: map[string]any{"tls": map[string]any{"mode": "prefer"}}},
		{name: "bad CA", credentials: map[string]any{"tls": map[string]any{"mode": "verify-full", "caPEM": "synthetic-bad-ca"}}},
		{name: "bad name", credentials: map[string]any{"tls": map[string]any{"mode": "verify-full", "serverName": "https://db.example.test"}}},
		{name: "null CA", credentials: map[string]any{"tls": map[string]any{"mode": "verify-full", "caPEM": nil}}},
		{name: "null name", credentials: map[string]any{"tls": map[string]any{"mode": "verify-full", "serverName": nil}}},
		{name: "case TLS", credentials: map[string]any{"TLS": map[string]any{"mode": "verify-full"}}},
		{name: "unicode TLS", credentials: map[string]any{"tlſ": map[string]any{"mode": "verify-full"}}},
		{name: "case mode", credentials: map[string]any{"tls": map[string]any{"Mode": "verify-full"}}},
		{name: "conflicting case mode", credentials: map[string]any{"tls": map[string]any{"mode": "verify-full", "Mode": "disable"}}},
		{name: "unknown policy", credentials: map[string]any{"tls": map[string]any{"mode": "verify-full", "insecure": true}}},
		{name: "valid", credentials: map[string]any{"tls": map[string]any{"mode": "verify-full", "serverName": "db.example.test"}}, valid: true},
	}
	for _, descriptor := range agentDatabaseTLSServiceDescription.Methods {
		for _, intercept := range []bool{false, true} {
			for _, fixture := range fixtures {
				t.Run(descriptor.MethodName+"/"+fixture.name+map[bool]string{false: "/direct", true: "/intercepted"}[intercept], func(t *testing.T) {
					provider := &tlsRPCProvider{supports: true}
					service, err := newDatabaseTLSService(provider)
					if err != nil {
						t.Fatal(err)
					}
					values := tlsRPCPayload(t, tlsRPCFixtureRequest(databaseConnectionOperation(descriptor.MethodName), tlsRPCFixtureConnection(false))).AsMap()
					if fixture.omit {
						delete(values, "credentials")
					} else {
						values["credentials"] = fixture.credentials
					}
					payload, err := structpb.NewStruct(values)
					if err != nil {
						t.Fatal(err)
					}
					wire, err := proto.Marshal(payload)
					if err != nil {
						t.Fatal(err)
					}
					var interceptor grpc.UnaryServerInterceptor
					intercepted := 0
					if intercept {
						interceptor = func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
							intercepted++
							if info.FullMethod != "/"+agentDatabaseTLSServiceName+"/"+descriptor.MethodName || info.Server != service {
								t.Fatalf("bad interceptor info: %#v", info)
							}
							return handler(ctx, request)
						}
					}
					_, err = descriptor.Handler(service, context.Background(), func(target any) error { return proto.Unmarshal(wire, target.(*structpb.Struct)) }, interceptor)
					if fixture.valid {
						if err != nil || !reflect.DeepEqual(provider.calls, []string{descriptor.MethodName}) {
							t.Fatalf("valid err=%v calls=%v", err, provider.calls)
						}
					} else {
						if grpcstatus.Code(err) != codes.InvalidArgument || grpcstatus.Convert(err).Message() != string(ncpdatabase.CodeTLSConfigurationInvalid) || len(provider.calls) != 0 {
							t.Fatalf("invalid err=%v calls=%v", err, provider.calls)
						}
					}
					if intercept && intercepted != 1 {
						t.Fatalf("interceptor calls=%d", intercepted)
					}
				})
			}
		}
	}
}

func TestDatabaseTLSRPCLegacyHandlersRejectKnownTLS(t *testing.T) {
	for _, descriptor := range agentDatabaseServiceDescription.Methods {
		if descriptor.MethodName == "Discover" {
			continue
		}
		t.Run(descriptor.MethodName, func(t *testing.T) {
			provider := &tlsRPCProvider{supports: true}
			service := newDatabaseService(provider)
			for _, policy := range []*ncpdatabase.TLSPolicy{{Mode: ncpdatabase.TLSVerifyFull}, {}} {
				connection := tlsRPCFixtureConnection(false)
				connection.Credentials.TLS = policy
				payload := tlsRPCPayload(t, tlsRPCFixtureRequest(databaseConnectionOperation(descriptor.MethodName), connection))
				_, err := descriptor.Handler(service, context.Background(), func(target any) error { proto.Merge(target.(*structpb.Struct), payload); return nil }, nil)
				if grpcstatus.Convert(err).Message() != string(ncpdatabase.CodeTLSPolicyUnsupported) || len(provider.calls) != 0 {
					t.Fatalf("legacy accepted TLS: %v, calls=%v", err, provider.calls)
				}
			}
		})
	}
	// 即使旧 provider 没有可选测试能力，也应先拒绝 TLS 而不是掩盖为 unavailable。
	service := newDatabaseService(struct{ DatabaseProvider }{&tlsRPCProvider{}})
	_, err := service.TestConnection(context.Background(), tlsRPCPayload(t, tlsRPCFixtureConnection(true)))
	if grpcstatus.Convert(err).Message() != string(ncpdatabase.CodeTLSPolicyUnsupported) {
		t.Fatal(err)
	}
}

func TestDatabaseTLSRPCClientUnimplementedNeverFallsBack(t *testing.T) {
	for _, operation := range tlsRPCOperations() {
		t.Run(string(operation), func(t *testing.T) {
			transport := &tlsRPCTransport{err: grpcstatus.Error(codes.Unimplemented, "synthetic-private-detail")}
			connection := tlsRPCFixtureConnection(true)
			var result map[string]any
			err := callDatabaseConnectionClient(context.Background(), transport, connection, tlsRPCFixtureRequest(operation, connection), &result, operation)
			if ErrorCode(err) != string(ncpdatabase.CodeTLSPolicyUnsupported) || len(transport.calls) != 1 || transport.calls[0] != "/"+agentDatabaseTLSServiceName+"/"+string(operation) {
				t.Fatalf("err=%v calls=%v", err, transport.calls)
			}
			for chain := err; chain != nil; chain = errors.Unwrap(chain) {
				if strings.Contains(chain.Error(), "synthetic-private-detail") {
					t.Fatal("private gRPC error retained")
				}
			}
		})
	}
}

func TestDatabaseTLSRPCQueryModeRoutingAndErrorPriority(t *testing.T) {
	for _, strict := range []bool{false, true} {
		for _, mode := range []ncpdatabase.QueryResultMode{"", ncpdatabase.QueryResultModeAuto, ncpdatabase.QueryResultModeRows, ncpdatabase.QueryResultModeExec} {
			operation := databaseOperationQuery
			if mode != "" {
				operation = databaseOperationQueryWithOptions
			}
			connection := tlsRPCFixtureConnection(strict)
			request := ncpdatabase.QueryRequest{Connection: connection, SQL: "UPDATE fixture SET value=1", ResultMode: mode}
			for _, unimplemented := range []bool{false, true} {
				provider := &tlsRPCProvider{supports: true}
				services := tlsRPCRegistrar{}
				registerDatabaseServices(services, provider)
				transport := &tlsRPCTransport{services: services}
				if unimplemented {
					transport.err = grpcstatus.Error(codes.Unimplemented, "old agent")
				}
				var result ncpdatabase.QueryResult
				err := callDatabaseConnectionClient(context.Background(), transport, connection, request, &result, operation)
				if unimplemented {
					want := ncpdatabase.CodeAgentUnavailable
					if mode != "" {
						want = ncpdatabase.CodeResultModeUnsupported
					}
					if strict {
						want = ncpdatabase.CodeTLSPolicyUnsupported
					}
					if ErrorCode(err) != string(want) {
						t.Fatalf("strict=%v mode=%s error=%v want=%s", strict, mode, err, want)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				prefix := agentDatabaseServiceName
				if strict {
					prefix = agentDatabaseTLSServiceName
				}
				if !reflect.DeepEqual(transport.calls, []string{"/" + prefix + "/" + string(operation)}) {
					t.Fatal(transport.calls)
				}
				value, present := transport.payloads[0].AsMap()["resultMode"]
				if present != (mode != "") || present && value != string(mode) {
					t.Fatalf("mode changed: %v, present=%v", value, present)
				}
			}
		}
	}
	provider := &tlsRPCProvider{supports: true, err: &ncpdatabase.DatabaseError{Code: ncpdatabase.CodeResultModeUnsupported}}
	services := tlsRPCRegistrar{}
	registerDatabaseServices(services, provider)
	transport := &tlsRPCTransport{services: services}
	connection := tlsRPCFixtureConnection(true)
	var result ncpdatabase.QueryResult
	err := callDatabaseConnectionClient(context.Background(), transport, connection, tlsRPCFixtureRequest(databaseOperationQueryWithOptions, connection), &result, databaseOperationQueryWithOptions)
	if ErrorCode(err) != string(ncpdatabase.CodeResultModeUnsupported) || len(provider.calls) != 1 || len(transport.calls) != 1 {
		t.Fatalf("explicit provider error changed: %v", err)
	}
}

func TestDatabaseTLSRPCStrictJSONRejectsAmbiguousProtocol(t *testing.T) {
	fixtures := []string{
		`{"sourceId":"fixture","credentials":{"tls":{"mode":"verify-full"}},"credentials":{}}`,
		`{"sourceId":"fixture","credentials":{"tls":{"mode":"verify-full"},"tls":null}}`,
		`{"sourceId":"fixture","credentials":{"tls":{"mode":"verify-full","mode":"disable"}}}`,
		`{"sourceId":"fixture","credentials":{"tls":{"mode":"verify-full","caPEM":"","caPEM":""}}}`,
		`{"sourceId":"fixture","Credentials":{"tls":{"mode":"verify-full"}}}`,
		`{"sourceId":"fixture","credentialſ":{"tlſ":{"mode":"verify-full"}}}`,
		`{"sourceId":"fixture","credentials":{"TLS":{"mode":"verify-full"}}}`,
		`{"sourceId":"fixture","credentials":{"tls":{"mode":"verify-full","ServerName":"db.example.test"}}}`,
		`{"sourceId":"fixture","credentials":{"tls":{"mode":"verify-full","serverName":null}}}`,
		`{"sourceId":"fixture","credentials":{"tls":{"mode":"verify-full","caPEM":null}}}`,
		`{"sourceId":"fixture","credentials":{"tls":{"mode":null}}}`,
		`{"sourceId":"fixture","credentials":{"tls":{"mode":"verify-full","extra":true}}}`,
		`{"sourceId":"fixture","credentials":{"extra":true,"tls":{"mode":"verify-full"}}}`,
		`{"sourceId":"fixture","extra":true,"credentials":{"tls":{"mode":"verify-full"}}}`,
		`{"sourceId":"fixture","credentials":{"tls":{"mode":"verify-full"}}} {}`,
		`{"sourceId":"fixture","credentials":{"tls":{"mode":"verify-full"}}} trailing`,
		`[]`, `null`,
	}
	for _, raw := range fixtures {
		var request ncpdatabase.Connection
		if err := decodeStrictDatabaseJSON([]byte(raw), &request); err == nil {
			t.Errorf("ambiguous JSON accepted: %s", raw)
		}
	}
	// 用户列名不是协议字段，可同时包含 TLS、tls、大小写不同及 null。
	var request ncpdatabase.InsertRequest
	raw := `{"sourceId":"fixture","credentials":{"tls":{"mode":"verify-full"}},"table":"fixture","values":{"TLS":null,"tls":{"mode":"plain"},"credentialſ":"text","id":9007199254740993,"ID":2}}`
	if err := decodeStrictDatabaseJSON([]byte(raw), &request); err != nil {
		t.Fatal(err)
	}
	if request.Values["id"] != json.Number("9007199254740993") {
		t.Fatalf("raw precision=%v", request.Values["id"])
	}
	// 不属于具体请求的协议字段仍须由 DisallowUnknownFields 拒绝。
	if err := decodeStrictDatabaseJSON([]byte(raw), new(ncpdatabase.Connection)); err == nil {
		t.Fatal("request-specific unknown fields accepted")
	}
}

func TestDatabaseTLSRPCRejectsInvalidPolicyAndModesBeforeInvoke(t *testing.T) {
	for _, operation := range tlsRPCOperations() {
		transport := &tlsRPCTransport{}
		connection := tlsRPCFixtureConnection(true)
		connection.Credentials.TLS.Mode = "prefer"
		err := callDatabaseConnectionClient(context.Background(), transport, connection, tlsRPCFixtureRequest(operation, connection), new(map[string]any), operation)
		if ErrorCode(err) != string(ncpdatabase.CodeTLSConfigurationInvalid) || len(transport.calls) != 0 {
			t.Fatalf("%s: err=%v calls=%v", operation, err, transport.calls)
		}
	}
	provider := &tlsRPCProvider{supports: true}
	service, err := newDatabaseTLSService(provider)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []ncpdatabase.QueryResultMode{"ROWS", " rows", "exec ", "bogus"} {
		request := ncpdatabase.QueryRequest{Connection: tlsRPCFixtureConnection(true), SQL: "UPDATE fixture SET value=1", ResultMode: mode}
		_, err := service.QueryWithOptions(context.Background(), tlsRPCPayload(t, request))
		if grpcstatus.Convert(err).Message() != string(ncpdatabase.CodeSQLInvalid) || len(provider.calls) != 0 {
			t.Fatalf("bad mode=%s err=%v calls=%v", mode, err, provider.calls)
		}
		if _, err := QueryDatabase(context.Background(), "", request); ErrorCode(err) != string(ncpdatabase.CodeSQLInvalid) {
			t.Fatalf("client mode validation=%v", err)
		}
	}
}

func TestDatabaseTLSRPCResponseBudgetsAndSafeErrors(t *testing.T) {
	connection := tlsRPCFixtureConnection(true)
	request := tlsRPCFixtureRequest(databaseOperationQuery, connection)
	for _, providerBudget := range []bool{false, true} {
		provider := &tlsRPCProvider{supports: true, queryResult: ncpdatabase.QueryResult{Columns: []string{"value"}, Rows: [][]any{{strings.Repeat("x", ncpdatabase.MaxResultBytes)}}}}
		services := tlsRPCRegistrar{}
		registerDatabaseServices(services, provider)
		transport := &tlsRPCTransport{services: services}
		if !providerBudget {
			transport.response = tlsRPCPayload(t, map[string]any{"columns": []string{"value"}, "rows": [][]any{{strings.Repeat("x", ncpdatabase.MaxResultBytes)}}})
		}
		var result ncpdatabase.QueryResult
		if err := callDatabaseConnectionClient(context.Background(), transport, connection, request, &result, databaseOperationQuery); ErrorCode(err) != string(ncpdatabase.CodeResultTooLarge) {
			t.Fatalf("providerBudget=%v err=%v", providerBudget, err)
		}
		if len(transport.calls) != 1 {
			t.Fatalf("budget replay=%v", transport.calls)
		}
	}
	for _, code := range []ncpdatabase.ErrorCode{ncpdatabase.CodeTLSConfigurationInvalid, ncpdatabase.CodeTLSPolicyUnsupported, ncpdatabase.CodeTLSVerificationFailed, ncpdatabase.CodeCredentialConflict} {
		provider := &tlsRPCProvider{supports: true, err: &ncpdatabase.DatabaseError{Code: code, Cause: errors.New("synthetic-private-certificate")}}
		services := tlsRPCRegistrar{}
		registerDatabaseServices(services, provider)
		transport := &tlsRPCTransport{services: services}
		var result ncpdatabase.QueryResult
		err := callDatabaseConnectionClient(context.Background(), transport, connection, request, &result, databaseOperationQuery)
		if ErrorCode(err) != string(code) {
			t.Fatalf("code=%s err=%v", code, err)
		}
		for chain := err; chain != nil; chain = errors.Unwrap(chain) {
			if strings.Contains(chain.Error(), "synthetic-private-certificate") {
				t.Fatal("cause exposed")
			}
		}
	}
}

func TestDatabaseTLSRPCPublicWrappersRejectInvalidPolicyBeforeDial(t *testing.T) {
	connection := tlsRPCFixtureConnection(true)
	connection.Credentials.TLS.Mode = "disable"
	// 非法策略必须早于 socket 参数检查；不会创建连接或 listener。
	calls := []struct {
		name string
		call func() error
	}{
		{"TestConnection", func() error { _, err := TestDatabaseConnection(context.Background(), "", connection); return err }},
		{"Catalog", func() error {
			_, err := CatalogDatabase(context.Background(), "", ncpdatabase.CatalogRequest{Connection: connection})
			return err
		}},
		{"Query", func() error {
			_, err := QueryDatabase(context.Background(), "", ncpdatabase.QueryRequest{Connection: connection})
			return err
		}},
		{"QueryWithOptions", func() error {
			_, err := QueryDatabase(context.Background(), "", ncpdatabase.QueryRequest{Connection: connection, ResultMode: ncpdatabase.QueryResultModeExec})
			return err
		}},
		{"Rows", func() error {
			_, err := ReadDatabaseRows(context.Background(), "", ncpdatabase.RowsRequest{Connection: connection})
			return err
		}},
		{"Insert", func() error {
			_, err := InsertDatabaseRow(context.Background(), "", ncpdatabase.InsertRequest{Connection: connection})
			return err
		}},
		{"Update", func() error {
			_, err := UpdateDatabaseRow(context.Background(), "", ncpdatabase.UpdateRequest{InsertRequest: ncpdatabase.InsertRequest{Connection: connection}})
			return err
		}},
		{"Delete", func() error {
			_, err := DeleteDatabaseRow(context.Background(), "", ncpdatabase.DeleteRequest{Connection: connection})
			return err
		}},
	}
	for _, test := range calls {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); ErrorCode(err) != string(ncpdatabase.CodeTLSConfigurationInvalid) {
				t.Fatal(err)
			}
		})
	}
}

func TestDatabaseTLSRPCHandlerRejectsRootAliasesAndDecoderErrors(t *testing.T) {
	for _, descriptor := range agentDatabaseTLSServiceDescription.Methods {
		provider := &tlsRPCProvider{supports: true}
		service, err := newDatabaseTLSService(provider)
		if err != nil {
			t.Fatal(err)
		}
		for _, alias := range []string{"Credentials", "credentialſ"} {
			values := tlsRPCPayload(t, tlsRPCFixtureRequest(databaseConnectionOperation(descriptor.MethodName), tlsRPCFixtureConnection(true))).AsMap()
			values[alias] = values["credentials"]
			delete(values, "credentials")
			payload, err := structpb.NewStruct(values)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := proto.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			_, err = descriptor.Handler(service, context.Background(), func(target any) error { return proto.Unmarshal(wire, target.(*structpb.Struct)) }, nil)
			if grpcstatus.Code(err) != codes.InvalidArgument || len(provider.calls) != 0 {
				t.Fatalf("%s alias %s: err=%v calls=%v", descriptor.MethodName, alias, err, provider.calls)
			}
		}
		decodeErr := errors.New("synthetic decode failure")
		_, err = descriptor.Handler(service, context.Background(), func(any) error { return decodeErr }, func(context.Context, any, *grpc.UnaryServerInfo, grpc.UnaryHandler) (any, error) {
			t.Fatal("decoder failure reached interceptor")
			return nil, nil
		})
		if !errors.Is(err, decodeErr) || len(provider.calls) != 0 {
			t.Fatalf("decode failure=%v", err)
		}
	}
}

func TestDatabaseTLSRPCConnectionSnapshotSeparatesPolicy(t *testing.T) {
	original := tlsRPCFixtureConnection(true)
	snapshot := snapshotDatabaseConnection(original)
	original.Credentials.TLS.Mode = "disable"
	original.Credentials.TLS.ServerName = "changed.example.test"
	if snapshot.Credentials.TLS == original.Credentials.TLS || snapshot.Credentials.TLS.Mode != ncpdatabase.TLSVerifyFull || snapshot.Credentials.TLS.ServerName != "db.example.test" {
		t.Fatal("connection policy aliases caller")
	}
	provider := &tlsRPCProvider{supports: true}
	services := tlsRPCRegistrar{}
	registerDatabaseServices(services, provider)
	transport := &tlsRPCTransport{services: services}
	for _, operation := range tlsRPCOperations() {
		var result map[string]any
		err := callDatabaseConnectionClient(context.Background(), transport, snapshot, tlsRPCFixtureRequest(operation, snapshot), &result, operation)
		if err != nil || provider.connection.Credentials.TLS.Mode != ncpdatabase.TLSVerifyFull {
			t.Fatalf("%s: frozen request lost: %v", operation, err)
		}
	}
}

func TestDatabaseTLSRPCUnimplementedKeepsContextPriority(t *testing.T) {
	connection := tlsRPCFixtureConnection(true)
	for _, operation := range tlsRPCOperations() {
		for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded} {
			transport := &tlsRPCTransport{err: errors.Join(grpcstatus.Error(codes.Unimplemented, "synthetic private method"), sentinel)}
			err := callDatabaseConnectionClient(context.Background(), transport, connection, tlsRPCFixtureRequest(operation, connection), new(map[string]any), operation)
			if ErrorCode(err) != string(ncpdatabase.CodeTimeout) || !errors.Is(err, sentinel) || len(transport.calls) != 1 {
				t.Fatalf("%s priority=%v calls=%v", operation, err, transport.calls)
			}
		}
	}
}
