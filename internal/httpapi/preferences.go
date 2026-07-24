package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Kkwans/nas-control-plane/internal/auth"
	"github.com/Kkwans/nas-control-plane/internal/controlstore"
)

const maxControlBodySize = 16 * 1024

func (api *handler) preferences(response http.ResponseWriter, request *http.Request) {
	if api.controlStore == nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "PREFERENCES_UNAVAILABLE", "用户偏好暂不可用。")
		return
	}
	preferences, err := api.controlStore.Preferences(request.Context(), currentPrincipal(request).ID)
	if err != nil {
		api.writeError(response, request, http.StatusInternalServerError, "PREFERENCES_READ_FAILED", "用户偏好读取失败。")
		return
	}
	writeJSON(response, http.StatusOK, preferences)
}

func (api *handler) updatePreferences(response http.ResponseWriter, request *http.Request) {
	if api.controlStore == nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "PREFERENCES_UNAVAILABLE", "用户偏好暂不可用。")
		return
	}
	var input controlstore.Preferences
	if !api.decodeControlBody(response, request, &input) {
		return
	}
	preferences, err := api.controlStore.UpdatePreferences(request.Context(), currentPrincipal(request).ID, input)
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "PREFERENCES_INPUT_INVALID", "刷新间隔必须在 2 秒到 5 分钟之间。")
		return
	}
	writeJSON(response, http.StatusOK, preferences)
}

func (api *handler) databaseProjectPreferences(response http.ResponseWriter, request *http.Request) {
	if api.controlStore == nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "DATABASE_PREFERENCES_UNAVAILABLE", "数据库项目偏好暂不可用。")
		return
	}
	preferences, err := api.controlStore.DatabaseProjectPreferences(request.Context())
	if err != nil {
		api.writeError(response, request, http.StatusInternalServerError, "DATABASE_PREFERENCES_READ_FAILED", "数据库项目偏好读取失败。")
		return
	}
	writeJSON(response, http.StatusOK, preferences)
}

func (api *handler) updateDatabaseProjectPreference(response http.ResponseWriter, request *http.Request) {
	if api.controlStore == nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "DATABASE_PREFERENCES_UNAVAILABLE", "数据库项目偏好暂不可用。")
		return
	}
	var input controlstore.DatabaseProjectPreference
	if !api.decodeControlBody(response, request, &input) {
		return
	}
	preference, err := api.controlStore.SetDatabaseProjectPreference(request.Context(), input)
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "DATABASE_PREFERENCES_INPUT_INVALID", "数据库项目偏好参数无效。")
		return
	}
	writeJSON(response, http.StatusOK, preference)
}

func currentPrincipal(request *http.Request) auth.Principal {
	principal, _ := request.Context().Value(principalContextKey{}).(auth.Principal)
	return principal
}

func (api *handler) decodeControlBody(response http.ResponseWriter, request *http.Request, destination any) bool {
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxControlBodySize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		api.writeError(response, request, http.StatusBadRequest, "CONTROL_INPUT_INVALID", "请求参数无效。")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		api.writeError(response, request, http.StatusBadRequest, "CONTROL_INPUT_INVALID", "请求只能包含一个 JSON 对象。")
		return false
	}
	return true
}
