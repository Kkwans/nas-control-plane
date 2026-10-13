//go:build linux

package terminal

import (
	"errors"
	"io"
	"os"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

type hostPTYTransport struct {
	file *os.File
	raw  syscall.RawConn
	ops  hostPTYOperations

	closeOnce sync.Once
	closeErr  error
}

// 仅为纯 fake 验证保留 syscall 接缝，不允许在 callback 外使用 descriptor。
type hostPTYOperations struct {
	read        func(int, []byte) (int, error)
	write       func(int, []byte) (int, error)
	setNonblock func(int, bool) error
	setWinsize  func(int, uint, *unix.Winsize) error
}

func newHostPTYTransport(file *os.File) *hostPTYTransport {
	return &hostPTYTransport{
		file: file,
		ops: hostPTYOperations{
			read: unix.Read, write: unix.Write,
			setNonblock: unix.SetNonblock, setWinsize: unix.IoctlSetWinsize,
		},
	}
}

// Init 必须在 session 暴露前完成；构造时不做初始化，便于先建立资源 owner。
func (t *hostPTYTransport) Init() error {
	raw, err := t.file.SyscallConn()
	if err != nil {
		return err
	}
	t.raw = raw
	return t.initNonblock()
}

func (t *hostPTYTransport) initNonblock() error {
	var opErr error
	rawErr := t.raw.Control(func(fd uintptr) {
		opErr = t.ops.setNonblock(int(fd), true)
	})
	return errors.Join(opErr, rawErr)
}

func (t *hostPTYTransport) TryRead(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	var n int
	var opErr error
	rawErr := t.raw.Read(func(fd uintptr) bool {
		n, opErr = t.ops.read(int(fd), p)
		// 即使 EAGAIN/EINTR 也结束本次调用，绝不进入 RawConn poller 等待。
		return true
	})
	if n < 0 {
		n = 0
	}
	err := errors.Join(opErr, rawErr)
	if n == 0 && err == nil {
		err = io.EOF
	}
	return n, err
}

func (t *hostPTYTransport) TryWrite(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	var n int
	var opErr error
	rawErr := t.raw.Write(func(fd uintptr) bool {
		n, opErr = t.ops.write(int(fd), p)
		return true
	})
	if n < 0 {
		n = 0
	}
	return n, errors.Join(opErr, rawErr)
}

func (t *hostPTYTransport) Resize(rows, cols uint16) error {
	var opErr error
	rawErr := t.raw.Control(func(fd uintptr) {
		opErr = t.ops.setWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols})
	})
	return errors.Join(opErr, rawErr)
}

func (t *hostPTYTransport) Close() error {
	t.closeOnce.Do(func() { t.closeErr = t.file.Close() })
	return t.closeErr
}
