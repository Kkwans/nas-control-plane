package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/Kkwans/nas-control-plane/internal/system"
	"github.com/go-chi/chi/v5"
)

const (
	defaultAgentTimeout       = 5 * time.Second
	defaultTerminalPOCTimeout = 5 * time.Minute
	defaultRealtimeInterval   = 5 * time.Second
	defaultDatabaseTimeout    = 20 * time.Second
)

type AgentClient interface {
	Probe(context.Context, string) (agentsocket.AgentStatus, error)
	CollectCapabilities(context.Context, string) (system.Capabilities, error)
	CollectSystemSummary(context.Context, string) (system.Summary, error)
	CollectDockerInventory(context.Context, string) (docker.Inventory, error)
	ControlContainer(context.Context, string, docker.ContainerActionRequest) (docker.ContainerActionResult, error)
	ReadContainerLogs(context.Context, string, docker.ContainerLogsRequest) (docker.ContainerLogsResult, error)
}

type DatabaseAgentClient interface {
	DiscoverDatabases(context.Context, string) (ncpdatabase.Discovery, error)
	CatalogDatabase(context.Context, string, ncpdatabase.CatalogRequest) (ncpdatabase.Catalog, error)
	QueryDatabase(context.Context, string, ncpdatabase.QueryRequest) (ncpdatabase.QueryResult, error)
	ReadDatabaseRows(context.Context, string, ncpdatabase.RowsRequest) (ncpdatabase.RowsResult, error)
	InsertDatabaseRow(context.Context, string, ncpdatabase.InsertRequest) (ncpdatabase.MutationResult, error)
	UpdateDatabaseRow(context.Context, string, ncpdatabase.UpdateRequest) (ncpdatabase.MutationResult, error)
	DeleteDatabaseRow(context.Context, string, ncpdatabase.DeleteRequest) (ncpdatabase.MutationResult, error)
}

type ControlStore interface {
	Preferences(context.Context, int64) (controlstore.Preferences, error)
	UpdatePreferences(context.Context, int64, controlstore.Preferences) (controlstore.Preferences, error)
	DatabaseProjectPreferences(context.Context) ([]controlstore.DatabaseProjectPreference, error)
	SetDatabaseProjectPreference(context.Context, controlstore.DatabaseProjectPreference) (controlstore.DatabaseProjectPreference, error)
	SiteProfiles(context.Context) ([]controlstore.SiteProfile, error)
	UpsertSiteProfile(context.Context, controlstore.SiteProfile) (controlstore.SiteProfile, error)
}

type Config struct {
	Agent               AgentClient
	DatabaseAgent       DatabaseAgentClient
	AgentSocketPath     string
	AgentTimeout        time.Duration
	Auth                Authenticator
	ControlStore        ControlStore
	SessionCookieSecure bool
	Terminal            TerminalClient
	TerminalPOCEnabled  bool
	TerminalTimeout     time.Duration
	RequestID           func() string
}

type HealthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

type ErrorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId"`
}

type ServiceListResponse struct {
	CollectedAt time.Time        `json:"collectedAt"`
	Services    []docker.Project `json:"services"`
}

type handler struct {
	agent               AgentClient
	databaseAgent       DatabaseAgentClient
	agentSocketPath     string
	agentTimeout        time.Duration
	auth                Authenticator
	controlStore        ControlStore
	sessionCookieSecure bool
	terminal            TerminalClient
	terminalEnabled     bool
	terminalTimeout     time.Duration
	newRequestID        func() string
}

type socketAgentClient struct{}

func (socketAgentClient) Probe(ctx context.Context, socketPath string) (agentsocket.AgentStatus, error) {
	return agentsocket.Probe(ctx, socketPath)
}

func (socketAgentClient) CollectCapabilities(ctx context.Context, socketPath string) (system.Capabilities, error) {
	return agentsocket.CollectCapabilities(ctx, socketPath)
}

func (socketAgentClient) CollectSystemSummary(ctx context.Context, socketPath string) (system.Summary, error) {
	return agentsocket.CollectSystemSummary(ctx, socketPath)
}

func (socketAgentClient) CollectDockerInventory(ctx context.Context, socketPath string) (docker.Inventory, error) {
	return agentsocket.CollectDockerInventory(ctx, socketPath)
}

