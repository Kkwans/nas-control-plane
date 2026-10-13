package httpapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
)

func TestJobFailurePersistsCodeAndDoesNotOverwriteTerminal(t *testing.T) {
	for _, status := range []string{"completed", "failed", "cancelled", "interrupted"} {
		t.Run(status, func(t *testing.T) {
			store := &snapshotJobStore{}
			jobs := newJobRegistry(store)
			job := jobs.create("compose-deploy", "fixture")
			if status == "failed" {
				jobs.fail(job.ID, "COMPOSE_DEPLOY_ROLLBACK_CONFLICT", "recovery detail", "recovery detail")
			} else {
				jobs.update(job.ID, status, "original terminal", "original detail", 0)
			}
			before, _ := store.Job(context.Background(), job.ID)
			if status == "failed" && (before.ErrorCode != "COMPOSE_DEPLOY_ROLLBACK_CONFLICT" || before.Error != "recovery detail" || before.Message != "recovery detail" || before.CompletedAt.IsZero()) {
				t.Fatalf("failure details not saved: %+v", before)
			}
			jobs.fail(job.ID, "COMPOSE_DEPLOY_FAILED", "late failure", "late detail")
			after, _ := store.Job(context.Background(), job.ID)
			if !sameJobRecord(before, after) {
				t.Fatalf("terminal result changed: before %+v, after %+v", before, after)
			}
		})
	}
}

func TestJobFailurePendingResultRetriesWithoutReplacement(t *testing.T) {
	store := &snapshotJobStore{}
	jobs := newJobRegistry(store)
	job := jobs.create("compose-deploy", "fixture")
	jobs.update(job.ID, "running", "running", "", 0)
	store.updateErr = errors.New("storage unavailable")
	jobs.fail(job.ID, "COMPOSE_DEPLOY_ROLLBACK_UNVERIFIED", "first recovery detail", "first recovery detail")
	current, _ := jobs.get(job.ID)
	if current.Status != "running" || current.PersistenceStatus != "pending" || current.ErrorCode != "" || current.CompletedAt != nil {
		t.Fatalf("unsaved terminal result published: %+v", current)
	}
	jobs.fail(job.ID, "COMPOSE_DEPLOY_FAILED", "late failure", "late detail")
	jobs.update(job.ID, "completed", "late success", "", 100)
	store.updateErr = nil
	jobs.flush()
	current, _ = jobs.get(job.ID)
	if current.Status != "failed" || current.ErrorCode != "COMPOSE_DEPLOY_ROLLBACK_UNVERIFIED" || current.Message != "first recovery detail" || current.Error != "first recovery detail" || current.PersistenceStatus != "" || current.CompletedAt == nil {
		t.Fatalf("pending failure lost details: %+v", current)
	}
	durable, _ := store.Job(context.Background(), job.ID)
	if durable.ErrorCode != current.ErrorCode || durable.Message != current.Message || durable.Error != current.Error || durable.Status != current.Status {
		t.Fatalf("failure did not persist: %+v", durable)
	}
}

func TestJobFailureDoesNotOverwriteNewerStoreRevision(t *testing.T) {
	store := &snapshotJobStore{}
	jobs := newJobRegistry(store)
	job := jobs.create("compose-deploy", "fixture")
	newer, _ := store.Job(context.Background(), job.ID)
	newer.Status = "interrupted"
	newer.ErrorCode = "EXTERNAL_RESULT"
	newer.Message = "newer durable result"
	if _, err := store.UpdateJob(context.Background(), newer, 1); err != nil {
		t.Fatal(err)
	}
	jobs.fail(job.ID, "COMPOSE_DEPLOY_FAILED", "stale failure", "stale detail")
	jobs.flush()
	durable, _ := store.Job(context.Background(), job.ID)
	if durable.Status != "interrupted" || durable.ErrorCode != "EXTERNAL_RESULT" || durable.Revision != 2 || durable.Message != newer.Message {
		t.Fatalf("stale failure overwrote newer revision: %+v", durable)
	}
	current, _ := jobs.get(job.ID)
	if current.Status != "queued" || current.PersistenceStatus != "pending" {
		t.Fatalf("unconfirmed stale failure was published: %+v", current)
	}
}

func TestJobFailureAcknowledgementLossAcceptsExactResult(t *testing.T) {
	store := &snapshotJobStore{committedError: true}
	jobs := newJobRegistry(store)
	job := jobs.create("compose-deploy", "fixture")
	jobs.fail(job.ID, "COMPOSE_DEPLOY_CONFIG_RESTORE_FAILED", "recovery detail", "recovery detail")
	current, _ := jobs.get(job.ID)
	if current.Status != "failed" || current.ErrorCode != "COMPOSE_DEPLOY_CONFIG_RESTORE_FAILED" || current.PersistenceStatus != "" {
		t.Fatalf("durable failure acknowledgement was lost: %+v", current)
	}
}

