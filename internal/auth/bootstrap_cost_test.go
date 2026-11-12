package auth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestBootstrapCostSkipsHashAfterInitialization(t *testing.T) {
	service := openTestService(t, Options{})
	ctx := context.Background()
	password := strings.Repeat("fixture-", 12)
	var calls int
	generateHash := func(material []byte, cost int) ([]byte, error) {
		calls++
		if !bytes.Equal(material, passwordMaterial(password)) || cost != bcrypt.DefaultCost {
			t.Fatal("hash input or cost changed")
		}
		return []byte("fixture-hash"), nil
	}
	principal, err := service.bootstrap(ctx, "  fixture-root  ", password, generateHash)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || principal.ID == 0 || principal.Username != "fixture-root" || principal.Role != RootRole {
		t.Fatalf("hash calls = %d, principal = %#v", calls, principal)
	}
	var storedHash []byte
	if err := service.database.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, principal.ID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(storedHash, []byte("fixture-hash")) {
		t.Fatal("generated hash was not stored")
	}
	calls = 0
	got, err := service.bootstrap(ctx, "another-root", password, generateHash)
	if ErrorCode(err) != "AUTH_ALREADY_INITIALIZED" || got != (Principal{}) || calls != 0 {
		t.Fatalf("repeat bootstrap: principal = %#v, error = %v, hash calls = %d", got, err, calls)
	}
	assertBootstrapUserCount(t, service, 1)
}

func TestBootstrapCostPreservesInputErrorPrecedence(t *testing.T) {
	service := openTestService(t, Options{})
	if _, err := service.bootstrap(context.Background(), "fixture-root", "fixture-password", func([]byte, int) ([]byte, error) {
		return []byte("fixture-hash"), nil
	}); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, item := range []struct {
		name, username, password, message string
		ctx                               context.Context
	}{
		{"cancelled-before-input", "", "", "", cancelled},
		{"username-before-password", "", "", "username is invalid", context.Background()},
		{"username-before-initialized", "bad user", "fixture-password", "username is invalid", context.Background()},
		{"password-before-initialized", "another-root", "short", "password is invalid", context.Background()},
	} {
		t.Run(item.name, func(t *testing.T) {
			var calls int
			principal, err := service.bootstrap(item.ctx, item.username, item.password, func([]byte, int) ([]byte, error) {
				calls++
				return []byte("fixture-hash"), nil
			})
			if item.ctx.Err() != nil {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled bootstrap error = %v", err)
				}
			} else if ErrorCode(err) != "AUTH_INPUT_INVALID" || errors.Unwrap(err).Error() != item.message {
				t.Fatalf("input error = %v (%v), want %s", err, errors.Unwrap(err), item.message)
			}
			if calls != 0 || principal != (Principal{}) {
				t.Fatalf("invalid request: hash calls = %d, principal = %#v", calls, principal)
			}
		})
	}
	assertBootstrapUserCount(t, service, 1)
}

func TestBootstrapCostQueryFailureSkipsHashAndWrite(t *testing.T) {
	service := openTestService(t, Options{})
	ctx := context.Background()
	// 只破坏 users 查询，密码策略读取仍成功；避免把密码校验失败误当作预检覆盖。
	if _, err := service.database.Exec(`ALTER TABLE users RENAME TO bootstrap_users_fixture`); err != nil {
		t.Fatal(err)
	}
	if err := service.validatePassword(ctx, "fixture-password"); err != nil {
		t.Fatalf("password validation unexpectedly failed: %v", err)
	}
	_, wantErr := service.Initialized(ctx)
	if ErrorCode(wantErr) != "AUTH_DATABASE_QUERY_FAILED" {
		t.Fatalf("injected query error = %v", wantErr)
	}
	var calls int
	principal, err := service.bootstrap(ctx, "fixture-root", "fixture-password", func([]byte, int) ([]byte, error) {
		calls++
		return []byte("fixture-hash"), nil
	})
	if ErrorCode(err) != ErrorCode(wantErr) || errors.Unwrap(err).Error() != errors.Unwrap(wantErr).Error() {
		t.Fatalf("bootstrap error = %v (%v), want initialized query error %v", err, errors.Unwrap(err), wantErr)
	}
	if calls != 0 || principal != (Principal{}) {
		t.Fatalf("failed query: hash calls = %d, principal = %#v", calls, principal)
	}
	if _, err := service.database.Exec(`ALTER TABLE bootstrap_users_fixture RENAME TO users`); err != nil {
		t.Fatal(err)
	}
	assertBootstrapUserCount(t, service, 0)
}

