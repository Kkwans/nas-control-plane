package controlstore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func composeActionJobFixture(id, action, status string) JobRecord {
	job := jobFixture(id)
	job.Type, job.Reference, job.Status = "compose-"+action, "项目-one", status
	if status == "completed" {
		state := "running"
		if action == "stop" {
			state = "stopped"
		}
		raw, _ := json.Marshal(ComposeActionJobResult{ProjectID: job.Reference, Action: action, State: state, ServiceCount: 2})
		job.ComposeActionResultJSON = string(raw)
	}
	return job
}

func TestParseComposeActionJobResult(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart"} {
		job := composeActionJobFixture("job", action, "completed")
		got, err := ParseComposeActionJobResult(job)
		if err != nil || got == nil || got.Action != action || got.ServiceCount != 2 {
			t.Fatalf("valid result: %#v %v", got, err)
		}
	}
	job := composeActionJobFixture("job", "start", "completed")
	valid := job.ComposeActionResultJSON
	cases := map[string]string{
		"missing": "", "null": "null", "array": "[]", "empty": "{}", "malformed": "{",
		"unknown":           strings.Replace(valid, `"action":`, `"secret":true,"action":`, 1),
		"duplicate":         strings.Replace(valid, `"action":`, `"action":"start","action":`, 1),
		"escaped_duplicate": strings.Replace(valid, `"action":`, `"\u0061ction":"start","action":`, 1),
		"missing_key":       strings.Replace(valid, `,"state":"running"`, "", 1),
		"null_value":        strings.Replace(valid, `"state":"running"`, `"state":null`, 1),
		"wrong_string_type": strings.Replace(valid, `"state":"running"`, `"state":true`, 1),
		"wrong_count_type":  strings.Replace(valid, `"serviceCount":2`, `"serviceCount":"2"`, 1),
		"null_count":        strings.Replace(valid, `"serviceCount":2`, `"serviceCount":null`, 1),
		"zero_count":        strings.Replace(valid, `"serviceCount":2`, `"serviceCount":0`, 1),
		"large_count":       strings.Replace(valid, `"serviceCount":2`, `"serviceCount":1025`, 1),
		"fraction_count":    strings.Replace(valid, `"serviceCount":2`, `"serviceCount":2.1`, 1),
		"overflow_count":    strings.Replace(valid, `"serviceCount":2`, `"serviceCount":9223372036854775808`, 1),
		"wrong_action":      strings.Replace(valid, `"action":"start"`, `"action":"stop"`, 1),
		"wrong_state":       strings.Replace(valid, `"state":"running"`, `"state":"stopped"`, 1),
		"trailing":          valid + "{}", "junk": valid + "!",
		"oversized":    valid + strings.Repeat(" ", maxComposeActionResultBytes-len(valid)+1),
		"invalid_utf8": strings.Replace(valid, "项目", string([]byte{0xff}), 1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			job.ComposeActionResultJSON = raw
			if got, err := ParseComposeActionJobResult(job); got != nil || !errors.Is(err, ErrJobComposeActionResultInvalid) {
				t.Fatalf("invalid result accepted: %#v %v", got, err)
			}
		})
	}
	job.ComposeActionResultJSON = valid + strings.Repeat(" ", maxComposeActionResultBytes-len(valid))
	if got, err := ParseComposeActionJobResult(job); got == nil || err != nil {
		t.Fatalf("boundary rejected: %#v %v", got, err)
	}
	for _, count := range []int{1, 1024} {
		result := ComposeActionJobResult{ProjectID: strings.Repeat("界", 85) + "a", Action: "start", State: "running", ServiceCount: count}
		raw, _ := json.Marshal(result)
		job.Reference = result.ProjectID
		job.ComposeActionResultJSON = string(raw)
		if got, err := ParseComposeActionJobResult(job); got == nil || err != nil {
			t.Fatalf("valid bounds: %#v %v", got, err)
		}
	}
	for _, id := range []string{"", " spaced", "spaced ", "a\nb", strings.Repeat("界", 86)} {
		result := ComposeActionJobResult{ProjectID: id, Action: "start", State: "running", ServiceCount: 1}
		raw, _ := json.Marshal(result)
		job.Reference = id
		job.ComposeActionResultJSON = string(raw)
		if got, err := ParseComposeActionJobResult(job); got != nil || err == nil {
			t.Fatalf("invalid ID accepted: %q", id)
		}
	}
}

