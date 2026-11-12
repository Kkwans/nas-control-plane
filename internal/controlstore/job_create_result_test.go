package controlstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func creationResultFixture() string {
	return `{"containerId":"` + strings.Repeat("ab", 32) + `","created":true,"started":false}`
}

func TestParseContainerCreateJobResult(t *testing.T) {
	valid := creationResultFixture()
	job := jobFixture("creation")
	job.Type, job.Status, job.CreateResultJSON = "docker-container-create", "completed", valid
	result, err := ParseContainerCreateJobResult(job)
	if err != nil || result == nil || !result.Created || result.Started || result.ContainerID != strings.Repeat("ab", 32) {
		t.Fatalf("valid: %#v %v", result, err)
	}
	cases := map[string]string{
		"missing": "", "null": "null", "array": "[]", "extra": strings.Replace(valid, `}`, `,"secret":"no"}`, 1),
		"duplicate":       strings.Replace(valid, `}`, `,"created":true}`, 1),
		"case":            strings.Replace(valid, "containerId", "ContainerId", 1),
		"missing_started": strings.Replace(valid, `,"started":false`, "", 1),
		"false_created":   strings.Replace(valid, `"created":true`, `"created":false`, 1),
		"null_started":    strings.Replace(valid, `"started":false`, `"started":null`, 1),
		"wrong_type":      strings.Replace(valid, `"started":false`, `"started":"false"`, 1),
		"uppercase_id":    strings.Replace(valid, strings.Repeat("ab", 32), strings.Repeat("AB", 32), 1),
		"short_id":        strings.Replace(valid, strings.Repeat("ab", 32), "ab", 1),
		"nonhex_id":       strings.Replace(valid, strings.Repeat("ab", 32), strings.Repeat("z", 64), 1),
		"trailing":        valid + "{}", "trailing_junk": valid + "!", "oversize": valid + strings.Repeat(" ", 257),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			job.CreateResultJSON = raw
			if got, err := ParseContainerCreateJobResult(job); got != nil || !errors.Is(err, ErrJobCreateResultInvalid) {
				t.Fatalf("accepted invalid result: %#v %v", got, err)
			}
		})
	}
	job.CreateResultJSON = valid
	for _, state := range []struct{ kind, status string }{{"docker-image-pull", "completed"}, {"docker-container-create", "running"}, {"docker-container-create", "failed"}, {"docker-container-create", "interrupted"}} {
		job.Type, job.Status = state.kind, state.status
		if got, err := ParseContainerCreateJobResult(job); got != nil || err == nil {
			t.Fatalf("accepted ineligible result: %#v %v", got, err)
		}
	}
}

