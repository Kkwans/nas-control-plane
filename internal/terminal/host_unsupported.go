//go:build !linux

package terminal

import (
	"context"
	"errors"
)

type unsupportedHostStarter struct{}

func NewHostStarter() Starter {
	return unsupportedHostStarter{}
}

func (unsupportedHostStarter) Start(context.Context, StartRequest) (Session, error) {
	return nil, coded("TERMINAL_HOST_UNSUPPORTED", errors.New("host PTY is only supported on Linux"))
}
