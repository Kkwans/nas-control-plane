package agentsocket

import (
	"context"
	"errors"
	"testing"

	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

type mutationReceiptRPCProvider struct {
	DatabaseProvider
	calls  int
	result ncpdatabase.MutationResult
	err    error
}

func (p *mutationReceiptRPCProvider) Insert(context.Context, ncpdatabase.InsertRequest) (ncpdatabase.MutationResult, error) {
	p.calls++
	return p.result, p.err
}
func (p *mutationReceiptRPCProvider) Update(context.Context, ncpdatabase.UpdateRequest) (ncpdatabase.MutationResult, error) {
	p.calls++
	return p.result, p.err
}
func (p *mutationReceiptRPCProvider) Delete(context.Context, ncpdatabase.DeleteRequest) (ncpdatabase.MutationResult, error) {
	p.calls++
	return p.result, p.err
}

// 直接调用 service 和客户端编码回调，不创建监听器或拨号。
func TestDatabaseMutationReceiptRPC(t *testing.T) {
	for _, op := range []struct {
		name string
		call func(*databaseService, context.Context, *structpb.Struct) (*structpb.Struct, error)
	}{
		{"insert", (*databaseService).Insert}, {"update", (*databaseService).Update}, {"delete", (*databaseService).Delete},
	} {
		t.Run(op.name, func(t *testing.T) {
			for _, invalid := range []bool{false, true} {
				provider := &mutationReceiptRPCProvider{}
				if invalid {
					provider.err = &ncpdatabase.DatabaseError{Code: ncpdatabase.CodeMutationReceiptInvalid, Cause: errors.New("password=private; SQL=private")}
				}
				service := newDatabaseService(provider)
				var result ncpdatabase.MutationResult
				calls := 0
				err := callDatabasePayload(map[string]any{"sourceId": "fixture", "table": "items"}, &result, func(request *structpb.Struct) (*structpb.Struct, error) {
					calls++
					response, err := op.call(service, context.Background(), request)
					if invalid && (response != nil || grpcstatus.Code(err) != codes.Internal || grpcstatus.Convert(err).Message() != string(ncpdatabase.CodeMutationReceiptInvalid)) {
						t.Fatalf("RPC status=%v response=%v", err, response)
					}
					return response, err
				})
				if calls != 1 || provider.calls != 1 {
					t.Fatalf("client/provider calls=%d/%d", calls, provider.calls)
				}
				if invalid {
					assertDatabaseRPCStableError(t, err, ncpdatabase.CodeMutationReceiptInvalid)
				} else if err != nil || result.RowsAffected != 0 {
					t.Fatalf("valid zero=%#v err=%v", result, err)
				}
			}
		})
	}
}
