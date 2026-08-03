package agentsocket

import (
	"context"
	"net"
	"testing"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestAgentDockerImagesServiceRemovesBatchWithItemResults(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	provider := &batchDockerImageProvider{result: docker.ImageRemoveBatchResult{
		Items:       []docker.ImageRemoveBatchItem{{ImageID: "sha256:used", ErrorCode: "DOCKER_IMAGE_IN_USE"}},
		FailedCount: 1,
	}}
	RegisterAgentDockerImagesServiceServer(server, newDockerImageService(provider))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	connection, err := grpc.DialContext(context.Background(), "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	request, err := structpb.NewStruct(map[string]any{"image_ids": []any{"sha256:used"}})
	if err != nil {
		t.Fatalf("NewStruct() error = %v", err)
	}
	response, err := NewAgentDockerImagesServiceClient(connection).RemoveImages(context.Background(), request)
	if err != nil {
		t.Fatalf("RemoveImages() error = %v", err)
	}
	if response.AsMap()["failedCount"] != float64(1) || provider.request.ImageIDs[0] != "sha256:used" {
		t.Fatalf("response=%#v request=%#v", response.AsMap(), provider.request)
	}
}

type batchDockerImageProvider struct {
	request docker.ImageRemoveBatchRequest
	result  docker.ImageRemoveBatchResult
}

func (provider *batchDockerImageProvider) List(context.Context) (docker.ImageInventory, error) {
	return docker.ImageInventory{}, nil
}

func (provider *batchDockerImageProvider) Search(context.Context, docker.HubSearchRequest) (docker.HubSearchResult, error) {
	return docker.HubSearchResult{}, nil
}

func (provider *batchDockerImageProvider) Tags(context.Context, docker.HubTagsRequest) (docker.HubTagsResult, error) {
	return docker.HubTagsResult{}, nil
}

func (provider *batchDockerImageProvider) Pull(context.Context, docker.ImagePullRequest) (docker.ImagePullResult, error) {
	return docker.ImagePullResult{}, nil
}

func (provider *batchDockerImageProvider) Remove(context.Context, docker.ImageRemoveRequest) (docker.ImageRemoveResult, error) {
	return docker.ImageRemoveResult{}, nil
}

func (provider *batchDockerImageProvider) RemoveBatch(_ context.Context, request docker.ImageRemoveBatchRequest) (docker.ImageRemoveBatchResult, error) {
	provider.request = request
	return provider.result, nil
}
