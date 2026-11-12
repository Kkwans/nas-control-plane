//go:build linux

package agentsocket

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// 仅在受保护的目录内回收。目录锁覆盖 listener 的整个生命周期，
// 约束本版本的并发启动；拥有同一 UID 或 root 权限的任意外部写入不属于隔离边界。
// 不使用独立锁文件，避免锁文件删除/替换导致两个实例锁住不同 inode。
type socketEntry struct {
	mode    uint32
	uid     uint32
	dev     uint64
	ino     uint64
	links   uint64
	changed unix.Timespec
}

func (e socketEntry) socketOwnedBy(uid uint32) bool {
	return e.mode&unix.S_IFMT == unix.S_IFSOCK && e.uid == uid && e.links == 1
}

type socketRecoveryDirectory interface {
	inspect() (socketEntry, error)
	remove() error
}

type pinnedSocketDirectory struct {
	fd   int
	name string
}

func (d *pinnedSocketDirectory) inspect() (socketEntry, error) {
	var st unix.Stat_t
	err := unix.Fstatat(d.fd, d.name, &st, unix.AT_SYMLINK_NOFOLLOW)
	return socketEntry{mode: st.Mode, uid: st.Uid, dev: uint64(st.Dev), ino: st.Ino, links: uint64(st.Nlink), changed: st.Ctim}, err
}
func (d *pinnedSocketDirectory) remove() error { return unix.Unlinkat(d.fd, d.name, 0) }

// /proc/self/fd 把 bind、探活和权限操作限定在同一目录 inode；
// /proc 不可用时启动失败，不退回未经固定的原始路径。
func (d *pinnedSocketDirectory) path() string {
	return fmt.Sprintf("/proc/self/fd/%d/%s", d.fd, d.name)
}
func (d *pinnedSocketDirectory) close() { _ = unix.Close(d.fd) }

func trustedSocketDirectory(st unix.Stat_t, uid uint32, final bool) bool {
	if st.Mode&unix.S_IFMT != unix.S_IFDIR || (st.Uid != 0 && st.Uid != uid) {
		return false
	}
	if st.Mode&0o022 == 0 {
		return true
	}
	// /tmp 等 sticky 祖先不允许其他用户替换后续的受信任所有者目录。
	return !final && st.Mode&unix.S_ISVTX != 0
}

func pinSocketDirectory(path string) (*pinnedSocketDirectory, error) {
	directory := filepath.Dir(path)
	if directory == "/" {
		return nil, errors.New("socket requires a dedicated directory")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = unix.Close(fd)
		}
	}()
	parts := strings.Split(strings.TrimPrefix(directory, "/"), "/")
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return nil, err
	}
	if !trustedSocketDirectory(st, uint32(os.Geteuid()), directory == "/") {
		return nil, errors.New("untrusted socket directory")
	}
	for i, part := range parts {
		if part == "" {
			continue
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(err, unix.ENOENT) {
			if mkdirErr := unix.Mkdirat(fd, part, uint32(defaultSocketDirectoryMode)); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				return nil, mkdirErr
			}
			next, err = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		}
		if err != nil {
			return nil, err
		}
		_ = unix.Close(fd)
		fd = next
		if err := unix.Fstat(fd, &st); err != nil {
			return nil, err
		}
		if !trustedSocketDirectory(st, uint32(os.Geteuid()), i == len(parts)-1) {
			return nil, errors.New("untrusted socket directory")
		}
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, err
	}
	ok = true
	return &pinnedSocketDirectory{fd: fd, name: filepath.Base(path)}, nil
}

// 只有明确 ECONNREFUSED 才表示绑定文件没有监听者。超时、拒绝访问、
// 不存在、协议错误和其他未知结果均保持原样，交给管理员处理。
func recoverStaleSocket(d socketRecoveryDirectory, uid uint32, probe func() error) error {
	before, err := d.inspect()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return coded("AGENT_SOCKET_PATH_CHECK_FAILED", err)
	}
	if !before.socketOwnedBy(uid) {
		return coded("AGENT_SOCKET_PATH_OCCUPIED", errors.New("socket path is not a single owned socket"))
	}
	if err := probe(); !errors.Is(err, unix.ECONNREFUSED) {
		return coded("AGENT_SOCKET_PATH_OCCUPIED", errors.New("socket is active or liveness is uncertain"))
	}
	current, err := d.inspect()
	if err != nil || current != before {
		return coded("AGENT_SOCKET_PATH_OCCUPIED", errors.New("socket changed during liveness check"))
	}
	if err := d.remove(); err != nil {
		return coded("AGENT_SOCKET_STALE_REMOVE_FAILED", err)
	}
	return nil
}

func removeOwnedSocket(d socketRecoveryDirectory, expected socketEntry) {
	current, err := d.inspect()
	if err == nil && current == expected {
		_ = d.remove()
	}
}

func listenUnixSocket(config SocketConfig) (net.Listener, func(), error) {
	config, err := normalizedSocketConfig(config)
	if err != nil {
		return nil, nil, err
	}
	if filepath.Clean(config.SocketPath) != config.SocketPath {
		return nil, nil, coded("AGENT_SOCKET_PATH_INVALID", errors.New("socket path must be clean"))
	}
	pinned, err := pinSocketDirectory(config.SocketPath)
	if err != nil {
		return nil, nil, coded("AGENT_SOCKET_DIRECTORY_UNSAFE", err)
	}
	release := true
	defer func() {
		if release {
			pinned.close()
		}
	}()
	if err := recoverStaleSocket(pinned, uint32(os.Geteuid()), func() error {
		connection, err := net.DialTimeout("unix", pinned.path(), 250*time.Millisecond)
		if err == nil {
			_ = connection.Close()
		}
		return err
	}); err != nil {
		return nil, nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: pinned.path(), Net: "unix"})
	if err != nil {
		return nil, nil, coded("AGENT_SOCKET_LISTEN_FAILED", err)
	}
	// Go 默认 Close 会按路径 unlink，可能误删替换后的端点，必须禁用。
	listener.SetUnlinkOnClose(false)
	entry, err := pinned.inspect()
	if err != nil || !entry.socketOwnedBy(uint32(os.Geteuid())) {
		_ = listener.Close()
		return nil, nil, coded("AGENT_SOCKET_PATH_CHECK_FAILED", errors.New("created socket could not be verified"))
	}
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			_ = listener.Close()
			removeOwnedSocket(pinned, entry)
			pinned.close()
		})
	}
	release = false
	pinnedConfig := config
	pinnedConfig.SocketPath = pinned.path()
	if err := applySocketPermissions(filepath.Dir(pinned.path()), pinnedConfig); err != nil {
		// chmod/chown 会改变 ctime，只有同一 inode 才刷新清理标识。
		if current, checkErr := pinned.inspect(); checkErr == nil && current.dev == entry.dev && current.ino == entry.ino && current.socketOwnedBy(entry.uid) {
			entry = current
		}
		cleanup()
		return nil, nil, err
	}
	// 权限更新后的 ctime 纳入退出时的身份校验。
	current, err := pinned.inspect()
	if err != nil || current.dev != entry.dev || current.ino != entry.ino || !current.socketOwnedBy(entry.uid) {
		cleanup()
		return nil, nil, coded("AGENT_SOCKET_PATH_CHECK_FAILED", errors.New("created socket changed during setup"))
	}
	entry = current
	return listener, cleanup, nil
}
