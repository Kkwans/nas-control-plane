package controlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func openJobTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}

func jobFixture(id string) JobRecord {
	now := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	return JobRecord{ID: id, Type: "docker-image-pull", Reference: "alpine:3", Status: "queued",
		Progress: 42, DownloadedBytes: 420, TotalBytes: 1000, SpeedBytes: 21,
		LayersJSON: `{"layer-a":{"id":"layer-a","current":420,"total":1000}}`,
		CreatedAt:  now, UpdatedAt: now}
}

func requireJob(t *testing.T, store *Store, id string) JobRecord {
	t.Helper()
	job, err := store.Job(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestJobRevisionContracts(t *testing.T) {
	store, _ := openJobTestStore(t)
	ctx := context.Background()
	job := jobFixture("one")
	job.Revision = 99
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	original := requireJob(t, store, job.ID)
	if original.Revision != 1 {
		t.Fatalf("initial revision = %d", original.Revision)
	}
	job.Status = "failed"
	if err := store.CreateJob(ctx, job); !errors.Is(err, ErrJobExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if got := requireJob(t, store, job.ID); !reflect.DeepEqual(got, original) {
		t.Fatalf("duplicate overwrote record: %#v", got)
	}
	job.Status = "running"
	job.Type, job.Reference, job.CreatedAt = "changed", "changed", time.Now()
	job.CancellationStatus, job.ErrorCode = "requested", "EXAMPLE"
	revision, err := store.UpdateJob(ctx, job, 1)
	if err != nil || revision != 2 {
		t.Fatalf("update: %d %v", revision, err)
	}
	saved := requireJob(t, store, job.ID)
	if saved.Revision != 2 || saved.Type != original.Type || saved.Reference != original.Reference || !saved.CreatedAt.Equal(original.CreatedAt) || saved.CancellationStatus != "requested" || saved.ErrorCode != "EXAMPLE" {
		t.Fatalf("update roundtrip: %#v", saved)
	}
	if _, err := store.UpdateJob(ctx, original, 1); !errors.Is(err, ErrJobConflict) {
		t.Fatalf("stale update: %v", err)
	}
	if err := store.DeleteJobRevision(ctx, job.ID, 1); !errors.Is(err, ErrJobConflict) {
		t.Fatalf("stale delete: %v", err)
	}
	if got := requireJob(t, store, job.ID); !reflect.DeepEqual(got, saved) {
		t.Fatalf("conflict changed record: %#v", got)
	}
	for _, revision := range []int64{-1, 0, math.MaxInt64} {
		if _, err := store.UpdateJob(ctx, job, revision); !errors.Is(err, ErrJobInvalidRevision) {
			t.Fatalf("invalid update revision %d: %v", revision, err)
		}
	}
	if err := store.DeleteJobRevision(ctx, job.ID, 0); !errors.Is(err, ErrJobInvalidRevision) {
		t.Fatalf("invalid delete: %v", err)
	}
	if err := store.DeleteJobRevision(ctx, job.ID, 2); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := store.UpdateJob(ctx, saved, 2); !errors.Is(err, ErrJobNotFound) {
			t.Fatalf("late update: %v", err)
		}
		if err := store.DeleteJobRevision(ctx, job.ID, 2); !errors.Is(err, ErrJobNotFound) {
			t.Fatalf("repeat delete: %v", err)
		}
		if _, err := store.Job(ctx, job.ID); !errors.Is(err, ErrJobNotFound) {
			t.Fatalf("deleted record reappeared: %v", err)
		}
	}
}

func TestJobCASConcurrentConnections(t *testing.T) {
	store, path := openJobTestStore(t)
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	ctx := context.Background()
	for _, operation := range []string{"create", "update", "delete", "update-delete"} {
		t.Run(operation, func(t *testing.T) {
			job := jobFixture(operation)
			if operation != "create" {
				if err := store.CreateJob(ctx, job); err != nil {
					t.Fatal(err)
				}
			}
			const workers = 16
			start := make(chan struct{})
			outcomes := make(chan error, workers)
			var wg sync.WaitGroup
			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					connection := store
					if i%2 == 1 {
						connection = other
					}
					<-start
					var err error
					switch operation {
					case "create":
						err = connection.CreateJob(ctx, job)
					case "update":
						_, err = connection.UpdateJob(ctx, job, 1)
					case "delete":
						err = connection.DeleteJobRevision(ctx, job.ID, 1)
					case "update-delete":
						if i%2 == 0 {
							_, err = connection.UpdateJob(ctx, job, 1)
						} else {
							err = connection.DeleteJobRevision(ctx, job.ID, 1)
						}
					}
					outcomes <- err
				}(i)
			}
			close(start)
			wg.Wait()
			close(outcomes)
			wins := 0
			for err := range outcomes {
				if err == nil {
					wins++
					continue
				}
				expected := ErrJobConflict
				if operation == "create" {
					expected = ErrJobExists
				}
				if operation == "delete" {
					expected = ErrJobNotFound
				}
				if operation == "update-delete" && errors.Is(err, ErrJobNotFound) {
					continue
				}
				if !errors.Is(err, expected) {
					t.Errorf("unexpected competing result: %v", err)
				}
			}
			if wins != 1 {
				t.Fatalf("successful competing writes = %d", wins)
			}
		})
	}
}

