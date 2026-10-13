//go:build linux

package agentsocket

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

type fakeRecoveryDirectory struct {
	entries   []socketEntry
	errors    []error
	checks    int
	removes   int
	removeErr error
}

func (d *fakeRecoveryDirectory) inspect() (socketEntry, error) {
	i := d.checks
	d.checks++
	if i >= len(d.entries) {
		i = len(d.entries) - 1
	}
	var err error
	if d.checks-1 < len(d.errors) {
		err = d.errors[d.checks-1]
	}
	return d.entries[i], err
}
func (d *fakeRecoveryDirectory) remove() error { d.removes++; return d.removeErr }

func TestRecoverStaleSocketDecision(t *testing.T) {
	owned := socketEntry{mode: unix.S_IFSOCK | 0o660, uid: 42, dev: 1, ino: 20, links: 1, changed: unix.Timespec{Sec: 10}}
	changed := func(f func(*socketEntry)) socketEntry { e := owned; f(&e); return e }
	tests := []struct {
		name       string
		first      socketEntry
		second     socketEntry
		statErrors []error
		probeErr   error
		removeErr  error
		code       string
		probes     int
		removes    int
	}{
		{name: "missing", first: owned, statErrors: []error{os.ErrNotExist}},
		{name: "stat denied", first: owned, statErrors: []error{os.ErrPermission}, code: "AGENT_SOCKET_PATH_CHECK_FAILED"},
		{name: "ordinary file", first: changed(func(e *socketEntry) { e.mode = unix.S_IFREG | 0o600 }), code: "AGENT_SOCKET_PATH_OCCUPIED"},
		{name: "symlink", first: changed(func(e *socketEntry) { e.mode = unix.S_IFLNK | 0o777 }), code: "AGENT_SOCKET_PATH_OCCUPIED"},
		{name: "directory", first: changed(func(e *socketEntry) { e.mode = unix.S_IFDIR | 0o750 }), code: "AGENT_SOCKET_PATH_OCCUPIED"},
		{name: "unowned", first: changed(func(e *socketEntry) { e.uid++ }), code: "AGENT_SOCKET_PATH_OCCUPIED"},
		{name: "hard linked", first: changed(func(e *socketEntry) { e.links++ }), code: "AGENT_SOCKET_PATH_OCCUPIED"},
		{name: "active", first: owned, code: "AGENT_SOCKET_PATH_OCCUPIED", probes: 1},
		{name: "permission denied", first: owned, probeErr: unix.EACCES, code: "AGENT_SOCKET_PATH_OCCUPIED", probes: 1},
		{name: "timed out", first: owned, probeErr: os.ErrDeadlineExceeded, code: "AGENT_SOCKET_PATH_OCCUPIED", probes: 1},
		{name: "disappeared on dial", first: owned, probeErr: unix.ENOENT, code: "AGENT_SOCKET_PATH_OCCUPIED", probes: 1},
		{name: "different protocol", first: owned, probeErr: unix.EPROTOTYPE, code: "AGENT_SOCKET_PATH_OCCUPIED", probes: 1},
		{name: "unknown", first: owned, probeErr: errors.New("unknown"), code: "AGENT_SOCKET_PATH_OCCUPIED", probes: 1},
		{name: "stale", first: owned, second: owned, probeErr: unix.ECONNREFUSED, probes: 1, removes: 1},
		{name: "wrapped refusal", first: owned, second: owned, probeErr: &net.OpError{Op: "dial", Net: "unix", Err: os.NewSyscallError("connect", unix.ECONNREFUSED)}, probes: 1, removes: 1},
		{name: "replaced inode", first: owned, second: changed(func(e *socketEntry) { e.ino++ }), probeErr: unix.ECONNREFUSED, code: "AGENT_SOCKET_PATH_OCCUPIED", probes: 1},
		{name: "replaced device", first: owned, second: changed(func(e *socketEntry) { e.dev++ }), probeErr: unix.ECONNREFUSED, code: "AGENT_SOCKET_PATH_OCCUPIED", probes: 1},
		{name: "changed timestamp", first: owned, second: changed(func(e *socketEntry) { e.changed.Nsec++ }), probeErr: unix.ECONNREFUSED, code: "AGENT_SOCKET_PATH_OCCUPIED", probes: 1},
		{name: "changed ownership", first: owned, second: changed(func(e *socketEntry) { e.uid++ }), probeErr: unix.ECONNREFUSED, code: "AGENT_SOCKET_PATH_OCCUPIED", probes: 1},
		{name: "replacement symlink", first: owned, second: changed(func(e *socketEntry) { e.mode = unix.S_IFLNK }), probeErr: unix.ECONNREFUSED, code: "AGENT_SOCKET_PATH_OCCUPIED", probes: 1},
		{name: "disappeared on recheck", first: owned, second: owned, statErrors: []error{nil, os.ErrNotExist}, probeErr: unix.ECONNREFUSED, code: "AGENT_SOCKET_PATH_OCCUPIED", probes: 1},
		{name: "recheck denied", first: owned, second: owned, statErrors: []error{nil, os.ErrPermission}, probeErr: unix.ECONNREFUSED, code: "AGENT_SOCKET_PATH_OCCUPIED", probes: 1},
		{name: "unlink denied", first: owned, second: owned, probeErr: unix.ECONNREFUSED, removeErr: os.ErrPermission, code: "AGENT_SOCKET_STALE_REMOVE_FAILED", probes: 1, removes: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &fakeRecoveryDirectory{entries: []socketEntry{tt.first, tt.second}, errors: tt.statErrors, removeErr: tt.removeErr}
			probes := 0
			err := recoverStaleSocket(d, 42, func() error { probes++; return tt.probeErr })
			if ErrorCode(err) != tt.code || probes != tt.probes || d.removes != tt.removes {
				t.Fatalf("code=%q probes=%d removes=%d; want %q %d %d", ErrorCode(err), probes, d.removes, tt.code, tt.probes, tt.removes)
			}
		})
	}
}

