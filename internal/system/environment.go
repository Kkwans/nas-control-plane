package system

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// OSEnvironment 是 ncp-agent 在宿主机上使用的只读 Environment 实现。
type OSEnvironment struct{}

func NewOSEnvironment() OSEnvironment {
	return OSEnvironment{}
}

func (OSEnvironment) Architecture() string {
	return runtime.GOARCH
}

func (OSEnvironment) Hostname() (string, error) {
	return os.Hostname()
}

func (OSEnvironment) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

func (OSEnvironment) PathExists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}

func (OSEnvironment) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func (OSEnvironment) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

func (OSEnvironment) Glob(pattern string) ([]string, error) {
	return filepath.Glob(pattern)
}

func (OSEnvironment) NetworkInterfaces() ([]string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(interfaces))
	for _, networkInterface := range interfaces {
		names = append(names, networkInterface.Name)
	}
	return names, nil
}

func (OSEnvironment) EffectiveUID() int {
	return os.Geteuid()
}
