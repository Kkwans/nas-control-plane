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

func TestUserManagementLifecycleAndSessionRevocation(t *testing.T) {
	service := openTestService(t, Options{})
	ctx := context.Background()
	root, err := service.Bootstrap(ctx, "root-admin", testPassword(t))
	if err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	operatorPassword := testPassword(t) + "-operator"
	operator, err := service.CreateUser(ctx, "operator", operatorPassword)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if operator.Role != RootRole || operator.Disabled {
		t.Fatalf("operator = %#v", operator)
	}
	if _, err := service.CreateUser(ctx, "OPERATOR", operatorPassword); ErrorCode(err) != "AUTH_USERNAME_EXISTS" {
		t.Fatalf("duplicate CreateUser() error code = %q", ErrorCode(err))
	}
	session, err := service.Login(ctx, "operator", operatorPassword)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	disabled, err := service.SetUserDisabled(ctx, root.ID, operator.ID, true)
	if err != nil {
		t.Fatalf("SetUserDisabled() error = %v", err)
	}
	if !disabled.Disabled {
		t.Fatalf("disabled user = %#v", disabled)
	}
	if _, err := service.Authenticate(ctx, session.Token); ErrorCode(err) != "AUTH_UNAUTHORIZED" {
		t.Fatalf("disabled session error code = %q", ErrorCode(err))
	}
	if _, err := service.Login(ctx, "operator", operatorPassword); ErrorCode(err) != "AUTH_INVALID_CREDENTIALS" {
		t.Fatalf("disabled login error code = %q", ErrorCode(err))
	}
	if _, err := service.SetUserDisabled(ctx, root.ID, operator.ID, false); err != nil {
		t.Fatalf("enable user error = %v", err)
	}
	nextPassword := operatorPassword + "-next"
	if err := service.UpdatePassword(ctx, root.ID, operator.ID, "", nextPassword); err != nil {
		t.Fatalf("admin UpdatePassword() error = %v", err)
	}
	if _, err := service.Login(ctx, "operator", operatorPassword); ErrorCode(err) != "AUTH_INVALID_CREDENTIALS" {
		t.Fatalf("old password error code = %q", ErrorCode(err))
	}
	if _, err := service.Login(ctx, "operator", nextPassword); err != nil {
		t.Fatalf("new password Login() error = %v", err)
	}
	if err := service.DeleteUser(ctx, root.ID, operator.ID); err != nil {
		t.Fatalf("DeleteUser() error = %v", err)
	}
	users, err := service.Users(ctx)
	if err != nil {
		t.Fatalf("Users() error = %v", err)
	}
	if len(users) != 1 || users[0].ID != root.ID {
		t.Fatalf("users = %#v", users)
	}
}

func TestUserManagementProtectsCurrentAndLastEnabledUser(t *testing.T) {
	service := openTestService(t, Options{})
	ctx := context.Background()
	rootPassword := testPassword(t)
	root, err := service.Bootstrap(ctx, "root-admin", rootPassword)
	if err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	if _, err := service.SetUserDisabled(ctx, root.ID, root.ID, true); ErrorCode(err) != "AUTH_CURRENT_USER_PROTECTED" {
		t.Fatalf("disable current error code = %q", ErrorCode(err))
	}
	if err := service.DeleteUser(ctx, root.ID, root.ID); ErrorCode(err) != "AUTH_CURRENT_USER_PROTECTED" {
		t.Fatalf("delete current error code = %q", ErrorCode(err))
	}
	session, err := service.Login(ctx, root.Username, rootPassword)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if err := service.UpdatePassword(ctx, root.ID, root.ID, "wrong-password", rootPassword+"-next"); ErrorCode(err) != "AUTH_CURRENT_PASSWORD_INVALID" {
		t.Fatalf("wrong current password code = %q", ErrorCode(err))
	}
	if err := service.UpdatePassword(ctx, root.ID, root.ID, rootPassword, rootPassword+"-next"); err != nil {
		t.Fatalf("self UpdatePassword() error = %v", err)
	}
	if _, err := service.Authenticate(ctx, session.Token); ErrorCode(err) != "AUTH_UNAUTHORIZED" {
		t.Fatalf("revoked self session error code = %q", ErrorCode(err))
	}
}

func TestPasswordPolicyDefaultsToSixCharactersAndPersistsRules(t *testing.T) {
	service := openTestService(t, Options{})
	ctx := context.Background()

	defaultPolicy, err := service.PasswordPolicy(ctx)
	if err != nil {
		t.Fatalf("PasswordPolicy() error = %v", err)
	}
	if defaultPolicy.MinLength != 6 || defaultPolicy.RequireUppercase || defaultPolicy.RequireLowercase ||
		defaultPolicy.RequireDigit || defaultPolicy.RequireSpecial {
		t.Fatalf("default password policy = %#v", defaultPolicy)
	}
	root, err := service.Bootstrap(ctx, "root-admin", "123123")
	if err != nil {
		t.Fatalf("Bootstrap() with six numeric characters error = %v", err)
	}
	policy := PasswordPolicy{
		MinLength:        8,
		RequireUppercase: true,
		RequireLowercase: true,
		RequireDigit:     true,
		RequireSpecial:   true,
	}
	if _, err := service.UpdatePasswordPolicy(ctx, policy); err != nil {
		t.Fatalf("UpdatePasswordPolicy() error = %v", err)
	}
	if _, err := service.CreateUser(ctx, "weak-user", "12345678"); ErrorCode(err) != "AUTH_INPUT_INVALID" {
		t.Fatalf("weak CreateUser() error code = %q", ErrorCode(err))
	}
	if _, err := service.CreateUser(ctx, "strong-user", "Ncp-1234"); err != nil {
		t.Fatalf("strong CreateUser() error = %v", err)
	}
	if err := service.UpdatePassword(ctx, root.ID, root.ID, "123123", "Ncp-5678"); err != nil {
		t.Fatalf("UpdatePassword() with policy error = %v", err)
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
