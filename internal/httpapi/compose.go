package httpapi

import (
	"context"
	"net/http"
	"time"

	ncpcompose "github.com/Kkwans/nas-control-plane/internal/compose"
)

const composeRequestTimeout = 20 * time.Second

func (api *handler) readComposeConfig(response http.ResponseWriter, request *http.Request) {
	var input ncpcompose.ReadRequest
	if !api.decodeControlBody(response, request, &input) {
		return
	}
	requestContext, cancel := context.WithTimeout(request.Context(), composeRequestTimeout)
	defer cancel()
	result, err := api.compose.ReadComposeConfig(requestContext, api.agentSocketPath, input)
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "COMPOSE_CONFIG_READ_FAILED", "Compose 配置读取失败，请确认项目配置文件仍然存在。")
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (api *handler) validateComposeConfig(response http.ResponseWriter, request *http.Request) {
	var input ncpcompose.ValidateRequest
	if !api.decodeControlBody(response, request, &input) {
		return
	}
	requestContext, cancel := context.WithTimeout(request.Context(), composeRequestTimeout)
	defer cancel()
	result, err := api.compose.ValidateComposeConfig(requestContext, api.agentSocketPath, input)
	if err != nil {
		api.writeError(response, request, http.StatusUnprocessableEntity, "COMPOSE_CONFIG_INVALID", "Compose 配置校验失败，请检查 YAML 结构、服务引用和环境变量。")
		return
	}
	writeJSON(response, http.StatusOK, result)
}
