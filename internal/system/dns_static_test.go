package system

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestStaticResolvDNSControllerPreviewConfirmAndRollback(t *testing.T) {
	directory := t.TempDir()
	resolvPath := filepath.Join(directory, "resolv.conf")
	backupDir := filepath.Join(directory, "backups")
	original := []byte("# NAS generated\nsearch lan\nnameserver 192.168.5.1\noptions timeout:2\n")
	if err := os.WriteFile(resolvPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	controller, err := NewStaticResolvDNSController(resolvPath, backupDir)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := controller.Preview(context.Background(), DNSChangeRequest{Nameservers: []string{"1.1.1.1", "8.8.8.8"}})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Before.Nameservers[0] != "192.168.5.1" || preview.After.SearchDomains[0] != "lan" || !preview.RequiresConfirm {
		t.Fatalf("preview = %#v", preview)
	}
	if current, _ := os.ReadFile(resolvPath); string(current) != string(original) {
		t.Fatalf("preview mutated source: %q", current)
	}
	result, err := controller.Confirm(context.Background(), DNSChangeConfirmation{PreviewID: preview.PreviewID, Confirmed: true})
	if err != nil || !result.Applied || !result.RollbackAvailable {
		t.Fatalf("confirm = %#v, error = %v", result, err)
	}
	current, _ := os.ReadFile(resolvPath)
	if got := parseNameservers(string(current)); len(got) != 2 || got[0] != "1.1.1.1" || got[1] != "8.8.8.8" {
		t.Fatalf("applied nameservers = %#v, content = %q", got, current)
	}
	backups, err := filepath.Glob(filepath.Join(backupDir, "*.bak"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %#v, error = %v", backups, err)
	}
	// Rollback metadata is persisted so a restarted Agent can still safely
	// restore the exact change while rejecting any intervening edit.
	restarted, err := NewStaticResolvDNSController(resolvPath, backupDir)
	if err != nil {
		t.Fatal(err)
	}
	rolledBack, err := restarted.Rollback(context.Background(), DNSRollbackRequest{ChangeID: result.ChangeID})
	if err != nil || rolledBack.Applied || rolledBack.RollbackAvailable {
		t.Fatalf("rollback = %#v, error = %v", rolledBack, err)
	}
	current, _ = os.ReadFile(resolvPath)
	if string(current) != string(original) {
		t.Fatalf("rollback content = %q", current)
	}
}

func TestStaticResolvDNSControllerRejectsConcurrentChanges(t *testing.T) {
	directory := t.TempDir()
	resolvPath := filepath.Join(directory, "resolv.conf")
	if err := os.WriteFile(resolvPath, []byte("nameserver 192.168.5.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	controller, err := NewStaticResolvDNSController(resolvPath, filepath.Join(directory, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	preview, err := controller.Preview(context.Background(), DNSChangeRequest{Nameservers: []string{"1.1.1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resolvPath, []byte("nameserver 9.9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = controller.Confirm(context.Background(), DNSChangeConfirmation{PreviewID: preview.PreviewID, Confirmed: true})
	if err == nil || err.Error() != "DNS_SOURCE_CHANGED" {
		t.Fatalf("confirm error = %v", err)
	}
}

func TestStaticResolvDNSControllerRejectsSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.conf")
	link := filepath.Join(directory, "resolv.conf")
	if err := os.WriteFile(target, []byte("nameserver 192.168.5.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStaticResolvDNSController(link, filepath.Join(directory, "backups")); err == nil || err.Error() != "DNS_STATIC_FILE_UNSAFE" {
		t.Fatalf("constructor error = %v", err)
	}
}
