package agentsocket

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	DefaultSocketPath          = "/run/ncp/agent.sock"
	defaultSocketMode          = 0o660
	defaultSocketDirectoryMode = 0o750
	protocolVersion            = "p0-v1"
)

type SocketConfig struct {
	SocketPath  string
	SocketGroup string
	SocketMode  os.FileMode
}

func Serve(ctx context.Context, config SocketConfig) error {
	listener, cleanup, err := listenUnixSocket(config)
	if err != nil {
		return err
	}
	defer cleanup()

	grpcServer := grpc.NewServer()
	RegisterAgentProbeServiceServer(grpcServer, newStatusService())
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			grpcServer.Stop()
		case <-stopped:
		}
	}()

	err = grpcServer.Serve(listener)
	close(stopped)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return coded("AGENT_SOCKET_SERVE_FAILED", err)
	}
	return nil
}

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

func normalizedSocketConfig(config SocketConfig) (SocketConfig, error) {
	if config.SocketPath == "" {
		config.SocketPath = DefaultSocketPath
	}
	if !filepath.IsAbs(config.SocketPath) {
		return SocketConfig{}, coded("AGENT_SOCKET_PATH_INVALID", errors.New("socket path must be absolute"))
	}
	if config.SocketMode == 0 {
		config.SocketMode = defaultSocketMode
	}
	if config.SocketMode.Perm() != defaultSocketMode {
		return SocketConfig{}, coded("AGENT_SOCKET_MODE_INVALID", fmt.Errorf("socket mode must be %o", defaultSocketMode))
	}
	return config, nil
}

func applySocketPermissions(directory string, config SocketConfig) error {
	groupID, err := socketGroupID(config.SocketGroup)
	if err != nil {
		return err
	}
	if groupID >= 0 {
		if err := os.Chown(directory, -1, groupID); err != nil {
			return coded("AGENT_SOCKET_DIRECTORY_OWNER_FAILED", err)
		}
	}
	if err := os.Chmod(directory, defaultSocketDirectoryMode); err != nil {
		return coded("AGENT_SOCKET_DIRECTORY_MODE_FAILED", err)
	}
	if groupID >= 0 {
		if err := os.Chown(config.SocketPath, -1, groupID); err != nil {
			return coded("AGENT_SOCKET_OWNER_FAILED", err)
		}
	}
	if err := os.Chmod(config.SocketPath, config.SocketMode); err != nil {
		return coded("AGENT_SOCKET_MODE_FAILED", err)
	}
	return nil
}

func socketGroupID(groupName string) (int, error) {
	if groupName == "" {
		return -1, nil
	}
	group, err := user.LookupGroup(groupName)
	if err != nil {
		return -1, coded("AGENT_SOCKET_GROUP_INVALID", err)
	}
	groupID, err := strconv.Atoi(group.Gid)
	if err != nil {
		return -1, coded("AGENT_SOCKET_GROUP_INVALID", err)
	}
	return groupID, nil
}

func ValidateServerSocketGroup(groupName string) error {
	if groupName == "" {
		return coded("AGENT_SOCKET_GROUP_REQUIRED", errors.New("socket group is required"))
	}
	_, err := socketGroupID(groupName)
	return err
}

type statusService struct{}

func newStatusService() *statusService {
	return &statusService{}
}

func (statusService) GetStatus(ctx context.Context, _ *emptypb.Empty) (*structpb.Struct, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return structpb.NewStruct(map[string]any{
		"protocol_version": protocolVersion,
		"agent_euid":       os.Geteuid(),
		"transport":        "unix",
	})
}
