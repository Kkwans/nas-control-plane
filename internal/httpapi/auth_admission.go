package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	authAttemptInterval    = 5 * time.Second
	authAttemptBurst       = 10
	authAttemptConcurrency = 2
)

// authAdmission 只保存 handler 级固定状态，不按来源或账号建立配额。
// credit 以时间表示连续补充的预算，避免浮点误差。
type authAdmission struct {
	mu     sync.Mutex
	now    func() time.Time
	last   time.Time
	credit time.Duration
	active int
}

func newAuthAdmission(now func() time.Time) *authAdmission {
	return &authAdmission{now: now, last: now(), credit: authAttemptBurst * authAttemptInterval}
}

// acquire 不排队；调用者必须在整个凭据操作结束后释放许可。
func (gate *authAdmission) acquire(ctx context.Context) (release func(), retryAfter int) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if ctx.Err() != nil {
		return nil, 0
	}
	now := gate.now() // 生产时保留 time.Now 的单调时钟信息。
	if elapsed := now.Sub(gate.last); elapsed > 0 {
		maximum := authAttemptBurst * authAttemptInterval
		if elapsed >= maximum-gate.credit {
			gate.credit = maximum
		} else {
			gate.credit += elapsed
		}
		gate.last = now
	}
	if gate.credit < authAttemptInterval {
		remaining := authAttemptInterval - gate.credit
		return nil, int((remaining + time.Second - 1) / time.Second)
	}
	if gate.active >= authAttemptConcurrency {
		return nil, 1
	}
	// 时钟注入或锁竞争期间也可能取消；取消请求不扣预算。
	if ctx.Err() != nil {
		return nil, 0
	}
	gate.credit -= authAttemptInterval
	gate.active++
	var once sync.Once
	return func() {
		once.Do(func() {
			gate.mu.Lock()
			gate.active--
			gate.mu.Unlock()
		})
	}, 0
}

func (api *handler) admitCredentials(response http.ResponseWriter, request *http.Request) (func(), bool) {
	release, retryAfter := api.authAdmission.acquire(request.Context())
	if release != nil {
		return release, true
	}
	if retryAfter == 0 {
		api.writeAuthError(response, request, request.Context().Err())
	} else {
		response.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		api.writeError(response, request, http.StatusTooManyRequests, "AUTH_RATE_LIMITED", "登录尝试过于频繁，请稍后重试。")
	}
	return nil, false
}
