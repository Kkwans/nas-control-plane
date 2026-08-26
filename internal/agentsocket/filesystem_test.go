package agentsocket

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	"github.com/Kkwans/nas-control-plane/internal/filesystem"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestAgentFilesystemServiceListsPathOverGRPC(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterAgentFilesystemServiceServer(server, newFilesystemService(filesystem.NewBrowser()))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })

	connection, err := grpc.DialContext(context.Background(), "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	path := t.TempDir()
	request, err := structpb.NewStruct(map[string]any{"path": path, "limit": float64(10)})
	if err != nil {
		t.Fatal(err)
	}
	response, err := NewAgentFilesystemServiceClient(connection).ListPath(context.Background(), request)
	if err != nil {
		t.Fatalf("ListPath() error = %v", err)
	}
	if response.AsMap()["path"] != path || response.AsMap()["parent"] != filepath.Dir(path) {
		t.Fatalf("response = %#v", response.AsMap())
	}
}

func TestFilesystemServiceRejectsRelativePath(t *testing.T) {
	service := newFilesystemService(filesystem.NewBrowser())
	request, err := structpb.NewStruct(map[string]any{"path": "relative"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ListPath(context.Background(), request)
	if err == nil || err.Error() != "rpc error: code = InvalidArgument desc = FILES_PATH_INVALID" {
		t.Fatalf("error = %v", err)
	}
}
