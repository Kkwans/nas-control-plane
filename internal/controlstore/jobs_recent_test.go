package controlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

const legacyRecentJobsQuery = `SELECT ` + jobReadColumns + ` FROM jobs WHERE (? = '' OR type = ?) ORDER BY created_at_unix DESC, id DESC LIMIT ?`

func readRecentTestQuery(t testing.TB, db *sql.DB, query string, args ...any) []JobRecord {
	t.Helper()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := make([]JobRecord, 0)
	for rows.Next() {
		record, err := scanJob(rows)
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func requireRecentQueryPlans(t testing.TB, db *sql.DB) {
	t.Helper()
	for _, tc := range []struct {
		query, index string
		args         []any
	}{
		{recentJobsQuery, "jobs_created_id", []any{500}},
		{recentJobsByTypeQuery, "jobs_type_created_id", []any{"docker-image-pull", 500}},
	} {
		rows, err := db.Query("EXPLAIN QUERY PLAN "+tc.query, tc.args...)
		if err != nil {
			t.Fatal(err)
		}
		var details []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		plan := strings.Join(details, "; ")
		if !strings.Contains(plan, "USING INDEX "+tc.index) || strings.Contains(plan, "TEMP B-TREE") {
			t.Fatalf("recent query must use ordered index without temporary sort: %s", plan)
		}
		if tc.index == "jobs_type_created_id" && !strings.Contains(plan, "SEARCH jobs") {
			t.Fatalf("filtered query must seek the selected type: %s", plan)
		}
		t.Log(plan)
	}
}

func TestRecentJobsIndexedResultsAndLimits(t *testing.T) {
	store, _ := openJobTestStore(t)
	ctx := context.Background()
	all := make([]JobRecord, 0, 2110)
	kinds := []string{"docker-image-pull", "compose-deploy", "docker-image-remove", " custom-kind "}
	for i := 0; i < 2110; i++ {
		job := jobFixture(fmt.Sprintf("record-%04d", i))
		job.Type = kinds[i%len(kinds)]
		// 多条同秒，插入顺序与预期次序不同；保留完整元数据比较。
		job.CreatedAt = job.CreatedAt.Add(time.Duration(i%17) * time.Second)
		job.Message, job.ErrorCode, job.CancellationStatus = "历史消息", "EXAMPLE", "unknown"
		job.Status = "interrupted"
		job.CompletedAt = job.UpdatedAt
		if err := store.CreateJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		all = append(all, requireJob(t, store, job.ID))
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].ID > all[j].ID
		}
		return all[i].CreatedAt.After(all[j].CreatedAt)
	})
	for _, kind := range []string{"", "docker-image-pull", "compose-deploy", "docker-image-remove", " custom-kind ", "custom-kind", "missing"} {
		for _, limit := range []int{-1, 0, 1, 100, 500, 501} {
			t.Run(fmt.Sprintf("%q/%d", kind, limit), func(t *testing.T) {
				effective := limit
				if effective < 1 || effective > 500 {
					effective = 100
				}
				expected := make([]JobRecord, 0)
				for _, job := range all {
					if kind == "" || job.Type == kind {
						expected = append(expected, job)
					}
					if len(expected) == effective {
						break
					}
				}
				got, err := store.Jobs(ctx, kind, limit)
				if err != nil {
					t.Fatal(err)
				}
				old := readRecentTestQuery(t, store.database, legacyRecentJobsQuery, kind, kind, effective)
				if !reflect.DeepEqual(got, expected) || !reflect.DeepEqual(got, old) {
					t.Fatalf("changed filtered order, limits or record data: got %d expected %d legacy %d", len(got), len(expected), len(old))
				}
			})
		}
	}
	requireRecentQueryPlans(t, store.database)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, kind := range []string{"", "docker-image-pull"} {
		if _, err := store.Jobs(canceled, kind, 500); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled %q: %v", kind, err)
		}
	}
}