func TestBootstrapCostHashFailureAndCancellationDoNotInsert(t *testing.T) {
	for _, name := range []string{"hash-error", "cancelled-during-hash"} {
		t.Run(name, func(t *testing.T) {
			service := openTestService(t, Options{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			hashErr := errors.New("injected hash failure")
			var calls int
			principal, err := service.bootstrap(ctx, "fixture-root", "fixture-password", func([]byte, int) ([]byte, error) {
				calls++
				if name == "hash-error" {
					return nil, hashErr
				}
				cancel()
				return []byte("fixture-hash"), nil
			})
			if name == "hash-error" {
				if ErrorCode(err) != "AUTH_PASSWORD_HASH_FAILED" || !errors.Is(err, hashErr) {
					t.Fatalf("hash failure = %v", err)
				}
			} else if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled insertion error = %v", err)
			}
			if calls != 1 || principal != (Principal{}) {
				t.Fatalf("failed hash request: calls = %d, principal = %#v", calls, principal)
			}
			assertBootstrapUserCount(t, service, 0)
		})
	}
}

func TestBootstrapCostConcurrentPrechecksStillCreateOneRoot(t *testing.T) {
	service := openTestService(t, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	reached := make(chan struct{}, 2)
	resume := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(resume) })
	var calls atomic.Int32
	generateHash := func([]byte, int) ([]byte, error) {
		calls.Add(1)
		reached <- struct{}{}
		select {
		case <-resume:
			return []byte("fixture-hash"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	type result struct {
		principal Principal
		err       error
	}
	results := make(chan result, 2)
	for _, username := range []string{"first-root", "second-root"} {
		go func() {
			principal, err := service.bootstrap(ctx, username, "fixture-password", generateHash)
			results <- result{principal, err}
		}()
	}
	// 两个请求都进入 hash 才放行，保证都已通过空库预检。
	for range 2 {
		select {
		case <-reached:
		case <-ctx.Done():
			t.Fatal("both bootstrap requests did not pass the precheck")
		}
	}
	release.Do(func() { close(resume) })
	var successes, conflicts int
	var winner Principal
	for range 2 {
		select {
		case got := <-results:
			if got.err == nil {
				successes++
				winner = got.principal
			} else if ErrorCode(got.err) == "AUTH_ALREADY_INITIALIZED" && got.principal == (Principal{}) {
				conflicts++
			} else {
				t.Fatalf("concurrent bootstrap error = %v, principal = %#v", got.err, got.principal)
			}
		case <-ctx.Done():
			t.Fatal("concurrent bootstrap did not finish")
		}
	}
	if calls.Load() != 2 || successes != 1 || conflicts != 1 {
		t.Fatalf("hash calls = %d, successes = %d, conflicts = %d", calls.Load(), successes, conflicts)
	}
	assertBootstrapUserCount(t, service, 1)
	var stored Principal
	if err := service.database.QueryRow(`SELECT id, username, role FROM users`).Scan(&stored.ID, &stored.Username, &stored.Role); err != nil {
		t.Fatal(err)
	}
	if stored != winner || winner.ID == 0 || winner.Role != RootRole {
		t.Fatalf("stored root = %#v, winner = %#v", stored, winner)
	}
}

func assertBootstrapUserCount(t *testing.T, service *Service, want int) {
	t.Helper()
	var count int
	if err := service.database.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("users count = %d, want %d", count, want)
	}
}
