//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJobOwnerLockExcludesSecondAndResolvesAlias(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db.sqlite")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockJobDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias.sqlite")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	if release, err := lockJobDatabase(alias); err == nil {
		release()
		t.Fatal("second owner admitted")
	}
	unlock()
	release, err := lockJobDatabase(alias)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(path + ".jobs.lock"); err != nil {
		t.Fatal("lock inode removed")
	}
}
func TestJobOwnerLockRejectsSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path+".jobs.lock"); err != nil {
		t.Fatal(err)
	}
	if release, err := lockJobDatabase(path); err == nil {
		release()
		t.Fatal("symlink accepted")
	}
}