func TestComposeActionResultEligibilityAndContamination(t *testing.T) {
	for _, status := range []string{"queued", "running", "failed", "interrupted", "cancelled", "unknown"} {
		job := composeActionJobFixture("job", "start", "completed")
		job.Status = status
		if got, err := ParseComposeActionJobResult(job); got != nil || err == nil {
			t.Fatalf("accepted status %s", status)
		}
		job.ComposeActionResultJSON = ""
		if got, err := ParseComposeActionJobResult(job); got != nil || err != nil {
			t.Fatalf("absent result %s: %v", status, err)
		}
	}
	for _, change := range []func(*JobRecord){
		func(j *JobRecord) { j.Type = "docker-image-pull" },
		func(j *JobRecord) { j.Type = "compose-project-start" },
		func(j *JobRecord) { j.Type = "compose-deploy" },
		func(j *JobRecord) { j.Reference = "other" },
		func(j *JobRecord) { j.CreateResultJSON = "{}" },
		func(j *JobRecord) { j.ProjectActionResultJSON = "{}" },
	} {
		job := composeActionJobFixture("job", "start", "completed")
		change(&job)
		if got, err := ParseComposeActionJobResult(job); got != nil || err == nil {
			t.Fatal("accepted incompatible result")
		}
	}
	project := projectActionJobFixture("project", "start", "completed")
	project.ComposeActionResultJSON = "{}"
	if got, err := ParseProjectActionJobResult(project); got != nil || err == nil {
		t.Fatal("project contamination accepted")
	}
	create := jobFixture("create")
	create.Type = "docker-container-create"
	create.Status = "completed"
	create.CreateResultJSON = `{"containerId":"` + strings.Repeat("a", 64) + `","created":true,"started":false}`
	create.ComposeActionResultJSON = "{}"
	if got, err := ParseContainerCreateJobResult(create); got != nil || err == nil {
		t.Fatal("create contamination accepted")
	}
}

