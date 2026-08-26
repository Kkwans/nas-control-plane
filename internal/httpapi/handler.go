package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	ncpcompose "github.com/Kkwans/nas-control-plane/internal/compose"
	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	ncpdatabase "github.com/Kkwans/nas-control-plane/internal/database"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/Kkwans/nas-control-plane/internal/filesystem"
	"github.com/Kkwans/nas-control-plane/internal/journal"
	"github.com/Kkwans/nas-control-plane/internal/system"
	"github.com/go-chi/chi/v5"
)

const (
	defaultAgentTimeout                 = 5 * time.Second
	defaultDockerContainerActionTimeout = 45 * time.Second
	defaultComposeLifecycleTimeout      = 90 * time.Second
	defaultTerminalTimeout              = 30 * time.Minute
	defaultRealtimeInterval             = 5 * time.Second
	defaultDatabaseTimeout              = 20 * time.Second
	defaultDockerImageTimeout           = 10 * time.Minute
	defaultDNSControlTimeout            = 45 * time.Second
	defaultPublicEgressTimeout          = 15 * time.Second
	defaultProxyInspectionTimeout       = 20 * time.Second
)

type AgentClient interface {
	Probe(context.Context, string) (agentsocket.AgentStatus, error)
	CollectCapabilities(context.Context, string) (system.Capabilities, error)
	CollectSystemSummary(context.Context, string) (system.Summary, error)
	CollectSystemDetails(context.Context, string) (system.Details, error)
	CollectDockerInventory(context.Context, string) (docker.Inventory, error)
	ControlContainer(context.Context, string, docker.ContainerActionRequest) (docker.ContainerActionResult, error)
	ReadContainerLogs(context.Context, string, docker.ContainerLogsRequest) (docker.ContainerLogsResult, error)
	QueryJournal(context.Context, string, journal.Query) (journal.Page, error)
}

// Optional Agent capabilities keep existing fakes source-compatible while
// Docker resource and NAS path browsing roll out through explicit RPCs.
type DockerResourcesAgentClient interface {
	CollectDockerResources(context.Context, string) (docker.Resources, error)
}

type FilesystemAgentClient interface {
	ListPath(context.Context, string, filesystem.Request) (filesystem.Page, error)
}

type WebProbeAgentClient interface {
	ProbeWeb(context.Context, string, string) (agentsocket.WebProbeResult, error)
}

