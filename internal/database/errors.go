package database

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"net"
	"strings"
)

// ErrorCode is the stable, machine-readable outcome of a database operation.
// Error() on DatabaseError deliberately returns only this code so driver
// messages (which may contain endpoints or user supplied SQL) cannot leak to
// logs or HTTP responses by accident.
type ErrorCode string

const (
	CodeCredentialsRequired     ErrorCode = "credentials_required"
	CodeAuthFailed              ErrorCode = "auth_failed"
	CodeUnreachable             ErrorCode = "unreachable"
	CodeDatabaseNotFound        ErrorCode = "database_not_found"
	CodePermissionDenied        ErrorCode = "permission_denied"
	CodeSQLInvalid              ErrorCode = "sql_invalid"
	CodeResultModeUnsupported   ErrorCode = "result_mode_unsupported"
	CodeResultTooLarge          ErrorCode = "result_too_large"
	CodeConstraintFailed        ErrorCode = "constraint_failed"
	CodeAgentUnavailable        ErrorCode = "agent_unavailable"
	CodeTimeout                 ErrorCode = "timeout"
	CodeTLSConfigurationInvalid ErrorCode = "tls_configuration_invalid"
	CodeTLSPolicyUnsupported    ErrorCode = "tls_policy_unsupported"
	CodeTLSVerificationFailed   ErrorCode = "tls_verification_failed"

	// These codes describe failures in the server-side credential boundary.
	// They are intentionally separate from remote database outcomes.
	CodeCredentialStoreUnavailable ErrorCode = "credential_store_unavailable"
	CodeCredentialCorrupt          ErrorCode = "credential_corrupt"
	CodeCredentialConflict         ErrorCode = "credential_conflict"
	CodeKeyUnavailable             ErrorCode = "key_unavailable"
	CodeKeyRotationFailed          ErrorCode = "key_rotation_failed"
	CodeMigrationFailed            ErrorCode = "migration_failed"
)

// DatabaseError retains safe connection context without retaining a DSN,
// password, token, or raw driver error text.
type DatabaseError struct {
	Code      ErrorCode
	Driver    Driver
	Endpoint  string
	Database  string
	SourceID  string
	Operation string
	Cause     error
}

func (e *DatabaseError) Error() string {
	if e == nil {
		return ""
	}
	return string(e.Code)
}