func (socketAgentClient) ControlContainer(ctx context.Context, socketPath string, request docker.ContainerActionRequest) (docker.ContainerActionResult, error) {
	return agentsocket.ControlContainer(ctx, socketPath, request)
}

func (socketAgentClient) ReadContainerLogs(ctx context.Context, socketPath string, request docker.ContainerLogsRequest) (docker.ContainerLogsResult, error) {
	return agentsocket.ReadContainerLogs(ctx, socketPath, request)
}

func (socketAgentClient) DiscoverDatabases(ctx context.Context, socketPath string) (ncpdatabase.Discovery, error) {
	return agentsocket.DiscoverDatabases(ctx, socketPath)
}

func (socketAgentClient) CatalogDatabase(ctx context.Context, socketPath string, request ncpdatabase.CatalogRequest) (ncpdatabase.Catalog, error) {
	return agentsocket.CatalogDatabase(ctx, socketPath, request)
}

func (socketAgentClient) QueryDatabase(ctx context.Context, socketPath string, request ncpdatabase.QueryRequest) (ncpdatabase.QueryResult, error) {
	return agentsocket.QueryDatabase(ctx, socketPath, request)
}

func (socketAgentClient) ReadDatabaseRows(ctx context.Context, socketPath string, request ncpdatabase.RowsRequest) (ncpdatabase.RowsResult, error) {
	return agentsocket.ReadDatabaseRows(ctx, socketPath, request)
}

func (socketAgentClient) InsertDatabaseRow(ctx context.Context, socketPath string, request ncpdatabase.InsertRequest) (ncpdatabase.MutationResult, error) {
	return agentsocket.InsertDatabaseRow(ctx, socketPath, request)
}

func (socketAgentClient) UpdateDatabaseRow(ctx context.Context, socketPath string, request ncpdatabase.UpdateRequest) (ncpdatabase.MutationResult, error) {
	return agentsocket.UpdateDatabaseRow(ctx, socketPath, request)
}

func (socketAgentClient) DeleteDatabaseRow(ctx context.Context, socketPath string, request ncpdatabase.DeleteRequest) (ncpdatabase.MutationResult, error) {
	return agentsocket.DeleteDatabaseRow(ctx, socketPath, request)
}

func NewHandler(config Config) http.Handler {
	if config.Agent == nil {
		config.Agent = socketAgentClient{}
	}
	if config.DatabaseAgent == nil {
		config.DatabaseAgent = socketAgentClient{}
	}
	if config.AgentSocketPath == "" {
		config.AgentSocketPath = agentsocket.DefaultSocketPath
	}
	if config.AgentTimeout <= 0 {
		config.AgentTimeout = defaultAgentTimeout
	}
	if config.TerminalPOCEnabled && config.Terminal == nil {
		config.Terminal = socketTerminalClient{}
	}
	if config.TerminalTimeout <= 0 {
		config.TerminalTimeout = defaultTerminalPOCTimeout
	}
	if config.RequestID == nil {
		config.RequestID = defaultRequestID
	}

	api := &handler{
		agent:               config.Agent,
		databaseAgent:       config.DatabaseAgent,
		agentSocketPath:     config.AgentSocketPath,
		agentTimeout:        config.AgentTimeout,
		auth:                config.Auth,
		controlStore:        config.ControlStore,
		sessionCookieSecure: config.SessionCookieSecure,
		terminal:            config.Terminal,
		terminalEnabled:     config.TerminalPOCEnabled,
		terminalTimeout:     config.TerminalTimeout,
		newRequestID:        config.RequestID,
	}
	router := chi.NewRouter()
	router.Use(api.withRequestID)
	router.Get("/healthz", api.healthz)
	router.Route("/api/v1", func(routes chi.Router) {
		routes.Get("/auth/status", api.authStatus)
		routes.Post("/auth/bootstrap", api.bootstrap)
		routes.Post("/auth/login", api.login)
		routes.Post("/auth/logout", api.logout)
		routes.Group(func(protected chi.Router) {
			protected.Use(api.requireAuthentication)
			protected.Get("/system/capabilities", api.capabilities)
			protected.Get("/system/agent-status", api.agentStatus)
			protected.Get("/system/summary", api.systemSummary)
			protected.Get("/system/events", api.systemEvents)
			protected.Get("/preferences", api.preferences)
			protected.Put("/preferences", api.updatePreferences)
			protected.Get("/docker/inventory", api.dockerInventory)
			protected.Post("/docker/containers/{containerID}/actions/{action}", api.containerAction)
			protected.Get("/docker/containers/{containerID}/logs", api.containerLogs)
			protected.Get("/services", api.services)
			protected.Get("/sites", api.sites)
			protected.Put("/sites/{projectID}", api.updateSite)
			protected.Get("/databases/discovery", api.databaseDiscovery)
			protected.Get("/databases/project-preferences", api.databaseProjectPreferences)
			protected.Put("/databases/project-preferences", api.updateDatabaseProjectPreference)
			protected.Post("/databases/catalog", api.databaseCatalog)
			protected.Post("/databases/query", api.databaseQuery)
			protected.Post("/databases/rows", api.databaseRows)
			protected.Post("/databases/rows/insert", api.databaseInsert)
			protected.Post("/databases/rows/update", api.databaseUpdate)
			protected.Post("/databases/rows/delete", api.databaseDelete)
		})
	})
	if api.terminalEnabled {
		router.With(api.requireAuthentication).Get("/ws/terminal", api.terminalWebSocket)
	}
	router.NotFound(func(response http.ResponseWriter, request *http.Request) {
		api.writeError(response, request, http.StatusNotFound, "ROUTE_NOT_FOUND", "请求的资源不存在。")
	})
	router.MethodNotAllowed(func(response http.ResponseWriter, request *http.Request) {
		api.writeError(response, request, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "请求方法不受支持。")
	})
	return router
}

