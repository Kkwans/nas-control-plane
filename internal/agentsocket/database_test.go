package agentsocket

import (
	"context"
	"errors"
	"strings"
	"testing"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

func TestDatabaseResponseSendsStableCodeWithoutDriverMessage(t *testing.T) {
	source := ncpdatabase.Source{ID: "pg-1", Driver: ncpdatabase.DriverPostgreSQL, Host: "db.internal", Port: 5432, DefaultDatabase: "control"}
	_, err := databaseResponse(struct{}{}, &ncpdatabase.DatabaseError{
		Code: ncpdatabase.CodeAuthFailed, Driver: source.Driver, Endpoint: "db.internal:5432/control",
		Operation: "test_connection", Cause: errors.New("password=super-secret"),
	})
	if err == nil {
		t.Fatal("expected gRPC status error")
	}
	status := grpcstatus.Convert(err)
	if status.Code() != codes.InvalidArgument || status.Message() != string(ncpdatabase.CodeAuthFailed) {
		t.Fatalf("status = %s: %s", status.Code(), status.Message())
	}
	if strings.Contains(status.Message(), "super-secret") {
		t.Fatal("driver message leaked through RPC status")
	}
}

func TestDatabaseRPCErrorMapsAgentAndTimeoutSeparately(t *testing.T) {
	if got := ErrorCode(databaseRPCError(grpcstatus.Error(codes.Unavailable, "transport closed"))); got != string(ncpdatabase.CodeAgentUnavailable) {
		t.Fatalf("agent error code = %q", got)
	}
	if got := ErrorCode(databaseRPCError(grpcstatus.Error(codes.DeadlineExceeded, "deadline"))); got != string(ncpdatabase.CodeTimeout) {
		t.Fatalf("timeout error code = %q", got)
	}
	if got := ErrorCode(databaseRPCError(grpcstatus.Error(codes.InvalidArgument, string(ncpdatabase.CodePermissionDenied)))); got != string(ncpdatabase.CodePermissionDenied) {
		t.Fatalf("database error code = %q", got)
	}
	if got := ErrorCode(databaseRPCError(context.Canceled)); got != string(ncpdatabase.CodeTimeout) {
		t.Fatalf("canceled error code = %q", got)
	}
}
