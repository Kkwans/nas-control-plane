package journal

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestRunPOCVerifiesHistoricalFiltersCursorAndFollowCancellation(t *testing.T) {
	reader := newFakePOCReader([]Page{
		{Entries: []Entry{{Cursor: "cursor-1"}}, NextCursor: "cursor-1"},
		{Entries: []Entry{{Cursor: "cursor-2", Unit: "session-1.scope"}}},
		{Entries: []Entry{{Cursor: "cursor-1", Unit: "session-1.scope"}}},
	})
	writer := &fakeMarkerWriter{afterWrite: func(call int, _, message string) {
		if call == 3 {
			reader.entries <- Entry{Cursor: "cursor-follow", Message: message}
		}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result, err := RunPOC(ctx, reader, writer)

	if err != nil {
		t.Fatalf("run journal POC: %v", err)
	}
	if !result.Query || !result.CursorPagination || !result.UnitFilter || !result.TimeFilter || !result.Follow || !result.FollowCanceled {
		t.Fatalf("result = %#v", result)
	}
	if len(writer.calls) != 3 {
		t.Fatalf("marker writes = %#v", writer.calls)
	}
	if len(reader.queries) != 3 {
		t.Fatalf("query count = %d", len(reader.queries))
	}
	if reader.queries[0].Identifier != result.Identifier || reader.queries[0].Limit != 1 || reader.queries[1].Cursor != "cursor-1" {
		t.Fatalf("cursor query sequence = %#v", reader.queries)
	}
	filterQuery := reader.queries[2]
	if filterQuery.Unit != "session-1.scope" || filterQuery.Since == nil || filterQuery.Until == nil {
		t.Fatalf("filter query = %#v", filterQuery)
	}
	if reader.followQuery.Identifier != result.Identifier {
		t.Fatalf("follow query = %#v", reader.followQuery)
	}
}

func TestWaitForPageUsesStageSpecificTimeoutCode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	_, err := waitForPage(ctx, emptyPOCReader{}, Query{}, "JOURNAL_POC_CURSOR_TIMEOUT", func(Page) bool { return false })

	if ErrorCode(err) != "JOURNAL_POC_CURSOR_TIMEOUT" {
		t.Fatalf("error code = %q", ErrorCode(err))
	}
}

type fakePOCReader struct {
	mu          sync.Mutex
	pages       []Page
	queries     []Query
	followQuery Query
	entries     chan Entry
	done        chan error
}

type emptyPOCReader struct{}

func (emptyPOCReader) Query(context.Context, Query) (Page, error) {
	return Page{}, nil
}

func (emptyPOCReader) Follow(context.Context, Query) (Stream, error) {
	return Stream{}, nil
}

func newFakePOCReader(pages []Page) *fakePOCReader {
	return &fakePOCReader{
		pages:   pages,
		entries: make(chan Entry, 1),
		done:    make(chan error, 1),
	}
}

func (f *fakePOCReader) Query(_ context.Context, query Query) (Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, query)
	page := f.pages[0]
	f.pages = f.pages[1:]
	return page, nil
}

func (f *fakePOCReader) Follow(ctx context.Context, query Query) (Stream, error) {
	f.mu.Lock()
	f.followQuery = query
	f.mu.Unlock()
	go func() {
		<-ctx.Done()
		close(f.entries)
		f.done <- coded("JOURNAL_FOLLOW_CANCELED", ctx.Err())
		close(f.done)
	}()
	return Stream{Entries: f.entries, Done: f.done}, nil
}

type markerCall struct {
	identifier string
	message    string
}

type fakeMarkerWriter struct {
	calls      []markerCall
	afterWrite func(int, string, string)
}

func (f *fakeMarkerWriter) Write(_ context.Context, identifier, message string) error {
	f.calls = append(f.calls, markerCall{identifier: identifier, message: message})
	if f.afterWrite != nil {
		f.afterWrite(len(f.calls), identifier, message)
	}
	return nil
}