func (api *handler) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestID := api.newRequestID()
		if requestID == "" {
			requestID = defaultRequestID()
		}
		response.Header().Set("X-Request-ID", requestID)
		contextWithRequestID := context.WithValue(request.Context(), requestIDContextKey{}, requestID)
		next.ServeHTTP(response, request.WithContext(contextWithRequestID))
	})
}

func (api *handler) healthz(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, HealthResponse{Status: "ok", Service: "ncp-server"})
}

func (api *handler) capabilities(response http.ResponseWriter, request *http.Request) {
	requestContext, cancel := context.WithTimeout(request.Context(), api.agentTimeout)
	defer cancel()

	capabilities, err := api.agent.CollectCapabilities(requestContext, api.agentSocketPath)
	if err != nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "SYSTEM_CAPABILITIES_UNAVAILABLE", "系统能力暂不可用，请确认 Agent 已启动。")
		return
	}
	writeJSON(response, http.StatusOK, capabilities)
}

func (api *handler) agentStatus(response http.ResponseWriter, request *http.Request) {
	requestContext, cancel := context.WithTimeout(request.Context(), api.agentTimeout)
	defer cancel()

	status, err := api.agent.Probe(requestContext, api.agentSocketPath)
	if err != nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "AGENT_STATUS_UNAVAILABLE", "Agent 状态暂不可用，请确认 Agent 已启动。")
		return
	}
	writeJSON(response, http.StatusOK, status)
}

func (api *handler) systemSummary(response http.ResponseWriter, request *http.Request) {
	requestContext, cancel := context.WithTimeout(request.Context(), api.agentTimeout)
	defer cancel()

	summary, err := api.agent.CollectSystemSummary(requestContext, api.agentSocketPath)
	if err != nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "SYSTEM_SUMMARY_UNAVAILABLE", "系统实时概览暂不可用，请确认 Root Agent 已启动。")
		return
	}
	writeJSON(response, http.StatusOK, summary)
}

func (api *handler) systemEvents(response http.ResponseWriter, request *http.Request) {
	flusher, ok := response.(http.Flusher)
	if !ok {
		api.writeError(response, request, http.StatusInternalServerError, "REALTIME_STREAM_UNSUPPORTED", "当前服务不支持实时数据流。")
		return
	}

	response.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	response.Header().Set("Cache-Control", "no-cache, no-transform")
	response.Header().Set("Connection", "keep-alive")
	response.Header().Set("X-Accel-Buffering", "no")
	response.WriteHeader(http.StatusOK)
	interval := realtimeInterval(request)
	_, _ = fmt.Fprintf(response, "retry: %d\n\n", interval.Milliseconds())
	flusher.Flush()

	sendSnapshot := func() bool {
		_, err := fmt.Fprintf(
			response,
			"event: snapshot\ndata: {\"collectedAt\":%q}\n\n",
			time.Now().UTC().Format(time.RFC3339),
		)
		if err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if !sendSnapshot() {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case <-ticker.C:
			if !sendSnapshot() {
				return
			}
		}
	}
}

