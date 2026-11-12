package database

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

type keyCreateBarrierReader struct {
	original io.Reader
	entered  chan struct{}
	release  chan struct{}
	calls    atomic.Int32
}

func (r *keyCreateBarrierReader) Read(p []byte) (int, error) {
	if r.calls.Add(1) == 1 {
		close(r.entered)
		<-r.release
	}
	return r.original.Read(p)
}

// 本测试不并行运行：临时替换随机源，使两个初始化精确停在存在性预检之后。
func TestKeyCreateDoesNotReplaceConcurrentWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deployment-key.json")
	original := rand.Reader
	barrier := &keyCreateBarrierReader{original: original, entered: make(chan struct{}), release: make(chan struct{})}
	rand.Reader = barrier
	defer func() { rand.Reader = original }()
	done := make(chan error, 1)
	go func() { _, err := CreateFileKeyProvider(path); done <- err }()
	<-barrier.entered
	winner, err := CreateFileKeyProvider(path)
	if err != nil {
		close(barrier.release)
		<-done
		t.Fatal(err)
	}
	wanted, err := winner.Current(context.Background())
	if err != nil {
		close(barrier.release)
		<-done
		t.Fatal(err)
	}
	close(barrier.release)
	firstErr := <-done
	actual, err := winner.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if firstErr == nil || !bytes.Equal(wanted.Key, actual.Key) {
		t.Fatalf("loser succeeded=%v, winner key overwritten=%v", firstErr == nil, !bytes.Equal(wanted.Key, actual.Key))
	}
}

func TestKeyCreatePreservesDanglingSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deployment-key.json")
	target := filepath.Join(t.TempDir(), "missing-key.json")
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := CreateFileKeyProvider(path); err == nil {
		t.Fatal("initialization replaced dangling symlink")
	}
	got, err := os.Readlink(path)
	if err != nil || got != target {
		t.Fatalf("existing symlink changed: %v", err)
	}
}
