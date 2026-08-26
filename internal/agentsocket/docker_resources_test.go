package agentsocket

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/emptypb"
	grpcstatus "google.golang.org/grpc/status"
)

func TestDockerResourcesServiceReturnsProviderData(t *testing.T) {
	service := newDockerResourcesService(fakeDockerResourcesProvider{result: docker.Resources{
		CollectedAt: time.Unix(10, 0).UTC(),
		Networks:    []docker.Network{{Name: "bridge", Driver: "bridge", Subnets: []string{"172.17.0.0/16"}, Gateways: []string{"172.17.0.1"}}},
		Volumes:     []docker.Volume{{Name: "app-data", Driver: "local", Scope: "local"}},
	}})
	response, err := service.ListResources(context.Background(), &emptypb.Empty{})
	if err != nil {
		t.Fatalf("ListResources() error = %v", err)
	}
	if response.AsMap()["collectedAt"] == nil || len(response.AsMap()["networks"].([]any)) != 1 || len(response.AsMap()["volumes"].([]any)) != 1 {
		t.Fatalf("response = %#v", response.AsMap())
	}
}

func TestDockerResourcesServiceMapsProviderFailure(t *testing.T) {
	service := newDockerResourcesService(fakeDockerResourcesProvider{err: errors.New("engine unavailable")})
	_, err := service.ListResources(context.Background(), &emptypb.Empty{})
	if grpcstatus.Code(err) != codes.Unavailable || grpcstatus.Convert(err).Message() != "DOCKER_RESOURCES_UNAVAILABLE" {
		t.Fatalf("error = %v", err)
	}
}

type fakeDockerResourcesProvider struct {
	result docker.Resources
	err    error
}

func (f fakeDockerResourcesProvider) ListResources(context.Context) (docker.Resources, error) {
	return f.result, f.err
}
