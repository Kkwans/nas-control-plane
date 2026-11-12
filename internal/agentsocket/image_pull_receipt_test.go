package agentsocket

import (
	"context"
	"io"
	"testing"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/protobuf/types/known/structpb"
)

type fakeImagePullReceiver struct {
	messages []*structpb.Struct
	err      error
	calls    int
}

func (f *fakeImagePullReceiver) Recv() (*structpb.Struct, error) {
	f.calls++
	if len(f.messages) == 0 {
		if f.err != nil {
			return nil, f.err
		}
		return nil, io.EOF
	}
	value := f.messages[0]
	f.messages = f.messages[1:]
	return value, nil
}
func pullWire(t *testing.T, fields map[string]any) *structpb.Struct {
	t.Helper()
	value, err := structpb.NewStruct(fields)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func TestImagePullReceiptRPCBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields map[string]any
		valid  bool
	}{
		{"valid", map[string]any{"reference": "alpine:latest", "completed": true}, true},
		{"wrong target", map[string]any{"reference": "nginx:latest", "completed": true}, false},
		{"blank", map[string]any{"reference": " \t", "completed": true}, false},
		{"missing reference", map[string]any{"completed": true}, false},
		{"wrong reference type", map[string]any{"reference": 42, "completed": true}, false},
		{"incomplete", map[string]any{"reference": "alpine:latest", "completed": false}, false},
		{"missing completed", map[string]any{"reference": "alpine:latest"}, false},
		{"wrong completed type", map[string]any{"reference": "alpine:latest", "completed": "true"}, false},
		{"null completed", map[string]any{"reference": "alpine:latest", "completed": nil}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := docker.ImagePullRequest{Reference: " alpine:latest "}
			check := func(result docker.ImagePullResult, err error) {
				t.Helper()
				if tc.valid {
					if err != nil || !result.Completed || result.Reference != "alpine:latest" {
						t.Fatalf("%+v %v", result, err)
					}
				} else if ErrorCode(err) != "AGENT_RPC_RESPONSE_INVALID" || result != (docker.ImagePullResult{}) {
					t.Fatalf("invalid receipt returned: %+v %v", result, err)
				}
			}
			check(decodeImagePullResponse(pullWire(t, tc.fields), request))
			tc.fields["type"] = "completed"
			stream := &fakeImagePullReceiver{messages: []*structpb.Struct{pullWire(t, map[string]any{"type": "progress", "layerId": "layer", "status": "Downloading", "current": 3, "total": 9}), pullWire(t, tc.fields)}}
			progressCalls := 0
			check(receiveImagePullStream(stream, request, func(p docker.ImagePullProgress) {
				progressCalls++
				if p.LayerID != "layer" || p.Current != 3 || p.Total != 9 {
					t.Fatalf("progress lost: %+v", p)
				}
			}))
			if progressCalls != 1 || stream.calls != 2 {
				t.Fatalf("calls: progress=%d receive=%d", progressCalls, stream.calls)
			}
		})
	}
	result, err := decodeImagePullResponse(nil, docker.ImagePullRequest{Reference: "alpine:latest"})
	if ErrorCode(err) != "AGENT_RPC_RESPONSE_INVALID" || result != (docker.ImagePullResult{}) {
		t.Fatalf("nil: %+v %v", result, err)
	}
}
func TestImagePullReceiptStreamEnd(t *testing.T) {
	for _, err := range []error{io.EOF, context.Canceled, context.DeadlineExceeded} {
		stream := &fakeImagePullReceiver{err: err}
		result, got := receiveImagePullStream(stream, docker.ImagePullRequest{Reference: "alpine:latest"}, nil)
		expected := "AGENT_RPC_RESPONSE_INVALID"
		if err != io.EOF {
			expected = ErrorCode(rpcError(err))
		}
		if ErrorCode(got) != expected || result != (docker.ImagePullResult{}) || stream.calls != 1 {
			t.Fatalf("end %v: %+v %v", err, result, got)
		}
	}
}

func TestImagePullReceiptStreamMissingCompletion(t *testing.T) {
	stream := &fakeImagePullReceiver{messages: []*structpb.Struct{nil}}
	result, err := receiveImagePullStream(stream, docker.ImagePullRequest{Reference: "alpine:latest"}, nil)
	if ErrorCode(err) != "AGENT_RPC_RESPONSE_INVALID" || result != (docker.ImagePullResult{}) || stream.calls != 2 {
		t.Fatalf("nil message falsely completed: %+v %v calls=%d", result, err, stream.calls)
	}
}
