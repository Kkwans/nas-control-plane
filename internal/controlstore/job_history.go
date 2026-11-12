package controlstore

import (
	"context"
	"database/sql"
	"errors"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrJobHistoryCorrupt      = errors.New("JOB_HISTORY_CORRUPT")
	ErrJobHistoryInvalidQuery = errors.New("JOB_HISTORY_INVALID_QUERY")
)

// JobHistoryKey 直接保留数据库排序键，不从格式化时间反推游标。
type JobHistoryKey struct {
	CreatedAtUnix int64
	ID            string
}

type JobHistoryQuery struct {
	Type   string
	Limit  int
	Before *JobHistoryKey
}

// JobHistoryEntry 只描述最后成功保存的摘要，不承诺当前执行或资源状态。
type JobHistoryEntry struct {
	ID                 string     `json:"id"`
	Type               string     `json:"type"`
	Status             string     `json:"status"`
	Reference          string     `json:"reference,omitempty"`
	Message            string     `json:"message"`
	Error              string     `json:"error,omitempty"`
	ErrorCode          string     `json:"errorCode,omitempty"`
	CancellationStatus string     `json:"cancellationStatus,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
	CompletedAt        *time.Time `json:"completedAt,omitempty"`
	CreatedAtUnix      int64      `json:"-"`
}

type JobHistoryPage struct {
	Items []JobHistoryEntry
	Next  *JobHistoryKey
}

// 文本先按 BLOB 字节长度投影；超限、NULL 及类型错误均不会把完整字段带入 Go。
// 独立超限标记便于证明超限不是正常 NULL；不读取 layers_json、任何结果 JSON 或其他执行字段。
// 这里只限制驱动返回的字段大小，不保证 SQLite 内部读取/计算的峰值内存。
const jobHistoryColumns = `
 CASE WHEN typeof(id) = 'text' AND length(CAST(id AS BLOB)) <= 256 THEN id END,
 CASE WHEN length(CAST(id AS BLOB)) > 256 THEN 1 ELSE 0 END,
 CASE WHEN typeof(type) = 'text' AND length(CAST(type AS BLOB)) <= 128 THEN type END,
 CASE WHEN length(CAST(type AS BLOB)) > 128 THEN 1 ELSE 0 END,
 CASE WHEN typeof(status) = 'text' AND length(CAST(status AS BLOB)) <= 64 THEN status END,
 CASE WHEN length(CAST(status AS BLOB)) > 64 THEN 1 ELSE 0 END,
 CASE WHEN typeof(reference) = 'text' AND length(CAST(reference AS BLOB)) <= 4096 THEN reference END,
 CASE WHEN length(CAST(reference AS BLOB)) > 4096 THEN 1 ELSE 0 END,
 CASE WHEN typeof(message) = 'text' AND length(CAST(message AS BLOB)) <= 8192 THEN message END,
 CASE WHEN length(CAST(message AS BLOB)) > 8192 THEN 1 ELSE 0 END,
 CASE WHEN typeof(error) = 'text' AND length(CAST(error AS BLOB)) <= 8192 THEN error END,
 CASE WHEN length(CAST(error AS BLOB)) > 8192 THEN 1 ELSE 0 END,
 CASE WHEN typeof(error_code) = 'text' AND length(CAST(error_code AS BLOB)) <= 128 THEN error_code END,
 CASE WHEN length(CAST(error_code AS BLOB)) > 128 THEN 1 ELSE 0 END,
 CASE WHEN typeof(cancellation_status) = 'text' AND length(CAST(cancellation_status AS BLOB)) <= 64 THEN cancellation_status END,
 CASE WHEN length(CAST(cancellation_status AS BLOB)) > 64 THEN 1 ELSE 0 END,
 CASE WHEN typeof(created_at_unix) = 'integer' THEN created_at_unix END,
 CASE WHEN typeof(updated_at_unix) = 'integer' THEN updated_at_unix END,
 CASE WHEN typeof(completed_at_unix) = 'integer' THEN completed_at_unix END`

// 固定四条查询使深页可通过既有复合索引做范围 SEARCH，而非 OFFSET 或可选条件 OR。
const jobHistoryQuery = `SELECT ` + jobHistoryColumns + ` FROM jobs ORDER BY created_at_unix DESC, id DESC LIMIT ?`
const jobHistoryByTypeQuery = `SELECT ` + jobHistoryColumns + ` FROM jobs WHERE type = ? ORDER BY created_at_unix DESC, id DESC LIMIT ?`
const jobHistoryBeforeQuery = `SELECT ` + jobHistoryColumns + ` FROM jobs WHERE (created_at_unix, id) < (?, ?) ORDER BY created_at_unix DESC, id DESC LIMIT ?`
const jobHistoryByTypeBeforeQuery = `SELECT ` + jobHistoryColumns + ` FROM jobs WHERE type = ? AND (created_at_unix, id) < (?, ?) ORDER BY created_at_unix DESC, id DESC LIMIT ?`

func validJobHistoryText(value string, maxBytes int, required, noControls bool) bool {
	if len(value) > maxBytes || !utf8.ValidString(value) || (required && value == "") {
		return false
	}
	if noControls {
		for _, character := range value {
			if unicode.IsControl(character) {
				return false
			}
		}
	}
	return true
}

func validJobHistoryUnix(seconds int64) bool {
	// 先比较整数范围，避免 time.Unix 对 int64 极值的内部溢出影响判断。
	return seconds >= -62167219200 && seconds <= 253402300799
}

// ValidateJobHistoryKey 同时供 HTTP 游标解码使用，返回参数错误而不泄露数据。
func ValidateJobHistoryKey(key JobHistoryKey) error {
	if !validJobHistoryText(key.ID, 256, true, true) || !validJobHistoryUnix(key.CreatedAtUnix) {
		return ErrJobHistoryInvalidQuery
	}
	return nil
}

// ValidateJobHistoryQuery 不补默认值、不 trim 类型；HTTP 层负责省略参数的默认值。
func ValidateJobHistoryQuery(query JobHistoryQuery) error {
	if query.Limit < 1 || query.Limit > 100 || !validJobHistoryText(query.Type, 128, false, true) {
		return ErrJobHistoryInvalidQuery
	}
	if query.Before != nil {
		return ValidateJobHistoryKey(*query.Before)
	}
	return nil
}

// ValidateJobHistoryEntry 允许未知类型和状态，并可由 HTTP 防御性验证可选 reader。
func ValidateJobHistoryEntry(entry JobHistoryEntry) error {
	if !validJobHistoryText(entry.ID, 256, true, true) ||
		!validJobHistoryText(entry.Type, 128, true, true) ||
		!validJobHistoryText(entry.Status, 64, true, false) ||
		!validJobHistoryText(entry.Reference, 4096, false, false) ||
		!validJobHistoryText(entry.Message, 8192, false, false) ||
		!validJobHistoryText(entry.Error, 8192, false, false) ||
		!validJobHistoryText(entry.ErrorCode, 128, false, false) ||
		!validJobHistoryText(entry.CancellationStatus, 64, false, false) ||
		!validJobHistoryTime(entry.CreatedAt) || !validJobHistoryTime(entry.UpdatedAt) ||
		entry.CreatedAtUnix != entry.CreatedAt.Unix() || !validJobHistoryUnix(entry.CreatedAtUnix) {
		return ErrJobHistoryCorrupt
	}
	if entry.CompletedAt != nil && (!validJobHistoryTime(*entry.CompletedAt) || entry.CompletedAt.Unix() == 0) {
		return ErrJobHistoryCorrupt
	}
	return nil
}

func validJobHistoryTime(value time.Time) bool {
	return value.Location() == time.UTC && value.Nanosecond() == 0 && value.Year() >= 0 && value.Year() <= 9999 && validJobHistoryUnix(value.Unix())
}

type jobHistoryRow struct {
	text      [8]sql.NullString
	oversized [8]bool
	created   sql.NullInt64
	updated   sql.NullInt64
	completed sql.NullInt64
}

func (row *jobHistoryRow) scan(rows *sql.Rows) error {
	return rows.Scan(&row.text[0], &row.oversized[0], &row.text[1], &row.oversized[1],
		&row.text[2], &row.oversized[2], &row.text[3], &row.oversized[3],
		&row.text[4], &row.oversized[4], &row.text[5], &row.oversized[5],
		&row.text[6], &row.oversized[6], &row.text[7], &row.oversized[7],
		&row.created, &row.updated, &row.completed)
}

func (row jobHistoryRow) key() (JobHistoryKey, error) {
	key := JobHistoryKey{ID: row.text[0].String, CreatedAtUnix: row.created.Int64}
	if !row.text[0].Valid || row.oversized[0] || !row.created.Valid || ValidateJobHistoryKey(key) != nil {
		return JobHistoryKey{}, ErrJobHistoryCorrupt
	}
	return key, nil
}

func (row jobHistoryRow) entry() (JobHistoryEntry, error) {
	for index, field := range row.text {
		if !field.Valid || row.oversized[index] {
			return JobHistoryEntry{}, ErrJobHistoryCorrupt
		}
	}
	if !row.created.Valid || !row.updated.Valid || !row.completed.Valid ||
		!validJobHistoryUnix(row.created.Int64) || !validJobHistoryUnix(row.updated.Int64) ||
		!validJobHistoryUnix(row.completed.Int64) {
		return JobHistoryEntry{}, ErrJobHistoryCorrupt
	}
	entry := JobHistoryEntry{
		ID: row.text[0].String, Type: row.text[1].String, Status: row.text[2].String,
		Reference: row.text[3].String, Message: row.text[4].String, Error: row.text[5].String,
		ErrorCode: row.text[6].String, CancellationStatus: row.text[7].String,
		CreatedAt: time.Unix(row.created.Int64, 0).UTC(), UpdatedAt: time.Unix(row.updated.Int64, 0).UTC(),
		CreatedAtUnix: row.created.Int64,
	}
	if row.completed.Int64 != 0 {
		completed := time.Unix(row.completed.Int64, 0).UTC()
		entry.CompletedAt = &completed
	}
	if err := ValidateJobHistoryEntry(entry); err != nil {
		return JobHistoryEntry{}, err
	}
	return entry, nil
}

// JobHistory 单次有界 SELECT；不跨页保留快照、事务或共享可变结果。
func (s *Store) JobHistory(ctx context.Context, query JobHistoryQuery) (JobHistoryPage, error) {
	if err := ctx.Err(); err != nil {
		return JobHistoryPage{}, err
	}
	if err := ValidateJobHistoryQuery(query); err != nil {
		return JobHistoryPage{}, err
	}
	statement, args := jobHistoryQuery, []any{query.Limit + 1}
	switch {
	case query.Type != "" && query.Before != nil:
		statement, args = jobHistoryByTypeBeforeQuery, []any{query.Type, query.Before.CreatedAtUnix, query.Before.ID, query.Limit + 1}
	case query.Type != "":
		statement, args = jobHistoryByTypeQuery, []any{query.Type, query.Limit + 1}
	case query.Before != nil:
		statement, args = jobHistoryBeforeQuery, []any{query.Before.CreatedAtUnix, query.Before.ID, query.Limit + 1}
	}
	rows, err := s.database.QueryContext(ctx, statement, args...)
	if err != nil {
		return JobHistoryPage{}, err
	}
	defer rows.Close()
	page := JobHistoryPage{Items: make([]JobHistoryEntry, 0, query.Limit)}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return JobHistoryPage{}, err
		}
		var row jobHistoryRow
		if err := row.scan(rows); err != nil {
			if err := ctx.Err(); err != nil {
				return JobHistoryPage{}, err
			}
			return JobHistoryPage{}, ErrJobHistoryCorrupt
		}
		// 探测行也验证排序键；坏键不能被伪装成已到历史末尾。
		if _, err := row.key(); err != nil {
			return JobHistoryPage{}, err
		}
		if len(page.Items) == query.Limit {
			last := page.Items[len(page.Items)-1]
			page.Next = &JobHistoryKey{ID: last.ID, CreatedAtUnix: last.CreatedAtUnix}
			continue
		}
		entry, err := row.entry()
		if err != nil {
			return JobHistoryPage{}, err
		}
		page.Items = append(page.Items, entry)
	}
	if err := rows.Err(); err != nil {
		return JobHistoryPage{}, err
	}
	if err := rows.Close(); err != nil {
		return JobHistoryPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return JobHistoryPage{}, err
	}
	return page, nil
}