func TestContainerCreateResultStorage(t *testing.T) {
	store, _ := openJobTestStore(t)
	ctx := context.Background()
	job := jobFixture("creation")
	job.Type = "docker-container-create"
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	job.Status, job.CreateResultJSON = "completed", creationResultFixture()
	if _, err := store.UpdateJob(ctx, job, 1); err != nil {
		t.Fatal(err)
	}
	got := requireJob(t, store, job.ID)
	if got.CreateResultJSON != job.CreateResultJSON {
		t.Fatal("result did not roundtrip")
	}
	jobs, err := store.Jobs(ctx, job.Type, 1)
	if err != nil || len(jobs) != 1 || jobs[0].CreateResultJSON != job.CreateResultJSON {
		t.Fatalf("list: %#v %v", jobs, err)
	}
	raw, err := json.Marshal(got)
	if err != nil || strings.Contains(string(raw), "containerId") || strings.Contains(string(raw), "createResult") {
		t.Fatalf("raw result exposed: %s %v", raw, err)
	}
	page, err := store.JobHistory(ctx, JobHistoryQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(page)
	if strings.Contains(string(raw), "containerId") || strings.Contains(string(raw), "createResult") {
		t.Fatalf("history result exposed: %s", raw)
	}
	for i := 0; i < 2; i++ {
		if err := store.migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if requireJob(t, store, job.ID).CreateResultJSON != job.CreateResultJSON {
		t.Fatal("migration lost result")
	}
	job.ID = "direct"
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if requireJob(t, store, job.ID).CreateResultJSON != job.CreateResultJSON {
		t.Fatal("create result lost")
	}
	job.ID = "invalid"
	job.CreateResultJSON = "{}"
	if err := store.CreateJob(ctx, job); !errors.Is(err, ErrJobCreateResultInvalid) {
		t.Fatalf("invalid create: %v", err)
	}
	job.ID = "creation"
	if _, err := store.UpdateJob(ctx, job, 2); !errors.Is(err, ErrJobCreateResultInvalid) {
		t.Fatalf("invalid update: %v", err)
	}
	if requireJob(t, store, job.ID).Revision != 2 {
		t.Fatal("invalid update mutated state")
	}
}

func TestContainerCreateResultCorruptProjectionAndRecovery(t *testing.T) {
	store, _ := openJobTestStore(t)
	ctx := context.Background()
	job := jobFixture("creation")
	job.Type, job.Status, job.CreateResultJSON = "docker-container-create", "completed", creationResultFixture()
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []any{strings.Repeat("x", 1<<20), []byte(creationResultFixture()), "{}"} {
		if _, err := store.database.Exec(`UPDATE jobs SET create_result_json = ? WHERE id = ?`, payload, job.ID); err != nil {
			t.Fatal(err)
		}
		got := requireJob(t, store, job.ID)
		if len(got.CreateResultJSON) > 256 || got.CreateResultJSON == "" {
			t.Fatal("corruption not bounded/marked")
		}
		if result, err := ParseContainerCreateJobResult(got); result != nil || err == nil {
			t.Fatal("corruption became usable")
		}
		jobs, err := store.Jobs(ctx, "", 1)
		if err != nil || len(jobs) != 1 || len(jobs[0].CreateResultJSON) > 256 {
			t.Fatalf("unbounded list: %v", err)
		}
		if _, err := store.JobHistory(ctx, JobHistoryQuery{Limit: 1}); err != nil {
			t.Fatalf("summary read result column: %v", err)
		}
	}
	for _, status := range []string{"queued", "running"} {
		if _, err := store.database.Exec(`UPDATE jobs SET status = ?, create_result_json = ? WHERE id = ?`, status, creationResultFixture(), job.ID); err != nil {
			t.Fatal(err)
		}
		if err := store.RecoverInterruptedJobs(ctx); err != nil {
			t.Fatal(err)
		}
		got := requireJob(t, store, job.ID)
		if got.Status != "interrupted" || got.CreateResultJSON != "" {
			t.Fatalf("recovery retained result: %#v", got)
		}
	}
}

func TestContainerCreateResultWritesUseStoredType(t *testing.T) {
	store, _ := openJobTestStore(t)
	ctx := context.Background()
	job := jobFixture("creation")
	job.Type = "docker-container-create"
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	job.Type, job.Status = "docker-image-pull", "completed"
	if _, err := store.UpdateJob(ctx, job, 1); !errors.Is(err, ErrJobCreateResultInvalid) {
		t.Fatalf("changed type bypassed required result: %v", err)
	}
	if err := store.UpsertJob(ctx, job); !errors.Is(err, ErrJobCreateResultInvalid) {
		t.Fatalf("legacy changed type bypassed required result: %v", err)
	}
	other := jobFixture("pull")
	if err := store.CreateJob(ctx, other); err != nil {
		t.Fatal(err)
	}
	other.Type, other.Status, other.CreateResultJSON = "docker-container-create", "completed", creationResultFixture()
	if _, err := store.UpdateJob(ctx, other, 1); !errors.Is(err, ErrJobCreateResultInvalid) {
		t.Fatalf("changed type accepted foreign result: %v", err)
	}
	if err := store.UpsertJob(ctx, other); !errors.Is(err, ErrJobCreateResultInvalid) {
		t.Fatalf("legacy changed type accepted foreign result: %v", err)
	}
	if requireJob(t, store, job.ID).Revision != 1 || requireJob(t, store, other.ID).Revision != 1 {
		t.Fatal("invalid write mutated revision")
	}
}
