package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/Kkwans/nas-control-plane/internal/system"
	"github.com/go-chi/chi/v5"
)

const (
	defaultAgentTimeout       = 5 * time.Second
	defaultTerminalPOCTimeout = 5 * time.Minute
)

type AgentClient interface {
	Probe(context.Context, string) (agentsocket.AgentStatus, error)
	CollectCapabilities(context.Context, string) (system.Capabilities, error)
	CollectSystemSummary(context.Context, string) (system.Summary, error)
	CollectDockerInventory(context.Context, string) (docker.Inventory, error)
}

type Config struct {
	Agent              AgentClient
	AgentSocketPath    string
	AgentTimeout       time.Duration
	Terminal           TerminalClient
	TerminalPOCEnabled bool
	TerminalTimeout    time.Duration
	RequestID          func() string
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
	agent           AgentClient
	agentSocketPath string
	agentTimeout    time.Duration
	terminal        TerminalClient
	terminalEnabled bool
	terminalTimeout time.Duration
	newRequestID    func() string
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

func NewHandler(config Config) http.Handler {
	if config.Agent == nil {
		config.Agent = socketAgentClient{}
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
		agent:           config.Agent,
		agentSocketPath: config.AgentSocketPath,
		agentTimeout:    config.AgentTimeout,
		terminal:        config.Terminal,
		terminalEnabled: config.TerminalPOCEnabled,
		terminalTimeout: config.TerminalTimeout,
		newRequestID:    config.RequestID,
	}
	router := chi.NewRouter()
	router.Use(api.withRequestID)
	router.Get("/healthz", api.healthz)
	router.Get("/api/v1/system/capabilities", api.capabilities)
	router.Get("/api/v1/system/agent-status", api.agentStatus)
	router.Get("/api/v1/system/summary", api.systemSummary)
	router.Get("/api/v1/docker/inventory", api.dockerInventory)
	router.Get("/api/v1/services", api.services)
	if api.terminalEnabled {
		router.Get("/ws/terminal", api.terminalWebSocket)
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
