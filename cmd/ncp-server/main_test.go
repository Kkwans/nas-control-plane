package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestRunAgentProbeRejectsUnexpectedArguments(t *testing.T) {
	if err := runAgentProbe(context.Background(), []string{"unexpected"}, io.Discard); err == nil {
		t.Fatal("agent-probe 应拒绝额外位置参数")
	}
}

func TestRunHTTPServerRejectsUnexpectedArguments(t *testing.T) {
	if err := runHTTPServer(context.Background(), []string{"unexpected"}); err == nil {
		t.Fatal("serve 应拒绝额外位置参数")
	}
}

func TestRunDatabaseKeyInitCreatesPrivateKeyAndRefusesReplacement(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "secrets", "database-credentials.key")
	if err := runDatabaseKeyInit([]string{"--path", keyPath}); err != nil {
		t.Fatalf("database-key-init error = %v", err)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("key permissions = %o, want 600", permissions)
	}
	if err := runDatabaseKeyInit([]string{"--path", keyPath}); err == nil {
		t.Fatal("database-key-init replaced an existing key")
	}
}

func TestRunDatabaseKeyInitRejectsMissingPath(t *testing.T) {
	if err := runDatabaseKeyInit(nil); err == nil {
		t.Fatal("database-key-init accepted an empty path")
	}
}
