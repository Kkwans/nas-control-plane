package controlstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func projectActionResultFixture(action, status string) string {
	state, opposite := "running", "stopped"
	if action == "stop" {
		state, opposite = opposite, state
	}
	result := ProjectActionJobResult{ProjectID: "standalone", Kind: "standalone", Action: action,
		Items: []ProjectActionJobItem{{ContainerID: strings.Repeat("ab", 32), Outcome: "succeeded", State: state}}}
	if status == "failed" || status == "interrupted" {
		result.Items = append(result.Items, ProjectActionJobItem{ContainerID: strings.Repeat("cd", 32), Outcome: "state-mismatch", State: opposite, ErrorCode: "DOCKER_CONTAINER_STATE_MISMATCH"})
	}
	if status == "interrupted" {
		result.Items = append(result.Items, ProjectActionJobItem{ContainerID: strings.Repeat("ef", 32), Outcome: "unknown", State: "unknown", ErrorCode: "JOB_EXECUTION_INTERRUPTED"})
	}
	raw, _ := json.Marshal(result)
	return string(raw)
}

func projectActionJobFixture(id, action, status string) JobRecord {
	job := jobFixture(id)
	job.Type, job.Reference, job.Status = "docker-project-"+action, "standalone", status
	if status == "completed" || status == "failed" || status == "interrupted" {
		job.ProjectActionResultJSON = projectActionResultFixture(action, status)
	}
	return job
}

func TestParseProjectActionJobResult(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart"} {
		for _, status := range []string{"completed", "failed", "interrupted"} {
			t.Run(action+"/"+status, func(t *testing.T) {
				job := projectActionJobFixture("project", action, status)
				result, err := ParseProjectActionJobResult(job)
				if err != nil || result == nil || result.Action != action || result.ProjectID != "standalone" || result.Kind != "standalone" {
					t.Fatalf("valid result: %#v %v", result, err)
				}
			})
		}
	}
	job := projectActionJobFixture("project", "start", "completed")
	valid := job.ProjectActionResultJSON
	cases := map[string]string{
		"missing": "", "null": "null", "array": "[]", "empty_object": "{}", "malformed": "{",
		"missing_kind":      strings.Replace(valid, `"kind":"standalone",`, "", 1),
		"missing_items":     `{"projectId":"standalone","kind":"standalone","action":"start"}`,
		"old_containers":    strings.Replace(valid, `"items":`, `"containers":`, 1),
		"empty_items":       `{"projectId":"standalone","kind":"standalone","action":"start","items":[]}`,
		"null_items":        `{"projectId":"standalone","kind":"standalone","action":"start","items":null}`,
		"null_item":         `{"projectId":"standalone","kind":"standalone","action":"start","items":[null]}`,
		"wrong_items_type":  strings.Replace(valid, `"items":[`, `"items":{"items":[`, 1),
		"wrong_metadata":    strings.Replace(valid, `"projectId":"standalone"`, `"projectId":"other"`, 1),
		"wrong_kind":        strings.Replace(valid, `"kind":"standalone"`, `"kind":"compose"`, 1),
		"wrong_action":      strings.Replace(valid, `"action":"start"`, `"action":"restart"`, 1),
		"wrong_case":        strings.Replace(valid, `"projectId":`, `"ProjectId":`, 1),
		"wrong_type":        strings.Replace(valid, `"kind":"standalone"`, `"kind":true`, 1),
		"null_field":        strings.Replace(valid, `"kind":"standalone"`, `"kind":null`, 1),
		"extra":             strings.Replace(valid, `"kind":`, `"secret":"no","kind":`, 1),
		"duplicate":         strings.Replace(valid, `"kind":`, `"kind":"standalone","kind":`, 1),
		"escaped_duplicate": strings.Replace(valid, `"kind":`, `"\u006bind":"standalone","kind":`, 1),
		"nested_duplicate":  strings.Replace(valid, `"outcome":`, `"outcome":"succeeded","outcome":`, 1),
		"nested_extra":      strings.Replace(valid, `"outcome":`, `"secret":"no","outcome":`, 1),
		"nested_missing":    strings.Replace(valid, `,"state":"running"`, "", 1),
		"nested_null":       strings.Replace(valid, `"state":"running"`, `"state":null`, 1),
		"nested_wrong_type": strings.Replace(valid, `"state":"running"`, `"state":[]`, 1),
		"nested_null_code":  strings.Replace(valid, `"state":"running"`, `"state":"running","errorCode":null`, 1),
		"short_id":          strings.Replace(valid, strings.Repeat("ab", 32), "ab", 1),
		"uppercase_id":      strings.Replace(valid, strings.Repeat("ab", 32), strings.Repeat("AB", 32), 1),
		"nonhex_id":         strings.Replace(valid, strings.Repeat("ab", 32), strings.Repeat("z", 64), 1),
		"old_outcome":       strings.Replace(valid, `"succeeded"`, `"completed"`, 1),
		"wrong_state":       strings.Replace(valid, `"running"`, `"stopped"`, 1),
		"unknown_state":     strings.Replace(valid, `"running"`, `"unknown"`, 1),
		"success_code":      strings.Replace(valid, `"state":"running"`, `"state":"running","errorCode":"PRIVATE"`, 1),
		"failed":            projectActionResultFixture("start", "failed"),
		"interrupted":       projectActionResultFixture("start", "interrupted"),
		"trailing":          valid + "{}", "trailing_junk": valid + "!",
		"oversize":     valid + strings.Repeat(" ", maxProjectActionResultBytes-len(valid)+1),
		"invalid_utf8": valid[:len(valid)-1] + string([]byte{0xff}) + "}",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			job.ProjectActionResultJSON = raw
			if got, err := ParseProjectActionJobResult(job); got != nil || !errors.Is(err, ErrJobProjectActionResultInvalid) {
				t.Fatalf("accepted invalid result: %#v %v", got, err)
			}
		})
	}
	job.ProjectActionResultJSON = valid + strings.Repeat(" ", maxProjectActionResultBytes-len(valid))
	if got, err := ParseProjectActionJobResult(job); err != nil || got == nil {
		t.Fatalf("exact byte limit rejected: %#v %v", got, err)
	}
}

