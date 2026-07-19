package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	Target Target
	Rows   uint16
	Cols   uint16
}

type SessionInfo struct {
	ID     string
	Target Target
}

// Session 表示已创建的受控终端会话。
type Session interface {
	Read(context.Context, []byte) (int, error)
	Write([]byte) (int, error)
	Resize(rows, cols uint16) error
	Close() error
}

type Starter interface {
	Start(context.Context, StartRequest) (Session, error)
}

type Manager struct {
	mu          sync.Mutex
	host        Starter
	container   Starter
	maxSessions int
	nextID      uint64
	sessions    map[string]Session
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
	defer m.mu.Unlock()
	if len(m.sessions) >= m.maxSessions {
		return SessionInfo{}, coded("TERMINAL_SESSION_LIMIT_REACHED", errors.New("terminal session limit reached"))
	}

	starter := m.host
	if request.Target == TargetContainer {
		starter = m.container
	}
	if starter == nil {
		return SessionInfo{}, coded("TERMINAL_TARGET_UNAVAILABLE", errors.New("terminal target is unavailable"))
	}
	session, err := starter.Start(ctx, request)
	if err != nil {
		return SessionInfo{}, err
	}
	if session == nil {
		return SessionInfo{}, coded("TERMINAL_SESSION_START_FAILED", errors.New("terminal starter returned nil session"))
	}

	m.nextID++
	info := SessionInfo{
		ID:     fmt.Sprintf("term-p0-%d", m.nextID),
		Target: request.Target,
	}
	m.sessions[info.ID] = session
	return info, nil
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
	}
	m.mu.Unlock()
	if !exists {
		return coded("TERMINAL_SESSION_NOT_FOUND", errors.New("terminal session not found"))
	}
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
	return validateDimensions(request.Rows, request.Cols)
}

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
