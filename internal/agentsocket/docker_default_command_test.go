package agentsocket

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestContainerDefaultCommandPayloadPreservesOmission(t *testing.T) {
	for _, body := range []string{`{"image":"fixture:latest"}`, `{"image":"fixture:latest","command":null}`} {
		t.Run(body, func(t *testing.T) {
			var input docker.ContainerCreateRequest
			if err := json.Unmarshal([]byte(body), &input); err != nil {
				t.Fatal(err)
			}
			spec, err := input.Normalize()
			if err != nil {
				t.Fatal(err)
			}
			wire, err := containerCreatePayload(spec)
			if err != nil {
				t.Fatal(err)
			}
			if _, exists := wire.Fields["command"]; exists {
				t.Fatal("default command must remain omitted instead of an explicit empty argv")
			}
			decoded, err := decodeContainerCreateRequest(wire)
			if err != nil {
				t.Fatal(err)
			}
			if decoded.Command != nil {
				t.Fatalf("command = %#v", decoded.Command)
			}
			provider := &fakeDockerCreateProvider{}
			if _, err := newDockerControlService(provider).CreateContainer(context.Background(), wire); err != nil {
				t.Fatal(err)
			}
			if provider.calls != 1 || provider.request.Command != nil {
				t.Fatalf("calls=%d command=%#v", provider.calls, provider.request.Command)
			}
		})
	}
}

func TestContainerDefaultCommandRoundTripRetainsExplicitArgv(t *testing.T) {
	values := [][]string{{"/bin/tool", "--flag"}, {"   ", "$HOME; literal argument", " 中文 "}}
	for _, command := range values {
		spec, err := (docker.ContainerCreateRequest{Image: "fixture:latest", Command: command}).Normalize()
		if err != nil {
			t.Fatal(err)
		}
		wire, err := containerCreatePayload(spec)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := wire.Fields["command"]; !exists {
			t.Fatal("explicit command omitted")
		}
		provider := &fakeDockerCreateProvider{}
		if _, err := newDockerControlService(provider).CreateContainer(context.Background(), wire); err != nil {
			t.Fatal(err)
		}
		if provider.calls != 1 || !reflect.DeepEqual(provider.request.Command, command) {
			t.Fatalf("calls=%d command=%#v", provider.calls, provider.request.Command)
		}
		command[0] = "caller changed"
		if provider.request.Command[0] == "caller changed" {
			t.Fatal("provider command aliases caller")
		}
	}
}

func TestContainerDefaultCommandDoesNotPermitInvalidExplicitArgv(t *testing.T) {
	for _, command := range [][]string{{}, {""}, {"line\nbreak"}, {"carriage\rreturn"}, {"nul\x00byte"}} {
		if _, err := (docker.ContainerCreateRequest{Image: "fixture:latest", Command: command}).Normalize(); docker.ErrorCode(err) != "DOCKER_CONTAINER_CREATE_INVALID" {
			t.Fatalf("Normalize(%#v) = %v", command, err)
		}
		wire, err := structpb.NewStruct(map[string]any{"image": "fixture:latest", "command": stringSliceAny(command)})
		if err != nil {
			t.Fatal(err)
		}
		provider := &fakeDockerCreateProvider{}
		_, err = newDockerControlService(provider).CreateContainer(context.Background(), wire)
		if status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != "DOCKER_CONTAINER_CREATE_INVALID" || provider.calls != 0 {
			t.Fatalf("command=%#v calls=%d err=%v", command, provider.calls, err)
		}
	}
}
