package controlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func jobHistoryFixture(id string, seconds int64) JobRecord {
	return JobRecord{
		ID: id, Type: "docker-image-pull", Status: "future-status", Reference: "repo/image:tag",
		Message: "历史消息", Error: "已保存错误", ErrorCode: "FUTURE_CODE", CancellationStatus: "future-cancellation",
		LayersJSON: `{"not-a-summary":"ignored"}`, CreatedAt: time.Unix(seconds, 0).UTC(),
		UpdatedAt: time.Unix(seconds+1, 0).UTC(), CompletedAt: time.Unix(seconds+2, 0).UTC(),
	}
}

func historyEntryOracle(job JobRecord) JobHistoryEntry {
	entry := JobHistoryEntry{
		ID: job.ID, Type: job.Type, Status: job.Status, Reference: job.Reference, Message: job.Message,
		Error: job.Error, ErrorCode: job.ErrorCode, CancellationStatus: job.CancellationStatus,
		CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt, CreatedAtUnix: job.CreatedAt.Unix(),
	}
	if !job.CompletedAt.IsZero() {
		completed := job.CompletedAt
		entry.CompletedAt = &completed
	}
	return entry
}

func seedJobHistory(t *testing.T, store *Store, records []JobRecord) {
	t.Helper()
	tx, err := store.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO jobs
 (id,type,status,reference,message,error,error_code,cancellation_status,layers_json,created_at_unix,updated_at_unix,completed_at_unix)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, job := range records {
		completed := int64(0)
		if !job.CompletedAt.IsZero() {
			completed = job.CompletedAt.Unix()
		}
		if _, err := stmt.Exec(job.ID, job.Type, job.Status, job.Reference, job.Message, job.Error,
			job.ErrorCode, job.CancellationStatus, job.LayersJSON, job.CreatedAt.Unix(), job.UpdatedAt.Unix(), completed); err != nil {
			t.Fatal(err)
		}
	}
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func requireJobHistory(t *testing.T, store *Store, query JobHistoryQuery) JobHistoryPage {
	t.Helper()
	page, err := store.JobHistory(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func TestJobHistoryTraversalMatchesIndependentOracle(t *testing.T) {
	store, _ := openJobTestStore(t)
	kinds := []string{"docker-image-pull", "compose-deploy", "docker-image-remove", " custom-kind ", "<未知>\"\\\u2028"}
	all := make([]JobHistoryEntry, 0, 2113)
	records := make([]JobRecord, 0, cap(all))
	for i := 0; i < cap(all); i++ {
		// 插入顺序与排序无关，许多同秒记录跨越 limit=1 和 limit=100 的页边界。
		job := jobHistoryFixture(fmt.Sprintf("legacy/%04d-任务\"\\<>&\u2028", i), 1800000000+int64((i*7)%29))
		job.Type = kinds[i%len(kinds)]
		if i%3 == 0 {
			job.CompletedAt = time.Time{}
		}
		records = append(records, job)
		all = append(all, historyEntryOracle(job))
	}
	seedJobHistory(t, store, records)
	sort.Slice(all, func(i, j int) bool {
		if all[i].CreatedAtUnix != all[j].CreatedAtUnix {
			return all[i].CreatedAtUnix > all[j].CreatedAtUnix
		}
		return all[i].ID > all[j].ID
	})
	for _, kind := range append([]string{"", "missing", "custom-kind"}, kinds...) {
		for _, limit := range []int{1, 37, 100} {
			t.Run(fmt.Sprintf("%q/limit=%d", kind, limit), func(t *testing.T) {
				want := make([]JobHistoryEntry, 0)
				for _, item := range all {
					if kind == "" || item.Type == kind {
						want = append(want, item)
					}
				}
				query := JobHistoryQuery{Type: kind, Limit: limit}
				got := make([]JobHistoryEntry, 0, len(want))
				for pageNumber := 0; ; pageNumber++ {
					if pageNumber > len(want) {
						t.Fatal("pagination did not terminate")
					}
					page := requireJobHistory(t, store, query)
					if page.Items == nil || len(page.Items) > limit {
						t.Fatalf("invalid page slice: %#v", page)
					}
					end := min(len(want), len(got)+limit)
					if !reflect.DeepEqual(page.Items, want[len(got):end]) {
						t.Fatalf("page %d differs from independent sorted fixture", pageNumber)
					}
					got = append(got, page.Items...)
					if end == len(want) {
						if page.Next != nil {
							t.Fatalf("last page must have no next key: %#v", page.Next)
						}
						break
					}
					last := page.Items[len(page.Items)-1]
					if page.Next == nil || *page.Next != (JobHistoryKey{ID: last.ID, CreatedAtUnix: last.CreatedAtUnix}) {
						t.Fatalf("next key must be last returned item, not probe: %#v", page.Next)
					}
					query.Before = page.Next
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("complete traversal differs: got %d want %d", len(got), len(want))
				}
			})
		}
	}
	// 生产 SQL 本身的计划；不能用测试内的近似语句证明索引范围查找。
	requireJobHistoryQueryPlans(t, store.database)
}

func requireJobHistoryQueryPlans(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, tc := range []struct {
		name, query, index string
		args               []any
		rangeSeek          bool
	}{
		{"mixed/latest", jobHistoryQuery, "jobs_created_id", []any{101}, false},
		{"typed/latest", jobHistoryByTypeQuery, "jobs_type_created_id", []any{"docker-image-pull", 101}, false},
		{"mixed/before", jobHistoryBeforeQuery, "jobs_created_id", []any{1800000005, "legacy/0500", 101}, true},
		{"typed/before", jobHistoryByTypeBeforeQuery, "jobs_type_created_id", []any{"docker-image-pull", 1800000005, "legacy/0500", 101}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := db.Query("EXPLAIN QUERY PLAN "+tc.query, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var details []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				details = append(details, detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			plan := strings.Join(details, "; ")
			if !strings.Contains(plan, "USING INDEX "+tc.index) || strings.Contains(plan, "TEMP B-TREE") {
				t.Fatalf("must use existing ordered index without sort: %s", plan)
			}
			if tc.index == "jobs_type_created_id" && !strings.Contains(plan, "SEARCH jobs") {
				t.Fatalf("typed page must seek exact type: %s", plan)
			}
			if tc.rangeSeek && (!strings.Contains(plan, "SEARCH jobs") || !strings.Contains(plan, "(created_at_unix,id)<(?,?)")) {
				t.Fatalf("deep page must seek tuple range, not merely ordered scan: %s", plan)
			}
			t.Log(plan)
		})
	}
}

func TestJobHistoryPageBoundariesAndChangingLimit(t *testing.T) {
	store, _ := openJobTestStore(t)
	page := requireJobHistory(t, store, JobHistoryQuery{Limit: 1})
	if len(page.Items) != 0 || page.Items == nil || page.Next != nil {
		t.Fatalf("empty result: %#v", page)
	}
	seedJobHistory(t, store, []JobRecord{jobHistoryFixture("old-style", 5), jobHistoryFixture("history", 5), jobHistoryFixture("tail", 4)})
	for _, limit := range []int{3, 4, 100} {
		page = requireJobHistory(t, store, JobHistoryQuery{Limit: limit})
		if len(page.Items) != 3 || page.Next != nil {
			t.Fatalf("exact/fewer records require no next key: %#v", page)
		}
	}
	page = requireJobHistory(t, store, JobHistoryQuery{Limit: 1})
	if len(page.Items) != 1 || page.Next == nil || page.Next.ID != "old-style" {
		t.Fatalf("limit+1 boundary: %#v", page)
	}
	next := requireJobHistory(t, store, JobHistoryQuery{Limit: 100, Before: page.Next})
	if len(next.Items) != 2 || next.Items[0].ID != "history" || next.Items[1].ID != "tail" || next.Next != nil {
		t.Fatalf("changed page size: %#v", next)
	}
}

func TestJobHistoryConcurrentInsertDeleteAndUpdateSemantics(t *testing.T) {
	store, _ := openJobTestStore(t)
	var records []JobRecord
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		records = append(records, jobHistoryFixture(id, 100))
	}
	seedJobHistory(t, store, records)
	first := requireJobHistory(t, store, JobHistoryQuery{Limit: 2})
	if first.Next == nil || first.Next.ID != "e" {
		t.Fatalf("first page: %#v", first)
	}
	// 分页请求之间的独立写入者；删除锚点仍可继续，同时排除较新的新增记录。
	done := make(chan error, 1)
	go func() {
		ctx := context.Background()
		for _, job := range []JobRecord{jobHistoryFixture("newer", 101), jobHistoryFixture("g", 100), jobHistoryFixture("dd", 100)} {
			if err := store.CreateJob(ctx, job); err != nil {
				done <- err
				return
			}
		}
		for _, id := range []string{"e", "c"} {
			if err := store.DeleteJob(ctx, id); err != nil {
				done <- err
				return
			}
		}
		changed := jobHistoryFixture("d", 999)
		changed.Status, changed.Message = "saved-new-state", "最新已保存消息"
		_, err := store.UpdateJob(ctx, changed, 1)
		done <- err
	}()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	second := requireJobHistory(t, store, JobHistoryQuery{Limit: 2, Before: first.Next})
	if len(second.Items) != 2 || second.Items[0].ID != "dd" || second.Items[1].ID != "d" ||
		second.Items[1].CreatedAtUnix != 100 || second.Items[1].Status != "saved-new-state" || second.Next == nil {
		t.Fatalf("changed rows must retain keyset semantics: %#v", second)
	}
	third := requireJobHistory(t, store, JobHistoryQuery{Limit: 2, Before: second.Next})
	if len(third.Items) != 2 || third.Items[0].ID != "b" || third.Items[1].ID != "a" || third.Next != nil {
		t.Fatalf("deleted row must not be backfilled from newer range: %#v", third)
	}
	latest := requireJobHistory(t, store, JobHistoryQuery{Limit: 2})
	if latest.Items[0].ID != "newer" || latest.Items[1].ID != "g" {
		t.Fatalf("back to latest must observe newer insertions: %#v", latest)
	}
	// 上次没有下一页，不代表较旧范围永远不会出现记录。
	if err := store.CreateJob(context.Background(), jobHistoryFixture("late-old", 99)); err != nil {
		t.Fatal(err)
	}
	late := requireJobHistory(t, store, JobHistoryQuery{Limit: 100, Before: &JobHistoryKey{ID: "a", CreatedAtUnix: 100}})
	if len(late.Items) != 1 || late.Items[0].ID != "late-old" {
		t.Fatalf("late older insertion: %#v", late)
	}
	if err := store.DeleteJob(context.Background(), "late-old"); err != nil {
		t.Fatal(err)
	}
	empty := requireJobHistory(t, store, JobHistoryQuery{Limit: 100, Before: &JobHistoryKey{ID: "a", CreatedAtUnix: 100}})
	if len(empty.Items) != 0 || empty.Next != nil {
		t.Fatalf("concurrent deletion may empty later page: %#v", empty)
	}
}

var historyTextFields = []struct {
	column string
	limit  int
}{
	{"id", 256}, {"type", 128}, {"status", 64}, {"reference", 4096},
	{"message", 8192}, {"error", 8192}, {"error_code", 128}, {"cancellation_status", 64},
}

func historyTextBytes(size int) string {
	return strings.Repeat("界", size/3) + strings.Repeat("x", size%3)
}

func resetHistoryFixture(t *testing.T, store *Store) {
	t.Helper()
	if _, err := store.database.Exec(`DELETE FROM jobs`); err != nil {
		t.Fatal(err)
	}
	seedJobHistory(t, store, []JobRecord{jobHistoryFixture("record", 100)})
}

func readRawHistoryRow(t *testing.T, store *Store) jobHistoryRow {
	t.Helper()
	rows, err := store.database.Query(jobHistoryQuery, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("missing raw row: %v", rows.Err())
	}
	var row jobHistoryRow
	if err := row.scan(rows); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("unexpected extra row or scan error: %v", rows.Err())
	}
	return row
}

func TestJobHistoryTextByteBoundsAndBoundedSQLProjection(t *testing.T) {
	store, _ := openJobTestStore(t)
	for index, field := range historyTextFields {
		t.Run(field.column, func(t *testing.T) {
			for _, size := range []int{field.limit - 1, field.limit, field.limit + 1} {
				resetHistoryFixture(t, store)
				// 列名来自固定测试表，不包含外部输入；字节数与 Unicode 字符数不同。
				if _, err := store.database.Exec("UPDATE jobs SET "+field.column+" = ?", historyTextBytes(size)); err != nil {
					t.Fatal(err)
				}
				page, err := store.JobHistory(context.Background(), JobHistoryQuery{Limit: 1})
				if size <= field.limit {
					if err != nil || len(page.Items) != 1 {
						t.Fatalf("valid %d-byte boundary: %v", size, err)
					}
				} else if !errors.Is(err, ErrJobHistoryCorrupt) || len(page.Items) != 0 || page.Next != nil {
					t.Fatalf("oversized field must fail whole page: %#v %v", page, err)
				}
				row := readRawHistoryRow(t, store)
				if row.oversized[index] != (size > field.limit) || row.text[index].Valid != (size <= field.limit) {
					t.Fatalf("SQL cap/flag mismatch at %d bytes: %#v", size, row)
				}
				if size <= field.limit && row.text[index].String != historyTextBytes(size) {
					t.Fatal("valid field was truncated or changed")
				}
			}
			resetHistoryFixture(t, store)
			// 巨大值完全在 SQLite 中构造。NUL 也必须计入字节长度；Go 只能收到 NULL 和标记。
			if _, err := store.database.Exec("UPDATE jobs SET " + field.column + " = CAST(zeroblob(8388608) AS TEXT)"); err != nil {
				t.Fatal(err)
			}
			row := readRawHistoryRow(t, store)
			if row.text[index].Valid || row.text[index].String != "" || !row.oversized[index] {
				t.Fatal("huge database field reached the Go projection")
			}
			if _, err := store.JobHistory(context.Background(), JobHistoryQuery{Limit: 100}); !errors.Is(err, ErrJobHistoryCorrupt) {
				t.Fatalf("huge stored field: %v", err)
			}
		})
	}
	resetHistoryFixture(t, store)
	if _, err := store.database.Exec(`UPDATE jobs SET layers_json = CAST(zeroblob(16777216) AS TEXT)`); err != nil {
		t.Fatal(err)
	}
	page := requireJobHistory(t, store, JobHistoryQuery{Limit: 100})
	if len(page.Items) != 1 || strings.Contains(strings.ToLower(jobHistoryColumns), "layers") {
		t.Fatal("summary must not select layers_json")
	}
}

func TestJobHistoryMalformedStoredTextAndNulls(t *testing.T) {
	store, _ := openJobTestStore(t)
	for _, field := range historyTextFields {
		t.Run(field.column+"/utf8", func(t *testing.T) {
			resetHistoryFixture(t, store)
			if _, err := store.database.Exec("UPDATE jobs SET "+field.column+" = ?", string([]byte{'a', 0xff})); err != nil {
				t.Fatal(err)
			}
			if _, err := store.JobHistory(context.Background(), JobHistoryQuery{Limit: 1}); !errors.Is(err, ErrJobHistoryCorrupt) {
				t.Fatalf("invalid UTF-8: %v", err)
			}
		})
		t.Run(field.column+"/blob", func(t *testing.T) {
			resetHistoryFixture(t, store)
			if _, err := store.database.Exec("UPDATE jobs SET "+field.column+" = ?", []byte("blob")); err != nil {
				t.Fatal(err)
			}
			if _, err := store.JobHistory(context.Background(), JobHistoryQuery{Limit: 1}); !errors.Is(err, ErrJobHistoryCorrupt) {
				t.Fatalf("non-text value: %v", err)
			}
		})
	}
	for _, column := range []string{"id", "type", "status"} {
		resetHistoryFixture(t, store)
		if _, err := store.database.Exec("UPDATE jobs SET " + column + " = ''"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.JobHistory(context.Background(), JobHistoryQuery{Limit: 1}); !errors.Is(err, ErrJobHistoryCorrupt) {
			t.Fatalf("missing required %s: %v", column, err)
		}
	}
	for _, column := range []string{"id", "type"} {
		for _, value := range []string{"a\x00b", "a\nb", "a\u0085b", "a\u007fb"} {
			resetHistoryFixture(t, store)
			if _, err := store.database.Exec("UPDATE jobs SET "+column+" = ?", value); err != nil {
				t.Fatal(err)
			}
			if _, err := store.JobHistory(context.Background(), JobHistoryQuery{Limit: 1}); !errors.Is(err, ErrJobHistoryCorrupt) {
				t.Fatalf("control character in %s: %v", column, err)
			}
		}
	}
	// 损坏或旧版弱约束表的 NULL 也不能在摘要中悄悄变成空串。
	if _, err := store.database.Exec(`DROP TABLE jobs;
 CREATE TABLE jobs (id TEXT, type TEXT, status TEXT, reference TEXT, message TEXT, error TEXT,
 error_code TEXT, cancellation_status TEXT, layers_json TEXT, created_at_unix INTEGER, updated_at_unix INTEGER, completed_at_unix INTEGER)`); err != nil {
		t.Fatal(err)
	}
	columns := []string{"created_at_unix", "updated_at_unix", "completed_at_unix"}
	for _, field := range historyTextFields {
		columns = append(columns, field.column)
	}
	for _, column := range columns {
		resetHistoryFixture(t, store)
		if _, err := store.database.Exec("UPDATE jobs SET " + column + " = NULL"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.JobHistory(context.Background(), JobHistoryQuery{Limit: 1}); !errors.Is(err, ErrJobHistoryCorrupt) {
			t.Fatalf("NULL %s: %v", column, err)
		}
	}
}

func TestJobHistoryStoredTimeBoundsAndTypes(t *testing.T) {
	store, _ := openJobTestStore(t)
	for _, column := range []string{"created_at_unix", "updated_at_unix", "completed_at_unix"} {
		t.Run(column, func(t *testing.T) {
			for _, seconds := range []int64{-62167219200, -62135596800, -1, 0, 1, 253402300799} {
				resetHistoryFixture(t, store)
				if _, err := store.database.Exec("UPDATE jobs SET "+column+" = ?", seconds); err != nil {
					t.Fatal(err)
				}
				page := requireJobHistory(t, store, JobHistoryQuery{Limit: 1})
				if _, err := json.Marshal(page.Items); err != nil {
					t.Fatalf("representable time %d: %v", seconds, err)
				}
				if column == "created_at_unix" && page.Items[0].CreatedAtUnix != seconds {
					t.Fatal("cursor key changed")
				}
				if column == "completed_at_unix" {
					completed := page.Items[0].CompletedAt
					if (completed == nil) != (seconds == 0) || (completed != nil && completed.Unix() != seconds) {
						t.Fatalf("zero sentinel or negative completion changed: %d %#v", seconds, completed)
					}
				}
			}
			for _, value := range []any{int64(-62167219201), int64(253402300800), int64(math.MinInt64), int64(math.MaxInt64), "not-an-integer", 1.5, []byte("100")} {
				resetHistoryFixture(t, store)
				if _, err := store.database.Exec("UPDATE jobs SET "+column+" = ?", value); err != nil {
					t.Fatal(err)
				}
				if _, err := store.JobHistory(context.Background(), JobHistoryQuery{Limit: 1}); !errors.Is(err, ErrJobHistoryCorrupt) {
					t.Fatalf("invalid timestamp %v: %v", value, err)
				}
			}
			resetHistoryFixture(t, store)
			if _, err := store.database.Exec("UPDATE jobs SET " + column + " = CAST(zeroblob(8388608) AS TEXT)"); err != nil {
				t.Fatal(err)
			}
			row := readRawHistoryRow(t, store)
			if column == "created_at_unix" && row.created.Valid || column == "updated_at_unix" && row.updated.Valid || column == "completed_at_unix" && row.completed.Valid {
				t.Fatal("huge non-integer timestamp reached Go")
			}
			if _, err := store.JobHistory(context.Background(), JobHistoryQuery{Limit: 1}); !errors.Is(err, ErrJobHistoryCorrupt) {
				t.Fatalf("huge timestamp must fail safely: %v", err)
			}
		})
	}
}

func TestJobHistoryProbeValidatesOnlyBoundedSortingKey(t *testing.T) {
	store, _ := openJobTestStore(t)
	for _, tc := range []struct {
		name, statement string
		args            []any
		corrupt         bool
	}{
		{"invalid-id", `UPDATE jobs SET id = ? WHERE id = 'probe'`, []any{string([]byte{'a', 0xff})}, true},
		{"empty-id", `UPDATE jobs SET id = '' WHERE id = 'probe'`, nil, true},
		{"huge-id", `UPDATE jobs SET id = CAST(zeroblob(8388608) AS TEXT) WHERE id = 'probe'`, nil, true},
		{"unrepresentable-key", `UPDATE jobs SET created_at_unix = -62167219201 WHERE id = 'probe'`, nil, true},
		{"huge-message", `UPDATE jobs SET message = CAST(zeroblob(8388608) AS TEXT) WHERE id = 'probe'`, nil, false},
		{"bad-updated", `UPDATE jobs SET updated_at_unix = 9223372036854775807 WHERE id = 'probe'`, nil, false},
		{"bad-type", `UPDATE jobs SET type = ? WHERE id = 'probe'`, []any{string([]byte{0xff})}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.database.Exec(`DELETE FROM jobs`); err != nil {
				t.Fatal(err)
			}
			seedJobHistory(t, store, []JobRecord{jobHistoryFixture("returned", 100), jobHistoryFixture("probe", 99)})
			if _, err := store.database.Exec(tc.statement, tc.args...); err != nil {
				t.Fatal(err)
			}
			page, err := store.JobHistory(context.Background(), JobHistoryQuery{Limit: 1})
			if tc.corrupt {
				if !errors.Is(err, ErrJobHistoryCorrupt) || len(page.Items) != 0 || page.Next != nil {
					t.Fatalf("invalid probe key must not pretend end of history: %#v %v", page, err)
				}
			} else {
				if err != nil || len(page.Items) != 1 || page.Next == nil || page.Next.ID != "returned" {
					t.Fatalf("probe only needs valid key: %#v %v", page, err)
				}
				if _, err := store.JobHistory(context.Background(), JobHistoryQuery{Limit: 1, Before: page.Next}); !errors.Is(err, ErrJobHistoryCorrupt) {
					t.Fatalf("bad row must fail once returned: %v", err)
				}
			}
		})
	}
}

func TestJobHistoryQueryValidation(t *testing.T) {
	store, _ := openJobTestStore(t)
	for _, query := range []JobHistoryQuery{
		{Limit: 0}, {Limit: -1}, {Limit: 101}, {Limit: math.MaxInt},
		{Limit: 1, Type: strings.Repeat("x", 129)}, {Limit: 1, Type: strings.Repeat("界", 43)},
		{Limit: 1, Type: "a\x00b"}, {Limit: 1, Type: "a\u0085b"}, {Limit: 1, Type: string([]byte{0xff})},
		{Limit: 1, Before: &JobHistoryKey{}}, {Limit: 1, Before: &JobHistoryKey{ID: "a\nb"}},
		{Limit: 1, Before: &JobHistoryKey{ID: strings.Repeat("x", 257)}},
		{Limit: 1, Before: &JobHistoryKey{ID: string([]byte{0xff})}},
		{Limit: 1, Before: &JobHistoryKey{ID: "id", CreatedAtUnix: math.MaxInt64}},
		{Limit: 1, Before: &JobHistoryKey{ID: "id", CreatedAtUnix: math.MinInt64}},
		{Limit: 1, Before: &JobHistoryKey{ID: "id", CreatedAtUnix: -62167219201}},
		{Limit: 1, Before: &JobHistoryKey{ID: "id", CreatedAtUnix: 253402300800}},
	} {
		if err := ValidateJobHistoryQuery(query); !errors.Is(err, ErrJobHistoryInvalidQuery) {
			t.Fatalf("invalid query helper: %#v %v", query, err)
		}
		if _, err := store.JobHistory(context.Background(), query); !errors.Is(err, ErrJobHistoryInvalidQuery) {
			t.Fatalf("invalid store query: %#v %v", query, err)
		}
	}
	for _, query := range []JobHistoryQuery{
		{Limit: 1}, {Limit: 100, Type: " future-kind "}, {Limit: 1, Type: historyTextBytes(128)},
		{Limit: 1, Before: &JobHistoryKey{ID: historyTextBytes(256), CreatedAtUnix: -62167219200}},
		{Limit: 1, Before: &JobHistoryKey{ID: "<>&\"\\\u2028\u2029", CreatedAtUnix: 253402300799}},
	} {
		if err := ValidateJobHistoryQuery(query); err != nil {
			t.Fatalf("valid query rejected: %#v %v", query, err)
		}
		requireJobHistory(t, store, query)
	}
}

func TestJobHistoryContextAndStoreErrors(t *testing.T) {
	store, _ := openJobTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.JobHistory(ctx, JobHistoryQuery{Limit: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query: %v", err)
	}
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, err := store.JobHistory(expired, JobHistoryQuery{Limit: 1}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired query: %v", err)
	}
	// Store 仅一个连接；占用连接证明等待读取也遵守请求取消，无监听器。
	tx, err := store.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	waiting, stop := context.WithCancel(context.Background())
	defer stop()
	waitsBefore := store.database.Stats().WaitCount
	done := make(chan error, 1)
	go func() {
		_, err := store.JobHistory(waiting, JobHistoryQuery{Limit: 1})
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for store.database.Stats().WaitCount == waitsBefore {
		if time.Now().After(deadline) {
			t.Fatal("history query did not wait for the occupied connection")
		}
		time.Sleep(time.Millisecond)
	}
	stop()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting canceled read: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.database.Exec(`DROP TABLE jobs`); err != nil {
		t.Fatal(err)
	}
	if page, err := store.JobHistory(context.Background(), JobHistoryQuery{Limit: 1}); err == nil || errors.Is(err, ErrJobHistoryCorrupt) || len(page.Items) != 0 {
		t.Fatalf("SQL failure must not be empty history or corrupt row: %#v %v", page, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if page, err := store.JobHistory(context.Background(), JobHistoryQuery{Limit: 1}); err == nil || len(page.Items) != 0 {
		t.Fatalf("closed store must fail: %#v %v", page, err)
	}
}

func TestJobHistoryCallerOwnershipAndDTO(t *testing.T) {
	store, _ := openJobTestStore(t)
	seedJobHistory(t, store, []JobRecord{jobHistoryFixture("new", 100), jobHistoryFixture("old", 99), jobHistoryFixture("oldest", 98)})
	query := JobHistoryQuery{Limit: 2}
	want := requireJobHistory(t, store, query)
	first := requireJobHistory(t, store, query)
	first.Items[0].Status = "caller-mutated"
	*first.Items[0].CompletedAt = time.Unix(1234, 0).UTC()
	first.Next.ID = "caller-mutated"
	if got := requireJobHistory(t, store, query); !reflect.DeepEqual(got, want) || first.Items[1].CompletedAt.Equal(*first.Items[0].CompletedAt) {
		t.Fatal("caller owns returned slice, pointers, and next key")
	}
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for j := 0; j < 10; j++ {
				page, err := store.JobHistory(context.Background(), query)
				if err != nil {
					t.Error(err)
					return
				}
				page.Items[0].Message = "independent owner"
				*page.Items[0].CompletedAt = time.Unix(int64(j), 0).UTC()
				page.Next.ID = "independent key"
			}
		}()
	}
	group.Wait()
	encoded, err := json.Marshal(want.Items[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	expectedFields := []string{"id", "type", "status", "reference", "message", "error", "errorCode", "cancellationStatus", "createdAt", "updatedAt", "completedAt"}
	if len(fields) != len(expectedFields) {
		t.Fatalf("summary exposed extra fields: %s", encoded)
	}
	for _, field := range expectedFields {
		if _, exists := fields[field]; !exists {
			t.Fatalf("missing summary field %s: %s", field, encoded)
		}
	}
	minimal := want.Items[0]
	minimal.Reference, minimal.Error, minimal.ErrorCode, minimal.CancellationStatus, minimal.Message = "", "", "", "", ""
	minimal.CompletedAt = nil
	encoded, err = json.Marshal(minimal)
	if err != nil {
		t.Fatal(err)
	}
	fields = nil
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 6 || string(fields["message"]) != `""` {
		t.Fatalf("optional fields and required message changed: %s", encoded)
	}
}

func TestValidateJobHistoryEntryRejectsProviderCorruption(t *testing.T) {
	valid := historyEntryOracle(jobHistoryFixture("old-id", 100))
	if err := ValidateJobHistoryEntry(valid); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*JobHistoryEntry){
		func(entry *JobHistoryEntry) { entry.ID = "" },
		func(entry *JobHistoryEntry) { entry.Type = "" },
		func(entry *JobHistoryEntry) { entry.Status = "" },
		func(entry *JobHistoryEntry) { entry.Message = strings.Repeat("x", 8193) },
		func(entry *JobHistoryEntry) { entry.Reference = string([]byte{0xff}) },
		func(entry *JobHistoryEntry) { entry.CreatedAtUnix++ },
		func(entry *JobHistoryEntry) { entry.CreatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) },
		func(entry *JobHistoryEntry) { entry.UpdatedAt = time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC) },
		func(entry *JobHistoryEntry) { entry.UpdatedAt = entry.UpdatedAt.Add(time.Nanosecond) },
		func(entry *JobHistoryEntry) { value := time.Unix(math.MaxInt64, 0).UTC(); entry.CompletedAt = &value },
		func(entry *JobHistoryEntry) { value := time.Unix(0, 0).UTC(); entry.CompletedAt = &value },
	} {
		entry := valid
		mutate(&entry)
		if err := ValidateJobHistoryEntry(entry); !errors.Is(err, ErrJobHistoryCorrupt) {
			t.Fatalf("invalid reader entry: %#v %v", entry, err)
		}
	}
}
