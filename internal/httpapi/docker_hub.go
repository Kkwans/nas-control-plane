package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/docker"
)

const dockerHubRequestTimeout = 20 * time.Second

func (api *handler) searchDockerHub(response http.ResponseWriter, request *http.Request) {
	query := strings.TrimSpace(request.URL.Query().Get("query"))
	if query == "" || len(query) > 120 {
		api.writeError(response, request, http.StatusBadRequest, "DOCKER_HUB_QUERY_INVALID", "请输入要搜索的 Docker 镜像关键字。")
		return
	}
	page := boundedQueryInt(request, "page", 1, 1, 1000)
	pageSize := boundedQueryInt(request, "pageSize", 20, 1, 50)
	requestContext, cancel := context.WithTimeout(request.Context(), dockerHubRequestTimeout)
	defer cancel()
	result, err := api.dockerImages.SearchDockerHub(requestContext, api.agentSocketPath, docker.HubSearchRequest{
		Query: query, Page: page, PageSize: pageSize,
	})
	if err != nil {
		api.writeError(response, request, http.StatusBadGateway, "DOCKER_HUB_SEARCH_FAILED", "Docker Hub 搜索暂不可用，请检查 Docker Engine 的仓库连接。")
		return
	}
	writeJSON(response, http.StatusOK, result)
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
	requestContext, cancel := context.WithTimeout(request.Context(), dockerHubRequestTimeout)
	defer cancel()
	result, err := api.dockerImages.ListDockerHubTags(requestContext, api.agentSocketPath, docker.HubTagsRequest{
		Namespace: namespace, Repository: repository, Page: page, PageSize: pageSize,
	})
	if err != nil {
		api.writeError(response, request, http.StatusBadGateway, "DOCKER_HUB_TAGS_FAILED", "Docker Hub 标签读取失败，请检查 NAS 的代理服务。")
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func boundedQueryInt(request *http.Request, key string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(request.URL.Query().Get(key))
	if err != nil || value < minimum || value > maximum {
		return fallback
	}
	return value
}

func safeDockerHubSegment(value string) bool {
	return value != "." && value != ".." && !strings.ContainsAny(value, "/\\ \t\r\n?#")
}
