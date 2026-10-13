package httpapi

import (
	"context"
	"net/http"

	"github.com/Kkwans/nas-control-plane/internal/auth"
)

// StreamAuthenticator 是长连接所需的实时会话能力；普通入站认证不能替代租约。
type StreamAuthenticator interface {
	AcquireStream(context.Context, string) (*auth.StreamLease, error)
}

// 生产注入的 Service 必须提供实时会话能力。
var _ StreamAuthenticator = (*auth.Service)(nil)

func (api *handler) requireLiveSession(next http.Handler) http.Handler {
	// 保留无 Auth 的隔离测试/嵌入语义，不新增生产配置开关。
	if api.auth == nil {
		return next
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		provider, ok := api.auth.(StreamAuthenticator)
		if !ok {
			api.writeError(response, request, http.StatusServiceUnavailable, "AUTH_STREAM_UNAVAILABLE", "实时会话验证暂不可用。")
			return
		}
		cookie, err := request.Cookie(sessionCookieName)
		if err != nil {
			api.writeError(response, request, http.StatusUnauthorized, "AUTH_UNAUTHORIZED", "请先以 Root 账号登录。")
			return
		}
		lease, err := provider.AcquireStream(request.Context(), cookie.Value)
		if err != nil {
			switch auth.ErrorCode(err) {
			case "AUTH_UNAUTHORIZED":
				api.writeError(response, request, http.StatusUnauthorized, "AUTH_UNAUTHORIZED", "登录会话已失效，请重新登录。")
			case "AUTH_STREAM_LIMIT_REACHED":
				api.writeError(response, request, http.StatusServiceUnavailable, "AUTH_STREAM_LIMIT_REACHED", "实时连接数量已达上限，请稍后重试。")
			default:
				api.writeError(response, request, http.StatusServiceUnavailable, "AUTH_STREAM_UNAVAILABLE", "实时会话验证暂不可用。")
			}
			return
		}
		if lease == nil || lease.Context == nil {
			if lease != nil {
				lease.Release()
			}
			api.writeError(response, request, http.StatusServiceUnavailable, "AUTH_STREAM_UNAVAILABLE", "实时会话验证暂不可用。")
			return
		}
		defer lease.Release()
		if lease.Context.Err() != nil {
			api.writeError(response, request, http.StatusUnauthorized, "AUTH_UNAUTHORIZED", "登录会话已失效，请重新登录。")
			return
		}
		if metadata := requestMetadataFromContext(request.Context()); metadata != nil {
			metadata.actor = lease.Principal.Username
		}
		ctx := context.WithValue(lease.Context, principalContextKey{}, lease.Principal)
		next.ServeHTTP(response, request.WithContext(ctx))
	})
}
