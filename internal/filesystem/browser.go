package filesystem

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultPageSize = 200
	MaxPageSize     = 1000
)

type EntryType string

const (
	EntryDirectory EntryType = "directory"
	EntryFile      EntryType = "file"
	EntrySymlink   EntryType = "symlink"
	EntryOther     EntryType = "other"
)

type Entry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Type     EntryType `json:"type"`
	Readable bool      `json:"readable"`
}

type Page struct {
	Path        string    `json:"path"`
	Parent      string    `json:"parent"`
	Entries     []Entry   `json:"entries"`
	NextCursor  string    `json:"nextCursor,omitempty"`
	CollectedAt time.Time `json:"collectedAt"`
}

type Provider interface {
	List(context.Context, Request) (Page, error)
}

type Request struct {
	Path   string
	Cursor string
	Limit  int
}

func (r Request) Normalize() (Request, error) {
	path := strings.TrimSpace(r.Path)
	if path == "" || !filepath.IsAbs(path) {
		return Request{}, coded("FILES_PATH_INVALID", errors.New("path must be a clean absolute path"))
	}
	path = filepath.Clean(path)
	if r.Cursor != "" {
		offset, err := strconv.Atoi(r.Cursor)
		if err != nil || offset < 0 {
			return Request{}, coded("FILES_CURSOR_INVALID", errors.New("cursor must be a non-negative offset"))
		}
	}
	if r.Limit == 0 {
		r.Limit = DefaultPageSize
	}
	if r.Limit < 1 || r.Limit > MaxPageSize {
		return Request{}, coded("FILES_LIMIT_INVALID", errors.New("limit is out of range"))
	}
	r.Path = path
	return r, nil
}

type Browser struct {
	readDir func(string) ([]fs.DirEntry, error)
	now     func() time.Time
}

func NewBrowser() *Browser {
	return &Browser{readDir: os.ReadDir, now: time.Now}
}

func (b *Browser) List(ctx context.Context, request Request) (Page, error) {
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	normalized, err := request.Normalize()
	if err != nil {
		return Page{}, err
	}
	entries, err := b.readDir(normalized.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Page{}, coded("FILES_PATH_NOT_FOUND", err)
		}
		if errors.Is(err, os.ErrPermission) {
			return Page{}, coded("FILES_PATH_UNREADABLE", err)
		}
		return Page{}, coded("FILES_DIRECTORY_READ_FAILED", err)
	}
	select {
	case <-ctx.Done():
		return Page{}, ctx.Err()
	default:
	}
	sort.SliceStable(entries, func(i, j int) bool {
		iDir, jDir := entryIsDirectory(entries[i]), entryIsDirectory(entries[j])
		if iDir != jDir {
			return iDir
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	offset := 0
	if normalized.Cursor != "" {
		offset, _ = strconv.Atoi(normalized.Cursor)
	}
	if offset > len(entries) {
		offset = len(entries)
	}
	end := offset + normalized.Limit
	if end > len(entries) {
		end = len(entries)
	}
	result := make([]Entry, 0, end-offset)
	for _, item := range entries[offset:end] {
		if err := ctx.Err(); err != nil {
			return Page{}, err
		}
		path := filepath.Join(normalized.Path, item.Name())
		result = append(result, Entry{Name: item.Name(), Path: path, Type: entryType(item), Readable: entryReadable(path)})
	}
	page := Page{Path: normalized.Path, Parent: filepath.Dir(normalized.Path), Entries: result, CollectedAt: b.now().UTC()}
	if end < len(entries) {
		page.NextCursor = strconv.Itoa(end)
	}
	return page, nil
}

func entryIsDirectory(entry fs.DirEntry) bool {
	return entry.IsDir()
}

func entryType(entry fs.DirEntry) EntryType {
	if entry.IsDir() {
		return EntryDirectory
	}
	if entry.Type()&os.ModeSymlink != 0 {
		return EntrySymlink
	}
	if entry.Type().IsRegular() {
		return EntryFile
	}
	return EntryOther
}

func entryReadable(path string) bool {
	handle, err := os.Open(path)
	if err != nil {
		return false
	}
	_ = handle.Close()
	return true
}

func coded(code string, err error) error {
	if err == nil {
		return errors.New(code)
	}
	return &codedError{code: code, err: err}
}

type codedError struct {
	code string
	err  error
}

func (e *codedError) Error() string { return e.code }
func (e *codedError) Unwrap() error { return e.err }

func ErrorCode(err error) string {
	var codedErr *codedError
	if errors.As(err, &codedErr) {
		return codedErr.code
	}
	return ""
}
