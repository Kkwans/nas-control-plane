//go:build linux

package filesystem

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxReadAccessChecksOrdinaryTemporaryPathsAndFollowsSymlinks(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	dir := filepath.Join(root, "dir")
	if err := os.WriteFile(file, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"file-link": file, "dir-link": dir, "missing-link": filepath.Join(root, "missing")} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"file", "dir", "file-link", "dir-link"} {
		t.Run(name, func(t *testing.T) {
			if err := checkEntryReadAccess(filepath.Join(root, name)); err != nil {
				t.Fatalf("checkEntryReadAccess() = %v", err)
			}
		})
	}
	for _, name := range []string{"missing", "missing-link"} {
		t.Run(name, func(t *testing.T) {
			if err := checkEntryReadAccess(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("checkEntryReadAccess() = %v", err)
			}
		})
	}
}
