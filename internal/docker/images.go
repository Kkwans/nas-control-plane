package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
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

type HubRepository struct {
	Name              string `json:"name"`
	Namespace         string `json:"namespace"`
	Description       string `json:"description"`
	StarCount         int64  `json:"starCount"`
	PullCount         int64  `json:"pullCount"`
	Official          bool   `json:"official"`
	Publisher         string `json:"publisher"`
	LastUpdated       string `json:"lastUpdated"`
	RepositoryType    string `json:"repositoryType"`
	StatusDescription string `json:"statusDescription"`
}

type HubSearchRequest struct {
	Query    string `json:"query"`
	Page     int    `json:"page"`
	PageSize int    `json:"pageSize"`
	Sort     string `json:"sort"`
}

type HubSearchResult struct {
	Count    int64           `json:"count"`
	Page     int             `json:"page"`
	PageSize int             `json:"pageSize"`
	Results  []HubRepository `json:"results"`
}

type HubTagsRequest struct {
	Namespace  string `json:"namespace"`
	Repository string `json:"repository"`
	Page       int    `json:"page"`
	PageSize   int    `json:"pageSize"`
}

type HubTag struct {
	Name          string   `json:"name"`
	LastUpdated   string   `json:"lastUpdated"`
	FullSize      int64    `json:"fullSize"`
	Architectures []string `json:"architectures"`
}

type HubTagsResult struct {
	Count    int64    `json:"count"`
	Page     int      `json:"page"`
	PageSize int      `json:"pageSize"`
	Results  []HubTag `json:"results"`
}

type ImageGateway interface {
	ListImages(context.Context) ([]ImageSummary, error)
	SearchImages(context.Context, string, int) ([]HubRepository, error)
	PullImageReference(context.Context, string) error
	RemoveImage(context.Context, string) error
}

type ImagePullProgress struct {
	LayerID string `json:"layerId"`
	Status  string `json:"status"`
	Current int64  `json:"current"`
	Total   int64  `json:"total"`
}

type progressImageGateway interface {
	PullImageReferenceWithProgress(context.Context, string, func(ImagePullProgress)) error
}

type ImageManager struct {
	gateway            ImageGateway
	hubHTTP            *http.Client
	hubRepositoryMu    sync.Mutex
	hubRepositoryCache map[string]hubRepositoryCacheEntry
	now                func() time.Time
	timeout            time.Duration
}

func NewImageManager(gateway ImageGateway) *ImageManager {
	if gateway == nil {
		gateway = unavailableImageGateway{}
	}
	return &ImageManager{
		gateway:            gateway,
		hubRepositoryCache: make(map[string]hubRepositoryCacheEntry),
		now:                time.Now,
		timeout:            imageOperationTimeout,
	}
}

func NewLiveImageManager() (*ImageManager, error) {
	return NewLiveImageManagerWithProxy("")
}

