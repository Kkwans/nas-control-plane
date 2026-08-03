package httpapi

import (
	"context"
	"net/http"

	"github.com/Kkwans/nas-control-plane/internal/system"
)

// SystemControlAgentClient is intentionally optional so the legacy AgentClient contract
// remains source-compatible until the main handler registers the new RPC methods.
type SystemControlAgentClient interface {
	CollectDNSCapability(context.Context, string) (system.DNSCapability, error)
	PreviewDNSChange(context.Context, string, system.DNSChangeRequest) (system.DNSChangePreview, error)
	ConfirmDNSChange(context.Context, string, system.DNSChangeConfirmation) (system.DNSChangeResult, error)
	RollbackDNSChange(context.Context, string, system.DNSRollbackRequest) (system.DNSChangeResult, error)
	GetPublicEgressCapability(context.Context, string) (system.PublicEgressCapability, error)
	DetectPublicEgress(context.Context, string) (system.PublicEgressResult, error)
}

func (api *handler) dnsCapability(response http.ResponseWriter, request *http.Request) {
	client, ok := api.agent.(SystemControlAgentClient)
	if !ok {
		api.writeError(response, request, http.StatusServiceUnavailable, "SYSTEM_DNS_UNAVAILABLE", "DNS 能力暂未接入 Root Agent。")
		return
	}
	requestContext, cancel := context.WithTimeout(request.Context(), api.agentTimeout)
	defer cancel()
	value, err := client.CollectDNSCapability(requestContext, api.agentSocketPath)
	if err != nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "SYSTEM_DNS_UNAVAILABLE", "DNS 能力暂不可用。")
		return
	}
	writeJSON(response, http.StatusOK, value)
}

func (api *handler) previewDNSChange(response http.ResponseWriter, request *http.Request) {
	client, ok := api.agent.(SystemControlAgentClient)
	if !ok {
		api.writeError(response, request, http.StatusServiceUnavailable, "SYSTEM_DNS_UNAVAILABLE", "DNS 修改能力暂未接入 Root Agent。")
		return
	}
	var input system.DNSChangeRequest
	if !api.decodeControlBody(response, request, &input) {
		return
	}
	if err := validateDNSChangeRequest(input); err != nil {
		api.writeError(response, request, http.StatusBadRequest, "SYSTEM_DNS_INPUT_INVALID", err.Error())
		return
	}
	requestContext, cancel := context.WithTimeout(request.Context(), api.agentTimeout)
	defer cancel()
	value, err := client.PreviewDNSChange(requestContext, api.agentSocketPath, input)
	if err != nil {
		api.writeError(response, request, http.StatusConflict, "SYSTEM_DNS_PREVIEW_FAILED", "DNS 修改无法预览；静态 resolv.conf 仅支持只读。")
		return
	}
	writeJSON(response, http.StatusOK, value)
}

func (api *handler) confirmDNSChange(response http.ResponseWriter, request *http.Request) {
	client, ok := api.agent.(SystemControlAgentClient)
	if !ok {
		api.writeError(response, request, http.StatusServiceUnavailable, "SYSTEM_DNS_UNAVAILABLE", "DNS 修改能力暂未接入 Root Agent。")
		return
	}
	var input system.DNSChangeConfirmation
	if !api.decodeControlBody(response, request, &input) {
		return
	}
	if input.PreviewID == "" || !input.Confirmed {
		api.writeError(response, request, http.StatusBadRequest, "SYSTEM_DNS_CONFIRMATION_REQUIRED", "必须提供有效 previewId 并明确确认 DNS 修改。")
		return
	}
	requestContext, cancel := context.WithTimeout(request.Context(), api.agentTimeout)
	defer cancel()
	value, err := client.ConfirmDNSChange(requestContext, api.agentSocketPath, input)
	if err != nil {
		api.writeError(response, request, http.StatusConflict, "SYSTEM_DNS_CONFIRM_FAILED", "DNS 修改未执行或已被拒绝。")
		return
	}
	writeJSON(response, http.StatusOK, value)
}

func (api *handler) rollbackDNSChange(response http.ResponseWriter, request *http.Request) {
	client, ok := api.agent.(SystemControlAgentClient)
	if !ok {
		api.writeError(response, request, http.StatusServiceUnavailable, "SYSTEM_DNS_UNAVAILABLE", "DNS 回滚能力暂未接入 Root Agent。")
		return
	}
	var input system.DNSRollbackRequest
	if !api.decodeControlBody(response, request, &input) {
		return
	}
	if input.ChangeID == "" {
		api.writeError(response, request, http.StatusBadRequest, "SYSTEM_DNS_ROLLBACK_INVALID", "必须提供 changeId。")
		return
	}
	requestContext, cancel := context.WithTimeout(request.Context(), api.agentTimeout)
	defer cancel()
	value, err := client.RollbackDNSChange(requestContext, api.agentSocketPath, input)
	if err != nil {
		api.writeError(response, request, http.StatusConflict, "SYSTEM_DNS_ROLLBACK_FAILED", "DNS 修改回滚失败或已不可用。")
		return
	}
	writeJSON(response, http.StatusOK, value)
}

func (api *handler) publicEgressCapability(response http.ResponseWriter, request *http.Request) {
	client, ok := api.agent.(SystemControlAgentClient)
	if !ok {
		api.writeError(response, request, http.StatusServiceUnavailable, "PUBLIC_EGRESS_UNAVAILABLE", "公网出口检测暂未接入 Root Agent。")
		return
	}
	requestContext, cancel := context.WithTimeout(request.Context(), api.agentTimeout)
	defer cancel()
	value, err := client.GetPublicEgressCapability(requestContext, api.agentSocketPath)
	if err != nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "PUBLIC_EGRESS_UNAVAILABLE", "公网出口检测能力暂不可用。")
		return
	}
	writeJSON(response, http.StatusOK, value)
}

// detectPublicEgress 只由用户显式 POST 触发；GET 能力接口不会调用外部端点。
func (api *handler) detectPublicEgress(response http.ResponseWriter, request *http.Request) {
	client, ok := api.agent.(SystemControlAgentClient)
	if !ok {
		api.writeError(response, request, http.StatusServiceUnavailable, "PUBLIC_EGRESS_UNAVAILABLE", "公网出口检测暂未接入 Root Agent。")
		return
	}
	requestContext, cancel := context.WithTimeout(request.Context(), api.agentTimeout)
	defer cancel()
	value, err := client.DetectPublicEgress(requestContext, api.agentSocketPath)
	if err != nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "PUBLIC_EGRESS_UNAVAILABLE", "公网出口检测端点不可用。")
		return
	}
	if value.Status != system.CapabilityStateAvailable {
		writeJSON(response, http.StatusServiceUnavailable, value)
		return
	}
	writeJSON(response, http.StatusOK, value)
}
