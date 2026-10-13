package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestStreamLogoutRevokesOnlyMatchingSession(t *testing.T) {
	clock := newStreamTestClock()
	service, root, first := streamTestService(t, clock, Options{})
	second := streamTestLogin(t, service, root.Username)
	other, err := service.CreateUser(context.Background(), "other", "secret-other")
	if err != nil {
		t.Fatal(err)
	}
	otherSession, err := service.Login(context.Background(), other.Username, "secret-other")
	if err != nil {
		t.Fatal(err)
	}
	// 四种路由共享同一 cookie 时均有独立租约。
	leases := make([]*StreamLease, 4)
	for index := range leases {
		leases[index] = streamTestAcquire(t, service, context.Background(), first.Token)
	}
	secondLease := streamTestAcquire(t, service, context.Background(), second.Token)
	otherLease := streamTestAcquire(t, service, context.Background(), otherSession.Token)
	if err := service.Logout(context.Background(), first.Token); err != nil {
		t.Fatal(err)
	}
	for _, lease := range leases {
		assertStreamCause(t, lease, StreamLoggedOut)
		lease.Release()
		lease.Release()
	}
	if secondLease.Context.Err() != nil || otherLease.Context.Err() != nil {
		t.Fatal("logout revoked an independent session")
	}
	if _, err := service.AcquireStream(context.Background(), first.Token); ErrorCode(err) != "AUTH_UNAUTHORIZED" {
		t.Fatalf("revoked admission code = %q", ErrorCode(err))
	}
	assertStreamResources(t, service, clock, 2)
	secondLease.Release()
	otherLease.Release()
	assertStreamResources(t, service, clock, 0)
}

func TestStreamUserChangesRevokeEveryTargetSession(t *testing.T) {
	for _, operation := range []string{"password", "disable", "delete"} {
		t.Run(operation, func(t *testing.T) {
			clock := newStreamTestClock()
			service, root, actorSession := streamTestService(t, clock, Options{})
			target, err := service.CreateUser(context.Background(), "target", "target-password")
			if err != nil {
				t.Fatal(err)
			}
			var targetLeases []*StreamLease
			var tokens []string
			for range 2 {
				session, err := service.Login(context.Background(), "target", "target-password")
				if err != nil {
					t.Fatal(err)
				}
				tokens = append(tokens, session.Token)
				for range 2 {
					targetLeases = append(targetLeases, streamTestAcquire(t, service, context.Background(), session.Token))
				}
			}
			actorLease := streamTestAcquire(t, service, context.Background(), actorSession.Token)
			var want StreamEndReason
			switch operation {
			case "password":
				err = service.UpdatePassword(context.Background(), root.ID, target.ID, "", "next-password")
				want = StreamPasswordChanged
			case "disable":
				_, err = service.SetUserDisabled(context.Background(), root.ID, target.ID, true)
				want = StreamUserDisabled
			case "delete":
				err = service.DeleteUser(context.Background(), root.ID, target.ID)
				want = StreamUserDeleted
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, lease := range targetLeases {
				assertStreamCause(t, lease, want)
			}
			if actorLease.Context.Err() != nil {
				t.Fatal("target change revoked actor")
			}
			if operation == "disable" {
				if _, err := service.SetUserDisabled(context.Background(), root.ID, target.ID, false); err != nil {
					t.Fatal(err)
				}
			}
			for _, token := range tokens {
				if _, err := service.AcquireStream(context.Background(), token); ErrorCode(err) != "AUTH_UNAUTHORIZED" {
					t.Fatalf("old admission code = %q", ErrorCode(err))
				}
			}
			assertStreamResources(t, service, clock, 1)
			actorLease.Release()
			assertStreamResources(t, service, clock, 0)
		})
	}
}

func TestStreamSelfPasswordChangeRevokesActor(t *testing.T) {
	clock := newStreamTestClock()
	service, root, session := streamTestService(t, clock, Options{})
	lease := streamTestAcquire(t, service, context.Background(), session.Token)
	if err := service.UpdatePassword(context.Background(), root.ID, root.ID, "root-password", "next-password"); err != nil {
		t.Fatal(err)
	}
	assertStreamCause(t, lease, StreamPasswordChanged)
	assertStreamResources(t, service, clock, 0)
}

func TestStreamExpiryUsesDatabaseSecondAndInjectedTimer(t *testing.T) {
	clock := newStreamTestClock()
	clock.Advance(750 * time.Millisecond)
	service, _, session := streamTestService(t, clock, Options{SessionLifetime: 5 * time.Second})
	lease := streamTestAcquire(t, service, context.Background(), session.Token)
	if want := time.Unix(session.ExpiresAt.Unix(), 0).UTC(); lease.ExpiresAt != want {
		t.Fatalf("expiry = %s, want database second %s", lease.ExpiresAt, want)
	}
	// 超过 SQL 查询的 2 秒预算仍有效；无需下一次请求或 SSE tick。
	clock.Advance(3 * time.Second)
	if lease.Context.Err() != nil {
		t.Fatal("query timeout became stream lifetime")
	}
	clock.Advance(1249 * time.Millisecond)
	if lease.Context.Err() != nil {
		t.Fatal("stream expired before database boundary")
	}
	clock.Advance(time.Millisecond)
	assertStreamCause(t, lease, StreamExpired)
	assertStreamResources(t, service, clock, 0)
	if _, err := service.AcquireStream(context.Background(), session.Token); ErrorCode(err) != "AUTH_UNAUTHORIZED" {
		t.Fatalf("expired admission code = %q", ErrorCode(err))
	}
}

func TestStreamAdmissionLimitAndParentCancellation(t *testing.T) {
	clock := newStreamTestClock()
	service, _, session := streamTestService(t, clock, Options{StreamLimit: 2})
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.AcquireStream(cancelled, session.Token); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled admission error = %v", err)
	}
	for _, token := range []string{"", "not-a-session"} {
		if _, err := service.AcquireStream(context.Background(), token); ErrorCode(err) != "AUTH_UNAUTHORIZED" {
			t.Fatalf("unauthorized admission code = %q", ErrorCode(err))
		}
	}
	assertStreamResources(t, service, clock, 0)
	parent, cancelParent := context.WithCancelCause(context.Background())
	first := streamTestAcquire(t, service, parent, session.Token)
	second := streamTestAcquire(t, service, context.Background(), session.Token)
	if _, err := service.AcquireStream(context.Background(), session.Token); ErrorCode(err) != "AUTH_STREAM_LIMIT_REACHED" {
		t.Fatalf("limit code = %q", ErrorCode(err))
	}
	if _, err := service.Authenticate(context.Background(), session.Token); err != nil {
		t.Fatal("limit changed database session")
	}
	cause := errors.New("request owner stopped")
	cancelParent(cause)
	assertStreamCause(t, first, cause)
	awaitStreamCondition(t, func() bool { return streamCount(service) == 1 && clock.Active() == 1 })
	third := streamTestAcquire(t, service, context.Background(), session.Token)
	second.Release()
	third.Release()
	assertStreamResources(t, service, clock, 0)
}

