package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBrowserPreservesTrailingWhitespacePathIdentity(t *testing.T) {
	root := t.TempDir()
	names := []string{"selected", "selected ", "selected\t", "selected\u00a0", "selected\u3000"}
	for _, name := range names {
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "marker"), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	browser := NewBrowser()
	parent, err := browser.List(context.Background(), Request{Path: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(parent.Entries) != len(names) {
		t.Fatalf("parent entries = %#v", parent.Entries)
	}
	for _, selected := range parent.Entries {
		child, err := browser.List(context.Background(), Request{Path: selected.Path})
		if err != nil {
			t.Fatal(err)
		}
		if child.Path != selected.Path || len(child.Entries) != 1 || child.Entries[0].Path != filepath.Join(selected.Path, "marker") {
			t.Fatalf("selected = %#v, child = %#v", selected, child)
		}
		data, err := os.ReadFile(child.Entries[0].Path)
		if err != nil || string(data) != selected.Name {
			t.Fatalf("selected %q read marker %q, err = %v", selected.Name, data, err)
		}
	}
}

func TestFilesystemRequestPreservesPathWhitespaceAndRejectsRelativeWhitespace(t *testing.T) {
	for _, path := range []string{"/selected ", "/selected\t", "/selected\u00a0", "/selected\u3000", "/ "} {
		normalized, err := (Request{Path: path}).Normalize()
		if err != nil || normalized.Path != path {
			t.Errorf("Normalize(%q) = %#v, err = %v", path, normalized, err)
		}
	}
	for _, path := range []string{"", " \t", " /selected", "\t/selected", "\u00a0/selected", "\u3000/selected", "relative "} {
		if _, err := (Request{Path: path}).Normalize(); ErrorCode(err) != "FILES_PATH_INVALID" {
			t.Errorf("Normalize(%q) error = %v", path, err)
		}
	}
}
