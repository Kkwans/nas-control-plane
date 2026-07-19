package journal

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

const (
	pocTimeout       = 8 * time.Second
	pocRetryInterval = 100 * time.Millisecond

	pocQueryMarkerOne = "NCP_P0_JOURNAL_QUERY_ONE"
	pocQueryMarkerTwo = "NCP_P0_JOURNAL_QUERY_TWO"
	pocFollowMarker   = "NCP_P0_JOURNAL_FOLLOW"
)

// POCReader is the read-only subset used by the controlled P0 journald check.
type POCReader interface {
	Query(context.Context, Query) (Page, error)
	Follow(context.Context, Query) (Stream, error)
}

// MarkerWriter writes one of the fixed, non-sensitive P0 journald markers.
type MarkerWriter interface {
	Write(context.Context, string, string) error
}

// POCResult records only verification flags and its temporary journal tag. It
// intentionally does not return raw journal messages.
type POCResult struct {
	Identifier       string `json:"identifier"`
	Query            bool   `json:"query"`
	CursorPagination bool   `json:"cursorPagination"`
	UnitFilter       bool   `json:"unitFilter"`
	TimeFilter       bool   `json:"timeFilter"`
	Follow           bool   `json:"follow"`
	FollowCanceled   bool   `json:"followCanceled"`
}

// RunPOC writes three fixed test markers, then verifies a bounded, read-only
// historical and follow path. Marker records remain subject to journald's
// normal retention policy and contain no secrets or user input.
func RunPOC(ctx context.Context, reader POCReader, writer MarkerWriter) (POCResult, error) {
	if reader == nil || writer == nil {
		return POCResult{}, coded("JOURNAL_POC_UNAVAILABLE", errors.New("journal POC dependencies are required"))
	}
	boundedContext, cancel := context.WithTimeout(ctx, pocTimeout)
	defer cancel()
	identifier, err := newPOCIdentifier()
	if err != nil {
		return POCResult{}, err
	}
	result := POCResult{Identifier: identifier}
	started := time.Now().UTC().Add(-time.Second)

	if err := writeMarker(boundedContext, writer, identifier, pocQueryMarkerOne); err != nil {
		return POCResult{}, err
	}
	if err := writeMarker(boundedContext, writer, identifier, pocQueryMarkerTwo); err != nil {
		return POCResult{}, err
	}

	first, err := waitForPage(boundedContext, reader, Query{Identifier: identifier, Limit: 1}, "JOURNAL_POC_QUERY_TIMEOUT", func(page Page) bool {
		return len(page.Entries) == 1 && page.NextCursor != ""
	})
	if err != nil {
		return POCResult{}, err
	}
	result.Query = true
	firstEntry := first.Entries[0]

	second, err := waitForPage(boundedContext, reader, Query{Identifier: identifier, Cursor: first.NextCursor, Limit: 1}, "JOURNAL_POC_CURSOR_TIMEOUT", func(page Page) bool {
		return len(page.Entries) == 1
	})
	if err != nil {
		return POCResult{}, err
	}
	if second.Entries[0].Cursor == firstEntry.Cursor {
		return POCResult{}, coded("JOURNAL_POC_CURSOR_FAILED", errors.New("journal cursor did not advance"))
	}
	result.CursorPagination = true

	unit := firstEntry.Unit
	if unit == "" {
		unit = second.Entries[0].Unit
	}
	if unit == "" {
		return POCResult{}, coded("JOURNAL_POC_UNIT_UNAVAILABLE", errors.New("journal record has no systemd unit"))
	}
	until := time.Now().UTC().Add(time.Second)
	if _, err := waitForPage(boundedContext, reader, Query{
		Identifier: identifier,
		Unit:       unit,
		Since:      &started,
		Until:      &until,
		Limit:      1,
	}, "JOURNAL_POC_FILTER_TIMEOUT", func(page Page) bool {
		return len(page.Entries) == 1
	}); err != nil {
		return POCResult{}, err
	}
	result.UnitFilter = true
	result.TimeFilter = true

	followContext, cancelFollow := context.WithCancel(boundedContext)
	defer cancelFollow()
	stream, err := reader.Follow(followContext, Query{Identifier: identifier, Since: &started})
	if err != nil {
		return POCResult{}, coded("JOURNAL_POC_FOLLOW_FAILED", err)
	}
	if err := writeMarker(boundedContext, writer, identifier, pocFollowMarker); err != nil {
		cancelFollow()
		return POCResult{}, err
	}
	if err := waitForFollowMarker(boundedContext, stream.Entries, pocFollowMarker); err != nil {
		cancelFollow()
		return POCResult{}, err
	}
	result.Follow = true

	cancelFollow()
	if err := waitForFollowCancellation(boundedContext, stream.Done); err != nil {
		return POCResult{}, err
	}
	result.FollowCanceled = true
	return result, nil
}

