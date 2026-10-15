package agentsocket

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type DatabaseProvider interface {
	Discover(context.Context) (ncpdatabase.Discovery, error)
	Catalog(context.Context, ncpdatabase.CatalogRequest) (ncpdatabase.Catalog, error)
	Query(context.Context, ncpdatabase.QueryRequest) (ncpdatabase.QueryResult, error)
	Rows(context.Context, ncpdatabase.RowsRequest) (ncpdatabase.RowsResult, error)
	Insert(context.Context, ncpdatabase.InsertRequest) (ncpdatabase.MutationResult, error)
	Update(context.Context, ncpdatabase.UpdateRequest) (ncpdatabase.MutationResult, error)
	Delete(context.Context, ncpdatabase.DeleteRequest) (ncpdatabase.MutationResult, error)
}

type DatabaseDiscoveryOptionsProvider interface {
	DiscoverWithOptions(context.Context, bool) (ncpdatabase.Discovery, error)
}

// DatabaseConnectionProvider is optional to keep the existing database
// provider contract source-compatible while newer Agents expose diagnostics.
type DatabaseConnectionProvider interface {
	TestConnection(context.Context, ncpdatabase.Connection) (ncpdatabase.ConnectionDiagnostic, error)
}

// DatabaseQueryOptionsProvider 显式声明结果模式支持，不能回退到旧 Query。
type DatabaseQueryOptionsProvider interface {
	QueryWithOptions(context.Context, ncpdatabase.QueryRequest) (ncpdatabase.QueryResult, error)
}

// DatabaseTLSPolicyProvider 显式承诺全部建连入口都会执行 TLS 策略。
type DatabaseTLSPolicyProvider interface {
	DatabaseProvider
	DatabaseConnectionProvider
	DatabaseQueryOptionsProvider
	SupportsDatabaseTLSPolicy() bool
}

type databaseService struct {
	provider       DatabaseProvider
	strictProvider DatabaseTLSPolicyProvider
}

type databaseTLSService struct{ *databaseService }

func (*databaseTLSService) databaseTLSServiceMarker() {}

func newDatabaseTLSService(provider DatabaseTLSPolicyProvider) (*databaseTLSService, error) {
	if provider == nil || !provider.SupportsDatabaseTLSPolicy() {
		return nil, &ncpdatabase.DatabaseError{Code: ncpdatabase.CodeTLSPolicyUnsupported}
	}
	return &databaseTLSService{databaseService: &databaseService{provider: provider, strictProvider: provider}}, nil
}

func newDatabaseService(provider DatabaseProvider) *databaseService {
	return &databaseService{provider: provider}
}

func (s *databaseService) Discover(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	force := false
	if request != nil {
		var options struct {
			Refresh bool `json:"refresh"`
		}
		if err := decodeDatabaseRequest(request, &options); err == nil {
			force = options.Refresh
		}
	}
	var result ncpdatabase.Discovery
	var err error
	if provider, ok := s.provider.(DatabaseDiscoveryOptionsProvider); ok {
		result, err = provider.DiscoverWithOptions(ctx, force)
	} else {
		result, err = s.provider.Discover(ctx)
	}
	return databaseResponse(result, err)
}

func (s *databaseService) TestConnection(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	var decoded ncpdatabase.Connection
	if err := s.decodeConnectionRequest(request, &decoded, &decoded); err != nil {
		return nil, err
	}
	if s.strictProvider != nil {
		result, err := s.strictProvider.TestConnection(ctx, decoded)
		return databaseResponse(result, err)
	}
	tester, ok := s.provider.(DatabaseConnectionProvider)
	if !ok {
		return databaseResponse(ncpdatabase.ConnectionDiagnostic{}, &ncpdatabase.DatabaseError{
			Code: ncpdatabase.CodeAgentUnavailable, Operation: "test_connection",
		})
	}
	result, err := tester.TestConnection(ctx, decoded)
	return databaseResponse(result, err)
}

func (s *databaseService) Catalog(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	var decoded ncpdatabase.CatalogRequest
	if err := s.decodeConnectionRequest(request, &decoded, &decoded.Connection); err != nil {
		return nil, err
	}
	result, err := s.provider.Catalog(ctx, decoded)
	return databaseResponse(result, err)
}

