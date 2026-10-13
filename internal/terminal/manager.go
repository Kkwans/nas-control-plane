package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
)

const (
	defaultMaxSessions = 1
	maxTerminalRows    = 200
	maxTerminalCols    = 400
	maxTerminalInput   = 32 * 1024
)

// Target 是 P0 允许创建的终端类别。它不承载用户给出的 Shell、命令或工作目录。
type Target string

const (
	TargetHost      Target = "host"
	TargetContainer Target = "container"
)

type StartRequest struct {
	Target      Target
	ContainerID string
	Rows        uint16
	Cols        uint16
}

type SessionInfo struct {
	ID           string
	Target       Target
	Shell        string
	Enhancement  string
	Reason       string
	Capabilities SessionCapabilities
	Rows         uint16
	Cols         uint16
}

type SessionMetadata struct {
	Shell        string
	Enhancement  string
	Reason       string
	Capabilities SessionCapabilities
}

// SessionCapabilities describes terminal behavior that the client can use for
// paste, multiline editing, and display decisions. It is deliberately about
// the active session, not the host or container in general.
type SessionCapabilities struct {
	Resize         bool `json:"resize"`
	Readline       bool `json:"readline"`
	BracketedPaste bool `json:"bracketedPaste"`
	MultilinePaste bool `json:"multilinePaste"`
	ANSIColors     bool `json:"ansiColors"`
}

// SessionMetadataProvider 的 Metadata 在发布前调用，不持有 Manager 锁。
type SessionMetadataProvider interface {
	Metadata() SessionMetadata
}

// Session 表示已创建的受控终端会话；实现须允许 Read、Write、Resize 与 Close 并发。
type Session interface {
	Read(context.Context, []byte) (int, error)
	Write([]byte) (int, error)
	Resize(rows, cols uint16) error
	Close() error
}

// Starter 须支持并发 Start，且每次返回独立的 Session。
// 非 nil Session（即使同时返回错误）的所有权交给 Manager；nil 时的部分资源由 Starter 回收。
type Starter interface {
	Start(context.Context, StartRequest) (Session, error)
}

// ErrSessionCleanupFailed 表示未发布会话的 Close 失败，不改变主错误码。
var ErrSessionCleanupFailed = errors.New("terminal session cleanup failed")

type Manager struct {
	mu           sync.Mutex
	host         Starter
	container    Starter
	maxSessions  int
	nextID       uint64
	sessions     map[string]Session
	openingCount int
	closingCount int
}

func NewManager(host, container Starter, maxSessions int) *Manager {
	if maxSessions <= 0 {
		maxSessions = defaultMaxSessions
	}
	return &Manager{
		host:        host,
		container:   container,
		maxSessions: maxSessions,
		sessions:    make(map[string]Session),
	}
}

func (m *Manager) Open(ctx context.Context, request StartRequest) (SessionInfo, error) {
	if err := validateStartRequest(request); err != nil {
		return SessionInfo{}, err
	}
	if err := ctx.Err(); err != nil {
		return SessionInfo{}, coded("TERMINAL_SESSION_CANCELED", err)
	}

	m.mu.Lock()
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return SessionInfo{}, coded("TERMINAL_SESSION_CANCELED", err)
	}
	if m.openingCount+len(m.sessions)+m.closingCount >= m.maxSessions {
		m.mu.Unlock()
		return SessionInfo{}, coded("TERMINAL_SESSION_LIMIT_REACHED", errors.New("terminal session limit reached"))
	}

	starter := m.host
	if request.Target == TargetContainer {
		starter = m.container
	}
	if starter == nil {
		m.mu.Unlock()
		return SessionInfo{}, coded("TERMINAL_TARGET_UNAVAILABLE", errors.New("terminal target is unavailable"))
	}
	// 预留名额持续到 Start/Metadata 返回及发布或清理完成；取消不能提前释放。
	m.openingCount++
	m.mu.Unlock()
	session, err := starter.Start(ctx, request)
	if err != nil {
		return SessionInfo{}, m.rejectOpening(session, err)
	}
	if session == nil {
		return SessionInfo{}, m.rejectOpening(nil, coded("TERMINAL_SESSION_START_FAILED", errors.New("terminal starter returned nil session")))
	}
	if err := ctx.Err(); err != nil {
		return SessionInfo{}, m.rejectOpening(session, coded("TERMINAL_SESSION_CANCELED", err))
	}

	info := SessionInfo{
		Target:       request.Target,
		Shell:        "shell",
		Enhancement:  "native",
		Capabilities: SessionCapabilities{Resize: true},
		Rows:         request.Rows,
		Cols:         request.Cols,
	}
	if metadataProvider, ok := session.(SessionMetadataProvider); ok {
		metadata := metadataProvider.Metadata()
		if metadata.Shell != "" {
			info.Shell = metadata.Shell
		}
		if metadata.Enhancement != "" {
			info.Enhancement = metadata.Enhancement
		}
		info.Reason = metadata.Reason
		info.Capabilities = metadata.Capabilities
		if info.Capabilities == (SessionCapabilities{}) {
			info.Capabilities = sessionCapabilitiesFor(info.Shell, info.Enhancement)
		}
	}
	m.mu.Lock()
	// 取消检查与发布共享线性化边界；检查之后的取消由已发布会话的调用方收尾。
	if err := ctx.Err(); err != nil {
		m.openingCount--
		m.closingCount++
		m.mu.Unlock()
		return SessionInfo{}, m.closeRejectedOpening(session, coded("TERMINAL_SESSION_CANCELED", err))
	}
	m.openingCount--
	m.nextID++
	info.ID = fmt.Sprintf("term-p0-%d", m.nextID)
	m.sessions[info.ID] = session
	m.mu.Unlock()
	return info, nil
}

