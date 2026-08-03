package docker

import (
	"context"
	"errors"
	"strings"
)

const MaxImageRemoveBatchSize = 50

const maxImageRemoveBatchSize = MaxImageRemoveBatchSize

type ImageRemoveBatchRequest struct {
	ImageIDs []string `json:"imageIds"`
}

func (r ImageRemoveBatchRequest) Normalize() (ImageRemoveBatchRequest, error) {
	if len(r.ImageIDs) == 0 || len(r.ImageIDs) > maxImageRemoveBatchSize {
		return ImageRemoveBatchRequest{}, coded("DOCKER_IMAGE_REMOVE_BATCH_INVALID", errors.New("image batch size is outside the supported range"))
	}
	seen := make(map[string]struct{}, len(r.ImageIDs))
	for index, value := range r.ImageIDs {
		imageID := strings.TrimSpace(value)
		if imageID == "" || len(imageID) > maxImageReferenceSize {
			return ImageRemoveBatchRequest{}, coded("DOCKER_IMAGE_REMOVE_BATCH_INVALID", errors.New("image id is invalid"))
		}
		if _, exists := seen[imageID]; exists {
			return ImageRemoveBatchRequest{}, coded("DOCKER_IMAGE_REMOVE_BATCH_INVALID", errors.New("image batch contains duplicate ids"))
		}
		seen[imageID] = struct{}{}
		r.ImageIDs[index] = imageID
	}
	return r, nil
}

func (r ImageRemoveBatchRequest) Validate() error {
	_, err := r.Normalize()
	return err
}

type ImageRemoveBatchItem struct {
	ImageID   string `json:"imageId"`
	Removed   bool   `json:"removed"`
	ErrorCode string `json:"errorCode,omitempty"`
}

type ImageRemoveBatchResult struct {
	Items        []ImageRemoveBatchItem `json:"items"`
	RemovedCount int                    `json:"removedCount"`
	FailedCount  int                    `json:"failedCount"`
	Completed    bool                   `json:"completed"`
}

// RemoveBatch performs a non-force removal for each image. The inventory is
// read before mutating anything so images referenced by containers are
// rejected explicitly and do not depend on Docker's error text. A race after
// this check remains safe because the gateway always uses Force=false.
func (m *ImageManager) RemoveBatch(ctx context.Context, request ImageRemoveBatchRequest) (ImageRemoveBatchResult, error) {
	request, err := request.Normalize()
	if err != nil {
		return ImageRemoveBatchResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ImageRemoveBatchResult{}, err
	}

	operationContext, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	images, err := m.gateway.ListImages(operationContext)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ImageRemoveBatchResult{}, err
		}
		return ImageRemoveBatchResult{}, coded("DOCKER_IMAGE_REMOVE_BATCH_LIST_FAILED", err)
	}

	result := ImageRemoveBatchResult{Items: make([]ImageRemoveBatchItem, 0, len(request.ImageIDs))}
	for _, imageID := range request.ImageIDs {
		if err := operationContext.Err(); err != nil {
			return result, err
		}
		item := ImageRemoveBatchItem{ImageID: imageID}
		if image, found := findImageForRemoval(images, imageID); found && image.Containers > 0 {
			item.ErrorCode = "DOCKER_IMAGE_IN_USE"
			result.FailedCount++
			result.Items = append(result.Items, item)
			continue
		}

		if err := m.gateway.RemoveImage(operationContext, imageID); err != nil {
			if operationContext.Err() != nil {
				return result, operationContext.Err()
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return result, err
			}
			item.ErrorCode = ErrorCode(err)
			if item.ErrorCode == "" {
				item.ErrorCode = "DOCKER_IMAGE_REMOVE_FAILED"
			}
			result.FailedCount++
			result.Items = append(result.Items, item)
			continue
		}
		item.Removed = true
		result.RemovedCount++
		result.Items = append(result.Items, item)
	}
	result.Completed = result.FailedCount == 0 && len(result.Items) == len(request.ImageIDs)
	return result, nil
}

func findImageForRemoval(images []ImageSummary, requestedID string) (ImageSummary, bool) {
	requestedID = strings.TrimSpace(requestedID)
	var match ImageSummary
	found := false
	for _, image := range images {
		imageID := strings.TrimSpace(image.ID)
		if imageID == "" {
			continue
		}
		if imageID == requestedID {
			return image, true
		}
		// Docker accepts short IDs. Only use a prefix when it identifies one
		// image; an ambiguous prefix must not accidentally block another image.
		if strings.HasPrefix(imageID, requestedID) {
			if found {
				return ImageSummary{}, false
			}
			match, found = image, true
		}
	}
	return match, found
}