func (s *databaseService) Query(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	var decoded ncpdatabase.QueryRequest
	if err := s.decodeConnectionRequest(request, &decoded, &decoded.Connection); err != nil {
		return nil, err
	}
	result, err := s.provider.Query(ctx, decoded)
	return databaseResponse(result, err)
}

func (s *databaseService) QueryWithOptions(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	var decoded ncpdatabase.QueryRequest
	if err := s.decodeConnectionRequest(request, &decoded, &decoded.Connection); err != nil {
		return nil, err
	}
	if err := ncpdatabase.ValidateQueryResultMode(decoded.ResultMode); err != nil {
		return databaseResponse(nil, err)
	}
	if s.strictProvider != nil {
		result, err := s.strictProvider.QueryWithOptions(ctx, decoded)
		return databaseResponse(result, err)
	}
	provider, ok := s.provider.(DatabaseQueryOptionsProvider)
	if !ok {
		return databaseResponse(nil, &ncpdatabase.DatabaseError{
			Code: ncpdatabase.CodeResultModeUnsupported, Operation: "query",
		})
	}
	result, err := provider.QueryWithOptions(ctx, decoded)
	return databaseResponse(result, err)
}

func (s *databaseService) Rows(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	var decoded ncpdatabase.RowsRequest
	if err := s.decodeConnectionRequest(request, &decoded, &decoded.Connection); err != nil {
		return nil, err
	}
	result, err := s.provider.Rows(ctx, decoded)
	return databaseResponse(result, err)
}

func (s *databaseService) Insert(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	var decoded ncpdatabase.InsertRequest
	if err := s.decodeConnectionRequest(request, &decoded, &decoded.Connection); err != nil {
		return nil, err
	}
	result, err := s.provider.Insert(ctx, decoded)
	return databaseResponse(result, err)
}

func (s *databaseService) Update(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	var decoded ncpdatabase.UpdateRequest
	if err := s.decodeConnectionRequest(request, &decoded, &decoded.Connection); err != nil {
		return nil, err
	}
	result, err := s.provider.Update(ctx, decoded)
	return databaseResponse(result, err)
}

func (s *databaseService) Delete(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	var decoded ncpdatabase.DeleteRequest
	if err := s.decodeConnectionRequest(request, &decoded, &decoded.Connection); err != nil {
		return nil, err
	}
	result, err := s.provider.Delete(ctx, decoded)
	return databaseResponse(result, err)
}

func decodeDatabaseRequest(request *structpb.Struct, destination any) error {
	if request == nil {
		return grpcstatus.Error(codes.InvalidArgument, "数据库请求不能为空")
	}
	encoded, err := json.Marshal(request.AsMap())
	if err != nil {
		return grpcstatus.Error(codes.InvalidArgument, "数据库请求格式无效")
	}
	if err := json.Unmarshal(encoded, destination); err != nil {
		return grpcstatus.Error(codes.InvalidArgument, "数据库请求格式无效")
	}
	return nil
}

// 每个建连入口都在调用 provider 前检查策略；旧 namespace 也不能吞掉 TLS。
func (s *databaseService) decodeConnectionRequest(request *structpb.Struct, destination any, connection *ncpdatabase.Connection) error {
	if s.strictProvider != nil {
		if err := decodeStrictDatabaseRequest(request, destination); err != nil {
			return err
		}
		if err := ncpdatabase.ValidateRequiredTLSPolicy(connection.Credentials.TLS); err != nil {
			_, statusErr := databaseResponse(nil, err)
			return statusErr
		}
		return nil
	}
	if err := decodeDatabaseRequest(request, destination); err != nil {
		return err
	}
	if connection.Credentials.TLS != nil {
		return grpcstatus.Error(codes.InvalidArgument, string(ncpdatabase.CodeTLSPolicyUnsupported))
	}
	return nil
}

func decodeStrictDatabaseRequest(request *structpb.Struct, destination any) error {
	if request == nil {
		return grpcstatus.Error(codes.InvalidArgument, string(ncpdatabase.CodeTLSConfigurationInvalid))
	}
	encoded, err := json.Marshal(request.AsMap())
	if err != nil {
		return grpcstatus.Error(codes.InvalidArgument, "数据库请求格式无效")
	}
	return decodeStrictDatabaseJSON(encoded, destination)
}

