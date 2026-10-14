package filesystem

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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

func TestBrowserChecksReadAccessForEveryEntryTypeWithoutOpeningEntries(t *testing.T) {
	root := t.TempDir()
	items := []fs.DirEntry{
		fakeDirEntry{name: "pipe", mode: os.ModeNamedPipe},
		fakeDirEntry{name: "link", mode: os.ModeSymlink},
		fakeDirEntry{name: "file"},
		fakeDirEntry{name: "dir", mode: os.ModeDir},
	}
	browser := NewBrowser()
	browser.readDir = func(path string) ([]fs.DirEntry, error) {
		if path != root {
			t.Fatalf("readDir path = %q", path)
		}
		return items, nil
	}
	var checked []string
	browser.checkReadAccess = func(path string) error {
		checked = append(checked, path)
		return nil
	}
	page, err := browser.List(context.Background(), Request{Path: root})
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := []EntryType{EntryDirectory, EntryFile, EntrySymlink, EntryOther}
	wantNames := []string{"dir", "file", "link", "pipe"}
	if len(page.Entries) != len(wantNames) || len(checked) != len(wantNames) {
		t.Fatalf("entries = %#v, checked = %#v", page.Entries, checked)
	}
	for i, entry := range page.Entries {
		wantPath := filepath.Join(root, wantNames[i])
		if entry.Name != wantNames[i] || entry.Type != wantTypes[i] || entry.Path != wantPath || !entry.Readable || checked[i] != wantPath {
			t.Errorf("entry[%d] = %#v, checked path = %q", i, entry, checked[i])
		}
	}
	// 所有条目仅由 fake 提供，磁盘上没有任何对应文件或特殊端点。
	actual, err := os.ReadDir(root)
	if err != nil || len(actual) != 0 {
		t.Fatalf("temporary directory changed: entries = %#v, err = %v", actual, err)
	}
}

func TestBrowserReadAccessFailuresKeepEntriesUnreadable(t *testing.T) {
	for _, accessErr := range []error{os.ErrNotExist, os.ErrPermission, errors.New("metadata unavailable")} {
		t.Run(accessErr.Error(), func(t *testing.T) {
			root := t.TempDir()
			browser := NewBrowser()
			browser.readDir = func(string) ([]fs.DirEntry, error) {
				return []fs.DirEntry{fakeDirEntry{name: "entry"}}, nil
			}
			browser.checkReadAccess = func(path string) error {
				if path != filepath.Join(root, "entry") {
					t.Fatalf("checked path = %q", path)
				}
				return accessErr
			}
			page, err := browser.List(context.Background(), Request{Path: root})
			if err != nil || len(page.Entries) != 1 || page.Entries[0].Readable {
				t.Fatalf("page = %#v, err = %v", page, err)
			}
		})
	}
}

func TestBrowserChecksReadAccessOnlyWithinPage(t *testing.T) {
	root := t.TempDir()
	browser := NewBrowser()
	browser.readDir = func(string) ([]fs.DirEntry, error) {
		return []fs.DirEntry{fakeDirEntry{name: "c"}, fakeDirEntry{name: "a"}, fakeDirEntry{name: "b"}}, nil
	}
	var checked []string
	browser.checkReadAccess = func(path string) error {
		checked = append(checked, path)
		return nil
	}
	page, err := browser.List(context.Background(), Request{Path: root, Cursor: "1", Limit: 1})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Name != "b" || page.NextCursor != "2" {
		t.Fatalf("page = %#v, err = %v", page, err)
	}
	if !reflect.DeepEqual(checked, []string{filepath.Join(root, "b")}) {
		t.Fatalf("checked paths = %#v", checked)
	}
}

func TestBrowserHonorsCancellationDuringReadAccess(t *testing.T) {
	for _, count := range []int{1, 2} {
		t.Run(strconv.Itoa(count)+" entries", func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			browser := NewBrowser()
			browser.readDir = func(string) ([]fs.DirEntry, error) {
				items := []fs.DirEntry{fakeDirEntry{name: "a"}, fakeDirEntry{name: "b"}}
				return items[:count], nil
			}
			checked := 0
			browser.checkReadAccess = func(string) error {
				checked++
				cancel()
				return nil
			}
			page, err := browser.List(ctx, Request{Path: t.TempDir()})
			if !errors.Is(err, context.Canceled) || checked != 1 || len(page.Entries) != 0 {
				t.Fatalf("page = %#v, err = %v, checked = %d", page, err, checked)
			}
		})
	}
}

func TestBrowserHonorsCancellationDuringSortForEmptyPage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	browser := NewBrowser()
	browser.readDir = func(string) ([]fs.DirEntry, error) {
		return []fs.DirEntry{
			fakeDirEntry{name: "b", onIsDir: cancel},
			fakeDirEntry{name: "a", onIsDir: cancel},
		}, nil
	}
	browser.checkReadAccess = func(string) error {
		t.Fatal("empty page must not check read access")
		return nil
	}
	page, err := browser.List(ctx, Request{Path: t.TempDir(), Cursor: "2"})
	if !errors.Is(err, context.Canceled) || len(page.Entries) != 0 {
		t.Fatalf("page = %#v, err = %v", page, err)
	}
}

type fakeDirEntry struct {
	name    string
	mode    fs.FileMode
	onIsDir func()
}

func (e fakeDirEntry) Name() string      { return e.name }
func (e fakeDirEntry) Type() fs.FileMode { return e.mode }
func (e fakeDirEntry) IsDir() bool {
	if e.onIsDir != nil {
		e.onIsDir()
	}
	return e.mode.IsDir()
}
func (e fakeDirEntry) Info() (fs.FileInfo, error) {
	panic("directory browsing must not request fake entry metadata")
}
