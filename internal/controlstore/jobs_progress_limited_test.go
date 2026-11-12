package controlstore

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestJobProgressLimitedMigrationDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "before-progress-limit.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// 已有生命周期字段的旧库，只增加进度预算标记，保留原始数据。
	_, err = db.Exec(`CREATE TABLE jobs (
 id TEXT PRIMARY KEY, type TEXT NOT NULL, status TEXT NOT NULL,
 reference TEXT NOT NULL DEFAULT '', message TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
 progress INTEGER NOT NULL DEFAULT 0, downloaded_bytes INTEGER NOT NULL DEFAULT 0,
 total_bytes INTEGER NOT NULL DEFAULT 0, speed_bytes INTEGER NOT NULL DEFAULT 0,
 layers_json TEXT NOT NULL DEFAULT '[]', created_at_unix INTEGER NOT NULL,
 updated_at_unix INTEGER NOT NULL, completed_at_unix INTEGER NOT NULL DEFAULT 0,
 revision INTEGER NOT NULL DEFAULT 1, cancellation_status TEXT NOT NULL DEFAULT '',
 error_code TEXT NOT NULL DEFAULT '');
 INSERT INTO jobs(id,type,status,progress,layers_json,created_at_unix,updated_at_unix,
 revision,cancellation_status,error_code)
 VALUES('legacy','docker-image-pull','running',42,'legacy payload',1,2,7,'requested','EXAMPLE')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for reopen := 0; reopen < 2; reopen++ {
		store, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		if err := store.migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		got := requireJob(t, store, "legacy")
		if got.ProgressLimited || got.Progress != 42 || got.Revision != 7 || got.Status != "running" ||
			got.LayersJSON != "legacy payload" || got.CancellationStatus != "requested" || got.ErrorCode != "EXAMPLE" ||
			got.CreatedAt.Unix() != 1 || got.UpdatedAt.Unix() != 2 {
			t.Fatalf("migration changed legacy data: %#v", got)
		}
		var columnType, defaultValue string
		var notNull int
		if err := store.database.QueryRow(`SELECT type, "notnull", dflt_value
 FROM pragma_table_info('jobs') WHERE name = 'progress_limited'`).Scan(&columnType, &notNull, &defaultValue); err != nil {
			t.Fatal(err)
		}
		if columnType != "INTEGER" || notNull != 1 || defaultValue != "0" {
			t.Fatalf("progress_limited definition = %q NOT NULL %d DEFAULT %q", columnType, notNull, defaultValue)
		}
		if reopen == 0 {
			// 未认识新列的写入者仍使用数据库默认值。
			if _, err := store.database.Exec(`INSERT INTO jobs(id,type,status,created_at_unix,updated_at_unix)
 VALUES('default','docker-image-pull','queued',3,3)`); err != nil {
				t.Fatal(err)
			}
		}
		if got := requireJob(t, store, "default"); got.ProgressLimited {
			t.Fatalf("omitted column did not default false: %#v", got)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestJobProgressLimitedCreateUpdateReadAndCAS(t *testing.T) {
	store, path := openJobTestStore(t)
	ctx := context.Background()
	plain := jobFixture("plain")
	if err := store.CreateJob(ctx, plain); err != nil {
		t.Fatal(err)
	}
	if got := requireJob(t, store, plain.ID); got.ProgressLimited {
		t.Fatalf("zero-value flag did not default false: %#v", got)
	}
	limited := jobFixture("limited")
	limited.ProgressLimited = true
	if err := store.CreateJob(ctx, limited); err != nil {
		t.Fatal(err)
	}
	limited.Revision = 1
	if got := requireJob(t, store, limited.ID); !reflect.DeepEqual(got, limited) {
		t.Fatalf("create roundtrip: got %#v, want %#v", got, limited)
	}
	duplicate := limited
	duplicate.ProgressLimited = false
	if err := store.CreateJob(ctx, duplicate); !errors.Is(err, ErrJobExists) {
		t.Fatalf("duplicate create: %v", err)
	}
	if got := requireJob(t, store, limited.ID); !reflect.DeepEqual(got, limited) {
		t.Fatalf("duplicate create changed saved flag: %#v", got)
	}
	stale := requireJob(t, store, plain.ID)
	updated := stale
	updated.ProgressLimited = true
	revision, err := store.UpdateJob(ctx, updated, stale.Revision)
	if err != nil || revision != 2 {
		t.Fatalf("update flag: revision %d, error %v", revision, err)
	}
	updated.Revision = revision
	if got := requireJob(t, store, plain.ID); !reflect.DeepEqual(got, updated) {
		t.Fatalf("update roundtrip: got %#v, want %#v", got, updated)
	}
	if _, err := store.UpdateJob(ctx, stale, stale.Revision); !errors.Is(err, ErrJobConflict) {
		t.Fatalf("stale update: %v", err)
	}
	if got := requireJob(t, store, plain.ID); !reflect.DeepEqual(got, updated) {
		t.Fatalf("stale update changed saved flag: %#v", got)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for _, kind := range []string{"", limited.Type} {
		jobs, err := reopened.Jobs(ctx, kind, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(jobs) != 2 {
			t.Fatalf("reopened list length = %d", len(jobs))
		}
		for _, job := range jobs {
			want := limited
			if job.ID == updated.ID {
				want = updated
			}
			if !reflect.DeepEqual(job, want) || !reflect.DeepEqual(job, requireJob(t, reopened, job.ID)) {
				t.Fatalf("reopened list roundtrip: got %#v, want %#v", job, want)
			}
		}
	}
}

func TestJobProgressLimitedLegacyUpsertAndRecovery(t *testing.T) {
	store, _ := openJobTestStore(t)
	ctx := context.Background()
	for _, initiallyLimited := range []bool{false, true} {
		id := "legacy-default"
		if initiallyLimited {
			id = "legacy-limited"
		}
		job := jobFixture(id)
		job.ProgressLimited = initiallyLimited
		if err := store.UpsertJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		job.Revision = 1
		if got := requireJob(t, store, id); !reflect.DeepEqual(got, job) {
			t.Fatalf("legacy insert roundtrip: got %#v, want %#v", got, job)
		}
		stale := job
		job.ProgressLimited = true
		if err := store.UpsertJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		job.Revision = 2
		if got := requireJob(t, store, id); !reflect.DeepEqual(got, job) {
			t.Fatalf("legacy update roundtrip: got %#v, want %#v", got, job)
		}
		if _, err := store.UpdateJob(ctx, stale, stale.Revision); !errors.Is(err, ErrJobConflict) {
			t.Fatalf("legacy update did not invalidate stale revision: %v", err)
		}
	}
	if err := store.RecoverInterruptedJobs(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"legacy-default", "legacy-limited"} {
		if got := requireJob(t, store, id); !got.ProgressLimited || got.Status != "interrupted" || got.Revision != 3 {
			t.Fatalf("recovery changed progress budget marker: %#v", got)
		}
	}
}
