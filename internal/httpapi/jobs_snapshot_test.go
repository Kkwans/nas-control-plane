package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
)

type snapshotJobStore struct {
	records   chan controlstore.JobRecord
	deleteErr error
}

func (store *snapshotJobStore) UpsertJob(_ context.Context, record controlstore.JobRecord) error {
	store.records <- record
	return nil
}
func (store *snapshotJobStore) Jobs(context.Context, string, int) ([]controlstore.JobRecord, error) {
	return nil, nil
}
func (store *snapshotJobStore) DeleteJob(context.Context, string) error          { return store.deleteErr }
func (store *snapshotJobStore) MarkRunningJobsInterrupted(context.Context) error { return nil }

func TestJobSnapshotEscapeIsolation(t *testing.T) {
	cases := []struct {
		name     string
		status   string
		snapshot func(*testing.T, *jobRegistry, string) jobSnapshot
	}{
		{"get", "completed", func(t *testing.T, r *jobRegistry, id string) jobSnapshot { j, _ := r.get(id); return j }},
		{"list", "completed", func(t *testing.T, r *jobRegistry, id string) jobSnapshot { return r.list("docker-image-pull")[0] }},
		{"cancel", "running", func(t *testing.T, r *jobRegistry, id string) jobSnapshot {
			called := false
			r.setCancel(id, func() { called = true })
			j, exists, err := r.cancel(id)
			if !exists || err != nil || !called {
				t.Fatalf("cancel = %v, %v; called = %v", exists, err, called)
			}
			return j
		}},
		{"cancel-terminal", "completed", func(t *testing.T, r *jobRegistry, id string) jobSnapshot {
			j, exists, err := r.cancel(id)
			if !exists || err == nil {
				t.Fatalf("cancel = %v, %v", exists, err)
			}
			return j
		}},
		{"cancel-unavailable", "running", func(t *testing.T, r *jobRegistry, id string) jobSnapshot {
			j, exists, err := r.cancel(id)
			if !exists || err == nil {
				t.Fatalf("cancel = %v, %v", exists, err)
			}
			return j
		}},
		{"delete-active", "running", func(t *testing.T, r *jobRegistry, id string) jobSnapshot {
			j, exists, err := r.delete(id)
			if !exists || err == nil {
				t.Fatalf("delete = %v, %v", exists, err)
			}
			return j
		}},
		{"delete-rollback", "completed", func(t *testing.T, r *jobRegistry, id string) jobSnapshot {
			r.store = &snapshotJobStore{deleteErr: errors.New("fake delete failure")}
			j, exists, err := r.delete(id)
			if !exists || err == nil {
				t.Fatalf("delete = %v, %v", exists, err)
			}
			return j
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newJobRegistry(nil)
			completedAt := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
			r.jobs["job"] = jobSnapshot{ID: "job", Type: "docker-image-pull", Status: tc.status, CompletedAt: &completedAt, Layers: map[string]jobLayer{"layer": {ID: "layer", Current: 1, Total: 100}}}
			snapshot := tc.snapshot(t, r, "job")
			before, _ := r.get("job")
			wantCompletedAt := *before.CompletedAt
			snapshot.Layers["layer"] = jobLayer{ID: "layer", Current: 99}
			snapshot.Layers["external"] = jobLayer{ID: "external"}
			*snapshot.CompletedAt = snapshot.CompletedAt.Add(time.Hour)
			after, _ := r.get("job")
			if after.Layers["layer"].Current != 1 || len(after.Layers) != 1 || !after.CompletedAt.Equal(wantCompletedAt) {
				t.Fatalf("snapshot mutation changed registry: %+v", after)
			}
			// 反向验证：后续进度写入不能改变已经返回的快照。
			r.store = nil
			r.updatePullProgress("job", jobLayer{ID: "layer", Current: 2, Total: 100})
			if snapshot.Layers["layer"].Current != 99 {
				t.Fatal("progress changed returned snapshot")
			}
		})
	}
}

func TestJobSnapshotCreateIsolation(t *testing.T) {
	r := newJobRegistry(nil)
	created := r.create("docker-image-pull", "fixture:latest")
	r.updatePullProgress(created.ID, jobLayer{ID: "layer", Current: 1, Total: 100})
	if len(created.Layers) != 0 {
		t.Fatal("progress changed create snapshot")
	}
	created.Layers["external"] = jobLayer{ID: "external"}
	current, _ := r.get(created.ID)
	if _, ok := current.Layers["external"]; ok {
		t.Fatal("create snapshot changed registry")
	}
}