func TestProjectActionJobResultOutcomeRules(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart"} {
		desired, opposite := "running", "stopped"
		if action == "stop" {
			desired, opposite = opposite, desired
		}
		for _, status := range []string{"failed", "interrupted"} {
			job := projectActionJobFixture("project", action, status)
			valid := job.ProjectActionResultJSON
			cases := map[string]string{
				"all_succeeded":          projectActionResultFixture(action, "completed"),
				"mismatch_unknown_state": strings.Replace(valid, `"state-mismatch","state":"`+opposite+`"`, `"state-mismatch","state":"unknown"`, 1),
				"mismatch_wrong_code":    strings.Replace(valid, "DOCKER_CONTAINER_STATE_MISMATCH", "SECRET", 1),
				"mismatch_desired_state": strings.Replace(valid, `"state-mismatch","state":"`+opposite+`"`, `"state-mismatch","state":"`+desired+`"`, 1),
				"duplicate_ids":          strings.Replace(valid, strings.Repeat("cd", 32), strings.Repeat("ab", 32), 1),
			}
			if status == "failed" {
				cases["unknown"] = projectActionResultFixture(action, "interrupted")
			} else {
				cases["no_unknown"] = projectActionResultFixture(action, "failed")
				cases["unknown_known_state"] = strings.Replace(valid, `"outcome":"unknown","state":"unknown"`, `"outcome":"unknown","state":"running"`, 1)
				cases["unknown_wrong_code"] = strings.Replace(valid, "JOB_EXECUTION_INTERRUPTED", "JOB_EXECUTION_TIMEOUT", 1)
			}
			for name, raw := range cases {
				t.Run(action+"/"+status+"/"+name, func(t *testing.T) {
					job.ProjectActionResultJSON = raw
					if got, err := ParseProjectActionJobResult(job); got != nil || !errors.Is(err, ErrJobProjectActionResultInvalid) {
						t.Fatalf("invalid outcome accepted: %#v %v", got, err)
					}
				})
			}
		}
	}
}

