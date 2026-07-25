package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const dockerHubAPIBase = "https://hub.docker.com/v2"

type dockerHubRepository struct {
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

type dockerHubSearchResponse struct {
	Count    int64                 `json:"count"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"pageSize"`
	Results  []dockerHubRepository `json:"results"`
}

type dockerHubTag struct {
	Name          string   `json:"name"`
	LastUpdated   string   `json:"lastUpdated"`
	FullSize      int64    `json:"fullSize"`
	Architectures []string `json:"architectures"`
}

type dockerHubTagsResponse struct {
	Count    int64          `json:"count"`
	Page     int            `json:"page"`
	PageSize int            `json:"pageSize"`
	Results  []dockerHubTag `json:"results"`
}

type dockerHubSearchWire struct {
	Count   int64 `json:"count"`
	Results []struct {
		RepoName         string `json:"repo_name"`
		ShortDescription string `json:"short_description"`
		StarCount        int64  `json:"star_count"`
		PullCount        int64  `json:"pull_count"`
		IsOfficial       bool   `json:"is_official"`
	} `json:"results"`
}

type dockerHubTagsWire struct {
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

func (api *handler) searchDockerHub(response http.ResponseWriter, request *http.Request) {
	query := strings.TrimSpace(request.URL.Query().Get("query"))
	if query == "" || len(query) > 120 {
		api.writeError(response, request, http.StatusBadRequest, "DOCKER_HUB_QUERY_INVALID", "请输入要搜索的 Docker 镜像关键字。")
		return
	}
	page := boundedQueryInt(request, "page", 1, 1, 1000)
	pageSize := boundedQueryInt(request, "pageSize", 20, 1, 50)
	endpoint := fmt.Sprintf("%s/search/repositories/?query=%s&page=%d&page_size=%d", dockerHubAPIBase, url.QueryEscape(query), page, pageSize)

	var payload dockerHubSearchWire
	if err := fetchDockerHubJSON(request, endpoint, &payload); err != nil {
		api.writeError(response, request, http.StatusBadGateway, "DOCKER_HUB_SEARCH_FAILED", "Docker Hub 搜索暂不可用，请稍后重试。")
		return
	}
	items := make([]dockerHubRepository, 0, len(payload.Results))
	for _, item := range payload.Results {
		namespace, name := splitDockerHubRepository(item.RepoName)
		items = append(items, dockerHubRepository{
			Name: name, Namespace: namespace, Description: strings.TrimSpace(item.ShortDescription),
			StarCount: item.StarCount, PullCount: item.PullCount, Official: item.IsOfficial,
			Publisher: namespace, RepositoryType: "image", StatusDescription: "active",
		})
	}
	writeJSON(response, http.StatusOK, dockerHubSearchResponse{Count: payload.Count, Page: page, PageSize: pageSize, Results: items})
}

func (api *handler) dockerHubTags(response http.ResponseWriter, request *http.Request) {
	namespace := strings.TrimSpace(request.URL.Query().Get("namespace"))
	repository := strings.TrimSpace(request.URL.Query().Get("repository"))
	if namespace == "" || repository == "" || !safeDockerHubSegment(namespace) || !safeDockerHubSegment(repository) {
		api.writeError(response, request, http.StatusBadRequest, "DOCKER_HUB_REPOSITORY_INVALID", "Docker Hub 仓库名称无效。")
		return
	}
	page := boundedQueryInt(request, "page", 1, 1, 1000)
	pageSize := boundedQueryInt(request, "pageSize", 25, 1, 100)
	endpoint := fmt.Sprintf("%s/namespaces/%s/repositories/%s/tags?page=%d&page_size=%d&ordering=last_updated", dockerHubAPIBase, url.PathEscape(namespace), url.PathEscape(repository), page, pageSize)

	var payload dockerHubTagsWire
	if err := fetchDockerHubJSON(request, endpoint, &payload); err != nil {
		api.writeError(response, request, http.StatusBadGateway, "DOCKER_HUB_TAGS_FAILED", "Docker Hub 标签读取失败，请稍后重试。")
		return
	}
	items := make([]dockerHubTag, 0, len(payload.Results))
	for _, item := range payload.Results {
		architectures := make([]string, 0, len(item.Images))
		seen := make(map[string]struct{})
		for _, image := range item.Images {
			architecture := strings.Trim(strings.Join([]string{image.OS, image.Architecture, image.Variant}, "/"), "/")
			if _, exists := seen[architecture]; architecture == "" || exists {
				continue
			}
			seen[architecture] = struct{}{}
			architectures = append(architectures, architecture)
		}
		items = append(items, dockerHubTag{Name: item.Name, LastUpdated: item.LastUpdated, FullSize: item.FullSize, Architectures: architectures})
	}
	writeJSON(response, http.StatusOK, dockerHubTagsResponse{Count: payload.Count, Page: page, PageSize: pageSize, Results: items})
}

func fetchDockerHubJSON(request *http.Request, endpoint string, destination any) error {
	ctx := request.Context()
	outbound, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	outbound.Header.Set("Accept", "application/json")
	outbound.Header.Set("User-Agent", "NAS-Control-Plane/1")
	client := &http.Client{Timeout: 12 * time.Second}
	result, err := client.Do(outbound)
	if err != nil {
		return err
	}
	defer result.Body.Close()
	if result.StatusCode != http.StatusOK {
		return fmt.Errorf("docker hub returned %s", result.Status)
	}
	return json.NewDecoder(result.Body).Decode(destination)
}

func boundedQueryInt(request *http.Request, key string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(request.URL.Query().Get(key))
	if err != nil || value < minimum || value > maximum {
		return fallback
	}
	return value
}

func splitDockerHubRepository(value string) (string, string) {
	parts := strings.SplitN(strings.Trim(value, "/"), "/", 2)
	if len(parts) == 1 {
		return "library", parts[0]
	}
	return parts[0], parts[1]
}

func safeDockerHubSegment(value string) bool {
	return value != "." && value != ".." && !strings.ContainsAny(value, "/\\ \t\r\n?#")
}
