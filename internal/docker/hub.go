package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const dockerHubAPIBase = "https://hub.docker.com/v2"

type hubTagsWire struct {
	Count   int64 `json:"count"`
	Results []struct {
		Name        string `json:"name"`
		LastUpdated string `json:"last_updated"`
		FullSize    int64  `json:"full_size"`
		Images      []struct {
			Architecture string `json:"architecture"`
			OS           string `json:"os"`
			Variant      string `json:"variant"`
		} `json:"images"`
	} `json:"results"`
}

func NewLiveImageManagerWithProxy(proxyAddress string) (*ImageManager, error) {
	apiClient, err := newMobyClient()
	if err != nil {
		return nil, err
	}
	manager := NewImageManager(&mobyImageGateway{client: apiClient})
	httpClient, err := hubHTTPClient(proxyAddress)
	if err != nil {
		return nil, err
	}
	manager.hubHTTP = httpClient
	return manager, nil
}

func (m *ImageManager) Tags(ctx context.Context, request HubTagsRequest) (HubTagsResult, error) {
	namespace := strings.TrimSpace(request.Namespace)
	repository := strings.TrimSpace(request.Repository)
	if !safeHubSegment(namespace) || !safeHubSegment(repository) || request.Page < 1 || request.PageSize < 1 || request.PageSize > 100 {
		return HubTagsResult{}, coded("DOCKER_HUB_REPOSITORY_INVALID", errors.New("repository request is invalid"))
	}
	endpoint := fmt.Sprintf(
		"%s/namespaces/%s/repositories/%s/tags?page=%d&page_size=%d&ordering=last_updated",
		dockerHubAPIBase, url.PathEscape(namespace), url.PathEscape(repository), request.Page, request.PageSize,
	)
	outbound, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return HubTagsResult{}, coded("DOCKER_HUB_TAGS_FAILED", err)
	}
	outbound.Header.Set("Accept", "application/json")
	outbound.Header.Set("User-Agent", "NAS-Control-Plane/1")
	result, err := m.hubClient().Do(outbound)
	if err != nil {
		return HubTagsResult{}, coded("DOCKER_HUB_TAGS_FAILED", err)
	}
	defer result.Body.Close()
	if result.StatusCode != http.StatusOK {
		return HubTagsResult{}, coded("DOCKER_HUB_TAGS_FAILED", fmt.Errorf("docker hub returned %s", result.Status))
	}
	var payload hubTagsWire
	if err := json.NewDecoder(result.Body).Decode(&payload); err != nil {
		return HubTagsResult{}, coded("DOCKER_HUB_TAGS_FAILED", err)
	}
	items := make([]HubTag, 0, len(payload.Results))
	for _, item := range payload.Results {
		seen := make(map[string]struct{})
		architectures := make([]string, 0, len(item.Images))
		for _, image := range item.Images {
			architecture := strings.Trim(strings.Join([]string{image.OS, image.Architecture, image.Variant}, "/"), "/")
			if architecture == "" {
				continue
			}
			if _, exists := seen[architecture]; exists {
				continue
			}
			seen[architecture] = struct{}{}
			architectures = append(architectures, architecture)
		}
		sort.Strings(architectures)
		items = append(items, HubTag{
			Name: item.Name, LastUpdated: item.LastUpdated, FullSize: item.FullSize, Architectures: architectures,
		})
	}
	return HubTagsResult{Count: payload.Count, Page: request.Page, PageSize: request.PageSize, Results: items}, nil
}

func hubHTTPClient(proxyAddress string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if strings.TrimSpace(proxyAddress) != "" {
		proxyURL, err := url.Parse(proxyAddress)
		if err != nil || proxyURL.Scheme == "" || proxyURL.Host == "" {
			return nil, errors.New("outbound proxy is invalid")
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return &http.Client{Transport: transport, Timeout: 15 * time.Second}, nil
}

func (m *ImageManager) hubClient() *http.Client {
	if m.hubHTTP != nil {
		return m.hubHTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func safeHubSegment(value string) bool {
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, "/\\ \t\r\n?#")
}
