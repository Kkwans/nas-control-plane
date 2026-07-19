// Package journal provides the P0 read-only journald access boundary.
package journal

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultLimit        = 100
	maximumLimit        = 200
	maximumFilterLength = 255
	maximumCursorLength = 4096
	maximumEntrySize    = 1024 * 1024
)

var (
	filterValuePattern = regexp.MustCompile(`^[A-Za-z0-9@._:-]+$`)
	cursorPattern      = regexp.MustCompile(`^[A-Za-z0-9=;:._-]+$`)
	sensitivePair      = regexp.MustCompile(`(?i)\b(?:password|passwd|token|cookie)\s*[:=]\s*\S+`)
	authorizationValue = regexp.MustCompile(`(?i)\bauthorization\s*:\s*(?:bearer\s+)?\S+`)
	bearerValue        = regexp.MustCompile(`(?i)\bbearer\s+\S+`)
)

// Runner is the deliberately narrow journalctl execution boundary. Query values
// are validated before they reach the runner and are always passed as arguments.
type Runner interface {
	Output(context.Context, ...string) ([]byte, error)
}

// Query describes one bounded historical journald read.
type Query struct {
	Unit       string
	Identifier string
	Since      *time.Time
	Until      *time.Time
	Cursor     string
	Limit      int
}

// Entry is the normalized, redacted subset of a journald record exposed by P0.
type Entry struct {
	Cursor     string
	Timestamp  time.Time
	Unit       string
	Identifier string
	Priority   int
	Message    string
}

// Page contains a historical result set. NextCursor is set only when another
// entry was observed beyond the requested limit.
type Page struct {
	Entries    []Entry
	NextCursor string
}

// Reader reads structured records through a fixed journalctl invocation.
type Reader struct {
	runner Runner
}

func NewReader(runner Runner) *Reader {
	return &Reader{runner: runner}
}

// Query returns one Cursor-paginated, read-only journal page.
func (r *Reader) Query(ctx context.Context, query Query) (Page, error) {
	if r == nil || r.runner == nil {
		return Page{}, coded("JOURNAL_QUERY_FAILED", errors.New("journal runner is unavailable"))
	}
	if err := ctx.Err(); err != nil {
		return Page{}, coded("JOURNAL_QUERY_CANCELED", err)
	}
	normalized, err := normalizeQuery(query)
	if err != nil {
		return Page{}, err
	}

	output, err := r.runner.Output(ctx, journalctlArguments(normalized)...)
	if err != nil {
		if ctx.Err() != nil {
			return Page{}, coded("JOURNAL_QUERY_CANCELED", ctx.Err())
		}
		return Page{}, coded("JOURNAL_QUERY_FAILED", err)
	}
	entries, err := parseEntries(output)
	if err != nil {
		return Page{}, err
	}

	page := Page{Entries: entries}
	if len(entries) > normalized.Limit {
		page.Entries = entries[:normalized.Limit]
		page.NextCursor = page.Entries[len(page.Entries)-1].Cursor
	}
	return page, nil
}

func normalizeQuery(query Query) (Query, error) {
	if query.Limit == 0 {
		query.Limit = defaultLimit
	}
	if query.Limit < 1 || query.Limit > maximumLimit {
		return Query{}, coded("JOURNAL_QUERY_INVALID", errors.New("journal page size is outside P0 bounds"))
	}
	if err := validateFilterValue(query.Unit); err != nil {
		return Query{}, err
	}
	if err := validateFilterValue(query.Identifier); err != nil {
		return Query{}, err
	}
	if query.Cursor != "" && (len(query.Cursor) > maximumCursorLength || !cursorPattern.MatchString(query.Cursor)) {
		return Query{}, coded("JOURNAL_QUERY_INVALID", errors.New("journal cursor is invalid"))
	}
	if query.Since != nil && query.Until != nil && query.Until.Before(*query.Since) {
		return Query{}, coded("JOURNAL_QUERY_INVALID", errors.New("journal time range is invalid"))
	}
	return query, nil
}

