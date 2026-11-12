package agentsocket

import (
	"context"
	"testing"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/Kkwans/nas-control-plane/internal/filesystem"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestFilesystemCodecPreservesPathWhitespace(t *testing.T) {
	for _, suffix := range []string{"", " ", "\t", "\u00a0", "\u3000"} {
		path := "/selected" + suffix
		request, err := (filesystem.Request{Path: path}).Normalize()
		if err != nil {
			t.Fatal(err)
		}
		wire, err := structpb.NewStruct(map[string]any{"path": request.Path})
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeFilesystemRequest(wire)
		if err != nil || decoded.Path != path {
			t.Fatalf("decoded = %#v, err = %v", decoded, err)
		}
		provider := &pathIdentityFilesystemProvider{}
		response, err := newFilesystemService(provider).ListPath(context.Background(), wire)
		if err != nil {
			t.Fatal(err)
		}
		if provider.calls != 1 || provider.request.Path != path {
			t.Fatalf("calls = %d, request = %#v", provider.calls, provider.request)
		}
		var page filesystem.Page
		if err := decodeDashboardResponse(response, &page); err != nil || page.Path != path || len(page.Entries) != 1 || page.Entries[0].Name != "child " || page.Entries[0].Path != path+"/child " {
			t.Fatalf("page = %#v, err = %v", page, err)
		}
	}
}

func TestContainerCreateCodecPreservesMountPathWhitespace(t *testing.T) {
	for _, suffix := range []string{"", " ", "\t", "\u00a0", "\u3000"} {
		source, target := "/selected"+suffix, "/data"+suffix
		request := docker.ContainerCreateRequest{
			Image: "alpine:latest", Command: []string{"/bin/true"},
			Mounts: []docker.ContainerMount{
				{Type: "bind", Source: source, Target: target},
				{Type: " VOLUME ", Source: " app-data ", Target: "/volume" + suffix},
				{Type: " tmpfs ", Source: " \t", Target: "/tmpfs" + suffix},
			},
		}
		spec, err := request.Normalize()
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
		decoded := provider.request
		if provider.calls != 1 || len(decoded.Mounts) != 3 || decoded.Mounts[0].Source != source || decoded.Mounts[0].Target != target || decoded.Mounts[1].Type != "volume" || decoded.Mounts[1].Source != "app-data" || decoded.Mounts[1].Target != "/volume"+suffix || decoded.Mounts[2].Type != "tmpfs" || decoded.Mounts[2].Source != "" || decoded.Mounts[2].Target != "/tmpfs"+suffix {
			t.Fatalf("calls = %d, decoded = %#v", provider.calls, decoded.Mounts)
		}
		final, err := decoded.Normalize()
		if err != nil || final.Mounts[0].Source != source || final.Mounts[0].Target != target {
			t.Fatalf("final = %#v, err = %v", final.Mounts, err)
		}
	}
}

func TestFilesystemPathIdentityRejectsLeadingWhitespaceBeforeProvider(t *testing.T) {
	for _, prefix := range []string{" ", "\t", "\u00a0", "\u3000"} {
		wire, err := structpb.NewStruct(map[string]any{"path": prefix + "/selected"})
		if err != nil {
			t.Fatal(err)
		}
		provider := &pathIdentityFilesystemProvider{}
		_, err = newFilesystemService(provider).ListPath(context.Background(), wire)
		if status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != "FILES_PATH_INVALID" || provider.calls != 0 {
			t.Errorf("prefix = %q, calls = %d, err = %v", prefix, provider.calls, err)
		}
	}
}

func TestContainerMountPathIdentityRejectsLeadingWhitespaceBeforeProvider(t *testing.T) {
	for _, prefix := range []string{" ", "\t", "\u00a0", "\u3000"} {
		for _, value := range []docker.ContainerMount{
			{Type: "bind", Source: prefix + "/source", Target: "/data"},
			{Type: "bind", Source: "/source", Target: prefix + "/data"},
			{Type: "volume", Source: "app-data", Target: prefix + "/data"},
			{Type: "tmpfs", Target: prefix + "/data"},
		} {
			wire, err := structpb.NewStruct(map[string]any{
				"image":   "alpine:latest",
				"command": []any{"/bin/true"},
				"mounts":  []any{map[string]any{"type": value.Type, "source": value.Source, "target": value.Target}},
			})
			if err != nil {
				t.Fatal(err)
			}
			provider := &fakeDockerCreateProvider{}
			_, err = newDockerControlService(provider).CreateContainer(context.Background(), wire)
			if status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != "DOCKER_CONTAINER_CREATE_INVALID" || provider.calls != 0 {
				t.Errorf("mount = %#v, calls = %d, err = %v", value, provider.calls, err)
			}
		}
	}
}

type pathIdentityFilesystemProvider struct {
	request filesystem.Request
	calls   int
}

func (p *pathIdentityFilesystemProvider) List(_ context.Context, request filesystem.Request) (filesystem.Page, error) {
	p.calls++
	p.request = request
	return filesystem.Page{Path: request.Path, Entries: []filesystem.Entry{{Name: "child ", Path: request.Path + "/child "}}}, nil
}