func TestJobFailureNeverResurrectsMissingOrDeletedEntry(t *testing.T) {
	store := &snapshotJobStore{}
	jobs := newJobRegistry(store)
	jobs.fail("missing", "COMPOSE_DEPLOY_FAILED", "failure", "detail")
	if len(jobs.list("")) != 0 {
		t.Fatal("missing job created by failure")
	}
	job := jobs.create("compose-deploy", "fixture")
	entry := jobs.lookup(job.ID)
	entry.opMu.Lock()
	if err := store.DeleteJobRevision(context.Background(), job.ID, entry.revision); err != nil {
		entry.opMu.Unlock()
		t.Fatal(err)
	}
	entry.deleted = true
	entry.opMu.Unlock()
	jobs.fail(job.ID, "COMPOSE_DEPLOY_FAILED", "failure", "detail")
	if entry.pending != nil || entry.snapshot().PersistenceStatus != "" || entry.snapshot().Status != "queued" {
		t.Fatal("failure changed deleted entry")
	}
	if _, err := store.Job(context.Background(), job.ID); !errors.Is(err, controlstore.ErrJobNotFound) {
		t.Fatalf("failure resurrected deleted record: %v", err)
	}
}

func TestJobFailureSerializesWithCancellation(t *testing.T) {
	tests := []struct {
		name            string
		running         bool
		failureFirst    bool
		wantStatus      string
		wantCancel      string
		wantRevision    int64
		wantCancelError error
	}{
		{name: "queued cancellation wins", wantStatus: "cancelled", wantCancel: "confirmed", wantRevision: 2},
		{name: "running cancellation then failure", running: true, wantStatus: "failed", wantCancel: "requested", wantRevision: 4},
		{name: "failure wins", failureFirst: true, wantStatus: "failed", wantRevision: 2, wantCancelError: errJobCancel},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &snapshotJobStore{}
			jobs := newJobRegistry(store)
			// Compose 不提供取消入口；这里使用可取消任务验证通用窄终态入口的锁与 CAS。
			job := jobs.create("docker-image-pull", "fixture")
			if test.running {
				jobs.update(job.ID, "running", "running", "", 0)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			store.beforeUpdate = func(record controlstore.JobRecord) {
				if test.failureFirst && record.Status == "failed" || !test.failureFirst && record.CancellationStatus != "" && record.Status != "failed" {
					close(entered)
					<-release
				}
			}
			cancelDone := make(chan error, 1)
			failDone := make(chan struct{})
			cancelJob := func() {
				_, _, err := jobs.cancel(job.ID)
				cancelDone <- err
			}
			failJob := func() {
				jobs.fail(job.ID, "COMPOSE_DEPLOY_ROLLBACK_CONFLICT", "recovery detail", "recovery detail")
				close(failDone)
			}
			if test.failureFirst {
				go failJob()
			} else {
				go cancelJob()
			}
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("first operation did not reach persistence")
			}
			secondStarted := make(chan struct{})
			go func() {
				close(secondStarted)
				if test.failureFirst {
					cancelJob()
				} else {
					failJob()
				}
			}()
			<-secondStarted
			close(release)
			select {
			case err := <-cancelDone:
				if !errors.Is(err, test.wantCancelError) {
					t.Fatalf("cancel error = %v, want %v", err, test.wantCancelError)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cancellation did not finish")
			}
			select {
			case <-failDone:
			case <-time.After(5 * time.Second):
				t.Fatal("failure did not finish")
			}
			durable, err := store.Job(context.Background(), job.ID)
			current, _ := jobs.get(job.ID)
			if err != nil || durable.Status != test.wantStatus || durable.CancellationStatus != test.wantCancel || durable.Revision != test.wantRevision || current.Status != durable.Status || current.PersistenceStatus != "" {
				t.Fatalf("cancellation/failure outcome lost: durable %+v, current %+v, err %v", durable, current, err)
			}
			if test.wantStatus == "failed" {
				if durable.ErrorCode != "COMPOSE_DEPLOY_ROLLBACK_CONFLICT" || durable.Message != "recovery detail" || durable.Error != "recovery detail" {
					t.Fatalf("failure details lost during cancellation: %+v", durable)
				}
			} else if durable.ErrorCode != "" {
				t.Fatalf("terminal cancellation overwritten by failure: %+v", durable)
			}
		})
	}
}
