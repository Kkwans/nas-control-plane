package httpapi

import (
	"context"
	"net/http"
	"strings"

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
		api.writeError(response, request, http.StatusConflict, "SYSTEM_DNS_PREVIEW_FAILED", dnsChangeFailureMessage(err, "DNS 修改无法预览。"))
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
		api.writeError(response, request, http.StatusConflict, "SYSTEM_DNS_CONFIRM_FAILED", dnsChangeFailureMessage(err, "DNS 修改未执行或已被拒绝。"))
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
		api.writeError(response, request, http.StatusConflict, "SYSTEM_DNS_ROLLBACK_FAILED", dnsChangeFailureMessage(err, "DNS 修改回滚失败或已不可用。"))
		return
	}
	writeJSON(response, http.StatusOK, value)
}

func dnsChangeFailureMessage(err error, fallback string) string {
	code := ""
	if err != nil {
		code = strings.TrimSpace(err.Error())
	}
	switch code {
	case "DNS_BACKEND_READ_ONLY":
		return "检测到静态 /etc/resolv.conf，未发现可管理的 systemd-resolved 或 NetworkManager；DNS 保持只读。"
	case "DNS_WRITE_ADAPTER_UNAVAILABLE":
		return "DNS 后端未提供安全的预览、应用和回滚适配器。"
	case "UGOS_DNS_SOCKET_UNAVAILABLE", "UGOS_DNS_READ_FAILED", "UGOS_DNS_CONFIG_INVALID":
		return "UGOS 网络服务暂不可用或返回了无法识别的配置；NCP 未修改 DNS。"
	case "DNS_NAMESERVERS_INVALID":
		return "请输入 1 至 6 个有效且不重复的 IPv4 或 IPv6 DNS 地址。"
	case "DNS_SEARCH_DOMAINS_UNSUPPORTED":
		return "UGOS 网络服务当前只支持修改 DNS 服务器，不支持在此修改搜索域。"
	case "DNS_PREVIEW_NOT_FOUND", "DNS_PREVIEW_EXPIRED":
		return "DNS 修改预览已失效，请重新预览后再确认。"
	case "DNS_SOURCE_CHANGED":
		return "DNS 配置已被其他进程修改，为避免覆盖新配置，本次操作已取消。"
	case "DNS_STATIC_FILE_UNSAFE":
		return "当前 DNS 配置不是可安全管理的普通文件，NCP 已拒绝写入。"
	case "DNS_BACKUP_FAILED", "DNS_BACKUP_INVALID", "DNS_CHANGE_RECORD_FAILED":
		return "DNS 配置备份或回滚记录不可用，NCP 未继续修改。"
	case "DNS_APPLY_FAILED", "DNS_APPLY_VERIFICATION_FAILED":
		return "DNS 配置应用或校验失败；NCP 已保留修改前备份。"
	case "DNS_ROLLBACK_FAILED", "DNS_ROLLBACK_VERIFICATION_FAILED":
		return "DNS 配置回滚或回滚校验失败，请检查 Root Agent 日志和备份。"
	default:
		return fallback
	}
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
	requestContext, cancel := context.WithTimeout(request.Context(), defaultPublicEgressTimeout)
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