func (e *DatabaseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// ErrorCodeOf returns the stable code carried by err. It does not inspect or
// expose the text of the underlying driver error.
func ErrorCodeOf(err error) ErrorCode {
	if err == nil {
		return ""
	}
	var databaseErr *DatabaseError
	if errors.As(err, &databaseErr) && databaseErr != nil {
		return databaseErr.Code
	}
	return ""
}

// CodeFromString accepts only the known wire-level error codes. This is used
// by the Agent RPC boundary, where the status message is deliberately a code
// rather than a driver error.
func CodeFromString(value string) ErrorCode {
	code := ErrorCode(strings.TrimSpace(value))
	switch code {
	case CodeCredentialsRequired, CodeAuthFailed, CodeUnreachable,
		CodeDatabaseNotFound, CodePermissionDenied, CodeSQLInvalid, CodeResultModeUnsupported,
		CodeConstraintFailed, CodeResultTooLarge, CodeAgentUnavailable, CodeTimeout,
		CodeCredentialStoreUnavailable, CodeCredentialCorrupt, CodeCredentialConflict, CodeKeyUnavailable,
		CodeKeyRotationFailed, CodeMigrationFailed,
		CodeTLSConfigurationInvalid, CodeTLSPolicyUnsupported, CodeTLSVerificationFailed:
		return code
	default:
		return ""
	}
}

func newDatabaseError(code ErrorCode, source Source, operation string, cause error) error {
	if isTLSErrorCode(code) {
		cause = nil
	}
	return &DatabaseError{
		Code:      code,
		Driver:    source.Driver,
		Endpoint:  sourceEndpoint(source, ""),
		SourceID:  source.ID,
		Operation: operation,
		Cause:     cause,
	}
}

// MapError turns a driver, context, or validation error into a stable
// DatabaseError. Legacy non-TLS causes remain available through Unwrap;
// TLS/x509 failures discard raw certificate/driver chains at this boundary.
func MapError(ctx context.Context, source Source, operation string, err error) error {
	if err == nil {
		return nil
	}
	var databaseErr *DatabaseError
	if errors.As(err, &databaseErr) && databaseErr != nil {
		if isTLSErrorCode(databaseErr.Code) || isTLSVerificationError(err) {
			safe := *databaseErr
			safe.Cause = nil
			if cause := tlsContextCause(ctx, err); cause != nil {
				safe.Code, safe.Cause = CodeTimeout, cause
			}
			return &safe
		}
		return databaseErr
	}
	code := ClassifyError(ctx, operation, err)
	cause := err
	if isTLSVerificationError(err) || errors.Is(err, errDatabaseTLSConfigurationInvalid) {
		// TLS/x509 错误可能携带证书、目标或底层驱动链，不保留它们。
		cause = nil
		if code == CodeTimeout {
			cause = tlsContextCause(ctx, err)
		}
	}
	return newDatabaseError(code, source, operation, cause)
}

// ClassifyError is intentionally conservative and driver-agnostic. Driver
// specific numeric codes are matched only where their meaning is stable; all
// returned categories are safe to expose to API callers.
func ClassifyError(ctx context.Context, operation string, err error) ErrorCode {
	if err == nil {
		return ""
	}
	if tlsContextCause(ctx, err) != nil {
		return CodeTimeout
	}
	if errors.Is(err, sql.ErrNoRows) {
		return CodeDatabaseNotFound
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) && networkErr.Timeout() {
		return CodeTimeout
	}

	if errors.Is(err, errDatabaseTLSConfigurationInvalid) {
		return CodeTLSConfigurationInvalid
	}
	if isTLSVerificationError(err) {
		return CodeTLSVerificationFailed
	}

	text := strings.ToLower(err.Error())
	if hasAny(text, "username is required", "user name is required", "user is required",
		"password is required", "database is required", "database name is required",
		"credentials required", "credentials are required", "用户名不能为空", "数据库名不能为空") {
		return CodeCredentialsRequired
	}
	if hasAny(text, "password authentication failed", "authentication failed", "access denied for user",
		"invalid password", "invalid authorization", "login failed", "not authorized to log in",
		"mysql error 1045", "error 1045", "sqlstate 28000") {
		return CodeAuthFailed
	}
	if hasAny(text, "permission denied", "permission denied for", "command denied", "insufficient privilege",
		"not enough privileges", "sqlstate 42501", "mysql error 1142", "error 1142") {
		return CodePermissionDenied
	}
	if hasAny(text, "unknown database", "database does not exist", "database not found",
		"no such database", "invalid catalog name", "3d000", "no such table",
		"table does not exist", "relation does not exist", "数据表不存在") ||
		(strings.Contains(text, "database ") && strings.Contains(text, " does not exist")) {
		return CodeDatabaseNotFound
	}
	if hasAny(text, "constraint failed", "constraint violation", "unique constraint", "duplicate key",
		"duplicate entry", "foreign key constraint", "not-null constraint", "check constraint",
		"sqlstate 23505", "sqlstate 23503", "sqlstate 23502", "sqlstate 23000") {
		return CodeConstraintFailed
	}
	if hasAny(text, "syntax error", "sql syntax", "parse error", "unterminated", "near \"",
		"invalid input syntax", "sqlstate 42601", "mysql error 1064", "error 1064") {
		return CodeSQLInvalid
	}
	if networkErrorText(text) {
		if hasAny(text, "timeout", "timed out", "deadline exceeded") {
			return CodeTimeout
		}
		return CodeUnreachable
	}

	if isConnectionOperation(operation) {
		return CodeUnreachable
	}
	return CodeSQLInvalid
}

func isConnectionOperation(operation string) bool {
	operation = strings.ToLower(strings.TrimSpace(operation))
	return strings.Contains(operation, "connect") || strings.Contains(operation, "ping") ||
		strings.Contains(operation, "diagnos") || strings.Contains(operation, "discover")
}

func networkErrorText(text string) bool {
	return hasAny(text, "connection refused", "connection reset", "broken pipe", "no route to host",
		"network is unreachable", "network unreachable", "host is down", "unknown host", "no such host",
		"dial tcp", "dial unix", "server closed the connection", "connection closed", "eof",
		"i/o timeout", "connectex")
}

func hasAny(text string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(text, fragment) {
			return true
		}
	}
	return false
}

func isTLSErrorCode(code ErrorCode) bool {
	return code == CodeTLSConfigurationInvalid || code == CodeTLSPolicyUnsupported || code == CodeTLSVerificationFailed
}

// 仅按类型分类；不得通过原始证书或驱动错误文字猜测 TLS 失败。
func isTLSVerificationError(err error) bool {
	var verification *tls.CertificateVerificationError
	var ech *tls.ECHRejectionError
	var headerPointer *tls.RecordHeaderError
	var unknownPointer *x509.UnknownAuthorityError
	var invalidPointer *x509.CertificateInvalidError
	var hostnamePointer *x509.HostnameError
	var rootsPointer *x509.SystemRootsError
	var alertPointer *tls.AlertError
	var algorithmPointer *x509.InsecureAlgorithmError
	var constraintPointer *x509.ConstraintViolationError
	var extensionPointer *x509.UnhandledCriticalExtension
	var header tls.RecordHeaderError
	var alert tls.AlertError
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	var roots x509.SystemRootsError
	var algorithm x509.InsecureAlgorithmError
	var constraint x509.ConstraintViolationError
	var extension x509.UnhandledCriticalExtension
	return errors.As(err, &verification) || errors.As(err, &ech) ||
		errors.As(err, &headerPointer) || errors.As(err, &unknownPointer) || errors.As(err, &invalidPointer) ||
		errors.As(err, &hostnamePointer) || errors.As(err, &rootsPointer) ||
		errors.As(err, &alertPointer) || errors.As(err, &algorithmPointer) ||
		errors.As(err, &constraintPointer) || errors.As(err, &extensionPointer) ||
		errors.As(err, &header) || errors.As(err, &alert) ||
		errors.As(err, &unknown) || errors.As(err, &invalid) || errors.As(err, &hostname) ||
		errors.As(err, &roots) || errors.As(err, &algorithm) || errors.As(err, &constraint) || errors.As(err, &extension)
}

func tlsContextCause(ctx context.Context, err error) error {
	var contextErr error
	if ctx != nil {
		contextErr = ctx.Err()
	}
	if errors.Is(contextErr, context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(contextErr, context.Canceled) || errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	return nil
}
