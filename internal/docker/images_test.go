package docker

import (
	"context"
	"encoding/json"
	"testing"
)

type imageGatewayStub struct {
	images []ImageSummary
}

func (stub imageGatewayStub) ListImages(context.Context) ([]ImageSummary, error) {
	return stub.images, nil
}

func (imageGatewayStub) PullImageReference(context.Context, string) error {
	return nil
}

func (imageGatewayStub) RemoveImage(context.Context, string) error {
	return nil
}

func TestImageManagerListNormalizesEmptyCollections(t *testing.T) {
	manager := NewImageManager(imageGatewayStub{
		images: []ImageSummary{{ID: "sha256:dangling"}},
	})

	inventory, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("list images: %v", err)
	}

	payload, err := json.Marshal(inventory)
	if err != nil {
		t.Fatalf("marshal image inventory: %v", err)
	}
	const want = `"repoTags":[]`
	if !containsJSON(payload, want) {
		t.Fatalf("expected repoTags to be an array, got %s", payload)
	}
	if !containsJSON(payload, `"repoDigests":[]`) {
		t.Fatalf("expected repoDigests to be an array, got %s", payload)
	}
}

func TestImageManagerListNormalizesNilInventory(t *testing.T) {
	manager := NewImageManager(imageGatewayStub{})

	inventory, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("list images: %v", err)
	}
	if inventory.Images == nil {
		t.Fatal("expected an empty image collection, got nil")
	}
}

func containsJSON(payload []byte, fragment string) bool {
	for index := 0; index+len(fragment) <= len(payload); index++ {
		if string(payload[index:index+len(fragment)]) == fragment {
			return true
		}
	}
	return false
}