func TestRecentJobsIndexesMigrateLegacyHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// 旧表和旧索引：显式证明新增索引不依赖新建空库，也不移除原索引。
	_, err = db.Exec(`CREATE TABLE jobs (
 id TEXT PRIMARY KEY, type TEXT NOT NULL, status TEXT NOT NULL,
 reference TEXT NOT NULL DEFAULT '', message TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
 progress INTEGER NOT NULL DEFAULT 0, downloaded_bytes INTEGER NOT NULL DEFAULT 0,
 total_bytes INTEGER NOT NULL DEFAULT 0, speed_bytes INTEGER NOT NULL DEFAULT 0,
 layers_json TEXT NOT NULL DEFAULT '[]', created_at_unix INTEGER NOT NULL,
 updated_at_unix INTEGER NOT NULL, completed_at_unix INTEGER NOT NULL DEFAULT 0);
 CREATE INDEX jobs_type_created ON jobs(type, created_at_unix DESC);
 INSERT INTO jobs VALUES
 ('legacy-a','compose-deploy','completed','project-a','已完成','',100,12,12,0,'[]',10,11,11),
 ('legacy-c','docker-image-pull','running','alpine:3','进行中','',40,4,10,2,'{}',10,12,0),
 ('legacy-b','docker-image-pull','failed','image:b','失败','example',20,2,10,0,'[]',10,13,13);`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	expected := []JobRecord{
		{ID: "legacy-c", Type: "docker-image-pull", Status: "running", Reference: "alpine:3", Message: "进行中", Progress: 40, DownloadedBytes: 4, TotalBytes: 10, SpeedBytes: 2, LayersJSON: "{}", CreatedAt: time.Unix(10, 0).UTC(), UpdatedAt: time.Unix(12, 0).UTC(), Revision: 1},
		{ID: "legacy-b", Type: "docker-image-pull", Status: "failed", Reference: "image:b", Message: "失败", Error: "example", Progress: 20, DownloadedBytes: 2, TotalBytes: 10, LayersJSON: "[]", CreatedAt: time.Unix(10, 0).UTC(), UpdatedAt: time.Unix(13, 0).UTC(), CompletedAt: time.Unix(13, 0).UTC(), Revision: 1},
		{ID: "legacy-a", Type: "compose-deploy", Status: "completed", Reference: "project-a", Message: "已完成", Progress: 100, DownloadedBytes: 12, TotalBytes: 12, LayersJSON: "[]", CreatedAt: time.Unix(10, 0).UTC(), UpdatedAt: time.Unix(11, 0).UTC(), CompletedAt: time.Unix(11, 0).UTC(), Revision: 1},
	}
	for reopen := 0; reopen < 2; reopen++ {
		store, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		// Open 已迁移；再次显式执行，覆盖重复迁移和第二次重新打开。
		if err := store.migrate(context.Background()); err != nil {
			store.Close()
			t.Fatal(err)
		}
		got, err := store.Jobs(context.Background(), "", 500)
		if err != nil || !reflect.DeepEqual(got, expected) {
			store.Close()
			t.Fatalf("migration changed history: %#v %v", got, err)
		}
		requireRecentQueryPlans(t, store.database)
		var count int
		if err := store.database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name IN ('jobs_type_created','jobs_created_id','jobs_type_created_id')`).Scan(&count); err != nil || count != 3 {
			store.Close()
			t.Fatalf("additive indexes: %d %v", count, err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// 不设置耗时断言；同一固定驱动/临时库比较旧索引查询和生产查询的读取成本。
func BenchmarkRecentJobsHistory(b *testing.B) {
	for _, size := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprintf("records_%d", size), func(b *testing.B) {
			for _, indexed := range []bool{false, true} {
				label := "legacy"
				if indexed {
					label = "indexed"
				}
				b.Run(label, func(b *testing.B) {
					store, err := Open(filepath.Join(b.TempDir(), "jobs.sqlite"))
					if err != nil {
						b.Fatal(err)
					}
					defer store.Close()
					if !indexed {
						if _, err := store.database.Exec(`DROP INDEX jobs_created_id; DROP INDEX jobs_type_created_id`); err != nil {
							b.Fatal(err)
						}
					}
					tx, err := store.database.Begin()
					if err != nil {
						b.Fatal(err)
					}
					defer tx.Rollback()
					stmt, err := tx.Prepare(`INSERT INTO jobs (id,type,status,message,created_at_unix,updated_at_unix) VALUES (?,?,'completed',?,?,?)`)
					if err != nil {
						b.Fatal(err)
					}
					for i := 0; i < size; i++ {
						kind := []string{"docker-image-pull", "compose-deploy", "docker-image-remove"}[i%3]
						if _, err := stmt.Exec(fmt.Sprintf("record-%08d", i), kind, strings.Repeat("x", 200), i/10, i/10); err != nil {
							stmt.Close()
							b.Fatal(err)
						}
					}
					if err := stmt.Close(); err != nil {
						b.Fatal(err)
					}
					if err := tx.Commit(); err != nil {
						b.Fatal(err)
					}
					for _, kind := range []string{"", "docker-image-pull"} {
						name := "mixed"
						if kind != "" {
							name = "typed"
						}
						b.Run(name, func(b *testing.B) {
							b.ReportAllocs()
							expected := min(500, size)
							if kind != "" {
								expected = min(500, (size+2)/3)
							}
							for i := 0; i < b.N; i++ {
								if indexed {
									got, err := store.Jobs(context.Background(), kind, 500)
									if err != nil || len(got) != expected {
										b.Fatalf("read: %d %v", len(got), err)
									}
								} else {
									got := readRecentTestQuery(b, store.database, legacyRecentJobsQuery, kind, kind, 500)
									if len(got) != expected {
										b.Fatalf("read: %d", len(got))
									}
								}
							}
						})
					}
				})
			}
		})
	}
}