func TestStreamExpiryDuringRegistrationReclaimsLateTimer(t *testing.T) {
	clock := newStreamTestClock()
	service, _, session := streamTestService(t, clock, Options{})
	service.afterFunc = func(delay time.Duration, callback func()) Timer {
		timer := clock.AfterFunc(delay, callback)
		clock.Advance(delay) // 同步到期，发生在 timer 停止句柄安装之前。
		return timer
	}
	if _, err := service.AcquireStream(context.Background(), session.Token); ErrorCode(err) != "AUTH_UNAUTHORIZED" {
		t.Fatalf("same-tick expiry code = %q", ErrorCode(err))
	}
	assertStreamResources(t, service, clock, 0)
}

func TestStreamParentCancellationDuringRegistrationReclaimsLateTimer(t *testing.T) {
	clock := newStreamTestClock()
	service, _, session := streamTestService(t, clock, Options{})
	parent, cancel := context.WithCancel(context.Background())
	service.afterFunc = func(delay time.Duration, callback func()) Timer {
		timer := clock.AfterFunc(delay, callback)
		cancel()
		return timer
	}
	if _, err := service.AcquireStream(parent, session.Token); !errors.Is(err, context.Canceled) {
		t.Fatalf("same-tick cancellation error = %v", err)
	}
	assertStreamResources(t, service, clock, 0)
}

