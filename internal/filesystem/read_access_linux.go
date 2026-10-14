//go:build linux

package filesystem

import "golang.org/x/sys/unix"

// 只提供当前有效身份的读取权限提示，不打开 FIFO、设备等目录条目。
// 默认跟随符号链接；权限检查不保证后续打开成功，也没有元数据 I/O 的硬超时。
func checkEntryReadAccess(path string) error {
	return unix.Faccessat(unix.AT_FDCWD, path, unix.R_OK, unix.AT_EACCESS)
}
