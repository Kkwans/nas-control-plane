package agentsocket

import (
	"errors"
	"github.com/Kkwans/nas-control-plane/internal/terminal"
)

// 内部零值兼容默认容量；注入 Manager 时只允许零值，由注入方独占容量决策。
// 即使终端关闭，也校验配置，避免无效值潜伏到后续启用。
func validateTerminalCapacity(config SocketConfig) (int, error) {
	capacity := config.TerminalMaxSessions
	if capacity == 0 {
		capacity = terminal.DefaultMaxSessions
	}
	if err := terminal.ValidateMaxSessions(capacity); err != nil {
		return 0, coded("TERMINAL_MAX_SESSIONS_INVALID", err)
	}
	if config.TerminalManager != nil && config.TerminalMaxSessions != 0 {
		return 0, coded("TERMINAL_CAPACITY_CONFIG_CONFLICT", errors.New("injected terminal manager requires zero terminal max sessions"))
	}
	return capacity, nil
}

func configuredTerminalManager(config SocketConfig, factory func(int) (*terminal.Manager, error)) (*terminal.Manager, error) {
	capacity, err := validateTerminalCapacity(config)
	if err != nil {
		return nil, err
	}
	if config.TerminalManager != nil {
		return config.TerminalManager, nil
	}
	return factory(capacity)
}
