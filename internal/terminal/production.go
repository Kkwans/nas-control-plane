package terminal

import "fmt"

const (
	DefaultMaxSessions    = defaultMaxSessions
	MaxProductionSessions = 4
)

// ValidateMaxSessions 严格校验显式容量；不把零值解释成默认值。
func ValidateMaxSessions(maxSessions int) error {
	if maxSessions < 1 || maxSessions > MaxProductionSessions {
		return coded("TERMINAL_MAX_SESSIONS_INVALID", fmt.Errorf("terminal max sessions must be between 1 and %d", MaxProductionSessions))
	}
	return nil
}

// NewProductionManager 的容量是单 Agent 主机与容器合计的所有权预算。
func NewProductionManager(maxSessions int) (*Manager, error) {
	return newProductionManager(maxSessions, func() Starter { return NewHostStarter() }, func() (Starter, error) { return NewContainerStarter() })
}

// 构造接缝允许测试容量接线而不创建 Docker client 或真实 Starter。
func newProductionManager(maxSessions int, host func() Starter, container func() (Starter, error)) (*Manager, error) {
	if err := ValidateMaxSessions(maxSessions); err != nil {
		return nil, err
	}
	containerStarter, err := container()
	if err != nil {
		return nil, err
	}
	return NewManager(host(), containerStarter, maxSessions), nil
}