type HostSiteDiscoveryAgentClient interface {
	DiscoverHostSiteCandidates(context.Context, string) ([]docker.HostSiteCandidate, error)
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

type DatabaseConnectionCoordinator interface {
	Connect(context.Context, ncpdatabase.Source, *ncpdatabase.Credentials) (ncpdatabase.ConnectionDiagnostic, error)
}

type DockerImageAgentClient interface {
	ListDockerImages(context.Context, string) (docker.ImageInventory, error)
	SearchDockerHub(context.Context, string, docker.HubSearchRequest) (docker.HubSearchResult, error)
	ListDockerHubTags(context.Context, string, docker.HubTagsRequest) (docker.HubTagsResult, error)
	PullDockerImage(context.Context, string, docker.ImagePullRequest) (docker.ImagePullResult, error)
	RemoveDockerImage(context.Context, string, docker.ImageRemoveRequest) (docker.ImageRemoveResult, error)
}

type DockerImageProgressAgentClient interface {
	PullDockerImageWithProgress(context.Context, string, docker.ImagePullRequest, func(docker.ImagePullProgress)) (docker.ImagePullResult, error)
}

type ComposeAgentClient interface {
	ReadComposeConfig(context.Context, string, ncpcompose.ReadRequest) (ncpcompose.ProjectConfig, error)
	ValidateComposeConfig(context.Context, string, ncpcompose.ValidateRequest) (ncpcompose.ValidationResult, error)
	DeployComposeConfig(context.Context, string, ncpcompose.DeployRequest) (ncpcompose.DeployResult, error)
}

type ControlStore interface {
	Preferences(context.Context, int64) (controlstore.Preferences, error)
	UpdatePreferences(context.Context, int64, controlstore.Preferences) (controlstore.Preferences, error)
	DatabaseProjectPreferences(context.Context) ([]controlstore.DatabaseProjectPreference, error)
	SetDatabaseProjectPreference(context.Context, controlstore.DatabaseProjectPreference) (controlstore.DatabaseProjectPreference, error)
	SiteProfiles(context.Context) ([]controlstore.SiteProfile, error)
	UpsertSiteProfile(context.Context, controlstore.SiteProfile) (controlstore.SiteProfile, error)
	RecordSiteVisit(context.Context, string) (time.Time, error)
	ComposeDraft(context.Context, string, string) (controlstore.ComposeDraft, error)
	SaveComposeDraft(context.Context, controlstore.ComposeDraft) (controlstore.ComposeDraft, error)
	RecordComposeRevision(context.Context, controlstore.ComposeRevision) (controlstore.ComposeRevision, error)
	ComposeRevisions(context.Context, string, int) ([]controlstore.ComposeRevision, error)
	RecordMetricSample(context.Context, controlstore.MetricSample) error
	MetricSamples(context.Context, time.Time) ([]controlstore.MetricSample, error)
}

type Config struct {
	Agent               AgentClient
	DatabaseAgent       DatabaseAgentClient
	DatabaseConnections DatabaseConnectionCoordinator
	DockerImages        DockerImageAgentClient
	DockerResources     DockerResourcesAgentClient
	Filesystem          FilesystemAgentClient
	Compose             ComposeAgentClient
	AgentSocketPath     string
	AgentTimeout        time.Duration
	Auth                Authenticator
	ControlStore        ControlStore
	SessionCookieSecure bool
	SiteAssetsDirectory string
	Terminal            TerminalClient
	TerminalEnabled     bool
	// TerminalPOCEnabled is kept for one compatibility window for callers that
	// still use the pre-release field name.
	TerminalPOCEnabled bool
	TerminalTimeout    time.Duration
	RequestID          func() string
	// Logger receives one structured record per HTTP request. It is optional so
	// embedders and tests can provide a sink appropriate to their environment.
	Logger *slog.Logger
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

type RealtimeSnapshot struct {
	CollectedAt time.Time         `json:"collectedAt"`
	Summary     *system.Summary   `json:"summary,omitempty"`
	Docker      *docker.Inventory `json:"docker,omitempty"`
	Errors      []string          `json:"errors,omitempty"`
}

type handler struct {
	agent               AgentClient
	databaseAgent       DatabaseAgentClient
	databaseConnections DatabaseConnectionCoordinator
	dockerImages        DockerImageAgentClient
	dockerResources     DockerResourcesAgentClient
	filesystem          FilesystemAgentClient
	compose             ComposeAgentClient
	agentSocketPath     string
	agentTimeout        time.Duration
	auth                Authenticator
	controlStore        ControlStore
	sessionCookieSecure bool
	siteAssetsDirectory string
	siteIconClient      siteIconHTTPClient
	terminal            TerminalClient
	terminalEnabled     bool
	terminalTimeout     time.Duration
	newRequestID        func() string
	logger              *slog.Logger
	jobs                *jobRegistry
}

func NewHandler(config Config) http.Handler {
	if config.Agent == nil {
		config.Agent = socketAgentClient{}
	}
	if config.DatabaseAgent == nil {
		config.DatabaseAgent = socketAgentClient{}
	}
	if config.DockerImages == nil {
		config.DockerImages = socketAgentClient{}
	}
	if config.DockerResources == nil {
		config.DockerResources = socketAgentClient{}
	}
	if config.Filesystem == nil {
		config.Filesystem = socketAgentClient{}
	}
	if config.Compose == nil {
		config.Compose = socketAgentClient{}
	}
	if config.AgentSocketPath == "" {
		config.AgentSocketPath = agentsocket.DefaultSocketPath
	}
	if config.AgentTimeout <= 0 {
		config.AgentTimeout = defaultAgentTimeout
	}
	terminalEnabled := config.TerminalEnabled || config.TerminalPOCEnabled
	if terminalEnabled && config.Terminal == nil {
		config.Terminal = socketTerminalClient{}
	}
	if config.TerminalTimeout <= 0 {
		config.TerminalTimeout = defaultTerminalTimeout
	}
	if config.RequestID == nil {
		config.RequestID = defaultRequestID
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	api := &handler{
		agent:               config.Agent,
		databaseAgent:       config.DatabaseAgent,
		databaseConnections: config.DatabaseConnections,
		dockerImages:        config.DockerImages,
		dockerResources:     config.DockerResources,
		filesystem:          config.Filesystem,
		compose:             config.Compose,
		agentSocketPath:     config.AgentSocketPath,
		agentTimeout:        config.AgentTimeout,
		auth:                config.Auth,
		controlStore:        config.ControlStore,
		sessionCookieSecure: config.SessionCookieSecure,
		siteAssetsDirectory: config.SiteAssetsDirectory,
		terminal:            config.Terminal,
		terminalEnabled:     terminalEnabled,
		terminalTimeout:     config.TerminalTimeout,
		newRequestID:        config.RequestID,
		logger:              config.Logger,
		jobs:                newJobRegistry(config.ControlStore),
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
			protected.Get("/system/details", api.systemDetails)
			protected.Get("/system/dns/capability", api.dnsCapability)
			protected.Post("/system/dns/preview", api.previewDNSChange)
			protected.Post("/system/dns/confirm", api.confirmDNSChange)
			protected.Post("/system/dns/rollback", api.rollbackDNSChange)
			protected.Get("/system/public-egress/capability", api.publicEgressCapability)
			protected.Post("/system/public-egress/detect", api.detectPublicEgress)
			protected.Get("/proxy/mihomo/capability", api.mihomoCapability)
			protected.Post("/proxy/mihomo/invoke", api.invokeMihomo)
			protected.Post("/proxy/mihomo/inspect", api.inspectMihomo)
			protected.Get("/system/events", api.systemEvents)
			protected.Get("/monitoring/samples", api.monitoringSamples)
			protected.Get("/logs", api.logs)
			protected.Get("/logs/events", api.logEvents)
			protected.Get("/preferences", api.preferences)
			protected.Put("/preferences", api.updatePreferences)
			protected.Get("/preferences/lists/{listKey}", api.listPreference)
			protected.Put("/preferences/lists/{listKey}", api.updateListPreference)
			protected.Get("/users", api.users)
			protected.Post("/users", api.createUser)
			protected.Get("/users/password-policy", api.passwordPolicy)
			protected.Put("/users/password-policy", api.updatePasswordPolicy)
			protected.Put("/users/{userID}/status", api.updateUserStatus)
			protected.Put("/users/{userID}/password", api.updateUserPassword)
			protected.Delete("/users/{userID}", api.deleteUser)
			protected.Get("/docker/inventory", api.dockerInventory)
			protected.Get("/docker/resources", api.dockerResourcesInventory)
			protected.Get("/files/entries", api.fileEntries)
			protected.Get("/docker/images", api.dockerImageInventory)
			protected.Get("/docker/hub/search", api.searchDockerHub)
			protected.Get("/docker/hub/tags", api.dockerHubTags)
			protected.Post("/docker/images/pull", api.pullDockerImage)
			protected.Post("/docker/images/remove", api.removeDockerImage)
			protected.Post("/docker/images/remove-batch", api.removeDockerImages)
			protected.Post("/docker/containers", api.createDockerContainer)
			protected.Post("/docker/projects/{projectID}/actions/{action}", api.controlStandaloneProject)
			protected.Post("/docker/compose/projects/{projectID}/actions/{action}", api.controlComposeProject)
			protected.Delete("/docker/compose/projects/{projectID}", api.deleteDockerProject)
			protected.Post("/docker/compose/config/read", api.readComposeConfig)
			protected.Post("/docker/compose/config/validate", api.validateComposeConfig)
			protected.Get("/docker/compose/drafts", api.composeDraft)
			protected.Put("/docker/compose/drafts", api.saveComposeDraft)
			protected.Post("/docker/compose/deploy", api.deployComposeConfig)
			protected.Get("/docker/compose/revisions", api.composeRevisions)
			protected.Get("/jobs/{jobID}", api.jobStatus)
			protected.Get("/jobs/{jobID}/events", api.jobEvents)
			protected.Get("/jobs", api.listJobs)
			protected.Post("/jobs/{jobID}/retry", api.retryJob)
			protected.Post("/jobs/{jobID}/cancel", api.cancelJob)
			protected.Delete("/jobs/{jobID}", api.deleteJob)
			protected.Post("/docker/containers/{containerID}/actions/{action}", api.containerAction)
			protected.Get("/docker/containers/{containerID}", api.dockerContainerDetails)
			protected.Get("/docker/containers/{containerID}/logs", api.containerLogs)
			protected.Get("/services", api.services)
			protected.Get("/sites", api.sites)
			protected.Get("/sites/icon-proxy", api.siteIconProxy)
			protected.Post("/sites", api.createSite)
			protected.Put("/sites/{siteID}", api.updateSite)
			protected.Delete("/sites/{siteID}", api.deleteSite)
			protected.Post("/sites/{siteID}/visit", api.recordSiteVisit)
			protected.Post("/sites/{siteID}/icon", api.uploadSiteIcon)
			protected.Get("/sites/{siteID}/icon", api.siteIcon)
			protected.Delete("/sites/{siteID}/icon", api.deleteSiteIcon)
			protected.Get("/sites/ignored", api.ignoredSites)
			protected.Post("/sites/{siteID}/restore", api.restoreSite)
			protected.Get("/databases/discovery", api.databaseDiscovery)
			protected.Post("/databases/connect", api.databaseConnect)
			protected.Post("/databases/test-connection", api.databaseTestConnection)
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
	summary, err := api.collectSystemSummary(request.Context())
	if err != nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "SYSTEM_SUMMARY_UNAVAILABLE", "系统实时概览暂不可用，请确认 Root Agent 已启动。")
		return
	}
	api.recordMetricSample(request.Context(), summary)
	writeJSON(response, http.StatusOK, summary)
}

func (api *handler) systemDetails(response http.ResponseWriter, request *http.Request) {
	details, err := api.collectSystemDetails(request.Context())
	if err != nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "SYSTEM_DETAILS_UNAVAILABLE", "系统详细信息暂不可用，请确认 Root Agent 已启动。")
		return
	}

	now := time.Now().UTC()
	details.Control.Nodes = append([]system.ControlNode{
		{
			ID:       "web",
			Name:     "Web 控制台",
			Detail:   "浏览器中的 Root 管理会话",
			Status:   "ready",
			Version:  agentsocket.BuildVersion,
			LastSeen: now,
		},
		{
			ID:       "server",
			Name:     "NCP Server",
			Detail:   "HTTP API、SQLite 与任务调度",
			Status:   "ready",
			Version:  agentsocket.BuildVersion,
			LastSeen: now,
		},
		{
			ID:       "socket",
			Name:     "Unix Socket",
			Detail:   "本机强类型 RPC 通道",
			Status:   "ready",
			Version:  agentsocket.ProtocolVersion,
			LastSeen: now,
		},
	}, details.Control.Nodes...)

	writeJSON(response, http.StatusOK, details)
}

