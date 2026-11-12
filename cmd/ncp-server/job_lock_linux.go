//go:build linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// 本机受信目录内的单 owner 约束；不承诺网络文件系统或恶意同 UID 的隔离。
// 不删除锁文件，避免等待者与新 owner 锁住不同 inode。
func lockJobDatabase(path string) (func(), error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// 先解析现有数据库的链接；尚未创建时解析已存在父目录。
	resolved, err := filepath.EvalSymlinks(absolute)
	if os.IsNotExist(err) {
		parent, parentErr := filepath.EvalSymlinks(filepath.Dir(absolute))
		if parentErr != nil {
			return nil, parentErr
		}
		resolved = filepath.Join(parent, filepath.Base(absolute))
		err = nil
	}
	if err != nil {
		return nil, err
	}
	fd, err := syscall.Open(resolved+".jobs.lock", syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, errors.New("JOB_OWNER_LOCK_OPEN_FAILED")
	}
	file := os.NewFile(uintptr(fd), "job-owner-lock")
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, errors.New("JOB_OWNER_LOCK_INVALID")
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, errors.New("JOB_OWNER_ALREADY_RUNNING")
	}
	return func() { _ = syscall.Flock(fd, syscall.LOCK_UN); _ = file.Close() }, nil
}
