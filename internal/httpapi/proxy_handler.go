package httpapi

import (
	"context"
	"net/http"

	"github.com/Kkwans/nas-control-plane/internal/system"
)

type ProxyControlAgentClient interface {
	ProbeMihomo(context.Context, string) (system.MihomoCapability, error)
	InvokeMihomo(context.Context, string, system.MihomoInvokeRequest) (system.MihomoInvokeResult, error)
}

func (api *handler) mihomoCapability(response http.ResponseWriter, request *http.Request) {
	client, ok := api.agent.(ProxyControlAgentClient)
	if !ok {
		api.writeError(response, request, http.StatusServiceUnavailable, "PROXY_MIHOMO_UNAVAILABLE", "Mihomo 能力暂未接入 Root Agent。")
		return
	}
	requestContext, cancel := context.WithTimeout(request.Context(), api.agentTimeout)
	defer cancel()
	value, err := client.ProbeMihomo(requestContext, api.agentSocketPath)
	if err != nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "PROXY_MIHOMO_UNAVAILABLE", "Mihomo 能力暂不可用。")
		return
	}
	writeJSON(response, http.StatusOK, value)
}

// invokeMihomo 只接受 allowlist operation；请求不包含 controller endpoint 或 token。
func (api *handler) invokeMihomo(response http.ResponseWriter, request *http.Request) {
	client, ok := api.agent.(ProxyControlAgentClient)
	if !ok {
		api.writeError(response, request, http.StatusServiceUnavailable, "PROXY_MIHOMO_UNAVAILABLE", "Mihomo 控制器暂未接入 Root Agent。")
		return
	}
	var input system.MihomoInvokeRequest
	if !api.decodeControlBody(response, request, &input) {
		return
	}
	if err := system.ValidateMihomoInvokeRequest(input); err != nil {
		api.writeError(response, request, http.StatusBadRequest, "PROXY_MIHOMO_REQUEST_INVALID", "Mihomo 操作不受支持或参数无效。")
		return
	}
	requestContext, cancel := context.WithTimeout(request.Context(), api.agentTimeout)
	defer cancel()
	value, err := client.InvokeMihomo(requestContext, api.agentSocketPath, input)
	if err != nil {
		api.writeError(response, request, http.StatusConflict, "PROXY_MIHOMO_INVOKE_FAILED", "Mihomo 受控调用失败。")
		return
	}
	writeJSON(response, http.StatusOK, value)
}
