package filesystem

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBrowserListsDirectoriesBeforeFilesAndPaginates(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "z.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	browser := NewBrowser()
	page, err := browser.List(context.Background(), Request{Path: root, Limit: 2})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Entries) != 2 || page.Entries[0].Name != "dir" || page.Entries[1].Name != "a.txt" {
		t.Fatalf("entries = %#v", page.Entries)
	}
	if page.NextCursor != "2" || page.Parent != filepath.Dir(root) {
		t.Fatalf("page = %#v", page)
	}

	next, err := browser.List(context.Background(), Request{Path: root, Cursor: page.NextCursor, Limit: 2})
	if err != nil {
		t.Fatalf("List(next) error = %v", err)
	}
	if len(next.Entries) != 1 || next.Entries[0].Name != "z.txt" || next.NextCursor != "" {
		t.Fatalf("next page = %#v", next)
	}
}

func TestRequestRejectsNonCanonicalPathsAndInvalidPagination(t *testing.T) {
	tests := []Request{
		{Path: "relative"},
		{Path: "/tmp", Cursor: "-1"},
		{Path: "/tmp", Cursor: "abc"},
		{Path: "/tmp", Limit: MaxPageSize + 1},
	}
	normalized, err := (Request{Path: "/tmp/../tmp/"}).Normalize()
	if err != nil || normalized.Path != "/tmp" {
		t.Fatalf("Normalize() = %#v, err = %v", normalized, err)
	}
	for _, request := range tests {
		if _, err := request.Normalize(); err == nil || ErrorCode(err) == "" {
			t.Errorf("Normalize(%#v) error = %v", request, err)
		}
	}
}

func TestBrowserMapsPathErrors(t *testing.T) {
	browser := NewBrowser()
	_, err := browser.List(context.Background(), Request{Path: filepath.Join(t.TempDir(), "missing")})
	if ErrorCode(err) != "FILES_PATH_NOT_FOUND" {
		t.Fatalf("ErrorCode() = %q, err = %v", ErrorCode(err), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = browser.List(ctx, Request{Path: "/tmp"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
}
