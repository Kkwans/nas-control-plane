package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	defaultStreamLimit  = 256
	authDatabaseTimeout = 2 * time.Second
)

// Timer 只需停止尚未执行的回调；Stop 不等待正在执行的回调。
type Timer interface {
	Stop() bool
}

// StreamEndReason 是进程内的稳定取消原因，不包含任何凭据。
type StreamEndReason string

func (reason StreamEndReason) Error() string { return string(reason) }

const (
	StreamLoggedOut       StreamEndReason = "logged_out"
	StreamPasswordChanged StreamEndReason = "password_changed"
	StreamUserDisabled    StreamEndReason = "user_disabled"
	StreamUserDeleted     StreamEndReason = "user_deleted"
	StreamExpired         StreamEndReason = "expired"
	StreamServiceClosed   StreamEndReason = "service_closed"
)

// StreamLease 绑定一次活动流；不得复制或作为 API 响应序列化。
// Context 的结束只撤销持续访问，不表示已经执行的 Root 操作已被回滚。
type StreamLease struct {
	Principal Principal       `json:"-"`
	Context   context.Context `json:"-"`
	ExpiresAt time.Time       `json:"-"`
	service   *Service
}

// Release 可与到期、注销、父 context 取消及 Service.Close 并发调用。
func (lease *StreamLease) Release() {
	if lease != nil && lease.service != nil {
		lease.service.endStream(lease, context.Canceled)
	}
}

type streamRegistration struct {
	tokenHash  [sha256.Size]byte
	userID     int64
	cancel     context.CancelCauseFunc
	stopTimer  func() bool
	stopParent func() bool
	ending     bool
	finish     sync.Once
}

// AcquireStream 在同一个准入读锁内权威验证并登记；撤销事务只能发生在其前或后。
// 即时撤销只覆盖这个 Service 实例，不能替代跨进程的一致性机制。
func (s *Service) AcquireStream(ctx context.Context, token string) (*StreamLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, unauthorized()
	}
	tokenHash := sha256.Sum256([]byte(token))
	s.lifecycleMu.RLock()
	defer s.lifecycleMu.RUnlock()
	if s.closed {
		return nil, coded("AUTH_STREAM_UNAVAILABLE", errors.New("stream admission is closed"))
	}

	queryContext, cancelQuery := context.WithTimeout(ctx, authDatabaseTimeout)
	var principal Principal
	var expiresAtUnix int64
	err := s.database.QueryRowContext(queryContext, `
		SELECT users.id, users.username, users.role, sessions.expires_at_unix
		FROM sessions
		INNER JOIN users ON users.id = sessions.user_id
		WHERE sessions.token_hash = ? AND sessions.expires_at_unix > ? AND users.disabled = 0
	`, tokenHash[:], s.now().UTC().Unix()).Scan(&principal.ID, &principal.Username, &principal.Role, &expiresAtUnix)
	cancelQuery()
	if errors.Is(err, sql.ErrNoRows) {
		return nil, unauthorized()
	}
	if err != nil {
		return nil, coded("AUTH_DATABASE_QUERY_FAILED", err)
	}

	// 查询预算绝不能成为活动流的生命周期。使用原始请求作为父 context。
	streamContext, cancel := context.WithCancelCause(ctx)
	lease := &StreamLease{
		Principal: principal,
		Context:   streamContext,
		ExpiresAt: time.Unix(expiresAtUnix, 0).UTC(),
		service:   s,
	}
	registration := &streamRegistration{tokenHash: tokenHash, userID: principal.ID, cancel: cancel}
	now := s.now()
	s.streamsMu.Lock()
	if err := ctx.Err(); err != nil {
		s.streamsMu.Unlock()
		cancel(err)
		return nil, err
	}
	if !lease.ExpiresAt.After(now) {
		s.streamsMu.Unlock()
		cancel(StreamExpired)
		return nil, unauthorized()
	}
	if len(s.streams) >= s.streamLimit {
		s.streamsMu.Unlock()
		cancel(context.Canceled)
		return nil, coded("AUTH_STREAM_LIMIT_REACHED", errors.New("active stream limit reached"))
	}
	s.streams[lease] = registration
	s.streamsMu.Unlock()

	// 允许测试调度器同步触发；登记先完成，迟到的停止句柄仍会被回收。
	timer := s.afterFunc(lease.ExpiresAt.Sub(s.now()), func() { s.endStream(lease, StreamExpired) })
	s.installStreamStop(lease, timer.Stop, true)
	stopParent := context.AfterFunc(ctx, func() { s.endStream(lease, context.Cause(ctx)) })
	s.installStreamStop(lease, stopParent, false)
	if err := ctx.Err(); err != nil {
		s.endStream(lease, context.Cause(ctx))
		return nil, err
	}
	if !lease.ExpiresAt.After(s.now()) {
		s.endStream(lease, StreamExpired)
		return nil, unauthorized()
	}
	if lease.Context.Err() != nil {
		lease.Release()
		return nil, unauthorized()
	}
	return lease, nil
}

func (s *Service) installStreamStop(lease *StreamLease, stop func() bool, timer bool) {
	s.streamsMu.Lock()
	registration, active := s.streams[lease]
	active = active && !registration.ending
	if active {
		if timer {
			registration.stopTimer = stop
		} else {
			registration.stopParent = stop
		}
	}
	s.streamsMu.Unlock()
	if !active {
		stop()
	}
}

func (s *Service) endStream(lease *StreamLease, cause error) {
	s.streamsMu.Lock()
	registration, active := s.streams[lease]
	if !active {
		s.streamsMu.Unlock()
		return
	}
	registration.ending = true
	stopTimer, stopParent := registration.stopTimer, registration.stopParent
	s.streamsMu.Unlock()

	// 必须先同步取消，再从登记表移除。否则并发 Release 摘除后尚未 cancel，
	// Logout 就可能漏掉该流并过早返回成功。取消和停止回调均不持有登记表锁。
	registration.finish.Do(func() {
		registration.cancel(cause)
		if stopTimer != nil {
			stopTimer()
		}
		if stopParent != nil {
			stopParent()
		}
	})
	s.streamsMu.Lock()
	delete(s.streams, lease)
	s.streamsMu.Unlock()
}

// 调用方持有 lifecycleMu 写锁；只有这里会批量结束某个会话或用户的流。
func (s *Service) revokeStreams(matches func(*streamRegistration) bool, cause error) {
	s.streamsMu.Lock()
	leases := make([]*StreamLease, 0, len(s.streams))
	for lease, registration := range s.streams {
		if matches(registration) {
			leases = append(leases, lease)
		}
	}
	s.streamsMu.Unlock()
	for _, lease := range leases {
		s.endStream(lease, cause)
	}
}

func (s *Service) revokeUserStreams(userID int64, cause StreamEndReason) {
	s.revokeStreams(func(registration *streamRegistration) bool { return registration.userID == userID }, cause)
}

func normalizedAfterFunc(afterFunc func(time.Duration, func()) Timer) func(time.Duration, func()) Timer {
	if afterFunc != nil {
		return afterFunc
	}
	return func(delay time.Duration, callback func()) Timer { return time.AfterFunc(delay, callback) }
}

func normalizedStreamLimit(limit int) int {
	if limit <= 0 {
		return defaultStreamLimit
	}
	return limit
}
