package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/auth"
)

const (
	sessionCookieName      = "ncp_session"
	maxCredentialsBodySize = 8 * 1024
)

type Authenticator interface {
	Initialized(context.Context) (bool, error)
	Bootstrap(context.Context, string, string) (auth.Principal, error)
	Login(context.Context, string, string) (auth.Session, error)
	Authenticate(context.Context, string) (auth.Principal, error)
	Logout(context.Context, string) error
}

type AuthStatusResponse struct {
	Initialized   bool            `json:"initialized"`
	Authenticated bool            `json:"authenticated"`
	User          *auth.Principal `json:"user,omitempty"`
}

type AuthSessionResponse struct {
	User      auth.Principal `json:"user"`
	ExpiresAt time.Time      `json:"expiresAt"`
}

type credentialsRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type principalContextKey struct{}

func (api *handler) authStatus(response http.ResponseWriter, request *http.Request) {
	if api.auth == nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "登录服务暂不可用。")
		return
	}
	initialized, err := api.auth.Initialized(request.Context())
	if err != nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "登录服务暂不可用。")
		return
	}
	status := AuthStatusResponse{Initialized: initialized}
	if principal, ok := api.optionalPrincipal(request.Context(), request); ok {
		status.Authenticated = true
		status.User = &principal
	}
	writeJSON(response, http.StatusOK, status)
}

func (api *handler) bootstrap(response http.ResponseWriter, request *http.Request) {
	if api.auth == nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "登录服务暂不可用。")
		return
	}
	credentials, err := decodeCredentials(request)
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "AUTH_INPUT_INVALID", "用户名或密码格式无效。")
		return
	}
	if _, err := api.auth.Bootstrap(request.Context(), credentials.Username, credentials.Password); err != nil {
		api.writeAuthError(response, request, err)
		return
	}
	session, err := api.auth.Login(request.Context(), credentials.Username, credentials.Password)
	if err != nil {
		api.writeAuthError(response, request, err)
		return
	}
	api.writeSessionCookie(response, session)
	writeJSON(response, http.StatusCreated, AuthSessionResponse{User: session.Principal, ExpiresAt: session.ExpiresAt})
}

func (api *handler) login(response http.ResponseWriter, request *http.Request) {
	if api.auth == nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "登录服务暂不可用。")
		return
	}
	credentials, err := decodeCredentials(request)
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "AUTH_INPUT_INVALID", "用户名或密码格式无效。")
		return
	}
	session, err := api.auth.Login(request.Context(), credentials.Username, credentials.Password)
	if err != nil {
		api.writeAuthError(response, request, err)
		return
	}
	api.writeSessionCookie(response, session)
	writeJSON(response, http.StatusOK, AuthSessionResponse{User: session.Principal, ExpiresAt: session.ExpiresAt})
}

func (api *handler) logout(response http.ResponseWriter, request *http.Request) {
	if api.auth != nil {
		if cookie, err := request.Cookie(sessionCookieName); err == nil {
			if err := api.auth.Logout(request.Context(), cookie.Value); err != nil {
				api.writeAuthError(response, request, err)
				return
			}
		}
	}
	api.clearSessionCookie(response)
	response.WriteHeader(http.StatusNoContent)
}

func (api *handler) requireAuthentication(next http.Handler) http.Handler {
	if api.auth == nil {
		return next
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		principal, ok := api.optionalPrincipal(request.Context(), request)
		if !ok {
			api.writeError(response, request, http.StatusUnauthorized, "AUTH_UNAUTHORIZED", "请先以 Root 账号登录。")
			return
		}
		contextWithPrincipal := context.WithValue(request.Context(), principalContextKey{}, principal)
		next.ServeHTTP(response, request.WithContext(contextWithPrincipal))
	})
}

func (api *handler) optionalPrincipal(ctx context.Context, request *http.Request) (auth.Principal, bool) {
	if api.auth == nil {
		return auth.Principal{}, false
	}
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil {
		return auth.Principal{}, false
	}
	principal, err := api.auth.Authenticate(ctx, cookie.Value)
	if err != nil {
		return auth.Principal{}, false
	}
	return principal, true
}

func (api *handler) writeSessionCookie(response http.ResponseWriter, session auth.Session) {
	maxAge := int(time.Until(session.ExpiresAt).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    session.Token,
		Path:     "/",
		Expires:  session.ExpiresAt,
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   api.sessionCookieSecure,
	})
}

func (api *handler) clearSessionCookie(response http.ResponseWriter) {
	http.SetCookie(response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   api.sessionCookieSecure,
	})
}

func (api *handler) writeAuthError(response http.ResponseWriter, request *http.Request, err error) {
	switch auth.ErrorCode(err) {
	case "AUTH_INPUT_INVALID":
		api.writeError(response, request, http.StatusBadRequest, "AUTH_INPUT_INVALID", "用户名或密码格式无效。")
	case "AUTH_ALREADY_INITIALIZED":
		api.writeError(response, request, http.StatusConflict, "AUTH_ALREADY_INITIALIZED", "Root 账号已经初始化，请直接登录。")
	case "AUTH_INVALID_CREDENTIALS", "AUTH_UNAUTHORIZED":
		api.writeError(response, request, http.StatusUnauthorized, "AUTH_INVALID_CREDENTIALS", "用户名或密码不正确。")
	default:
		api.writeError(response, request, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "登录服务暂不可用。")
	}
}

func decodeCredentials(request *http.Request) (credentialsRequest, error) {
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxCredentialsBodySize))
	decoder.DisallowUnknownFields()
	var credentials credentialsRequest
	if err := decoder.Decode(&credentials); err != nil {
		return credentialsRequest{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return credentialsRequest{}, errors.New("credentials payload contains multiple values")
	}
	return credentials, nil
}
