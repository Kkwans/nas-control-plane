package agentsocket

import (
	"math"
	"reflect"
	"testing"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestDockerImagesRemoveReceiptValid(t *testing.T) {
	for _, test := range []struct {
		name            string
		items           []any
		removed, failed int
		completed       bool
	}{
		{"all removed", []any{map[string]any{"imageId": "repo:first", "removed": true}, map[string]any{"imageId": "short-id", "removed": true, "errorCode": ""}}, 2, 0, true},
		{"reordered mixed", []any{map[string]any{"imageId": "short-id", "removed": false, "errorCode": "DOCKER_IMAGE_IN_USE"}, map[string]any{"imageId": "repo:first", "removed": true}}, 1, 1, false},
		{"all failed", []any{map[string]any{"imageId": "repo:first", "removed": false, "errorCode": "DOCKER_IMAGE_REMOVE_FAILED"}, map[string]any{"imageId": "short-id", "removed": false, "errorCode": "FUTURE_CODE"}}, 0, 2, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := structpb.NewStruct(map[string]any{"items": test.items, "removedCount": test.removed, "failedCount": test.failed, "completed": test.completed})
			if err != nil {
				t.Fatal(err)
			}
			result, err := decodeDockerImagesRemoveResponse(response, []string{"repo:first", "short-id"})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Items) != 2 || result.RemovedCount != test.removed || result.FailedCount != test.failed || result.Completed != test.completed {
				t.Fatalf("result = %#v", result)
			}
			for i, raw := range test.items {
				item := raw.(map[string]any)
				if result.Items[i].ImageID != item["imageId"] || result.Items[i].Removed != item["removed"] {
					t.Fatalf("item = %#v", result.Items[i])
				}
			}
		})
	}
}

func TestDockerImagesRemoveReceiptRejectsInvalid(t *testing.T) {
	tests := []struct {
		name   string
		change func(map[string]any)
	}{
		{"wrong ID", func(m map[string]any) { m["items"].([]any)[0].(map[string]any)["imageId"] = "other" }},
		{"duplicate ID", func(m map[string]any) { m["items"].([]any)[1].(map[string]any)["imageId"] = "first" }},
		{"untrimmed ID", func(m map[string]any) { m["items"].([]any)[0].(map[string]any)["imageId"] = " first " }},
		{"missing item", func(m map[string]any) {
			m["items"] = m["items"].([]any)[:1]
			m["failedCount"] = 0
			m["completed"] = true
		}},
		{"extra item", func(m map[string]any) {
			m["items"] = append(m["items"].([]any), map[string]any{"imageId": "third", "removed": true})
			m["removedCount"] = 2
		}},
		{"null item", func(m map[string]any) { m["items"].([]any)[0] = nil }},
		{"nonobject item", func(m map[string]any) { m["items"].([]any)[0] = "first" }},
		{"swapped counts", func(m map[string]any) { m["removedCount"] = 2; m["failedCount"] = 0 }},
		{"negative count", func(m map[string]any) { m["removedCount"] = -1; m["failedCount"] = 3 }},
		{"fractional count", func(m map[string]any) { m["removedCount"] = 0.5; m["failedCount"] = 1.5 }},
		{"large count", func(m map[string]any) { m["removedCount"] = math.MaxFloat64 }},
		{"NaN count", func(m map[string]any) { m["removedCount"] = math.NaN() }},
		{"infinite count", func(m map[string]any) { m["removedCount"] = math.Inf(1) }},
		{"mixed completed", func(m map[string]any) { m["completed"] = true }},
		{"successful incomplete", func(m map[string]any) {
			m["items"].([]any)[1] = map[string]any{"imageId": "second", "removed": true}
			m["removedCount"] = 2
			m["failedCount"] = 0
		}},
		{"removed with error", func(m map[string]any) { m["items"].([]any)[0].(map[string]any)["errorCode"] = "ERROR" }},
		{"failure missing error", func(m map[string]any) { delete(m["items"].([]any)[1].(map[string]any), "errorCode") }},
		{"failure blank error", func(m map[string]any) { m["items"].([]any)[1].(map[string]any)["errorCode"] = " \t\n" }},
	}
	for _, key := range []string{"items", "removedCount", "failedCount", "completed"} {
		for _, replacement := range []any{nil, "wrong type"} {
			tests = append(tests, struct {
				name   string
				change func(map[string]any)
			}{"invalid " + key, func(m map[string]any) { m[key] = replacement }})
		}
		tests = append(tests, struct {
			name   string
			change func(map[string]any)
		}{"missing " + key, func(m map[string]any) { delete(m, key) }})
	}
	for _, key := range []string{"imageId", "removed", "errorCode"} {
		for _, replacement := range []any{nil, 42} {
			tests = append(tests, struct {
				name   string
				change func(map[string]any)
			}{"invalid item " + key, func(m map[string]any) { m["items"].([]any)[0].(map[string]any)[key] = replacement }})
		}
	}
	for _, key := range []string{"imageId", "removed"} {
		tests = append(tests, struct {
			name   string
			change func(map[string]any)
		}{"missing item " + key, func(m map[string]any) { delete(m["items"].([]any)[1].(map[string]any), key) }})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wire := map[string]any{"items": []any{map[string]any{"imageId": "first", "removed": true}, map[string]any{"imageId": "second", "removed": false, "errorCode": "DOCKER_IMAGE_IN_USE"}}, "removedCount": 1, "failedCount": 1, "completed": false}
			test.change(wire)
			response, err := structpb.NewStruct(wire)
			if err != nil {
				t.Fatal(err)
			}
			result, err := decodeDockerImagesRemoveResponse(response, []string{"first", "second"})
			if ErrorCode(err) != "AGENT_RPC_RESPONSE_INVALID" || !reflect.DeepEqual(result, docker.ImageRemoveBatchResult{}) {
				t.Fatalf("result = %#v, error = %v", result, err)
			}
		})
	}
	result, err := decodeDockerImagesRemoveResponse(nil, []string{"first"})
	if ErrorCode(err) != "AGENT_RPC_RESPONSE_INVALID" || !reflect.DeepEqual(result, docker.ImageRemoveBatchResult{}) {
		t.Fatalf("nil response: %#v, %v", result, err)
	}
}

func TestDockerImagesRequestNormalizationOwnsSnapshot(t *testing.T) {
	for _, test := range []struct {
		name  string
		ids   []string
		valid bool
	}{
		{"success preserves reference compatibility", []string{" repo:tag ", " short-id "}, true},
		{"duplicate after trim", []string{" first ", "first"}, false},
		{"invalid after valid", []string{" first ", " "}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := append([]string(nil), test.ids...)
			result, err := normalizeDockerImagesRequest(docker.ImageRemoveBatchRequest{ImageIDs: test.ids})
			if (err == nil) != test.valid {
				t.Fatalf("error = %v", err)
			}
			if !reflect.DeepEqual(test.ids, original) {
				t.Fatalf("caller slice changed: %#v", test.ids)
			}
			if test.valid {
				if !reflect.DeepEqual(result.ImageIDs, []string{"repo:tag", "short-id"}) {
					t.Fatalf("normalized = %#v", result.ImageIDs)
				}
				result.ImageIDs[0] = "changed"
				if !reflect.DeepEqual(test.ids, original) {
					t.Fatal("normalized slice aliases caller")
				}
			}
		})
	}
}
