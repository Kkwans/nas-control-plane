package httpapi

import (
	"fmt"
	"strings"
	"testing"
	"unsafe"
)

func assertLogDedupBounds(t *testing.T, r *recentLogIDs) {
	t.Helper()
	if r.count > logDedupMaxIDs || len(r.seen) != r.count || r.bytes > logDedupMaxBytes {
		t.Fatalf("budget: count=%d map=%d bytes=%d", r.count, len(r.seen), r.bytes)
	}
	active := make(map[int]bool)
	bytes := 0
	for i := 0; i < r.count; i++ {
		index := (r.head + i) % logDedupMaxIDs
		active[index] = true
		id := r.ids[index]
		if _, ok := r.seen[id]; !ok {
			t.Fatal("ring ID missing from set")
		}
		bytes += len(id)
	}
	for i, id := range r.ids {
		if !active[i] && id != "" {
			t.Fatal("released slot retains ID")
		}
	}
	if bytes != r.bytes {
		t.Fatalf("bytes=%d want=%d", r.bytes, bytes)
	}
}

func TestRecentLogIDsCountFIFOAndWraparound(t *testing.T) {
	var r recentLogIDs
	for i := 0; i < logDedupMaxIDs*4; i++ {
		id := fmt.Sprint(i)
		if r.duplicate(id) || !r.duplicate(id) {
			t.Fatal("new/duplicate mismatch")
		}
		if i == logDedupMaxIDs-1 && !r.duplicate("0") {
			t.Fatal("oldest missing before eviction")
		}
		assertLogDedupBounds(t, &r)
		if i >= logDedupMaxIDs {
			if _, ok := r.seen[fmt.Sprint(i-logDedupMaxIDs)]; ok {
				t.Fatal("oldest was not evicted")
			}
		}
	}
	if r.duplicate("0") {
		t.Fatal("evicted ID was suppressed")
	}
}

func TestRecentLogIDsByteBoundaryAndReleasedSlots(t *testing.T) {
	var r recentLogIDs
	a, b := strings.Repeat("a", logDedupMaxBytes/2), strings.Repeat("b", logDedupMaxBytes/2)
	if r.duplicate(a) || r.duplicate(b) || r.bytes != logDedupMaxBytes {
		t.Fatal("exact budget not admitted")
	}
	if !r.duplicate(a) {
		t.Fatal("duplicate not suppressed")
	}
	if r.duplicate("c") {
		t.Fatal("new ID suppressed")
	}
	if _, ok := r.seen[a]; ok {
		t.Fatal("duplicate reordered FIFO")
	}
	assertLogDedupBounds(t, &r)
	whole := strings.Repeat("d", logDedupMaxBytes)
	if r.duplicate(whole) || r.count != 1 || !r.duplicate(whole) {
		t.Fatal("whole-budget ID failed")
	}
	assertLogDedupBounds(t, &r)
	if r.duplicate("e") || r.count != 1 || r.bytes != 1 {
		t.Fatal("whole-budget eviction failed")
	}
	assertLogDedupBounds(t, &r)
}

func TestRecentLogIDsOversizedAndEmpty(t *testing.T) {
	var r recentLogIDs
	if r.duplicate("") || !r.duplicate("") || r.count != 1 || r.bytes != 0 {
		t.Fatal("empty ID semantics changed")
	}
	oversized := strings.Repeat("x", logDedupMaxBytes+1)
	for i := 0; i < 3; i++ {
		if r.duplicate(oversized) || r.count != 1 || r.bytes != 0 {
			t.Fatal("oversized ID retained or suppressed")
		}
	}
	for i := 0; i < logDedupMaxIDs; i++ {
		r.duplicate(fmt.Sprint(i))
	}
	if r.duplicate("") {
		t.Fatal("empty ID not evicted")
	}
	assertLogDedupBounds(t, &r)
}

func TestRecentLogIDsCloneAndConnectionIsolation(t *testing.T) {
	payload := strings.Repeat("payload", 1024)
	id := payload[17:31]
	var a, b recentLogIDs
	if a.duplicate(id) || b.duplicate(id) {
		t.Fatal("connections share state")
	}
	if unsafe.StringData(a.ids[0]) == unsafe.StringData(id) {
		t.Fatal("ID retains payload backing storage")
	}
	for key := range a.seen {
		if unsafe.StringData(key) != unsafe.StringData(a.ids[0]) {
			t.Fatal("map and ring do not share cloned ID")
		}
	}
	if a.duplicate(strings.ToUpper(id)) {
		t.Fatal("IDs are not exact")
	}
	assertLogDedupBounds(t, &a)
}
