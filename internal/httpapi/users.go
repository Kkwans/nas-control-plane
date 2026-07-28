package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/Kkwans/nas-control-plane/internal/auth"
	"github.com/go-chi/chi/v5"
)

type userManager interface {
	Users(context.Context) ([]auth.User, error)
	PasswordPolicy(context.Context) (auth.PasswordPolicy, error)
	UpdatePasswordPolicy(context.Context, auth.PasswordPolicy) (auth.PasswordPolicy, error)
	CreateUser(context.Context, string, string) (auth.User, error)
	SetUserDisabled(context.Context, int64, int64, bool) (auth.User, error)
	DeleteUser(context.Context, int64, int64) error
	UpdatePassword(context.Context, int64, int64, string, string) error
}

func (api *handler) passwordPolicy(response http.ResponseWriter, request *http.Request) {
	manager, ok := api.auth.(userManager)
	if !ok {
		api.writeError(response, request, http.StatusServiceUnavailable, "USER_MANAGEMENT_UNAVAILABLE", "用户管理服务暂不可用。")
		return
	}
	policy, err := manager.PasswordPolicy(request.Context())
	if err != nil {
		api.writeUserError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, policy)
}

func (api *handler) updatePasswordPolicy(response http.ResponseWriter, request *http.Request) {
	manager, ok := api.auth.(userManager)
	if !ok {
		api.writeError(response, request, http.StatusServiceUnavailable, "USER_MANAGEMENT_UNAVAILABLE", "用户管理服务暂不可用。")
		return
	}
	var input auth.PasswordPolicy
	if err := decodeUserRequest(request, &input); err != nil {
		api.writeError(response, request, http.StatusBadRequest, "AUTH_INPUT_INVALID", "密码规则参数无效。")
		return
	}
	policy, err := manager.UpdatePasswordPolicy(request.Context(), input)
	if err != nil {
		api.writeUserError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, policy)
}

type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type userStatusRequest struct {
	Disabled bool `json:"disabled"`
}

type userPasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (api *handler) users(response http.ResponseWriter, request *http.Request) {
	manager, ok := api.auth.(userManager)
	if !ok {
		api.writeError(response, request, http.StatusServiceUnavailable, "USER_MANAGEMENT_UNAVAILABLE", "用户管理服务暂不可用。")
		return
	}
	users, err := manager.Users(request.Context())
	if err != nil {
		api.writeUserError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, users)
}

func (api *handler) createUser(response http.ResponseWriter, request *http.Request) {
	manager, ok := api.auth.(userManager)
	if !ok {
		api.writeError(response, request, http.StatusServiceUnavailable, "USER_MANAGEMENT_UNAVAILABLE", "用户管理服务暂不可用。")
		return
	}
	var input createUserRequest
	if err := decodeUserRequest(request, &input); err != nil {
		api.writeError(response, request, http.StatusBadRequest, "AUTH_INPUT_INVALID", "用户名或密码格式无效。")
		return
	}
	user, err := manager.CreateUser(request.Context(), input.Username, input.Password)
	if err != nil {
		api.writeUserError(response, request, err)
		return
	}
	writeJSON(response, http.StatusCreated, user)
}

func (api *handler) updateUserStatus(response http.ResponseWriter, request *http.Request) {
	manager, principal, targetID, ok := api.userOperationContext(response, request)
	if !ok {
		return
	}
	var input userStatusRequest
	if err := decodeUserRequest(request, &input); err != nil {
		api.writeError(response, request, http.StatusBadRequest, "AUTH_INPUT_INVALID", "账号状态参数无效。")
		return
	}
	user, err := manager.SetUserDisabled(request.Context(), principal.ID, targetID, input.Disabled)
	if err != nil {
		api.writeUserError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, user)
}

func (api *handler) updateUserPassword(response http.ResponseWriter, request *http.Request) {
	manager, principal, targetID, ok := api.userOperationContext(response, request)
	if !ok {
		return
	}
	var input userPasswordRequest
	if err := decodeUserRequest(request, &input); err != nil {
		api.writeError(response, request, http.StatusBadRequest, "AUTH_INPUT_INVALID", "密码参数无效。")
		return
	}
	if err := manager.UpdatePassword(request.Context(), principal.ID, targetID, input.CurrentPassword, input.NewPassword); err != nil {
		api.writeUserError(response, request, err)
		return
	}
	if principal.ID == targetID {
		api.clearSessionCookie(response)
	}
	response.WriteHeader(http.StatusNoContent)
}

func (api *handler) deleteUser(response http.ResponseWriter, request *http.Request) {
	manager, principal, targetID, ok := api.userOperationContext(response, request)
	if !ok {
		return
	}
	if err := manager.DeleteUser(request.Context(), principal.ID, targetID); err != nil {
		api.writeUserError(response, request, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (api *handler) userOperationContext(response http.ResponseWriter, request *http.Request) (userManager, auth.Principal, int64, bool) {
	manager, ok := api.auth.(userManager)
	if !ok {
		api.writeError(response, request, http.StatusServiceUnavailable, "USER_MANAGEMENT_UNAVAILABLE", "用户管理服务暂不可用。")
		return nil, auth.Principal{}, 0, false
	}
	principal, ok := request.Context().Value(principalContextKey{}).(auth.Principal)
	if !ok {
		api.writeError(response, request, http.StatusUnauthorized, "AUTH_UNAUTHORIZED", "请先登录。")
		return nil, auth.Principal{}, 0, false
	}
	targetID, err := strconv.ParseInt(chi.URLParam(request, "userID"), 10, 64)
	if err != nil || targetID <= 0 {
		api.writeError(response, request, http.StatusBadRequest, "AUTH_INPUT_INVALID", "用户标识无效。")
		return nil, auth.Principal{}, 0, false
	}
	return manager, principal, targetID, true
}

func (api *handler) writeUserError(response http.ResponseWriter, request *http.Request, err error) {
	switch auth.ErrorCode(err) {
	case "AUTH_INPUT_INVALID":
		api.writeError(response, request, http.StatusBadRequest, "AUTH_INPUT_INVALID", "用户名或密码不符合当前密码规则。")
	case "AUTH_USERNAME_EXISTS":
		api.writeError(response, request, http.StatusConflict, "AUTH_USERNAME_EXISTS", "该用户名已存在。")
	case "AUTH_USER_NOT_FOUND":
		api.writeError(response, request, http.StatusNotFound, "AUTH_USER_NOT_FOUND", "目标账号不存在。")
	case "AUTH_CURRENT_USER_PROTECTED":
		api.writeError(response, request, http.StatusConflict, "AUTH_CURRENT_USER_PROTECTED", "不能禁用或删除当前登录账号。")
	case "AUTH_LAST_USER_PROTECTED":
		api.writeError(response, request, http.StatusConflict, "AUTH_LAST_USER_PROTECTED", "至少需要保留一个可用账号。")
	case "AUTH_CURRENT_PASSWORD_INVALID":
		api.writeError(response, request, http.StatusUnauthorized, "AUTH_CURRENT_PASSWORD_INVALID", "当前密码不正确。")
	default:
		api.writeError(response, request, http.StatusInternalServerError, "USER_OPERATION_FAILED", "用户操作失败，请稍后重试。")
	}
}

func decodeUserRequest(request *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxCredentialsBodySize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("payload contains multiple values")
	}
	return nil
}
