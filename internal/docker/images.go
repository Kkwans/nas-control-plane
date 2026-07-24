package docker

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/moby/moby/client"
)

const (
	imageOperationTimeout = 10 * time.Minute
	maxImageReferenceSize = 512
)

type ImageSummary struct {
	ID          string    `json:"id"`
	RepoTags    []string  `json:"repoTags"`
	RepoDigests []string  `json:"repoDigests"`
	SizeBytes   int64     `json:"sizeBytes"`
	CreatedAt   time.Time `json:"createdAt"`
	Containers  int64     `json:"containers"`
}

type ImageInventory struct {
	CollectedAt time.Time      `json:"collectedAt"`
	Images      []ImageSummary `json:"images"`
}

type ImagePullRequest struct {
	Reference string `json:"reference"`
}

type ImagePullResult struct {
	Reference string `json:"reference"`
	Completed bool   `json:"completed"`
}

type ImageRemoveRequest struct {
	ImageID string `json:"imageId"`
}

type ImageRemoveResult struct {
	ImageID string `json:"imageId"`
	Removed bool   `json:"removed"`
}

type ImageGateway interface {
	ListImages(context.Context) ([]ImageSummary, error)
	PullImageReference(context.Context, string) error
	RemoveImage(context.Context, string) error
}

type ImageManager struct {
	gateway ImageGateway
	now     func() time.Time
	timeout time.Duration
}

func NewImageManager(gateway ImageGateway) *ImageManager {
	if gateway == nil {
		gateway = unavailableImageGateway{}
	}
	return &ImageManager{
		gateway: gateway,
		now:     time.Now,
		timeout: imageOperationTimeout,
	}
}

func NewLiveImageManager() (*ImageManager, error) {
	apiClient, err := client.New(client.WithHost(localDockerHost), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return NewImageManager(&mobyImageGateway{client: apiClient}), nil
}

func (m *ImageManager) List(ctx context.Context) (ImageInventory, error) {
	if err := ctx.Err(); err != nil {
		return ImageInventory{}, err
	}
	images, err := m.gateway.ListImages(ctx)
	if err != nil {
		return ImageInventory{}, coded("DOCKER_IMAGE_LIST_FAILED", err)
	}
	sort.Slice(images, func(left, right int) bool {
		leftName := imageDisplayName(images[left])
		rightName := imageDisplayName(images[right])
		if leftName != rightName {
			return leftName < rightName
		}
		return images[left].CreatedAt.After(images[right].CreatedAt)
	})
	return ImageInventory{CollectedAt: m.now().UTC(), Images: images}, nil
}

func (m *ImageManager) Pull(ctx context.Context, request ImagePullRequest) (ImagePullResult, error) {
	reference, err := normalizeImageReference(request.Reference)
	if err != nil {
		return ImagePullResult{}, err
	}
	operationContext, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	if err := m.gateway.PullImageReference(operationContext, reference); err != nil {
		return ImagePullResult{}, coded("DOCKER_IMAGE_PULL_FAILED", err)
	}
	return ImagePullResult{Reference: reference, Completed: true}, nil
}

func (m *ImageManager) Remove(ctx context.Context, request ImageRemoveRequest) (ImageRemoveResult, error) {
	imageID := strings.TrimSpace(request.ImageID)
	if imageID == "" || len(imageID) > maxImageReferenceSize {
		return ImageRemoveResult{}, coded("DOCKER_IMAGE_REMOVE_INVALID", errors.New("image id is invalid"))
	}
	operationContext, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	if err := m.gateway.RemoveImage(operationContext, imageID); err != nil {
		return ImageRemoveResult{}, coded("DOCKER_IMAGE_REMOVE_FAILED", err)
	}
	return ImageRemoveResult{ImageID: imageID, Removed: true}, nil
}

func normalizeImageReference(value string) (string, error) {
	reference := strings.TrimSpace(value)
	if reference == "" || len(reference) > maxImageReferenceSize || strings.ContainsAny(reference, " \t\r\n") {
		return "", coded("DOCKER_IMAGE_PULL_INVALID", errors.New("image reference is invalid"))
	}
	lastSlash := strings.LastIndex(reference, "/")
	hasTag := strings.LastIndex(reference, ":") > lastSlash
	hasDigest := strings.Contains(reference, "@sha256:")
	if !hasTag && !hasDigest {
		return "", coded("DOCKER_IMAGE_PULL_INVALID", errors.New("image reference must include a tag or digest"))
	}
	return reference, nil
}

func imageDisplayName(image ImageSummary) string {
	if len(image.RepoTags) > 0 {
		return strings.ToLower(image.RepoTags[0])
	}
	return image.ID
}

type mobyImageGateway struct {
	client *client.Client
}

func (g *mobyImageGateway) ListImages(ctx context.Context) ([]ImageSummary, error) {
	response, err := g.client.ImageList(ctx, client.ImageListOptions{All: false})
	if err != nil {
		return nil, err
	}
	result := make([]ImageSummary, 0, len(response.Items))
	for _, item := range response.Items {
		result = append(result, ImageSummary{
			ID:          item.ID,
			RepoTags:    append([]string(nil), item.RepoTags...),
			RepoDigests: append([]string(nil), item.RepoDigests...),
			SizeBytes:   item.Size,
			CreatedAt:   time.Unix(item.Created, 0).UTC(),
			Containers:  item.Containers,
		})
	}
	return result, nil
}

func (g *mobyImageGateway) PullImageReference(ctx context.Context, reference string) error {
	response, err := g.client.ImagePull(ctx, reference, client.ImagePullOptions{})
	if err != nil {
		return err
	}
	defer response.Close()
	return response.Wait(ctx)
}

func (g *mobyImageGateway) RemoveImage(ctx context.Context, imageID string) error {
	_, err := g.client.ImageRemove(ctx, imageID, client.ImageRemoveOptions{
		Force:         false,
		PruneChildren: false,
	})
	return err
}

type unavailableImageGateway struct{}

func (unavailableImageGateway) ListImages(context.Context) ([]ImageSummary, error) {
	return nil, errors.New("image gateway is not configured")
}

func (unavailableImageGateway) PullImageReference(context.Context, string) error {
	return errors.New("image gateway is not configured")
}

func (unavailableImageGateway) RemoveImage(context.Context, string) error {
	return errors.New("image gateway is not configured")
}