func TestJobSnapshotDeleteSuccessIsolation(t *testing.T) {
	r := newJobRegistry(nil)
	completedAt := time.Now().UTC()
	wantCompletedAt := completedAt
	original := jobSnapshot{ID: "job", Status: "completed", CompletedAt: &completedAt, Layers: map[string]jobLayer{"layer": {ID: "layer"}}}
	r.jobs[original.ID] = original
	deleted, exists, err := r.delete(original.ID)
	if !exists || err != nil {
		t.Fatalf("delete = %v, %v", exists, err)
	}
	deleted.Layers["external"] = jobLayer{}
	*deleted.CompletedAt = deleted.CompletedAt.Add(time.Hour)
	if len(original.Layers) != 1 || !original.CompletedAt.Equal(wantCompletedAt) {
		t.Fatal("delete snapshot retained internal aliases")
	}
	if _, exists := r.get(original.ID); exists {
		t.Fatal("job was not deleted")
	}
}

func TestJobSnapshotConcurrentProgressJSONAndPersistence(t *testing.T) {
	const iterations = 100
	// 每条持久化记录有独立的字符串和值字段，fake 不执行 Agent 或系统操作。
	const expectedRecords = 2 + iterations*4
	store := &snapshotJobStore{records: make(chan controlstore.JobRecord, expectedRecords)}
	r := newJobRegistry(store)
	created := r.create("docker-image-pull", "fixture:latest")
	r.update(created.ID, "completed", "done", "", 100)
	r.setCancel(created.ID, func() {})
	var wg sync.WaitGroup
	start := make(chan struct{})
	run := func(work func()) { wg.Add(1); go func() { defer wg.Done(); <-start; work() }() }
	run(func() {
		for i := 1; i <= iterations; i++ {
			r.updatePullProgress(created.ID, jobLayer{ID: fmt.Sprintf("layer-%d", i%16), Current: int64(i), Total: 1000})
		}
	})
	run(func() {
		for i := 1; i <= iterations; i++ {
			r.setExpectedTotal(created.ID, int64(i*1000))
		}
	})
	run(func() {
		for i := 0; i < iterations; i++ {
			r.completePull(created.ID)
		}
	})
	run(func() {
		for i := 0; i < iterations; i++ {
			r.update(created.ID, "completed", "done", "", 100)
		}
	})
	for reader := 0; reader < 4; reader++ {
		run(func() {
			for i := 0; i < iterations; i++ {
				current, _ := r.get(created.ID)
				listed := r.list("")
				// 取消成功路径已在隔离测试中验证；这里仅返回终态错误快照以避免额外持久化。
				cancelled, _, _ := r.cancel(created.ID)
				if _, err := json.Marshal(created); err != nil {
					t.Error(err)
				}
				for _, snapshot := range append(listed, current, cancelled) {
					if _, err := json.Marshal(snapshot); err != nil {
						t.Error(err)
					}
					snapshot.Layers["reader"] = jobLayer{ID: "reader"}
					if snapshot.CompletedAt != nil {
						*snapshot.CompletedAt = snapshot.CompletedAt.Add(time.Second)
					}
				}
			}
		})
	}
	close(start)
	wg.Wait()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for i := 0; i < expectedRecords; i++ {
		select {
		case record := <-store.records:
			var layers map[string]jobLayer
			if err := json.Unmarshal([]byte(record.LayersJSON), &layers); err != nil {
				t.Fatal(err)
			}
			if _, exists := layers["reader"]; exists {
				t.Fatal("snapshot mutation escaped into persisted layers")
			}
		case <-deadline.C:
			t.Fatalf("received %d of %d persistence records", i, expectedRecords)
		}
	}
	cancelled, _, _ := r.cancel(created.ID)
	if _, err := json.Marshal(cancelled); err != nil {
		t.Fatal(err)
	}
}

func TestJobSnapshotCancelPersistsCapturedLayers(t *testing.T) {
	store := &snapshotJobStore{records: make(chan controlstore.JobRecord, 4)}
	r := newJobRegistry(store)
	created := r.create("docker-image-pull", "fixture:latest")
	r.updatePullProgress(created.ID, jobLayer{ID: "layer", Current: 1, Total: 100})
	// cancel 回调在锁外执行；模拟任务停止前的最后一条进度更新。
	r.setCancel(created.ID, func() { r.updatePullProgress(created.ID, jobLayer{ID: "layer", Current: 2, Total: 100}) })
	cancelled, exists, err := r.cancel(created.ID)
	if !exists || err != nil {
		t.Fatalf("cancel = %v, %v", exists, err)
	}
	if cancelled.Layers["layer"].Current != 1 {
		t.Fatal("cancel callback changed returned snapshot")
	}
	var finalRecord controlstore.JobRecord
	for i := 0; i < 4; i++ {
		finalRecord = <-store.records
	}
	var layers map[string]jobLayer
	if err := json.Unmarshal([]byte(finalRecord.LayersJSON), &layers); err != nil {
		t.Fatal(err)
	}
	if layers["layer"].Current != 1 || finalRecord.DownloadedBytes != 1 {
		t.Fatalf("persisted mixed snapshot: %+v; layers = %+v", finalRecord, layers)
	}
}