func TestComposeActionResultStorageAndMigration(t *testing.T) {
	store, path := openJobTestStore(t)
	ctx := context.Background()
	for _, action := range []string{"start", "stop", "restart"} {
		job := composeActionJobFixture(action, action, "queued")
		if err := store.CreateJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		job = composeActionJobFixture(action, action, "completed")
		if _, err := store.UpdateJob(ctx, job, 1); err != nil {
			t.Fatal(err)
		}
		if got := requireJob(t, store, job.ID); got.ComposeActionResultJSON != job.ComposeActionResultJSON || got.Revision != 2 {
			t.Fatal("CAS roundtrip failed")
		}
		if _, err := store.UpdateJob(ctx, job, 1); !errors.Is(err, ErrJobConflict) {
			t.Fatalf("stale CAS: %v", err)
		}
		if err := store.UpsertJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		if got := requireJob(t, store, job.ID); got.Revision != 3 || got.ComposeActionResultJSON != job.ComposeActionResultJSON {
			t.Fatal("upsert roundtrip failed")
		}
		job.ID = "direct-" + action
		if err := store.CreateJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		job.ID = "upsert-" + action
		if err := store.UpsertJob(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := store.migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := store.Jobs(ctx, "", 20)
	if err != nil || len(jobs) != 9 {
		t.Fatalf("list: %v %d", err, len(jobs))
	}
	for _, job := range jobs {
		if got, err := ParseComposeActionJobResult(job); got == nil || err != nil {
			t.Fatalf("lost result: %v", err)
		}
		raw, _ := json.Marshal(job)
		if strings.Contains(string(raw), "serviceCount") {
			t.Fatal("raw JSON exposed")
		}
	}
	page, err := store.JobHistory(ctx, JobHistoryQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "serviceCount") {
		t.Fatal("history exposed result")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for _, job := range jobs {
		if got := requireJob(t, reopened, job.ID); !reflect.DeepEqual(got, job) {
			t.Fatal("reopen changed result")
		}
	}
}

func TestComposeActionResultCorruptProjectionAndRecovery(t *testing.T) {
	store, _ := openJobTestStore(t)
	ctx := context.Background()
	job := composeActionJobFixture("job", "start", "completed")
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []any{strings.Repeat("x", 1<<20), []byte(job.ComposeActionResultJSON), "{}", ""} {
		if _, err := store.database.Exec("UPDATE jobs SET compose_action_result_json = ? WHERE id = ?", payload, job.ID); err != nil {
			t.Fatal(err)
		}
		got := requireJob(t, store, job.ID)
		if len(got.ComposeActionResultJSON) > maxComposeActionResultBytes {
			t.Fatal("unbounded detail")
		}
		if result, err := ParseComposeActionJobResult(got); result != nil || err == nil {
			t.Fatal("corrupt success")
		}
		jobs, err := store.Jobs(ctx, "", 1)
		if err != nil || len(jobs) != 1 || len(jobs[0].ComposeActionResultJSON) > maxComposeActionResultBytes {
			t.Fatal("unbounded list")
		}
		if result, err := ParseComposeActionJobResult(jobs[0]); result != nil || err == nil {
			t.Fatal("corrupt list success")
		}
		if _, err := store.JobHistory(ctx, JobHistoryQuery{Limit: 1}); err != nil {
			t.Fatalf("summary read result: %v", err)
		}
	}
	for _, status := range []string{"queued", "running"} {
		if _, err := store.database.Exec("UPDATE jobs SET status = ?, compose_action_result_json = ?, create_result_json = '{}', project_action_result_json = '{}' WHERE id = ?", status, job.ComposeActionResultJSON, job.ID); err != nil {
			t.Fatal(err)
		}
		before := requireJob(t, store, job.ID)
		if err := store.RecoverInterruptedJobs(ctx); err != nil {
			t.Fatal(err)
		}
		got := requireJob(t, store, job.ID)
		if got.Status != "interrupted" || got.ComposeActionResultJSON != "" || got.CreateResultJSON != "" || got.ProjectActionResultJSON != "" || got.Revision != before.Revision+1 {
			t.Fatal("recovery retained result")
		}
	}
}

func TestComposeActionResultWritesUseStoredMetadata(t *testing.T) {
	ctx := context.Background()
	for _, method := range []string{"update", "upsert"} {
		for _, mismatch := range []string{"missing", "wrong_type", "wrong_action", "wrong_reference", "malformed", "contamination"} {
			t.Run(method+"/"+mismatch, func(t *testing.T) {
				store, _ := openJobTestStore(t)
				job := composeActionJobFixture("job", "start", "queued")
				if mismatch == "wrong_type" {
					job.Type = "docker-image-pull"
				}
				if mismatch == "wrong_reference" {
					job.Reference = "other"
				}
				if err := store.CreateJob(ctx, job); err != nil {
					t.Fatal(err)
				}
				before := requireJob(t, store, job.ID)
				job = composeActionJobFixture("job", "start", "completed")
				switch mismatch {
				case "missing":
					job.Type = "docker-image-pull"
					job.ComposeActionResultJSON = ""
				case "wrong_action":
					job = composeActionJobFixture("job", "stop", "completed")
				case "malformed":
					job.ComposeActionResultJSON = "{}"
				case "contamination":
					job.ProjectActionResultJSON = "{}"
				}
				var err error
				if method == "update" {
					_, err = store.UpdateJob(ctx, job, 1)
				} else {
					err = store.UpsertJob(ctx, job)
				}
				if err == nil {
					t.Fatal("invalid write accepted")
				}
				if got := requireJob(t, store, job.ID); !reflect.DeepEqual(got, before) {
					t.Fatal("invalid write mutated record")
				}
			})
		}
	}
	store, _ := openJobTestStore(t)
	for _, method := range []string{"create", "upsert"} {
		job := composeActionJobFixture(method, "start", "completed")
		job.ComposeActionResultJSON = "{}"
		var err error
		if method == "create" {
			err = store.CreateJob(ctx, job)
		} else {
			err = store.UpsertJob(ctx, job)
		}
		if !errors.Is(err, ErrJobComposeActionResultInvalid) {
			t.Fatalf("invalid insert: %v", err)
		}
		if _, err := store.Job(ctx, job.ID); !errors.Is(err, ErrJobNotFound) {
			t.Fatal("invalid insert persisted")
		}
	}
}

func TestComposeActionResultWriteFailureIsAtomic(t *testing.T) {
	store, _ := openJobTestStore(t)
	ctx := context.Background()
	job := composeActionJobFixture("job", "start", "queued")
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	original := requireJob(t, store, job.ID)
	if _, err := store.database.Exec(`CREATE TRIGGER reject_compose BEFORE UPDATE ON jobs BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	job = composeActionJobFixture("job", "start", "completed")
	if _, err := store.UpdateJob(ctx, job, 1); err == nil {
		t.Fatal("failed CAS accepted")
	}
	if err := store.UpsertJob(ctx, job); err == nil {
		t.Fatal("failed upsert accepted")
	}
	if got := requireJob(t, store, job.ID); !reflect.DeepEqual(got, original) {
		t.Fatal("partial completion survived failure")
	}
	if _, err := store.database.Exec("DROP TRIGGER reject_compose"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteJobRevision(ctx, job.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateJob(ctx, job, 1); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("late result resurrected job: %v", err)
	}
	if _, err := store.Job(ctx, job.ID); !errors.Is(err, ErrJobNotFound) {
		t.Fatal("deleted job reappeared")
	}
}

func TestComposeActionResultEscapedProjectBound(t *testing.T) {
	store, _ := openJobTestStore(t)
	defer store.Close()
	job := composeActionJobFixture("escaped", "start", "completed")
	job.Reference = strings.Repeat("<", 256)
	raw, _ := json.Marshal(ComposeActionJobResult{ProjectID: job.Reference, Action: "start", State: "running", ServiceCount: 1})
	job.ComposeActionResultJSON = string(raw)
	if len(raw) <= 1024 {
		t.Fatal("fixture must cover escaped expansion")
	}
	if err := store.CreateJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	got := requireJob(t, store, job.ID)
	if result, err := ParseComposeActionJobResult(got); err != nil || result == nil || result.ProjectID != job.Reference {
		t.Fatalf("escaped result lost: %+v %v", result, err)
	}
}