func TestStreamAcquireRegistrationAndLogoutAreLinearized(t *testing.T) {
	clock := newStreamTestClock()
	blocking := &streamBlockingClock{clock: clock}
	service, _, session := streamTestService(t, clock, Options{Clock: blocking.Now})
	reached, resume := blocking.BlockAfter(1) // SQL 验证后、登记前的时钟检查。
	acquired := make(chan *StreamLease, 1)
	acquireError := make(chan error, 1)
	go func() {
		lease, err := service.AcquireStream(context.Background(), session.Token)
		acquired <- lease
		acquireError <- err
	}()
	awaitStreamSignal(t, reached)
	if service.lifecycleMu.TryLock() {
		service.lifecycleMu.Unlock()
		t.Fatal("authorization gate released before registration")
	}
	logoutDone := make(chan error, 1)
	go func() { logoutDone <- service.Logout(context.Background(), session.Token) }()
	close(resume)
	lease := awaitStreamValue(t, acquired)
	if err := awaitStreamValue(t, acquireError); err != nil {
		t.Fatal(err)
	}
	if err := awaitStreamValue(t, logoutDone); err != nil {
		t.Fatal(err)
	}
	assertStreamCause(t, lease, StreamLoggedOut)
	assertStreamResources(t, service, clock, 0)
}

func TestStreamAdmissionCannotCrossDisableCommit(t *testing.T) {
	clock := newStreamTestClock()
	blocking := &streamBlockingClock{clock: clock}
	service, root, _ := streamTestService(t, clock, Options{Clock: blocking.Now})
	target, err := service.CreateUser(context.Background(), "target", "target-password")
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.Login(context.Background(), target.Username, "target-password")
	if err != nil {
		t.Fatal(err)
	}
	reached, resume := blocking.BlockAfter(0) // 事务复核后、UPDATE/Commit 前。
	disabled := make(chan error, 1)
	go func() {
		_, err := service.SetUserDisabled(context.Background(), root.ID, target.ID, true)
		disabled <- err
	}()
	awaitStreamSignal(t, reached)
	if service.lifecycleMu.TryRLock() {
		service.lifecycleMu.RUnlock()
		t.Fatal("revocation transaction does not hold admission gate")
	}
	admitted := make(chan error, 1)
	go func() {
		lease, err := service.AcquireStream(context.Background(), session.Token)
		if lease != nil {
			lease.Release()
		}
		admitted <- err
	}()
	close(resume)
	if err := awaitStreamValue(t, disabled); err != nil {
		t.Fatal(err)
	}
	if err := awaitStreamValue(t, admitted); ErrorCode(err) != "AUTH_UNAUTHORIZED" {
		t.Fatalf("post-commit admission code = %q", ErrorCode(err))
	}
	assertStreamResources(t, service, clock, 0)
}

func TestStreamFailedMutationsDoNotRevoke(t *testing.T) {
	clock := newStreamTestClock()
	service, root, session := streamTestService(t, clock, Options{})
	lease := streamTestAcquire(t, service, context.Background(), session.Token)
	cases := []struct {
		name string
		code string
		run  func() error
	}{
		{"bad-id", "AUTH_INPUT_INVALID", func() error { return service.DeleteUser(context.Background(), root.ID, 0) }},
		{"current-user", "AUTH_CURRENT_USER_PROTECTED", func() error { return service.DeleteUser(context.Background(), root.ID, root.ID) }},
		{"last-user-delete", "AUTH_LAST_USER_PROTECTED", func() error { return service.DeleteUser(context.Background(), root.ID+1, root.ID) }},
		{"last-user-disable", "AUTH_LAST_USER_PROTECTED", func() error {
			_, err := service.SetUserDisabled(context.Background(), root.ID+1, root.ID, true)
			return err
		}},
		{"wrong-password", "AUTH_CURRENT_PASSWORD_INVALID", func() error {
			return service.UpdatePassword(context.Background(), root.ID, root.ID, "incorrect", "next-password")
		}},
		{"password-policy", "AUTH_INPUT_INVALID", func() error {
			return service.UpdatePassword(context.Background(), root.ID, root.ID, "root-password", "short")
		}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if err := item.run(); ErrorCode(err) != item.code {
				t.Fatalf("code = %q, want %q", ErrorCode(err), item.code)
			}
			if lease.Context.Err() != nil {
				t.Fatal("failed mutation revoked lease")
			}
		})
	}
	lease.Release()
	assertStreamResources(t, service, clock, 0)
}