func realtimeInterval(request *http.Request) time.Duration {
	raw := request.URL.Query().Get("interval")
	if raw == "" {
		return defaultRealtimeInterval
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < controlstore.MinRefreshIntervalSeconds || seconds > controlstore.MaxRefreshIntervalSeconds {
		return defaultRealtimeInterval
	}
	return time.Duration(seconds) * time.Second
}

func (api *handler) dockerInventory(response http.ResponseWriter, request *http.Request) {
	inventory, ok := api.collectDockerInventory(response, request)
	if !ok {
		return
	}
	writeJSON(response, http.StatusOK, inventory)
}

func (api *handler) services(response http.ResponseWriter, request *http.Request) {
	inventory, ok := api.collectDockerInventory(response, request)
	if !ok {
		return
	}
	writeJSON(response, http.StatusOK, ServiceListResponse{
		CollectedAt: inventory.CollectedAt,
		Services:    inventory.Projects,
	})
}

func (api *handler) containerAction(response http.ResponseWriter, request *http.Request) {
	containerRequest := docker.ContainerActionRequest{
		ContainerID: chi.URLParam(request, "containerID"),
	}
	action, err := docker.ParseContainerAction(chi.URLParam(request, "action"))
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "DOCKER_CONTAINER_ACTION_INVALID", "容器操作参数无效。")
		return
	}
	containerRequest.Action = action
	if err := containerRequest.Validate(); err != nil {
		api.writeError(response, request, http.StatusBadRequest, "DOCKER_CONTAINER_ACTION_INVALID", "容器操作参数无效。")
		return
	}

	requestContext, cancel := context.WithTimeout(request.Context(), api.agentTimeout)
	defer cancel()
	result, err := api.agent.ControlContainer(requestContext, api.agentSocketPath, containerRequest)
	if err != nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "DOCKER_CONTAINER_ACTION_UNAVAILABLE", "容器操作暂不可用，请确认 Root Agent 与 Docker Engine 已启动。")
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (api *handler) containerLogs(response http.ResponseWriter, request *http.Request) {
	containerRequest := docker.ContainerLogsRequest{
		ContainerID: chi.URLParam(request, "containerID"),
	}
	if rawTail := request.URL.Query().Get("tail"); rawTail != "" {
		tail, err := strconv.Atoi(rawTail)
		if err != nil {
			api.writeError(response, request, http.StatusBadRequest, "DOCKER_LOGS_INPUT_INVALID", "日志条数参数无效。")
			return
		}
		containerRequest.Tail = tail
	}
	normalized, err := containerRequest.Normalize()
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "DOCKER_LOGS_INPUT_INVALID", "日志条数或容器标识无效。")
		return
	}

	requestContext, cancel := context.WithTimeout(request.Context(), api.agentTimeout)
	defer cancel()
	result, err := api.agent.ReadContainerLogs(requestContext, api.agentSocketPath, normalized)
	if err != nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "DOCKER_LOGS_UNAVAILABLE", "容器日志暂不可用，请确认 Root Agent 与 Docker Engine 已启动。")
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (api *handler) collectDockerInventory(response http.ResponseWriter, request *http.Request) (docker.Inventory, bool) {
	requestContext, cancel := context.WithTimeout(request.Context(), api.agentTimeout)
	defer cancel()

	inventory, err := api.agent.CollectDockerInventory(requestContext, api.agentSocketPath)
	if err != nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "DOCKER_INVENTORY_UNAVAILABLE", "Docker 实时清单暂不可用，请确认 Root Agent 与 Docker Engine 已启动。")
		return docker.Inventory{}, false
	}
	return inventory, true
}

func (api *handler) writeError(response http.ResponseWriter, request *http.Request, status int, code, message string) {
	writeJSON(response, status, ErrorResponse{
		Code:      code,
		Message:   message,
		RequestID: requestIDFromContext(request.Context()),
	})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

type requestIDContextKey struct{}

func requestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

var requestIDSequence atomic.Uint64

func defaultRequestID() string {
	return fmt.Sprintf("req-%d", requestIDSequence.Add(1))
}
