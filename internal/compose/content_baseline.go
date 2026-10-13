package compose

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	pathpkg "path"
	"unicode/utf8"
)

// ContentHash 对原始文件字节计算 SHA-256，不规范化换行、空白或 Unicode。
func ContentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// IsContentHash 仅接受 64 位小写十六进制摘要，不修剪或转换输入。
func IsContentHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for index := range value {
		if character := value[index]; !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func parseContentHash(value string) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	if !IsContentHash(value) {
		return result, errors.New("compose content baseline must be a canonical SHA-256 hash")
	}
	_, err := hex.Decode(result[:], []byte(value))
	return result, err
}

// readFileSnapshot 只把可无损传输的 UTF-8 字节暴露为可编辑文本。
func readFileSnapshot(target string, readFile func(string) ([]byte, error)) (FileSnapshot, error) {
	info, err := os.Stat(target)
	if err != nil {
		return FileSnapshot{}, err
	}
	if info.Size() > maxConfigBytes {
		return FileSnapshot{}, errors.New("compose config is too large")
	}
	content, err := readFile(target)
	if err != nil {
		return FileSnapshot{}, err
	}
	if len(content) > maxConfigBytes {
		return FileSnapshot{}, errors.New("compose config is too large")
	}
	if !utf8.Valid(content) {
		return FileSnapshot{}, errors.New("compose config is not valid UTF-8")
	}
	return FileSnapshot{
		Path: target, Name: pathpkg.Base(target), Content: string(content),
		ContentHash: ContentHash(content), Size: int64(len(content)),
	}, nil
}

var errContentConflict = errors.New("compose target no longer matches expected content")

// readCurrentContent 只区分可证实的内容/存在性/类型冲突和无法核实，不决定业务阶段。
// Lstat 不跟随目标符号链接；检查仍非跨进程 CAS，不保护父目录别名和后续改动。
func readCurrentContent(target string, expected [sha256.Size]byte, readFile func(string) ([]byte, error)) ([]byte, os.FileMode, error) {
	info, err := os.Lstat(target)
	if err != nil {
		return nil, 0, contentReadError(err)
	}
	if !info.Mode().IsRegular() {
		return nil, 0, errContentConflict
	}
	content, err := readFile(target)
	if err != nil {
		// 读取期间也可能被移除或换成其它类型；可证实的路径变化按冲突处理。
		current, statErr := os.Lstat(target)
		if errors.Is(statErr, os.ErrNotExist) || (statErr == nil && !current.Mode().IsRegular()) {
			return nil, 0, errors.Join(errContentConflict, err)
		}
		return nil, 0, contentReadError(err)
	}
	if sha256.Sum256(content) != expected {
		return nil, 0, errContentConflict
	}
	return content, info.Mode().Perm(), nil
}

func contentReadError(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return errors.Join(errContentConflict, err)
	}
	return err
}

func contentVerificationError(err error, conflictCode, unverifiedCode string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, errContentConflict) {
		return coded(conflictCode, err)
	}
	return coded(unverifiedCode, err)
}
