package agentsocket

import (
	"context"

	"github.com/Kkwans/nas-control-plane/internal/filesystem"
	"google.golang.org/protobuf/types/known/structpb"
)

func ListPath(ctx context.Context, socketPath string, request filesystem.Request) (filesystem.Page, error) {
	normalized, err := request.Normalize()
	if err != nil {
		return filesystem.Page{}, err
	}
	connection, err := dialSocket(socketPath)
	if err != nil {
		return filesystem.Page{}, err
	}
	defer connection.Close()
	values := map[string]any{"path": normalized.Path}
	if normalized.Cursor != "" {
		values["cursor"] = normalized.Cursor
	}
	if normalized.Limit != 0 {
		values["limit"] = normalized.Limit
	}
	payload, err := structpb.NewStruct(values)
	if err != nil {
		return filesystem.Page{}, coded("AGENT_RPC_REQUEST_INVALID", err)
	}
	response, err := NewAgentFilesystemServiceClient(connection).ListPath(ctx, payload)
	if err != nil {
		return filesystem.Page{}, rpcError(err)
	}
	var result filesystem.Page
	if err := decodeDashboardResponse(response, &result); err != nil {
		return filesystem.Page{}, err
	}
	if result.Entries == nil {
		result.Entries = []filesystem.Entry{}
	}
	return result, nil
}