func (api *handler) monitoringSamples(response http.ResponseWriter, request *http.Request) {
	if api.controlStore == nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "MONITORING_STORE_UNAVAILABLE", "监控历史暂不可用。")
		return
	}
	historyRange, err := ParseMonitoringRange(request.URL.Query(), time.Now().UTC())
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "MONITORING_RANGE_INVALID", "时间范围无效，最长只能查询 7 天且结束时间不能晚于当前时间。")
		return
	}
	samples, err := QueryMonitoringSamples(request.Context(), api.controlStore, historyRange)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		api.writeError(response, request, http.StatusInternalServerError, "MONITORING_READ_FAILED", "监控历史读取失败。")
		return
	}
	writeJSON(response, http.StatusOK, samples)
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
	scope := realtimeScope(request)
	_, _ = fmt.Fprintf(response, "retry: %d\n\n", interval.Milliseconds())
	flusher.Flush()

	sendSnapshot := func() bool {
		snapshot := RealtimeSnapshot{CollectedAt: time.Now().UTC()}
		if scope.summary {
			summary, err := api.collectSystemSummary(request.Context())
			if err != nil {
				snapshot.Errors = append(snapshot.Errors, "SYSTEM_SUMMARY_UNAVAILABLE")
			} else {
				snapshot.Summary = &summary
				api.recordMetricSample(request.Context(), summary)
			}
		}
		if scope.docker {
			inventory, err := api.collectDockerInventoryContext(request.Context())
			if err != nil {
				snapshot.Errors = append(snapshot.Errors, "DOCKER_INVENTORY_UNAVAILABLE")
			} else {
				snapshot.Docker = &inventory
			}
		}
		payload, err := json.Marshal(snapshot)
		if err != nil {
			return false
		}
		_, err = fmt.Fprintf(response, "event: snapshot\ndata: %s\n\n", payload)
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

type realtimeStreamScope struct {
	summary bool
	docker  bool
}

func realtimeScope(request *http.Request) realtimeStreamScope {
	scope := realtimeStreamScope{}
	for _, name := range request.URL.Query()["scope"] {
		switch name {
		case "summary":
			scope.summary = true
		case "docker":
			scope.docker = true
		}
	}
	if !scope.summary && !scope.docker {
		scope.summary = true
	}
	return scope
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

func (api *handler) collectSystemSummary(ctx context.Context) (system.Summary, error) {
	requestContext, cancel := context.WithTimeout(ctx, api.agentTimeout)
	defer cancel()
	return api.agent.CollectSystemSummary(requestContext, api.agentSocketPath)
}

func (api *handler) collectSystemDetails(ctx context.Context) (system.Details, error) {
	timeout := api.agentTimeout
	if timeout < 10*time.Second {
		timeout = 10 * time.Second
	}
	requestContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return api.agent.CollectSystemDetails(requestContext, api.agentSocketPath)
}

func (api *handler) collectDockerInventoryContext(ctx context.Context) (docker.Inventory, error) {
	requestContext, cancel := context.WithTimeout(ctx, api.agentTimeout)
	defer cancel()
	return api.agent.CollectDockerInventory(requestContext, api.agentSocketPath)
}

func (api *handler) recordMetricSample(ctx context.Context, summary system.Summary) {
	if api.controlStore == nil {
		return
	}
	metric := summary.MetricSample()
	temperatures := make([]controlstore.MetricTemperature, 0, len(metric.Temperatures))
	for _, temperature := range metric.Temperatures {
		temperatures = append(temperatures, controlstore.MetricTemperature{
			Name:               temperature.Name,
			TemperatureCelsius: temperature.TemperatureCelsius,
		})
	}
	_ = api.controlStore.RecordMetricSample(ctx, controlstore.MetricSample{
		CollectedAt:     metric.CollectedAt,
		CPUPercent:      metric.CPUPercent,
		MemoryPercent:   metric.MemoryPercent,
		Load1:           metric.Load1,
		DiskPercent:     metric.DiskPercent,
		NetworkReceive:  metric.NetworkReceive,
		NetworkTransmit: metric.NetworkTransmit,
		DiskReadBytes:   metric.DiskReadBytes,
		DiskWriteBytes:  metric.DiskWriteBytes,
		Temperatures:    temperatures,
	})
}

func (api *handler) writeError(response http.ResponseWriter, request *http.Request, status int, code, message string) {
	if metadata := requestMetadataFromContext(request.Context()); metadata != nil {
		metadata.errorCode = code
	}
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