func TestRemoveOwnedSocketPreservesReplacements(t *testing.T) {
	original := socketEntry{mode: unix.S_IFSOCK | 0o660, uid: 42, dev: 1, ino: 20, links: 1}
	for _, name := range []string{"same", "different inode", "ordinary file", "symlink", "changed owner", "changed timestamp", "missing"} {
		t.Run(name, func(t *testing.T) {
			current := original
			var statErr error
			switch name {
			case "different inode":
				current.ino++
			case "ordinary file":
				current.mode = unix.S_IFREG
			case "symlink":
				current.mode = unix.S_IFLNK
			case "changed owner":
				current.uid++
			case "changed timestamp":
				current.changed.Sec++
			case "missing":
				statErr = os.ErrNotExist
			}
			d := &fakeRecoveryDirectory{entries: []socketEntry{current}, errors: []error{statErr}}
			removeOwnedSocket(d, original)
			want := 0
			if name == "same" {
				want = 1
			}
			if d.removes != want {
				t.Fatalf("removes=%d want=%d", d.removes, want)
			}
		})
	}
}

func TestTrustedSocketDirectory(t *testing.T) {
	for _, tt := range []struct {
		name  string
		mode  uint32
		owner uint32
		final bool
		want  bool
	}{
		{"root runtime group access", unix.S_IFDIR | 0o750, 0, true, true},
		{"own private directory", unix.S_IFDIR | 0o700, 42, true, true},
		{"unowned", unix.S_IFDIR | 0o750, 43, true, false},
		{"group writable", unix.S_IFDIR | 0o770, 0, true, false},
		{"world writable", unix.S_IFDIR | 0o777, 0, false, false},
		{"sticky root ancestor", unix.S_IFDIR | unix.S_ISVTX | 0o777, 0, false, true},
		{"sticky final", unix.S_IFDIR | unix.S_ISVTX | 0o777, 0, true, false},
		{"unowned sticky ancestor", unix.S_IFDIR | unix.S_ISVTX | 0o777, 43, false, false},
		{"symlink", unix.S_IFLNK | 0o750, 0, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := trustedSocketDirectory(unix.Stat_t{Mode: tt.mode, Uid: tt.owner}, 42, tt.final); got != tt.want {
				t.Fatalf("trusted=%v want=%v", got, tt.want)
			}
		})
	}
}

func TestPinnedSocketDirectoryLockAndPaths(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "agent.sock")
	first, err := pinSocketDirectory(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.close()
	if second, err := pinSocketDirectory(path); err == nil {
		second.close()
		t.Fatal("concurrent startup acquired directory lock")
	}
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	entry, err := first.inspect()
	if err != nil || entry.mode&unix.S_IFMT != unix.S_IFREG {
		t.Fatalf("inspect=%+v err=%v", entry, err)
	}
	if err := recoverStaleSocket(first, uint32(os.Geteuid()), func() error { t.Fatal("must not probe ordinary file"); return nil }); ErrorCode(err) != "AGENT_SOCKET_PATH_OCCUPIED" {
		t.Fatalf("error=%v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "keep" {
		t.Fatalf("file changed: %q %v", contents, err)
	}
}

func TestPinnedSocketDirectoryRejectsSymlinkAndWritablePaths(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{alias, filepath.Join(alias, "child")} {
		if d, err := pinSocketDirectory(filepath.Join(directory, "agent.sock")); err == nil {
			d.close()
			t.Fatalf("accepted symlink directory %s", directory)
		}
	}
	if _, err := os.Stat(filepath.Join(target, "child")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("created child through symlink")
	}
	if err := os.Chmod(target, 0o770); err != nil {
		t.Fatal(err)
	}
	if d, err := pinSocketDirectory(filepath.Join(target, "agent.sock")); err == nil {
		d.close()
		t.Fatal("accepted group writable directory")
	}
}
