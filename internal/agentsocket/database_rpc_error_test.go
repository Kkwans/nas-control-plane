package agentsocket

import (
	"context"
	"errors"
	"fmt"
	"testing"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

func assertDatabaseRPCStableError(t *testing.T, err error, want ncpdatabase.ErrorCode) {
	t.Helper()
	if err == nil || err.Error() != string(want) || ErrorCode(err) != string(want) || ncpdatabase.ErrorCodeOf(err) != want {
		t.Fatalf("database error = %v, agentsocket code = %q, database code = %q, want %q", err, ErrorCode(err), ncpdatabase.ErrorCodeOf(err), want)
	}
	var socketErr *codedError
	var databaseErr *ncpdatabase.DatabaseError
	if !errors.As(err, &socketErr) || !errors.As(err, &databaseErr) || errors.Unwrap(err) != databaseErr {
		t.Fatalf("database error lost its compatible wrappers: %T", err)
	}
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if cause.Error() != string(want) && cause != context.Canceled && cause != context.DeadlineExceeded {
			t.Fatalf("database error retained an unsafe cause of type %T", cause)
		}
		if _, ok := grpcstatus.FromError(cause); ok {
			t.Fatal("database error retained a raw gRPC status")
		}
	}
	for _, operation := range []string{"manual_connect", "auto_connect", "query"} {
		mapped := ncpdatabase.MapError(context.Background(), ncpdatabase.Source{ID: "fixture", Driver: ncpdatabase.DriverMySQL}, operation, err)
		if mapped != databaseErr || ncpdatabase.ErrorCodeOf(mapped) != want {
			t.Fatalf("%s reclassified stable code as %q", operation, ncpdatabase.ErrorCodeOf(mapped))
		}
	}
}

// 仅经过内存状态编码和客户端回调，不创建监听器、连接或真实数据库。
func TestDatabaseRPCStableCodesSurviveCoordinatorMapping(t *testing.T) {
	for _, code := range []ncpdatabase.ErrorCode{
		ncpdatabase.CodeAuthFailed, ncpdatabase.CodeCredentialsRequired,
		ncpdatabase.CodePermissionDenied, ncpdatabase.CodeDatabaseNotFound,
		ncpdatabase.CodeTimeout, ncpdatabase.CodeResultTooLarge, ncpdatabase.CodeOperationBusy,
		ncpdatabase.CodeUnreachable, ncpdatabase.CodeSQLInvalid,
		ncpdatabase.CodeResultModeUnsupported, ncpdatabase.CodeConstraintFailed,
		ncpdatabase.CodeAgentUnavailable, ncpdatabase.CodeCredentialStoreUnavailable,
		ncpdatabase.CodeCredentialCorrupt, ncpdatabase.CodeKeyUnavailable,
		ncpdatabase.CodeKeyRotationFailed, ncpdatabase.CodeMigrationFailed,
		ncpdatabase.CodeCredentialConflict,
	} {
		t.Run(string(code), func(t *testing.T) {
			_, serviceErr := databaseResponse(nil, &ncpdatabase.DatabaseError{Code: code, Cause: errors.New("password=private; SQL=private")})
			for _, status := range []*grpcstatus.Status{grpcstatus.Convert(serviceErr), grpcstatus.New(codes.Unknown, string(code))} {
				withDetails, err := status.WithDetails(structpb.NewStringValue("password=private; SQL=private"))
				if err != nil {
					t.Fatal(err)
				}
				wire, err := proto.Marshal(withDetails.Proto())
				if err != nil {
					t.Fatal(err)
				}
				received := grpcstatus.New(codes.OK, "").Proto()
				if err := proto.Unmarshal(wire, received); err != nil {
					t.Fatal(err)
				}
				var result ncpdatabase.ConnectionDiagnostic
				calls := 0
				err = callDatabaseClient(nil, map[string]any{}, &result, func(AgentDatabaseServiceClient, *structpb.Struct) (*structpb.Struct, error) {
					calls++
					return nil, grpcstatus.FromProto(received).Err()
				})
				if calls != 1 || result.Connected {
					t.Fatalf("failed RPC calls = %d, connected = %v", calls, result.Connected)
				}
				assertDatabaseRPCStableError(t, err, code)
			}
		})
	}
}

