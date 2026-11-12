//go:build !linux

package filesystem

import "os"

// 非 Linux 保留原有探测行为；不承诺避免特殊文件打开或提供硬超时。
func checkEntryReadAccess(path string) error {
	handle, err := os.Open(path)
	if err != nil {
		return err
	}
	_ = handle.Close()
	return nil
}
