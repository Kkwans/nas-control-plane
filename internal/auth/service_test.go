package auth

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestBootstrapCreatesOneRootAccountAndLoginSession(t *testing.T) {
	service := openTestService(t, Options{})
	ctx := context.Background()

	initialized, err := service.Initialized(ctx)
	if err != nil {
		t.Fatalf("Initialized() error = %v", err)
	}
	if initialized {
		t.Fatal("new database must not be initialized")
	}

	principal, err := service.Bootstrap(ctx, "root-admin", testPassword(t))
	if err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	if principal.Username != "root-admin" || principal.Role != RootRole || principal.ID == 0 {
		t.Fatalf("principal = %#v", principal)
	}

	if _, err := service.Bootstrap(ctx, "another-root", testPassword(t)); ErrorCode(err) != "AUTH_ALREADY_INITIALIZED" {
		t.Fatalf("second Bootstrap() error code = %q", ErrorCode(err))
	}

	session, err := service.Login(ctx, "root-admin", testPassword(t))
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if session.Token == "" || !session.ExpiresAt.After(time.Now()) || session.Principal.Role != RootRole {
		t.Fatalf("session = %#v", session)
	}

	authenticated, err := service.Authenticate(ctx, session.Token)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if authenticated != principal {
		t.Fatalf("authenticated = %#v, want %#v", authenticated, principal)
	}
}

func TestLoginRejectsWrongCredentialsAndExpiredSessions(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	service := openTestService(t, Options{
		Clock:           func() time.Time { return now },
		SessionLifetime: time.Minute,
	})
	ctx := context.Background()
	if _, err := service.Bootstrap(ctx, "root-admin", testPassword(t)); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	if _, err := service.Login(ctx, "root-admin", testPassword(t)+"mismatch"); ErrorCode(err) != "AUTH_INVALID_CREDENTIALS" {
		t.Fatalf("wrong password error code = %q", ErrorCode(err))
	}

	session, err := service.Login(ctx, "root-admin", testPassword(t))
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := service.Authenticate(ctx, session.Token); ErrorCode(err) != "AUTH_UNAUTHORIZED" {
		t.Fatalf("expired session error code = %q", ErrorCode(err))
	}
}

func openTestService(t *testing.T, options Options) *Service {
	t.Helper()
	service, err := Open(filepath.Join(t.TempDir(), "ncp.sqlite"), options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return service
}

func testPassword(t *testing.T) string {
	t.Helper()
	sum := sha256.Sum256([]byte(t.Name()))
	return fmt.Sprintf("%x", sum[:])
}
