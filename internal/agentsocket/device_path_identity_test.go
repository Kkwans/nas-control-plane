package agentsocket

import (
	"context"
	"testing"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

// 内存 codec 与 fake provider，不创建 listener 或访问 Docker。
func TestContainerDeviceCodecPreservesPathIdentity(t *testing.T) {
	for _, suffix := range []string{"", " ", "\t", "\u00a0", "\u3000"} {
		host, target := "/dev/selected"+suffix, "/dev/target"+suffix
		input := docker.ContainerCreateRequest{Image: "fixture:latest", Devices: []docker.ContainerDevice{{HostPath: host, ContainerPath: target}}}
		spec, err := input.Normalize()
		if err != nil {
			t.Fatal(err)
		}
		wire, err := containerCreatePayload(spec)
		if err != nil {
			t.Fatal(err)
		}
		provider := &fakeDockerCreateProvider{}
		if _, err := newDockerControlService(provider).CreateContainer(context.Background(), wire); err != nil {
			t.Fatal(err)
		}
		final, err := provider.request.Normalize()
		if err != nil || provider.calls != 1 || len(final.Devices) != 1 || final.Devices[0].HostPath != host || final.Devices[0].ContainerPath != target {
			t.Fatalf("result %#v, calls %d, error %v", final.Devices, provider.calls, err)
		}
	}
}

func TestContainerDeviceRPCRejectsInvalidRawPathsBeforeProvider(t *testing.T) {
	for _, invalid := range []string{" /dev/a", "\t/dev/a", "\u00a0/dev/a", "/dev/a\r", "/dev/a\n", "/dev/a\x00"} {
		for _, field := range []string{"host_path", "container_path"} {
			device := map[string]any{"host_path": "/dev/a", "container_path": "/dev/b"}
			device[field] = invalid
			wire, err := structpb.NewStruct(map[string]any{"image": "fixture:latest", "devices": []any{device}})
			if err != nil {
				t.Fatal(err)
			}
			provider := &fakeDockerCreateProvider{}
			_, err = newDockerControlService(provider).CreateContainer(context.Background(), wire)
			if status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != "DOCKER_CONTAINER_CREATE_INVALID" || provider.calls != 0 {
				t.Errorf("%s=%q: calls %d, error %v", field, invalid, provider.calls, err)
			}
		}
	}
}