func TestProjectActionJobResultEligibilityAndBounds(t *testing.T) {
	for _, status := range []string{"queued", "running", "cancelled", "failed", "interrupted", "unknown"} {
		job := projectActionJobFixture("project", "start", status)
		job.ProjectActionResultJSON = ""
		if got, err := ParseProjectActionJobResult(job); err != nil || got != nil {
			t.Fatalf("absent noncompleted result %s: %#v %v", status, got, err)
		}
	}
	for _, change := range []func(*JobRecord){
		func(job *JobRecord) { job.Type = "docker-image-pull" },
		func(job *JobRecord) { job.Type = "docker-project-remove" },
		func(job *JobRecord) { job.Reference = "other" },
		func(job *JobRecord) { job.Reference = " standalone" },
		func(job *JobRecord) { job.Status = "queued" },
		func(job *JobRecord) { job.Status = "running" },
		func(job *JobRecord) { job.Status = "cancelled" },
	} {
		job := projectActionJobFixture("project", "start", "completed")
		change(&job)
		if got, err := ParseProjectActionJobResult(job); got != nil || !errors.Is(err, ErrJobProjectActionResultInvalid) {
			t.Fatalf("ineligible result accepted: %#v %v", got, err)
		}
	}
	result := ProjectActionJobResult{ProjectID: "standalone", Kind: "standalone", Action: "start"}
	for index := 0; index < 129; index++ {
		result.Items = append(result.Items, ProjectActionJobItem{ContainerID: fmt.Sprintf("%064x", index), Outcome: "succeeded", State: "running"})
		if index < 127 {
			continue
		}
		raw, _ := json.Marshal(result)
		job := projectActionJobFixture("project", "start", "completed")
		job.ProjectActionResultJSON = string(raw)
		got, err := ParseProjectActionJobResult(job)
		if index == 127 && (err != nil || got == nil || len(got.Items) != 128) {
			t.Fatalf("128 items rejected: %#v %v", got, err)
		}
		if index == 128 && (got != nil || !errors.Is(err, ErrJobProjectActionResultInvalid)) {
			t.Fatalf("129 items accepted: %#v %v", got, err)
		}
	}
}

