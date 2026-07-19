package journal

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestQueryReturnsCursorPageAndUsesFixedJournalctlArguments(t *testing.T) {
	runner := &fakeRunner{outputs: [][]byte{
		[]byte(entryJSON("cursor-1", "2026-07-19T10:00:00Z", "first") + "\n" + entryJSON("cursor-2", "2026-07-19T10:00:01Z", "second") + "\n-- cursor: cursor-2\n"),
		[]byte(entryJSON("cursor-2", "2026-07-19T10:00:01Z", "second") + "\n"),
	}}
	reader := NewReader(runner)

	first, err := reader.Query(context.Background(), Query{Identifier: "ncp-p0-journal", Limit: 1})
	if err != nil {
		t.Fatalf("query first page: %v", err)
	}
	if len(first.Entries) != 1 || first.Entries[0].Cursor != "cursor-1" || first.NextCursor != "cursor-1" {
		t.Fatalf("first page = %#v", first)
	}
	if !hasArgumentSequence(runner.outputArgs[0], "--identifier", "ncp-p0-journal") || !hasArgumentSequence(runner.outputArgs[0], "--lines=2") {
		t.Fatalf("first query args = %#v", runner.outputArgs[0])
	}

	second, err := reader.Query(context.Background(), Query{Identifier: "ncp-p0-journal", Cursor: first.NextCursor, Limit: 1})
	if err != nil {
		t.Fatalf("query second page: %v", err)
	}
	if len(second.Entries) != 1 || second.Entries[0].Cursor != "cursor-2" || second.NextCursor != "" {
		t.Fatalf("second page = %#v", second)
	}
	if !hasArgumentSequence(runner.outputArgs[1], "--after-cursor", "cursor-1") {
		t.Fatalf("second query args = %#v", runner.outputArgs[1])
	}
}

func TestQueryRejectsUnsafeUnitBeforeExecutingJournalctl(t *testing.T) {
	runner := &fakeRunner{}
	reader := NewReader(runner)

	_, err := reader.Query(context.Background(), Query{Unit: "ssh.service;rm -rf /", Limit: 10})

	if ErrorCode(err) != "JOURNAL_QUERY_INVALID" {
		t.Fatalf("error code = %q", ErrorCode(err))
	}
	if len(runner.outputArgs) != 0 {
		t.Fatalf("unsafe query executed journalctl: %#v", runner.outputArgs)
	}
}

func TestQueryRedactsSensitiveFragmentsFromMessage(t *testing.T) {
	runner := &fakeRunner{outputs: [][]byte{[]byte(entryJSON("cursor-redact", "2026-07-19T10:00:03Z", "token=secret Authorization: Bearer abc cookie: value") + "\n")}}
	reader := NewReader(runner)

	page, err := reader.Query(context.Background(), Query{Limit: 10})
	if err != nil {
		t.Fatalf("query journal: %v", err)
	}
	if got := page.Entries[0].Message; strings.Contains(got, "secret") || strings.Contains(got, "abc") || strings.Contains(got, "value") {
		t.Fatalf("message was not redacted: %q", got)
	}
}

type fakeRunner struct {
	mu         sync.Mutex
	outputs    [][]byte
	outputArgs [][]string
}

func (f *fakeRunner) Output(_ context.Context, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outputArgs = append(f.outputArgs, append([]string(nil), args...))
	if len(f.outputs) == 0 {
		return nil, nil
	}
	output := f.outputs[0]
	f.outputs = f.outputs[1:]
	return output, nil
}

func entryJSON(cursor, timestamp, message string) string {
	return `{"__CURSOR":"` + cursor + `","__REALTIME_TIMESTAMP":"` + micros(timestamp) + `","_SYSTEMD_UNIT":"session-1.scope","SYSLOG_IDENTIFIER":"ncp-p0-journal","PRIORITY":"6","MESSAGE":"` + message + `"}`
}

func micros(timestamp string) string {
	parsed, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		panic(err)
	}
	return strconv.FormatInt(parsed.UnixMicro(), 10)
}

func hasArgumentSequence(args []string, values ...string) bool {
	for index := 0; index+len(values) <= len(args); index++ {
		matched := true
		for offset, value := range values {
			if args[index+offset] != value {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