func TestDatabaseRPCStableCodesKeepWhitelistAndTransportPriority(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
		want ncpdatabase.ErrorCode
	}{
		{"unknown code", grpcstatus.Error(codes.InvalidArgument, "future_database_code"), ncpdatabase.CodeAgentUnavailable},
		{"raw auth detail", grpcstatus.Error(codes.Unauthenticated, "authentication failed: password=private"), ncpdatabase.CodeAgentUnavailable},
		{"code with private suffix", grpcstatus.Error(codes.InvalidArgument, "auth_failed: password=private"), ncpdatabase.CodeAgentUnavailable},
		{"conflict with private suffix", grpcstatus.Error(codes.Aborted, "credential_conflict: password=private"), ncpdatabase.CodeAgentUnavailable},
		{"conflict without stable code", grpcstatus.Error(codes.Aborted, "private conflict details"), ncpdatabase.CodeAgentUnavailable},
		{"private non-status error", errors.New("password=private; connection refused"), ncpdatabase.CodeAgentUnavailable},
		{"unimplemented method", grpcstatus.Error(codes.Unimplemented, "private method details"), ncpdatabase.CodeAgentUnavailable},
		{"whitespace around code", grpcstatus.Error(codes.InvalidArgument, "  auth_failed\n"), ncpdatabase.CodeAuthFailed},
		{"whitespace around conflict", grpcstatus.Error(codes.Aborted, "  credential_conflict\n"), ncpdatabase.CodeCredentialConflict},
		{"deadline before wire code", grpcstatus.Error(codes.DeadlineExceeded, "auth_failed"), ncpdatabase.CodeTimeout},
		{"canceled before wire code", grpcstatus.Error(codes.Canceled, "auth_failed"), ncpdatabase.CodeTimeout},
		{"budget before wire code", grpcstatus.Error(codes.ResourceExhausted, "auth_failed"), ncpdatabase.CodeResultTooLarge},
		{"deadline before conflict code", grpcstatus.Error(codes.DeadlineExceeded, "credential_conflict"), ncpdatabase.CodeTimeout},
		{"budget before conflict code", grpcstatus.Error(codes.ResourceExhausted, "credential_conflict"), ncpdatabase.CodeResultTooLarge},
		{"busy transport", grpcstatus.Error(codes.Unavailable, "operation_busy"), ncpdatabase.CodeOperationBusy},
		{"busy with private suffix", grpcstatus.Error(codes.Unavailable, "operation_busy: password=private"), ncpdatabase.CodeAgentUnavailable},
		{"deadline before busy code", grpcstatus.Error(codes.DeadlineExceeded, "operation_busy"), ncpdatabase.CodeTimeout},
		{"budget before busy code", grpcstatus.Error(codes.ResourceExhausted, "operation_busy"), ncpdatabase.CodeResultTooLarge},
		{"legacy receive budget", grpcstatus.Error(codes.ResourceExhausted, "grpc: received message larger than max (4194305 vs. 4194304)"), ncpdatabase.CodeResultTooLarge},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			assertDatabaseRPCStableError(t, databaseRPCError(testCase.err), testCase.want)
		})
	}
	if databaseRPCError(nil) != nil {
		t.Fatal("nil RPC error became a failure")
	}
}

func TestDatabaseRPCCredentialConflictDoesNotInventPersistenceOutcome(t *testing.T) {
	response, err := databaseResponse(ncpdatabase.ConnectionDiagnostic{
		Connected: true, Code: "credential_conflict", Operation: "manual_persist",
		Endpoint: "password=private",
	}, &ncpdatabase.DatabaseError{
		Code: ncpdatabase.CodeCredentialConflict, Operation: "manual_persist", Cause: errors.New("password=private"),
	})
	status := grpcstatus.Convert(err)
	if response != nil || status.Code() != codes.Aborted || status.Message() != "credential_conflict" {
		t.Fatalf("unexpected conflict encoding: response = %v, status = %v", response, status)
	}
	mapped := databaseRPCError(status.Err())
	assertDatabaseRPCStableError(t, mapped, ncpdatabase.CodeCredentialConflict)
	var typed *ncpdatabase.DatabaseError
	if !errors.As(mapped, &typed) || typed.Operation != "" || typed.Endpoint != "" || typed.Cause != nil {
		t.Fatalf("RPC conflict invented persistence evidence: %#v", typed)
	}
}

func TestDatabaseRPCContextSentinelsSurviveWithoutPrivateDetails(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		err   error
		cause error
	}{
		{"direct cancellation", context.Canceled, context.Canceled},
		{"wrapped cancellation", fmt.Errorf("password=private: %w", context.Canceled), context.Canceled},
		{"direct deadline", context.DeadlineExceeded, context.DeadlineExceeded},
		{"wrapped deadline", fmt.Errorf("SQL=private: %w", context.DeadlineExceeded), context.DeadlineExceeded},
		{"grpc cancellation", grpcstatus.Error(codes.Canceled, "password=private"), context.Canceled},
		{"grpc deadline", grpcstatus.Error(codes.DeadlineExceeded, "SQL=private"), context.DeadlineExceeded},
		{"cancellation before budget", errors.Join(grpcstatus.Error(codes.ResourceExhausted, "result_too_large"), context.Canceled), context.Canceled},
		{"deadline before wire code", errors.Join(grpcstatus.Error(codes.InvalidArgument, "auth_failed"), context.DeadlineExceeded), context.DeadlineExceeded},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := databaseRPCError(testCase.err)
			assertDatabaseRPCStableError(t, err, ncpdatabase.CodeTimeout)
			if !errors.Is(err, testCase.cause) {
				t.Fatalf("lost context sentinel %v", testCase.cause)
			}
		})
	}
}

func TestDatabaseRPCEarlyFailuresRemainTypedWithoutDialing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := TestDatabaseConnection(ctx, "", ncpdatabase.Connection{})
	assertDatabaseRPCStableError(t, err, ncpdatabase.CodeTimeout)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("early cancellation lost its context sentinel")
	}
	// 空目标在创建 gRPC 客户端之前被拒绝，不会尝试拨号。
	_, err = TestDatabaseConnection(context.Background(), "", ncpdatabase.Connection{})
	assertDatabaseRPCStableError(t, err, ncpdatabase.CodeAgentUnavailable)
}

// 直接调用编码边界，确保繁忙不会进入旧客户端的结果超限分支。
func TestDatabaseRPCBusyUsesUnavailableWithoutDetails(t *testing.T) {
	response, err := databaseResponse(ncpdatabase.QueryResult{Rows: [][]any{{"private result"}}}, &ncpdatabase.DatabaseError{
		Code: ncpdatabase.CodeOperationBusy, Operation: "private operation", Endpoint: "private endpoint", Cause: errors.New("password=private; SQL=private"),
	})
	status := grpcstatus.Convert(err)
	if response != nil || status.Code() != codes.Unavailable || status.Message() != "operation_busy" || len(status.Details()) != 0 {
		t.Fatalf("unexpected busy encoding: response = %v, status = %v", response, status)
	}
	assertDatabaseRPCStableError(t, databaseRPCError(err), ncpdatabase.CodeOperationBusy)
}