func TestJobStorageFailuresPreserveDurableState(t *testing.T) {
	store, path := openJobTestStore(t)
	ctx := context.Background()
	job := jobFixture("existing")
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	original := requireJob(t, store, job.ID)
	for _, operation := range []string{"INSERT", "UPDATE", "DELETE"} {
		t.Run(operation, func(t *testing.T) {
			_, err := store.database.ExecContext(ctx, fmt.Sprintf(`CREATE TRIGGER fail_job BEFORE %s ON jobs BEGIN SELECT RAISE(ABORT, 'injected storage failure'); END`, operation))
			if err != nil {
				t.Fatal(err)
			}
			switch operation {
			case "INSERT":
				err = store.CreateJob(ctx, jobFixture("rejected"))
			case "UPDATE":
				job.Status = "completed"
				_, err = store.UpdateJob(ctx, job, 1)
			case "DELETE":
				err = store.DeleteJobRevision(ctx, job.ID, 1)
			}
			if err == nil {
				t.Fatal("storage failure swallowed")
			}
			if _, err := store.database.ExecContext(ctx, "DROP TRIGGER fail_job"); err != nil {
				t.Fatal(err)
			}
			if got := requireJob(t, store, job.ID); !reflect.DeepEqual(got, original) {
				t.Fatalf("failed write changed record: %#v", got)
			}
		})
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := store.CreateJob(canceled, jobFixture("canceled")); !errors.Is(err, context.Canceled) {
		t.Fatalf("create cancellation: %v", err)
	}
	if _, err := store.UpdateJob(canceled, job, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("update cancellation: %v", err)
	}
	if err := store.DeleteJobRevision(canceled, job.ID, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("delete cancellation: %v", err)
	}
	if err := store.RecoverInterruptedJobs(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("recovery cancellation: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateJob(ctx, job, 1); err == nil {
		t.Fatal("closed database accepted update")
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := requireJob(t, reopened, job.ID); !reflect.DeepEqual(got, original) {
		t.Fatalf("reopen changed durable record: %#v", got)
	}
	for _, id := range []string{"rejected", "canceled"} {
		if _, err := reopened.Job(ctx, id); !errors.Is(err, ErrJobNotFound) {
			t.Fatalf("failed creation persisted: %s %v", id, err)
		}
	}
}

func TestJobMigrationRecoveryAndLegacyBridge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE jobs (
 id TEXT PRIMARY KEY, type TEXT NOT NULL, status TEXT NOT NULL,
 reference TEXT NOT NULL DEFAULT '', message TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
 progress INTEGER NOT NULL DEFAULT 0, downloaded_bytes INTEGER NOT NULL DEFAULT 0,
 total_bytes INTEGER NOT NULL DEFAULT 0, speed_bytes INTEGER NOT NULL DEFAULT 0,
 layers_json TEXT NOT NULL DEFAULT '[]', created_at_unix INTEGER NOT NULL,
 updated_at_unix INTEGER NOT NULL, completed_at_unix INTEGER NOT NULL DEFAULT 0);
 INSERT INTO jobs(id,type,status,layers_json,created_at_unix,updated_at_unix)
 VALUES('legacy','docker-image-pull','running','malformed legacy payload',1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := store.migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	legacy := requireJob(t, store, "legacy")
	if legacy.Revision != 1 || legacy.CancellationStatus != "" || legacy.ErrorCode != "" || legacy.LayersJSON != "malformed legacy payload" {
		t.Fatalf("migration changed legacy data: %#v", legacy)
	}
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	for _, status := range []string{"queued", "running", "completed", "failed", "cancelled", "interrupted"} {
		job := jobFixture(status)
		job.Status = status
		if status == "running" {
			job.CancellationStatus = "requested"
		}
		if err := store.CreateJob(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	// 恢复单条语句失败时不得留下部分已恢复记录。
	if _, err := store.database.Exec(`CREATE TRIGGER fail_recovery BEFORE UPDATE ON jobs WHEN OLD.id = 'running' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.RecoverInterruptedJobs(ctx); err == nil {
		t.Fatal("recovery swallowed failure")
	}
	if got := requireJob(t, store, "queued"); got.Status != "queued" || got.Revision != 1 {
		t.Fatalf("partial recovery: %#v", got)
	}
	if _, err := store.database.Exec(`DROP TRIGGER fail_recovery`); err != nil {
		t.Fatal(err)
	}
	if err := store.RecoverInterruptedJobs(ctx); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"queued", "running", "completed", "failed", "cancelled", "interrupted"} {
		got := requireJob(t, store, status)
		if status == "queued" || status == "running" {
			if got.Status != "interrupted" || got.Revision != 2 || got.SpeedBytes != 0 || got.DownloadedBytes != 420 || !got.CompletedAt.Equal(now) || !got.UpdatedAt.Equal(now) {
				t.Fatalf("recovery: %#v", got)
			}
			if status == "running" && got.CancellationStatus != "unknown" {
				t.Fatalf("cancellation recovery: %#v", got)
			}
			if _, err := store.UpdateJob(ctx, jobFixture(status), 1); !errors.Is(err, ErrJobConflict) {
				t.Fatalf("pre-recovery update: %v", err)
			}
		} else if got.Status != status || got.Revision != 1 {
			t.Fatalf("terminal changed: %#v", got)
		}
	}
	if err := store.MarkRunningJobsInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	if got := requireJob(t, store, "running"); got.Revision != 2 {
		t.Fatalf("repeated recovery increments terminal: %#v", got)
	}
	// 过渡接口仍可用，但必须推进版本，使 CAS 能检测旧写。
	legacy.Status = "failed"
	if err := store.UpsertJob(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if got := requireJob(t, store, "legacy"); got.Revision != 3 {
		t.Fatalf("legacy write revision = %d", got.Revision)
	}
	if err := store.DeleteJob(ctx, "legacy"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Job(ctx, "legacy"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("legacy delete: %v", err)
	}
}

func TestJobsListIncludesRevisionMetadataAndStableOrder(t *testing.T) {
	store, _ := openJobTestStore(t)
	ctx := context.Background()
	for _, id := range []string{"a", "c", "b"} {
		job := jobFixture(id)
		job.CancellationStatus, job.ErrorCode = "unknown", "JOB_EXECUTION_TIMEOUT"
		if err := store.CreateJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpdateJob(ctx, job, 1); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := store.Jobs(ctx, "docker-image-pull", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[0].ID != "c" || jobs[1].ID != "b" {
		t.Fatalf("unstable list ordering: %#v", jobs)
	}
	for _, job := range jobs {
		if !reflect.DeepEqual(job, requireJob(t, store, job.ID)) {
			t.Fatalf("list metadata differs from lookup: %#v", job)
		}
	}
	missing, err := store.Jobs(ctx, "other-kind", 10)
	if err != nil || len(missing) != 0 {
		t.Fatalf("kind filter: %#v %v", missing, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.Job(canceled, "a"); !errors.Is(err, context.Canceled) {
		t.Fatalf("lookup cancellation: %v", err)
	}
	if _, err := store.Jobs(canceled, "", 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("list cancellation: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Job(ctx, "a"); err == nil || errors.Is(err, ErrJobNotFound) {
		t.Fatalf("lookup masked database failure: %v", err)
	}
	if _, err := store.Jobs(ctx, "", 10); err == nil {
		t.Fatal("list masked database failure")
	}
}