// 在结构体解码前检查协议对象，拒绝 Go JSON 的重复合并、大小写别名和 null 字符串。
// values/keys 是用户数据，其列名保持大小写敏感，不按协议字段规则重写。
func decodeStrictDatabaseJSON(encoded []byte, destination any) error {
	invalid := grpcstatus.Error(codes.InvalidArgument, "数据库请求格式无效")
	invalidTLS := grpcstatus.Error(codes.InvalidArgument, string(ncpdatabase.CodeTLSConfigurationInvalid))
	root, err := strictDatabaseJSONObject(encoded, []string{"sourceId", "credentials", "sql", "resultMode", "schema", "table", "limit", "offset", "sortColumn", "sortDirection", "values", "keys"}, invalid)
	if err != nil {
		return err
	}
	if raw, present := root["credentials"]; present {
		credentials, err := strictDatabaseJSONObject(raw, []string{"username", "password", "token", "database", "tls"}, invalidTLS)
		if err != nil {
			return err
		}
		if raw, present := credentials["tls"]; present {
			policy, err := strictDatabaseJSONObject(raw, []string{"mode", "caPEM", "serverName"}, invalidTLS)
			if err != nil {
				return err
			}
			for _, raw := range policy {
				var value any
				if json.Unmarshal(raw, &value) != nil {
					return invalidTLS
				}
				if _, ok := value.(string); !ok {
					return invalidTLS
				}
			}
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return invalid
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return invalid
	}
	return nil
}

func strictDatabaseJSONObject(encoded []byte, allowed []string, invalid error) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, invalid
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, invalid
		}
		key, ok := token.(string)
		if !ok {
			return nil, invalid
		}
		canonical := false
		for _, name := range allowed {
			if key == name {
				canonical = true
				break
			}
		}
		if !canonical {
			return nil, invalid
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, invalid
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, invalid
		}
		fields[key] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, invalid
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, invalid
	}
	return fields, nil
}

func databaseResponse(result any, err error) (*structpb.Struct, error) {
	if err != nil {
		code := ncpdatabase.ErrorCodeOf(err)
		if code == "" {
			code = ncpdatabase.ClassifyError(context.Background(), "database", err)
			if code == "" {
				code = ncpdatabase.CodeUnreachable
			}
		}
		grpcCode := codes.InvalidArgument
		switch code {
		case ncpdatabase.CodeTimeout:
			grpcCode = codes.DeadlineExceeded
		case ncpdatabase.CodeUnreachable, ncpdatabase.CodeAgentUnavailable, ncpdatabase.CodeOperationBusy:
			grpcCode = codes.Unavailable
		case ncpdatabase.CodePermissionDenied:
			grpcCode = codes.PermissionDenied
		case ncpdatabase.CodeDatabaseNotFound:
			grpcCode = codes.NotFound
		case ncpdatabase.CodeConstraintFailed:
			grpcCode = codes.FailedPrecondition
		case ncpdatabase.CodeCredentialConflict:
			grpcCode = codes.Aborted
		case ncpdatabase.CodeResultTooLarge:
			grpcCode = codes.ResourceExhausted
		case ncpdatabase.CodeMutationReceiptInvalid:
			grpcCode = codes.Internal
		}
		return nil, grpcstatus.Error(grpcCode, string(code))
	}
	// provider 不一定经过 Manager；必须在 Marshal 分配大缓冲区前再次核查预算。
	if err := ncpdatabase.ValidateResultBudget(result); err != nil {
		return nil, databaseResultTooLargeStatus()
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, grpcstatus.Error(codes.Internal, "AGENT_DATABASE_RESPONSE_INVALID")
	}
	if len(encoded) > ncpdatabase.MaxResultBytes {
		return nil, databaseResultTooLargeStatus()
	}
	values := make(map[string]any)
	if err := json.Unmarshal(encoded, &values); err != nil {
		return nil, grpcstatus.Error(codes.Internal, "AGENT_DATABASE_RESPONSE_INVALID")
	}
	response, err := structpb.NewStruct(values)
	if err != nil {
		return nil, grpcstatus.Error(codes.Internal, "AGENT_DATABASE_RESPONSE_INVALID")
	}
	// JSON 与 protobuf 的结构开销不同；最终线格式也必须低于同一预算。
	if proto.Size(response) > ncpdatabase.MaxResultBytes {
		return nil, databaseResultTooLargeStatus()
	}
	return response, nil
}

func databaseResultTooLargeStatus() error {
	return grpcstatus.Error(codes.ResourceExhausted, string(ncpdatabase.CodeResultTooLarge))
}