// rejectOpening 原子转换资源责任，不能在 opening 与 closing 之间留下配额空窗。
func (m *Manager) rejectOpening(session Session, primary error) error {
	m.mu.Lock()
	m.openingCount--
	if session != nil {
		m.closingCount++
	}
	m.mu.Unlock()
	if session == nil {
		return primary
	}
	return m.closeRejectedOpening(session, primary)
}

func (m *Manager) closeRejectedOpening(session Session, primary error) error {
	defer func() { m.mu.Lock(); m.closingCount--; m.mu.Unlock() }()
	if err := session.Close(); err != nil {
		// 外层固定主错误码（包括空码），避免次级 Close 错误改变 RPC 映射。
		return coded(ErrorCode(primary), errors.Join(primary, ErrSessionCleanupFailed, err))
	}
	return primary
}

func (m *Manager) Read(ctx context.Context, sessionID string, output []byte) (int, error) {
	if len(output) == 0 {
		return 0, nil
	}
	session, err := m.session(sessionID)
	if err != nil {
		return 0, err
	}
	return session.Read(ctx, output)
}

func (m *Manager) Write(sessionID string, input []byte) error {
	if len(input) == 0 || len(input) > maxTerminalInput {
		return coded("TERMINAL_INPUT_INVALID", errors.New("terminal input size is invalid"))
	}
	session, err := m.session(sessionID)
	if err != nil {
		return err
	}
	written, err := session.Write(input)
	if err != nil {
		return coded("TERMINAL_WRITE_FAILED", err)
	}
	if written != len(input) {
		return coded("TERMINAL_WRITE_FAILED", io.ErrShortWrite)
	}
	return nil
}

func (m *Manager) Resize(sessionID string, rows, cols uint16) error {
	if err := validateDimensions(rows, cols); err != nil {
		return err
	}
	session, err := m.session(sessionID)
	if err != nil {
		return err
	}
	if err := session.Resize(rows, cols); err != nil {
		return coded("TERMINAL_RESIZE_FAILED", err)
	}
	return nil
}

func (m *Manager) Close(sessionID string) error {
	m.mu.Lock()
	session, exists := m.sessions[sessionID]
	if exists {
		delete(m.sessions, sessionID)
		m.closingCount++
	}
	m.mu.Unlock()
	if !exists {
		return coded("TERMINAL_SESSION_NOT_FOUND", errors.New("terminal session not found"))
	}
	defer func() { m.mu.Lock(); m.closingCount--; m.mu.Unlock() }()
	if err := session.Close(); err != nil {
		return coded("TERMINAL_CLOSE_FAILED", err)
	}
	return nil
}

func (m *Manager) session(sessionID string) (Session, error) {
	if sessionID == "" {
		return nil, coded("TERMINAL_SESSION_NOT_FOUND", errors.New("terminal session id is required"))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, exists := m.sessions[sessionID]
	if !exists {
		return nil, coded("TERMINAL_SESSION_NOT_FOUND", errors.New("terminal session not found"))
	}
	return session, nil
}

func validateStartRequest(request StartRequest) error {
	if err := ValidateTarget(request.Target); err != nil {
		return err
	}
	request.ContainerID = strings.TrimSpace(request.ContainerID)
	if request.Target == TargetContainer && !containerTargetPattern.MatchString(request.ContainerID) {
		return coded("TERMINAL_CONTAINER_TARGET_REJECTED", errors.New("container target is invalid"))
	}
	return validateDimensions(request.Rows, request.Cols)
}

var containerTargetPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func ValidateTarget(target Target) error {
	if target != TargetHost && target != TargetContainer {
		return coded("TERMINAL_TARGET_REJECTED", errors.New("terminal target is outside P0 scope"))
	}
	return nil
}

func validateDimensions(rows, cols uint16) error {
	if rows == 0 || cols == 0 || rows > maxTerminalRows || cols > maxTerminalCols {
		return coded("TERMINAL_DIMENSIONS_INVALID", errors.New("terminal dimensions are outside P0 bounds"))
	}
	return nil
}

type codedError struct {
	code string
	err  error
}

func (e *codedError) Error() string {
	return e.code
}

func (e *codedError) Unwrap() error {
	return e.err
}

func coded(code string, err error) error {
	return &codedError{code: code, err: err}
}

func ErrorCode(err error) string {
	var codedErr *codedError
	if errors.As(err, &codedErr) {
		return codedErr.code
	}
	return ""
}