func newMobyClient() (*client.Client, error) {
	apiClient, err := client.New(client.WithHost(localDockerHost), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return apiClient, nil
}

func (m *ImageManager) List(ctx context.Context) (ImageInventory, error) {
	if err := ctx.Err(); err != nil {
		return ImageInventory{}, err
	}
	images, err := m.gateway.ListImages(ctx)
	if err != nil {
		return ImageInventory{}, coded("DOCKER_IMAGE_LIST_FAILED", err)
	}
	if images == nil {
		images = make([]ImageSummary, 0)
	}
	for index := range images {
		if images[index].RepoTags == nil {
			images[index].RepoTags = make([]string, 0)
		}
		if images[index].RepoDigests == nil {
			images[index].RepoDigests = make([]string, 0)
		}
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

func (m *ImageManager) Search(ctx context.Context, request HubSearchRequest) (HubSearchResult, error) {
	query := strings.TrimSpace(request.Query)
	if request.Sort == "" {
		request.Sort = "relevance"
	}
	if query == "" || len(query) > 120 || request.Page < 1 || request.PageSize < 1 || request.PageSize > 50 || !validHubSearchSort(request.Sort) {
		return HubSearchResult{}, coded("DOCKER_HUB_QUERY_INVALID", errors.New("image search request is invalid"))
	}
	limit := request.Page * request.PageSize
	if limit > 100 {
		limit = 100
	}
	results, err := m.gateway.SearchImages(ctx, query, limit)
	if err != nil {
		return HubSearchResult{}, coded("DOCKER_HUB_SEARCH_FAILED", err)
	}
	if results == nil {
		results = make([]HubRepository, 0)
	}
	start := (request.Page - 1) * request.PageSize
	if start >= len(results) {
		return HubSearchResult{Count: int64(len(results)), Page: request.Page, PageSize: request.PageSize, Results: []HubRepository{}}, nil
	}
	end := start + request.PageSize
	if end > len(results) {
		end = len(results)
	}
	pageResults := append([]HubRepository(nil), results[start:end]...)
	m.enrichHubRepositories(ctx, pageResults)
	sortHubRepositories(pageResults, request.Sort)
	return HubSearchResult{
		Count: int64(len(results)), Page: request.Page, PageSize: request.PageSize,
		Results: pageResults,
	}, nil
}

func validHubSearchSort(value string) bool {
	switch value {
	case "relevance", "stars", "pulls", "updated":
		return true
	default:
		return false
	}
}

func sortHubRepositories(repositories []HubRepository, order string) {
	if order == "relevance" {
		return
	}
	sort.SliceStable(repositories, func(left, right int) bool {
		switch order {
		case "stars":
			return repositories[left].StarCount > repositories[right].StarCount
		case "pulls":
			return repositories[left].PullCount > repositories[right].PullCount
		case "updated":
			return repositories[left].LastUpdated > repositories[right].LastUpdated
		default:
			return false
		}
	})
}

func (m *ImageManager) Pull(ctx context.Context, request ImagePullRequest) (ImagePullResult, error) {
	return m.PullWithProgress(ctx, request, nil)
}

func (m *ImageManager) PullWithProgress(ctx context.Context, request ImagePullRequest, onProgress func(ImagePullProgress)) (ImagePullResult, error) {
	reference, err := normalizeImageReference(request.Reference)
	if err != nil {
		return ImagePullResult{}, err
	}
	operationContext, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	var pullError error
	if gateway, ok := m.gateway.(progressImageGateway); ok && onProgress != nil {
		pullError = gateway.PullImageReferenceWithProgress(operationContext, reference, onProgress)
	} else {
		pullError = m.gateway.PullImageReference(operationContext, reference)
	}
	if pullError != nil {
		return ImagePullResult{}, coded("DOCKER_IMAGE_PULL_FAILED", pullError)
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
	client     *client.Client
	httpClient *http.Client
}

func (g *mobyImageGateway) ListImages(ctx context.Context) ([]ImageSummary, error) {
	httpClient := g.httpClient
	if httpClient == nil {
		httpClient = newDockerUnixHTTPClient()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/images/json?all=0", nil)
	if err != nil {
		return nil, err
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker image list returned HTTP %d", response.StatusCode)
	}
	var items []struct {
		ID          string   `json:"Id"`
		RepoTags    []string `json:"RepoTags"`
		RepoDigests []string `json:"RepoDigests"`
		Size        int64    `json:"Size"`
		Created     int64    `json:"Created"`
		Containers  int64    `json:"Containers"`
	}
	if err := json.NewDecoder(response.Body).Decode(&items); err != nil {
		return nil, err
	}
	result := make([]ImageSummary, 0, len(items))
	for _, item := range items {
		result = append(result, ImageSummary{
			ID:          item.ID,
			RepoTags:    append([]string{}, item.RepoTags...),
			RepoDigests: append([]string{}, item.RepoDigests...),
			SizeBytes:   item.Size,
			CreatedAt:   time.Unix(item.Created, 0).UTC(),
			Containers:  item.Containers,
		})
	}
	return result, nil
}

func newDockerUnixHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/docker.sock")
			},
		},
	}
}

func (g *mobyImageGateway) SearchImages(ctx context.Context, query string, limit int) ([]HubRepository, error) {
	response, err := g.client.ImageSearch(ctx, query, client.ImageSearchOptions{Limit: limit})
	if err != nil {
		return nil, err
	}
	results := make([]HubRepository, 0, len(response.Items))
	for _, item := range response.Items {
		namespace, name := splitRepositoryName(item.Name)
		results = append(results, HubRepository{
			Name: name, Namespace: namespace, Description: strings.TrimSpace(item.Description),
			StarCount: int64(item.StarCount), Official: item.IsOfficial, Publisher: namespace,
			RepositoryType: "image", StatusDescription: "active",
		})
	}
	return results, nil
}

func (g *mobyImageGateway) PullImageReference(ctx context.Context, reference string) error {
	response, err := g.client.ImagePull(ctx, reference, client.ImagePullOptions{})
	if err != nil {
		return err
	}
	defer response.Close()
	return response.Wait(ctx)
}

func (g *mobyImageGateway) PullImageReferenceWithProgress(ctx context.Context, reference string, onProgress func(ImagePullProgress)) error {
	response, err := g.client.ImagePull(ctx, reference, client.ImagePullOptions{})
	if err != nil {
		return err
	}
	for message, streamError := range response.JSONMessages(ctx) {
		if streamError != nil {
			return streamError
		}
		if message.Error != nil {
			return message.Error
		}
		progress := ImagePullProgress{LayerID: message.ID, Status: message.Status}
		if message.Progress != nil {
			progress.Current = message.Progress.Current
			progress.Total = message.Progress.Total
		}
		onProgress(progress)
	}
	return nil
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

func (unavailableImageGateway) SearchImages(context.Context, string, int) ([]HubRepository, error) {
	return nil, errors.New("image gateway is not configured")
}

func (unavailableImageGateway) PullImageReference(context.Context, string) error {
	return errors.New("image gateway is not configured")
}

func (unavailableImageGateway) RemoveImage(context.Context, string) error {
	return errors.New("image gateway is not configured")
}

func splitRepositoryName(value string) (string, string) {
	parts := strings.SplitN(strings.Trim(value, "/"), "/", 2)
	if len(parts) == 1 {
		return "library", parts[0]
	}
	return parts[0], parts[1]
}
