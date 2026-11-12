package compose

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContentHashIsCanonicalRawByteSHA256(t *testing.T) {
	for _, content := range []string{"", "abc", "\xef\xbb\xbfservices:\r\n  中文: 😀  \r\n\n", "services: {}\n", "services: {}\r\n", "services: {} \n"} {
		t.Run(fmt.Sprintf("%q", content), func(t *testing.T) {
			want := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
			got := ContentHash([]byte(content))
			parsed, err := parseContentHash(got)
			if got != want || !IsContentHash(got) || err != nil || parsed != sha256.Sum256([]byte(content)) {
				t.Fatalf("hash = %q, parsed = %x, error = %v; want %q", got, parsed, err, want)
			}
		})
	}
	if ContentHash([]byte("services: {}\n")) == ContentHash([]byte("services: {}\r\n")) {
		t.Fatal("different raw bytes have the same hash")
	}
}

func TestContentHashRejectsNonCanonicalValues(t *testing.T) {
	valid := ContentHash([]byte("services: old\n"))
	for _, value := range []string{"", " ", valid[:63], valid + "0", strings.ToUpper(valid), " " + valid, valid + "\n", strings.Repeat("g", 64), strings.Repeat("０", 64), strings.Repeat("0", 63) + "-"} {
		t.Run(fmt.Sprintf("%q", value), func(t *testing.T) {
			if IsContentHash(value) {
				t.Fatalf("accepted invalid hash %q", value)
			}
			if _, err := parseContentHash(value); err == nil {
				t.Fatalf("parsed invalid hash %q", value)
			}
		})
	}
	if !IsContentHash(strings.Repeat("0", 64)) {
		t.Fatal("canonical all-zero hash should be syntactically valid")
	}
}

func TestReadFileSnapshotPreservesRawUTF8Bytes(t *testing.T) {
	for _, content := range []string{"", "services: {}\n", "\xef\xbb\xbfservices:\r\n  中文: 😀  \r\n\n", "services: {}  \t\n"} {
		t.Run(fmt.Sprintf("%q", content), func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "compose.yaml")
			if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := readFileSnapshot(target, os.ReadFile)
			if err != nil || got.Path != target || got.Name != "compose.yaml" || got.Content != content || got.Size != int64(len(content)) || got.ContentHash != fmt.Sprintf("%x", sha256.Sum256([]byte(content))) {
				t.Fatalf("snapshot = %+v, error = %v", got, err)
			}
		})
	}
}

func TestReadFileSnapshotUsesCapturedBytesForSizeAndHash(t *testing.T) {
	target := filepath.Join(t.TempDir(), "compose.yaml")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	content := []byte("services:\r\n  中文: 😀\r\n")
	got, err := readFileSnapshot(target, func(string) ([]byte, error) { return content, nil })
	if err != nil || got.Content != string(content) || got.Size != int64(len(content)) || got.ContentHash != ContentHash(content) {
		t.Fatalf("snapshot = %+v, error = %v", got, err)
	}
}

func TestReadFileSnapshotRejectsInvalidUTF8AndOversizedReads(t *testing.T) {
	for _, test := range []struct {
		name    string
		content []byte
	}{
		{name: "invalid-UTF-8", content: []byte{'#', 0xff, '\n'}},
		{name: "truncated-UTF-8", content: []byte{0xe4, 0xb8}},
		{name: "grows-after-stat", content: []byte(strings.Repeat("x", maxConfigBytes+1))},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "compose.yaml")
			if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := readFileSnapshot(target, func(string) ([]byte, error) { return test.content, nil })
			if err == nil || got != (FileSnapshot{}) {
				t.Fatalf("invalid bytes produced snapshot = %+v, error = %v", got, err)
			}
		})
	}
}

func TestReadFileSnapshotRejectsOversizedFileBeforeReading(t *testing.T) {
	target := filepath.Join(t.TempDir(), "compose.yaml")
	if err := os.WriteFile(target, []byte(strings.Repeat("x", maxConfigBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := readFileSnapshot(target, func(string) ([]byte, error) {
		t.Fatal("oversized file was read")
		return nil, nil
	})
	if err == nil {
		t.Fatal("oversized snapshot accepted")
	}
}