func newPOCIdentifier() (string, error) {
	var suffix [8]byte
	if _, err := cryptorand.Read(suffix[:]); err != nil {
		return "", coded("JOURNAL_POC_FAILED", err)
	}
	return fmt.Sprintf("ncp-p0-journal-%x", suffix), nil
}

func writeMarker(ctx context.Context, writer MarkerWriter, identifier, marker string) error {
	if err := writer.Write(ctx, identifier, marker); err != nil {
		if ctx.Err() != nil {
			return coded("JOURNAL_POC_MARKER_TIMEOUT", ctx.Err())
		}
		return coded("JOURNAL_POC_MARKER_FAILED", err)
	}
	return nil
}

func waitForPage(ctx context.Context, reader POCReader, query Query, timeoutCode string, ready func(Page) bool) (Page, error) {
	for {
		page, err := reader.Query(ctx, query)
		if err != nil {
			if ctx.Err() != nil {
				return Page{}, coded(timeoutCode, ctx.Err())
			}
			return Page{}, coded("JOURNAL_POC_QUERY_FAILED", err)
		}
		if ready(page) {
			return page, nil
		}
		timer := time.NewTimer(pocRetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Page{}, coded(timeoutCode, ctx.Err())
		case <-timer.C:
		}
	}
}

func waitForFollowMarker(ctx context.Context, entries <-chan Entry, marker string) error {
	for {
		select {
		case <-ctx.Done():
			return coded("JOURNAL_POC_FOLLOW_TIMEOUT", ctx.Err())
		case entry, open := <-entries:
			if !open {
				return coded("JOURNAL_POC_FOLLOW_FAILED", io.EOF)
			}
			if strings.Contains(entry.Message, marker) {
				return nil
			}
		}
	}
}

func waitForFollowCancellation(ctx context.Context, done <-chan error) error {
	select {
	case <-ctx.Done():
		return coded("JOURNAL_POC_CANCEL_TIMEOUT", ctx.Err())
	case terminalErr, open := <-done:
		if !open {
			return coded("JOURNAL_POC_FOLLOW_FAILED", io.EOF)
		}
		if ErrorCode(terminalErr) != "JOURNAL_FOLLOW_CANCELED" {
			return coded("JOURNAL_POC_FOLLOW_FAILED", terminalErr)
		}
		return nil
	}
}

// OSMarkerWriter is deliberately limited to P0's three fixed marker messages.
// It cannot be used as a generic host command execution surface.
type OSMarkerWriter struct{}

func (OSMarkerWriter) Write(ctx context.Context, identifier, marker string) error {
	if err := validateFilterValue(identifier); err != nil || !isPOCMarker(marker) {
		return coded("JOURNAL_POC_MARKER_INVALID", errors.New("journal POC marker is invalid"))
	}
	command := exec.CommandContext(ctx, "logger", "--priority", "user.notice", "--tag", identifier, marker)
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return coded("JOURNAL_POC_MARKER_FAILED", err)
	}
	return nil
}

func isPOCMarker(marker string) bool {
	return marker == pocQueryMarkerOne || marker == pocQueryMarkerTwo || marker == pocFollowMarker
}
