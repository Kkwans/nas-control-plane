package httpapi

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/docker"
)

type pullReceiptAgent struct {
	imageListTimeoutAgent
	result docker.ImagePullResult
	err    error
	calls  int
}

func (a *pullReceiptAgent) PullDockerImage(context.Context, string, docker.ImagePullRequest) (docker.ImagePullResult, error) {
	a.calls++
	return a.result, a.err
}
func TestImagePullReceiptRejectsWrongTarget(t *testing.T) {
	store := &snapshotJobStore{}
	jobs := newJobRegistry(store)
	job := jobs.create("docker-image-pull", "alpine:latest")
	agent := &pullReceiptAgent{result: docker.ImagePullResult{Reference: "nginx:latest", Completed: true}}
	api := &handler{jobs: jobs, dockerImages: agent}
	api.runImagePull(context.Background(), job.ID, docker.ImagePullRequest{Reference: "alpine:latest"})
	result, _ := jobs.get(job.ID)
	if result.Status != "interrupted" || result.ArtifactState != jobArtifactUnknown || agent.calls != 1 {
		t.Fatalf("wrong-target receipt accepted: %+v; calls=%d", result, agent.calls)
	}
}

type pullProgressReceiptAgent struct {
	*pullReceiptAgent
	afterProgress func()
}

func (a *pullProgressReceiptAgent) PullDockerImageWithProgress(ctx context.Context, socket string, request docker.ImagePullRequest, progress func(docker.ImagePullProgress)) (docker.ImagePullResult, error) {
	progress(docker.ImagePullProgress{LayerID: "layer", Status: "Downloading", Current: 3, Total: 9})
	if a.afterProgress != nil {
		a.afterProgress()
	}
	return a.PullDockerImage(ctx, socket, request)
}

func TestImagePullReceiptJobBoundaries(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, tc := range []struct {
			name, reference        string
			completed              bool
			status, code, artifact string
		}{
			{"valid", "alpine:latest", true, "completed", "", jobArtifactPresent},
			{"wrong target", "nginx:latest", true, "interrupted", "JOB_EXECUTION_INTERRUPTED", jobArtifactUnknown},
			{"blank", " \t", true, "interrupted", "JOB_EXECUTION_INTERRUPTED", jobArtifactUnknown},
			{"missing", "", true, "interrupted", "JOB_EXECUTION_INTERRUPTED", jobArtifactUnknown},
			{"incomplete", "alpine:latest", false, "interrupted", "JOB_EXECUTION_INTERRUPTED", jobArtifactUnknown},
		} {
			t.Run(fmt.Sprintf("%s/stream=%v", tc.name, streaming), func(t *testing.T) {
				store := &snapshotJobStore{}
				jobs := newJobRegistry(store)
				job := jobs.create("docker-image-pull", "alpine:latest")
				jobs.update(job.ID, "running", "running", "", 0)
				agent := &pullReceiptAgent{result: docker.ImagePullResult{Reference: tc.reference, Completed: tc.completed}}
				api := &handler{jobs: jobs, dockerImages: agent}
				if streaming {
					api.dockerImages = &pullProgressReceiptAgent{pullReceiptAgent: agent}
				}
				request := docker.ImagePullRequest{Reference: " alpine:latest ", ExpectedBytes: 9}
				api.runImagePull(context.Background(), job.ID, request)
				result, _ := jobs.get(job.ID)
				durable, err := store.Job(context.Background(), job.ID)
				if err != nil || result.Status != tc.status || result.ErrorCode != tc.code || result.ArtifactState != tc.artifact || result.CompletedAt == nil || agent.calls != 1 || durable.Status != tc.status || durable.ErrorCode != tc.code {
					t.Fatalf("result=%+v durable=%+v calls=%d err=%v", result, durable, agent.calls, err)
				}
				if streaming && (result.Layers["layer"].Current != 3 || result.TotalBytes != 9 || (tc.status != "completed" && result.DownloadedBytes != 3)) {
					t.Fatalf("progress lost: %+v", result)
				}
				if request.Reference != " alpine:latest " {
					t.Fatal("request changed")
				}
			})
		}
	}
}

