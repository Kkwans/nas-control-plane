package docker

import (
	"context"
	"errors"
	"testing"
)

func TestImageManagerRemoveBatchRejectsInUseItemsAndContinues(t *testing.T) {
	gateway := &batchImageGateway{
		images: []ImageSummary{
			{ID: "sha256:used", Containers: 1},
			{ID: "sha256:free"},
			{ID: "sha256:failed"},
		},
		removeErrors: map[string]error{"sha256:failed": errors.New("engine rejected removal")},
	}
	result, err := NewImageManager(gateway).RemoveBatch(context.Background(), ImageRemoveBatchRequest{
		ImageIDs: []string{"sha256:used", "sha256:free", "sha256:failed"},
	})
	if err != nil {
		t.Fatalf("RemoveBatch() error = %v", err)
	}
	if result.Completed || result.RemovedCount != 1 || result.FailedCount != 2 || len(result.Items) != 3 {
		t.Fatalf("result = %#v", result)
	}
	if result.Items[0].ErrorCode != "DOCKER_IMAGE_IN_USE" || result.Items[0].Removed {
		t.Fatalf("in-use item = %#v", result.Items[0])
	}
	if !result.Items[1].Removed || result.Items[1].ErrorCode != "" {
		t.Fatalf("free item = %#v", result.Items[1])
	}
	if result.Items[2].ErrorCode != "DOCKER_IMAGE_REMOVE_FAILED" || result.Items[2].Removed {
		t.Fatalf("failed item = %#v", result.Items[2])
	}
	if len(gateway.removed) != 2 || gateway.removed[0] != "sha256:free" || gateway.removed[1] != "sha256:failed" {
		t.Fatalf("removed calls = %#v", gateway.removed)
	}
}

func TestImageManagerRemoveBatchEnforcesMaximumWithoutDockerMutation(t *testing.T) {
	ids := make([]string, maxImageRemoveBatchSize+1)
	for index := range ids {
		ids[index] = "sha256:" + string(rune('a'+index%26)) + string(rune('0'+index%10))
	}
	gateway := &batchImageGateway{}
	_, err := NewImageManager(gateway).RemoveBatch(context.Background(), ImageRemoveBatchRequest{ImageIDs: ids})
	if ErrorCode(err) != "DOCKER_IMAGE_REMOVE_BATCH_INVALID" {
		t.Fatalf("error code = %q", ErrorCode(err))
	}
	if gateway.listCalls != 0 || len(gateway.removed) != 0 {
		t.Fatalf("gateway mutated: list=%d removed=%#v", gateway.listCalls, gateway.removed)
	}
}

func TestImageManagerRemoveBatchPreservesContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewImageManager(&batchImageGateway{}).RemoveBatch(ctx, ImageRemoveBatchRequest{ImageIDs: []string{"sha256:free"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
}

type batchImageGateway struct {
	images       []ImageSummary
	removeErrors map[string]error
	removed      []string
	listCalls    int
}

func (gateway *batchImageGateway) ListImages(context.Context) ([]ImageSummary, error) {
	gateway.listCalls++
	return gateway.images, nil
}

func (gateway *batchImageGateway) SearchImages(context.Context, string, int) ([]HubRepository, error) {
	return nil, nil
}

func (gateway *batchImageGateway) PullImageReference(context.Context, string) error {
	return nil
}

func (gateway *batchImageGateway) RemoveImage(_ context.Context, id string) error {
	gateway.removed = append(gateway.removed, id)
	return gateway.removeErrors[id]
}
