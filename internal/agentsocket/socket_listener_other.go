//go:build !linux

package agentsocket

import (
	"errors"
	"net"
	"os"
	"path/filepath"
)

// 非 Linux 平台维持原有行为，不尝试回收残留端点。
func listenUnixSocket(config SocketConfig) (net.Listener, func(), error) {
	config, err := normalizedSocketConfig(config)
	if err != nil {
		return nil, nil, err
	}
	if _, err := os.Lstat(config.SocketPath); err == nil {
		return nil, nil, coded("AGENT_SOCKET_PATH_OCCUPIED", errors.New("socket path already exists"))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, coded("AGENT_SOCKET_PATH_CHECK_FAILED", err)
	}

	directory := filepath.Dir(config.SocketPath)
	if err := os.MkdirAll(directory, defaultSocketDirectoryMode); err != nil {
		return nil, nil, coded("AGENT_SOCKET_DIRECTORY_FAILED", err)
	}

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: config.SocketPath, Net: "unix"})
	if err != nil {
		return nil, nil, coded("AGENT_SOCKET_LISTEN_FAILED", err)
	}
	cleanup := func() {
		_ = listener.Close()
		info, statErr := os.Lstat(config.SocketPath)
		if statErr == nil && info.Mode()&os.ModeSocket != 0 {
			_ = os.Remove(config.SocketPath)
		}
	}

	if err := applySocketPermissions(directory, config); err != nil {
		cleanup()
		return nil, nil, err
	}
	return listener, cleanup, nil
}