func TestImagePullReceiptJobPersistenceOnlyRetriesSave(t *testing.T) {
	for _, valid := range []bool{false, true} {
		t.Run(fmt.Sprint(valid), func(t *testing.T) {
			store := &snapshotJobStore{}
			jobs := newJobRegistry(store)
			job := jobs.create("docker-image-pull", "alpine:latest")
			jobs.update(job.ID, "running", "running", "", 0)
			reference := "nginx:latest"
			status := "interrupted"
			artifact := jobArtifactUnknown
			code := "JOB_EXECUTION_INTERRUPTED"
			if valid {
				reference = "alpine:latest"
				status = "completed"
				artifact = jobArtifactPresent
				code = ""
			}
			agent := &pullReceiptAgent{result: docker.ImagePullResult{Reference: reference, Completed: true}}
			api := &handler{jobs: jobs, dockerImages: &pullProgressReceiptAgent{pullReceiptAgent: agent, afterProgress: func() { store.updateErr = errors.New("storage unavailable") }}}
			api.runImagePull(context.Background(), job.ID, docker.ImagePullRequest{Reference: "alpine:latest"})
			current, _ := jobs.get(job.ID)
			if current.Status != "running" || current.PersistenceStatus != "pending" || current.ArtifactState != jobArtifactUnknown || current.CompletedAt != nil {
				t.Fatalf("unsaved result published: %+v", current)
			}
			jobs.update(job.ID, "completed", "late success", "", 100)
			store.updateErr = nil
			jobs.flush()
			jobs.flush()
			current, _ = jobs.get(job.ID)
			durable, _ := store.Job(context.Background(), job.ID)
			if current.Status != status || current.ArtifactState != artifact || current.ErrorCode != code || current.PersistenceStatus != "" || durable.Status != status || agent.calls != 1 {
				t.Fatalf("pending result lost or pull repeated: %+v durable=%+v calls=%d", current, durable, agent.calls)
			}
		})
	}
}

func TestImagePullReceiptJobCancellationAndTimeout(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint(deadline), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			code := "JOB_EXECUTION_INTERRUPTED"
			if deadline {
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				code = "JOB_EXECUTION_TIMEOUT"
			}
			defer cancel()
			cancel()
			store := &snapshotJobStore{}
			jobs := newJobRegistry(store)
			job := jobs.create("docker-image-pull", "alpine:latest")
			jobs.update(job.ID, "running", "running", "", 0)
			_, _, err := jobs.cancel(job.ID)
			if err != nil {
				t.Fatal(err)
			}
			agent := &pullReceiptAgent{err: ctx.Err()}
			api := &handler{jobs: jobs, dockerImages: &pullProgressReceiptAgent{pullReceiptAgent: agent}}
			api.runImagePull(ctx, job.ID, docker.ImagePullRequest{Reference: "alpine:latest"})
			current, _ := jobs.get(job.ID)
			if current.Status != "interrupted" || current.ErrorCode != code || current.ArtifactState != jobArtifactUnknown || current.CancellationStatus != "unknown" || current.DownloadedBytes != 3 || agent.calls != 1 {
				t.Fatalf("classification lost: %+v calls=%d", current, agent.calls)
			}
		})
	}
}

func TestImagePullReceiptJobDoesNotReplaceTerminal(t *testing.T) {
	for _, status := range []string{"completed", "interrupted", "cancelled", "failed"} {
		t.Run(status, func(t *testing.T) {
			store := &snapshotJobStore{}
			jobs := newJobRegistry(store)
			job := jobs.create("docker-image-pull", "alpine:latest")
			agent := &pullReceiptAgent{result: docker.ImagePullResult{Reference: "nginx:latest", Completed: true}}
			api := &handler{jobs: jobs, dockerImages: &pullProgressReceiptAgent{pullReceiptAgent: agent, afterProgress: func() { jobs.update(job.ID, status, "first terminal", "", 0) }}}
			api.runImagePull(context.Background(), job.ID, docker.ImagePullRequest{Reference: "alpine:latest"})
			current, _ := jobs.get(job.ID)
			durable, _ := store.Job(context.Background(), job.ID)
			if current.Status != status || current.Message != "first terminal" || current.ErrorCode != "" || durable.Status != status || agent.calls != 1 {
				t.Fatalf("terminal overwritten: %+v durable=%+v calls=%d", current, durable, agent.calls)
			}
		})
	}
}