func TestStreamDatabaseRollbackDoesNotRevoke(t *testing.T) {
	for _, operation := range []string{"logout", "password", "disable", "delete"} {
		t.Run(operation, func(t *testing.T) {
			clock := newStreamTestClock()
			service, root, _ := streamTestService(t, clock, Options{})
			target, err := service.CreateUser(context.Background(), "target", "target-password")
			if err != nil {
				t.Fatal(err)
			}
			session, err := service.Login(context.Background(), target.Username, "target-password")
			if err != nil {
				t.Fatal(err)
			}
			lease := streamTestAcquire(t, service, context.Background(), session.Token)
			_, err = service.database.Exec(`CREATE TRIGGER reject_session_delete BEFORE DELETE ON sessions BEGIN SELECT RAISE(ABORT, 'injected failure'); END`)
			if err != nil {
				t.Fatal(err)
			}
			switch operation {
			case "logout":
				err = service.Logout(context.Background(), session.Token)
			case "password":
				err = service.UpdatePassword(context.Background(), root.ID, target.ID, "", "next-password")
			case "disable":
				_, err = service.SetUserDisabled(context.Background(), root.ID, target.ID, true)
			case "delete":
				err = service.DeleteUser(context.Background(), root.ID, target.ID)
			}
			if ErrorCode(err) != "AUTH_DATABASE_WRITE_FAILED" {
				t.Fatalf("injected failure code = %q", ErrorCode(err))
			}
			if lease.Context.Err() != nil {
				t.Fatal("rolled-back mutation revoked lease")
			}
			if _, err := service.Authenticate(context.Background(), session.Token); err != nil {
				t.Fatal("rollback did not preserve session")
			}
			if _, err := service.Login(context.Background(), target.Username, "target-password"); err != nil {
				t.Fatal("rollback did not preserve password")
			}
			assertStreamResources(t, service, clock, 1)
			lease.Release()
			assertStreamResources(t, service, clock, 0)
		})
	}
}

