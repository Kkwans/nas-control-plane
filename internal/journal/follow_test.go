package journal

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestFollowDeliversEntryAndStopsWhenContextIsCanceled(t *testing.T) {
	pipeReader, pipeWriter := io.Pipe()
	runner := &fakeRunner{followReader: pipeReader, followWait: make(chan error, 1)}
	reader := NewReader(runner)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := reader.Follow(ctx, Query{Identifier: "ncp-p0-journal"})
	if err != nil {
		t.Fatalf("start follow: %v", err)
	}
	if _, err := io.WriteString(pipeWriter, entryJSON("cursor-follow", "2026-07-19T10:00:02Z", "follow")+"\n"); err != nil {
		t.Fatalf("write follow entry: %v", err)
	}
	select {
	case entry := <-stream.Entries:
		if entry.Cursor != "cursor-follow" || entry.Message != "follow" {
			t.Fatalf("follow entry = %#v", entry)
		}
	case <-time.After(time.Second):
		t.Fatal("follow did not deliver entry")
	}

	cancel()
	select {
	case doneErr := <-stream.Done:
		if ErrorCode(doneErr) != "JOURNAL_FOLLOW_CANCELED" {
			t.Fatalf("follow completion code = %q", ErrorCode(doneErr))
		}
	case <-time.After(time.Second):
		t.Fatal("follow did not stop after context cancellation")
	}
	if !hasArgumentSequence(runner.followArgs, "--follow", "--lines=0") {
		t.Fatalf("follow args = %#v", runner.followArgs)
	}
}

func TestFollowRejectsUnsafeUnitBeforeExecutingJournalctl(t *testing.T) {
	runner := &fakeRunner{}
	reader := NewReader(runner)

	_, err := reader.Follow(context.Background(), Query{Unit: "ssh.service;rm -rf /"})

	if ErrorCode(err) != "JOURNAL_QUERY_INVALID" {
		t.Fatalf("error code = %q", ErrorCode(err))
	}
	if runner.followArgs != nil {
		t.Fatalf("unsafe follow executed journalctl: %#v", runner.followArgs)
	}
}