func TestProjectActionResultStorageAndMigration(t *testing.T) {
	store, path := openJobTestStore(t)
	ctx := context.Background()
	for _, status := range []string{"completed", "failed", "interrupted"} {
		job := projectActionJobFixture(status, "start", "queued")
		if err := store.CreateJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		job.Status, job.ProjectActionResultJSON = status, projectActionResultFixture("start", status)
		if _, err := store.UpdateJob(ctx, job, 1); err != nil {
			t.Fatal(err)
		}
		got := requireJob(t, store, job.ID)
		if got.ProjectActionResultJSON != job.ProjectActionResultJSON || got.Revision != 2 {
			t.Fatal("update did not roundtrip result/revision")
		}
		if _, err := store.UpdateJob(ctx, job, 1); !errors.Is(err, ErrJobConflict) {
			t.Fatalf("stale result write: %v", err)
		}
		if err := store.UpsertJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		if got := requireJob(t, store, job.ID); got.ProjectActionResultJSON != job.ProjectActionResultJSON || got.Revision != 3 {
			t.Fatal("upsert did not roundtrip result/revision")
		}
		job.ID = "direct-" + status
		if err := store.CreateJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		job.ID = "legacy-" + status
		if err := store.UpsertJob(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := store.migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := store.Jobs(ctx, "docker-project-start", 20)
	if err != nil || len(jobs) != 9 {
		t.Fatalf("list results: %#v %v", jobs, err)
	}
	for _, job := range jobs {
		if job.ProjectActionResultJSON != projectActionResultFixture("start", job.Status) {
			t.Fatal("migration/list lost result")
		}
		raw, _ := json.Marshal(job)
		if strings.Contains(string(raw), "containerId") || strings.Contains(string(raw), "projectActionResult") || strings.Contains(string(raw), "succeeded") {
			t.Fatalf("raw result exposed: %s", raw)
		}
	}
	page, err := store.JobHistory(ctx, JobHistoryQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "containerId") || strings.Contains(string(raw), "projectActionResult") || strings.Contains(string(raw), "succeeded") {
		t.Fatalf("history result exposed: %s", raw)
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
			t.Fatalf("reopen changed result: %#v", got)
		}
	}
}

func TestProjectActionResultCorruptProjectionAndRecovery(t *testing.T) {
	store, _ := openJobTestStore(t)
	ctx := context.Background()
	job := projectActionJobFixture("project", "start", "completed")
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []any{strings.Repeat("x", 1<<20), []byte(job.ProjectActionResultJSON), "{}", ""} {
		if _, err := store.database.Exec(`UPDATE jobs SET project_action_result_json = ? WHERE id = ?`, payload, job.ID); err != nil {
			t.Fatal(err)
		}
		got := requireJob(t, store, job.ID)
		if len(got.ProjectActionResultJSON) > maxProjectActionResultBytes {
			t.Fatal("corruption projection was unbounded")
		}
		if result, err := ParseProjectActionJobResult(got); result != nil || !errors.Is(err, ErrJobProjectActionResultInvalid) {
			t.Fatal("corruption became usable success")
		}
		jobs, err := store.Jobs(ctx, "", 1)
		if err != nil || len(jobs) != 1 || len(jobs[0].ProjectActionResultJSON) > maxProjectActionResultBytes {
			t.Fatalf("unbounded list: %v", err)
		}
		if result, err := ParseProjectActionJobResult(jobs[0]); result != nil || !errors.Is(err, ErrJobProjectActionResultInvalid) {
			t.Fatal("corrupt list result became usable")
		}
		if _, err := store.JobHistory(ctx, JobHistoryQuery{Limit: 1}); err != nil {
			t.Fatalf("summary read result column: %v", err)
		}
	}
	for _, status := range []string{"queued", "running"} {
		if _, err := store.database.Exec(`UPDATE jobs SET status = ?, project_action_result_json = ? WHERE id = ?`, status, job.ProjectActionResultJSON, job.ID); err != nil {
			t.Fatal(err)
		}
		if err := store.RecoverInterruptedJobs(ctx); err != nil {
			t.Fatal(err)
		}
		got := requireJob(t, store, job.ID)
		if got.Status != "interrupted" || got.ProjectActionResultJSON != "" {
			t.Fatalf("recovery retained result: %#v", got)
		}
	}
}

func TestProjectActionResultWritesUseStoredMetadata(t *testing.T) {
	ctx := context.Background()
	for _, method := range []string{"update", "upsert"} {
		for _, mismatch := range []string{"missing", "wrong_type", "wrong_action", "wrong_reference", "malformed"} {
			t.Run(method+"/"+mismatch, func(t *testing.T) {
				store, _ := openJobTestStore(t)
				job := projectActionJobFixture("project", "start", "queued")
				if mismatch == "wrong_type" {
					job.Type = "docker-image-pull"
				}
				if mismatch == "wrong_reference" {
					job.Reference = "other"
				}
				if err := store.CreateJob(ctx, job); err != nil {
					t.Fatal(err)
				}
				original := requireJob(t, store, job.ID)
				job.Type, job.Reference, job.Status = "docker-project-start", "standalone", "completed"
				job.ProjectActionResultJSON = projectActionResultFixture("start", "completed")
				switch mismatch {
				case "missing":
					job.Type, job.ProjectActionResultJSON = "docker-image-pull", ""
				case "wrong_action":
					job.Type, job.ProjectActionResultJSON = "docker-project-stop", projectActionResultFixture("stop", "completed")
				case "malformed":
					job.ProjectActionResultJSON = "{}"
				}
				var err error
				if method == "update" {
					_, err = store.UpdateJob(ctx, job, 1)
				} else {
					err = store.UpsertJob(ctx, job)
				}
				if !errors.Is(err, ErrJobProjectActionResultInvalid) {
					t.Fatalf("invalid write accepted: %v", err)
				}
				if got := requireJob(t, store, job.ID); !reflect.DeepEqual(got, original) {
					t.Fatalf("invalid write mutated state: %#v", got)
				}
			})
		}
	}
	store, _ := openJobTestStore(t)
	for _, method := range []string{"create", "upsert"} {
		job := projectActionJobFixture(method, "start", "completed")
		job.ProjectActionResultJSON = "{}"
		var err error
		if method == "create" {
			err = store.CreateJob(ctx, job)
		} else {
			err = store.UpsertJob(ctx, job)
		}
		if !errors.Is(err, ErrJobProjectActionResultInvalid) {
			t.Fatalf("invalid %s accepted: %v", method, err)
		}
		if _, err := store.Job(ctx, job.ID); !errors.Is(err, ErrJobNotFound) {
			t.Fatalf("invalid %s persisted: %v", method, err)
		}
	}
}