func validateFilterValue(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > maximumFilterLength || !filterValuePattern.MatchString(value) {
		return coded("JOURNAL_QUERY_INVALID", errors.New("journal filter is invalid"))
	}
	return nil
}

func journalctlArguments(query Query) []string {
	args := []string{
		"--no-pager",
		"--quiet",
		"--output=json",
		"--show-cursor",
		fmt.Sprintf("--lines=%d", query.Limit+1),
	}
	if query.Unit != "" {
		args = append(args, "--unit", query.Unit)
	}
	if query.Identifier != "" {
		args = append(args, "--identifier", query.Identifier)
	}
	if query.Since != nil {
		args = append(args, "--since", query.Since.UTC().Format(time.RFC3339Nano))
	}
	if query.Until != nil {
		args = append(args, "--until", query.Until.UTC().Format(time.RFC3339Nano))
	}
	if query.Cursor != "" {
		args = append(args, "--after-cursor", query.Cursor)
	}
	return args
}

func parseEntries(output []byte) ([]Entry, error) {
	entries := make([]Entry, 0)
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	scanner.Buffer(make([]byte, 0, 64*1024), maximumEntrySize)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "-- cursor:") {
			continue
		}
		entry, err := parseEntry([]byte(line))
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, coded("JOURNAL_RESPONSE_INVALID", err)
	}
	return entries, nil
}

func parseEntry(line []byte) (Entry, error) {
	var record map[string]json.RawMessage
	if err := json.Unmarshal(line, &record); err != nil {
		return Entry{}, coded("JOURNAL_RESPONSE_INVALID", err)
	}

	cursor := recordString(record, "__CURSOR")
	if cursor == "" {
		return Entry{}, coded("JOURNAL_RESPONSE_INVALID", errors.New("journal record has no cursor"))
	}
	timestamp, err := parseTimestamp(recordString(record, "__REALTIME_TIMESTAMP"))
	if err != nil {
		return Entry{}, err
	}
	priority, err := parsePriority(recordString(record, "PRIORITY"))
	if err != nil {
		return Entry{}, err
	}

	return Entry{
		Cursor:     cursor,
		Timestamp:  timestamp,
		Unit:       recordString(record, "_SYSTEMD_UNIT"),
		Identifier: recordString(record, "SYSLOG_IDENTIFIER"),
		Priority:   priority,
		Message:    redact(recordString(record, "MESSAGE")),
	}, nil
}

func recordString(record map[string]json.RawMessage, key string) string {
	raw, exists := record[key]
	if !exists {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

func parseTimestamp(value string) (time.Time, error) {
	microseconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}, coded("JOURNAL_RESPONSE_INVALID", err)
	}
	return time.UnixMicro(microseconds).UTC(), nil
}

func parsePriority(value string) (int, error) {
	if value == "" {
		return 6, nil
	}
	priority, err := strconv.Atoi(value)
	if err != nil || priority < 0 || priority > 7 {
		return 0, coded("JOURNAL_RESPONSE_INVALID", errors.New("journal priority is invalid"))
	}
	return priority, nil
}

func redact(message string) string {
	message = sensitivePair.ReplaceAllStringFunc(message, func(fragment string) string {
		separator := strings.IndexAny(fragment, ":=")
		if separator < 0 {
			return "[REDACTED]"
		}
		return fragment[:separator+1] + " [REDACTED]"
	})
	message = authorizationValue.ReplaceAllStringFunc(message, func(fragment string) string {
		separator := strings.Index(fragment, ":")
		return fragment[:separator+1] + " [REDACTED]"
	})
	return bearerValue.ReplaceAllString(message, "Bearer [REDACTED]")
}

type codedError struct {
	code string
	err  error
}

func (e *codedError) Error() string {
	return e.code
}

func (e *codedError) Unwrap() error {
	return e.err
}

func coded(code string, err error) error {
	return &codedError{code: code, err: err}
}

func ErrorCode(err error) string {
	var codedErr *codedError
	if errors.As(err, &codedErr) {
		return codedErr.code
	}
	return ""
}