func TestStreamCommittedDisableRevokesBeforeResultReadFails(t *testing.T) {
	clock := newStreamTestClock()
	service, root, _ := streamTestService(t, clock, Options{})
	target, err := service.CreateUser(context.Background(), "target", "target-password")
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.Login(context.Background(), target.Username, "target-password")
	if err != nil {
		t.Fatal(err)
	}
	lease := streamTestAcquire(t, service, context.Background(), session.Token)
	_, err = service.database.Exec(`CREATE TRIGGER spoil_disable_result AFTER UPDATE OF disabled ON users BEGIN UPDATE users SET last_login_at_unix = 'unreadable' WHERE id = NEW.id; END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetUserDisabled(context.Background(), root.ID, target.ID, true); ErrorCode(err) != "AUTH_DATABASE_QUERY_FAILED" {
		t.Fatalf("result read code = %q", ErrorCode(err))
	}
	assertStreamCause(t, lease, StreamUserDisabled)
	assertStreamResources(t, service, clock, 0)
	if _, err := service.Authenticate(context.Background(), session.Token); ErrorCode(err) != "AUTH_UNAUTHORIZED" {
		t.Fatalf("committed session code = %q", ErrorCode(err))
	}
}

func TestStreamCloseAndConcurrentCleanupAreIdempotent(t *testing.T) {
	clock := newStreamTestClock()
	service, _, session := streamTestService(t, clock, Options{})
	parent, cancel := context.WithCancel(context.Background())
	leases := make([]*StreamLease, 64)
	for index := range leases {
		leases[index] = streamTestAcquire(t, service, parent, session.Token)
	}
	start := make(chan struct{})
	var wait sync.WaitGroup
	for _, lease := range leases {
		wait.Go(func() {
			<-start
			lease.Release()
			lease.Release()
		})
	}
	wait.Go(func() { <-start; clock.Advance(24 * time.Hour) })
	wait.Go(func() { <-start; cancel() })
	wait.Go(func() { <-start; _ = service.Logout(context.Background(), session.Token) })
	wait.Go(func() { <-start; _ = service.Close() })
	close(start)
	done := make(chan struct{})
	go func() { wait.Wait(); close(done) }()
	awaitStreamSignal(t, done)
	for _, lease := range leases {
		if lease.Context.Err() == nil {
			t.Fatal("concurrent cleanup left active stream")
		}
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	assertStreamResources(t, service, clock, 0)
	if _, err := service.AcquireStream(context.Background(), session.Token); ErrorCode(err) != "AUTH_STREAM_UNAVAILABLE" {
		t.Fatalf("closed admission code = %q", ErrorCode(err))
	}
}

func TestStreamCloseWaitsForAdmissionAndRejectsNewLeases(t *testing.T) {
	clock := newStreamTestClock()
	blocking := &streamBlockingClock{clock: clock}
	service, _, session := streamTestService(t, clock, Options{Clock: blocking.Now})
	reached, resume := blocking.BlockAfter(1)
	acquired := make(chan *StreamLease, 1)
	go func() {
		lease, _ := service.AcquireStream(context.Background(), session.Token)
		acquired <- lease
	}()
	awaitStreamSignal(t, reached)
	closed := make(chan error, 1)
	go func() { closed <- service.Close() }()
	close(resume)
	lease := awaitStreamValue(t, acquired)
	if lease == nil {
		t.Fatal("admission before Close was not registered")
	}
	if err := awaitStreamValue(t, closed); err != nil {
		t.Fatal(err)
	}
	assertStreamCause(t, lease, StreamServiceClosed)
	assertStreamResources(t, service, clock, 0)
	if _, err := service.AcquireStream(context.Background(), session.Token); ErrorCode(err) != "AUTH_STREAM_UNAVAILABLE" {
		t.Fatalf("closed admission code = %q", ErrorCode(err))
	}
}

func TestStreamDelayedLoginRechecksCredentialAndState(t *testing.T) {
	for _, operation := range []string{"password", "disable", "delete", "delete-recreate"} {
		t.Run(operation, func(t *testing.T) {
			clock := newStreamTestClock()
			random := &streamBlockingReader{reader: rand.Reader}
			service, root, _ := streamTestService(t, clock, Options{Random: random})
			target, err := service.CreateUser(context.Background(), "target", "target-password")
			if err != nil {
				t.Fatal(err)
			}
			reached, resume := random.BlockNext()
			loggedIn := make(chan error, 1)
			go func() {
				_, err := service.Login(context.Background(), target.Username, "target-password")
				loggedIn <- err
			}()
			awaitStreamSignal(t, reached) // 已完成旧密码 bcrypt 验证，但尚未取得提交锁。
			switch operation {
			case "password":
				err = service.UpdatePassword(context.Background(), root.ID, target.ID, "", "next-password")
			case "disable":
				_, err = service.SetUserDisabled(context.Background(), root.ID, target.ID, true)
			case "delete", "delete-recreate":
				err = service.DeleteUser(context.Background(), root.ID, target.ID)
				if err == nil && operation == "delete-recreate" {
					var replacement User
					replacement, err = service.CreateUser(context.Background(), target.Username, "target-password")
					if err == nil && replacement.ID != target.ID {
						t.Fatal("test did not reuse deleted SQLite user ID")
					}
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			close(resume)
			if err := awaitStreamValue(t, loggedIn); ErrorCode(err) != "AUTH_INVALID_CREDENTIALS" {
				t.Fatalf("delayed login code = %q", ErrorCode(err))
			}
			var count int
			if err := service.database.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, target.ID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("stale login inserted %d sessions", count)
			}
		})
	}
}

func TestStreamDelayedPasswordUpdateCannotOverwriteNewCredentialsOrState(t *testing.T) {
	for _, operation := range []string{"password", "disable", "delete"} {
		t.Run(operation, func(t *testing.T) {
			clock := newStreamTestClock()
			blocking := &streamBlockingClock{clock: clock}
			service, root, _ := streamTestService(t, clock, Options{Clock: blocking.Now})
			target, err := service.CreateUser(context.Background(), "target", "target-password")
			if err != nil {
				t.Fatal(err)
			}
			reached, resume := blocking.BlockAfter(0) // 当前密码已核验，新 hash 已计算。
			updated := make(chan error, 1)
			go func() {
				updated <- service.UpdatePassword(context.Background(), target.ID, target.ID, "target-password", "late-password")
			}()
			awaitStreamSignal(t, reached)
			want := "AUTH_CURRENT_PASSWORD_INVALID"
			switch operation {
			case "password":
				err = service.UpdatePassword(context.Background(), root.ID, target.ID, "", "winner-password")
			case "disable":
				_, err = service.SetUserDisabled(context.Background(), root.ID, target.ID, true)
			case "delete":
				err = service.DeleteUser(context.Background(), root.ID, target.ID)
				want = "AUTH_USER_NOT_FOUND"
			}
			if err != nil {
				t.Fatal(err)
			}
			close(resume)
			if err := awaitStreamValue(t, updated); ErrorCode(err) != want {
				t.Fatalf("delayed update code = %q, want %q", ErrorCode(err), want)
			}
			if operation == "password" {
				if _, err := service.Login(context.Background(), target.Username, "winner-password"); err != nil {
					t.Fatal("late update replaced committed password")
				}
			}
		})
	}
}

func streamTestService(t *testing.T, clock *streamTestClock, options Options) (*Service, Principal, Session) {
	t.Helper()
	if options.Clock == nil {
		options.Clock = clock.Now
	}
	if options.AfterFunc == nil {
		options.AfterFunc = clock.AfterFunc
	}
	service := openTestService(t, options)
	root, err := service.Bootstrap(context.Background(), "root", "root-password")
	if err != nil {
		t.Fatal(err)
	}
	return service, root, streamTestLogin(t, service, root.Username)
}

func streamTestLogin(t *testing.T, service *Service, username string) Session {
	t.Helper()
	session, err := service.Login(context.Background(), username, "root-password")
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func streamTestAcquire(t *testing.T, service *Service, parent context.Context, token string) *StreamLease {
	t.Helper()
	lease, err := service.AcquireStream(parent, token)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lease.Release)
	return lease
}

func assertStreamCause(t *testing.T, lease *StreamLease, want error) {
	t.Helper()
	if lease.Context.Err() == nil || !errors.Is(context.Cause(lease.Context), want) {
		t.Fatalf("stream cause = %v, want %v", context.Cause(lease.Context), want)
	}
}

func streamCount(service *Service) int {
	service.streamsMu.Lock()
	defer service.streamsMu.Unlock()
	return len(service.streams)
}

func assertStreamResources(t *testing.T, service *Service, clock *streamTestClock, want int) {
	t.Helper()
	if count := streamCount(service); count != want {
		t.Fatalf("active leases = %d, want %d", count, want)
	}
	if count := clock.Active(); count != want {
		t.Fatalf("active timers = %d, want %d", count, want)
	}
}

func awaitStreamSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	awaitStreamValue(t, signal)
}

func awaitStreamValue[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for deterministic test barrier")
		var zero T
		return zero
	}
}

func awaitStreamCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for cancellation cleanup")
		}
		runtime.Gosched()
	}
}

type streamTestClock struct {
	mu     sync.Mutex
	now    time.Time
	timers map[*streamTestTimer]struct{}
}

type streamTestTimer struct {
	clock    *streamTestClock
	at       time.Time
	callback func()
}

func newStreamTestClock() *streamTestClock {
	return &streamTestClock{now: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC), timers: make(map[*streamTestTimer]struct{})}
}

func (clock *streamTestClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *streamTestClock) AfterFunc(delay time.Duration, callback func()) Timer {
	clock.mu.Lock()
	timer := &streamTestTimer{clock: clock, at: clock.now.Add(delay), callback: callback}
	clock.timers[timer] = struct{}{}
	clock.mu.Unlock()
	return timer
}

func (clock *streamTestClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	var callbacks []func()
	for timer := range clock.timers {
		if !timer.at.After(clock.now) {
			delete(clock.timers, timer)
			callbacks = append(callbacks, timer.callback)
		}
	}
	clock.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
}

func (clock *streamTestClock) Active() int {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return len(clock.timers)
}

func (timer *streamTestTimer) Stop() bool {
	timer.clock.mu.Lock()
	defer timer.clock.mu.Unlock()
	_, active := timer.clock.timers[timer]
	delete(timer.clock.timers, timer)
	return active
}

type streamBlockingClock struct {
	mu      sync.Mutex
	clock   *streamTestClock
	skip    int
	reached chan struct{}
	resume  chan struct{}
}

func (clock *streamBlockingClock) BlockAfter(skip int) (<-chan struct{}, chan struct{}) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.skip = skip
	clock.reached, clock.resume = make(chan struct{}), make(chan struct{})
	return clock.reached, clock.resume
}

func (clock *streamBlockingClock) Now() time.Time {
	clock.mu.Lock()
	reached, resume := clock.reached, clock.resume
	if reached != nil && clock.skip > 0 {
		clock.skip--
		reached = nil
	} else if reached != nil {
		clock.reached, clock.resume = nil, nil
	}
	clock.mu.Unlock()
	if reached != nil {
		close(reached)
		<-resume
	}
	return clock.clock.Now()
}

type streamBlockingReader struct {
	mu      sync.Mutex
	reader  io.Reader
	reached chan struct{}
	resume  chan struct{}
}

func (reader *streamBlockingReader) BlockNext() (<-chan struct{}, chan struct{}) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	reader.reached, reader.resume = make(chan struct{}), make(chan struct{})
	return reader.reached, reader.resume
}

func (reader *streamBlockingReader) Read(buffer []byte) (int, error) {
	reader.mu.Lock()
	reached, resume := reader.reached, reader.resume
	reader.reached, reader.resume = nil, nil
	reader.mu.Unlock()
	if reached != nil {
		close(reached)
		<-resume
	}
	return reader.reader.Read(buffer)
}
