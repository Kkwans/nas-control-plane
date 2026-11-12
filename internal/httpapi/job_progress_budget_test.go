package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
	"unsafe"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/Kkwans/nas-control-plane/internal/docker"
)

func TestPullProgressBudgetFields(t *testing.T) {
	for _, tc := range []struct {
		name, id, status string
		accept, limited  bool
		want             int
	}{
		{"exact", strings.Repeat("i", 128), strings.Repeat("s", 256), true, false, 256},
		{"unicode boundary", "中", strings.Repeat("a", 254) + "中", true, true, 254},
		{"unicode exact", "中", strings.Repeat("中", 85) + "a", true, false, 256},
		{"invalid status", "ok", "a\xffb", true, true, 1},
		{"invalid id", "a\xff", "good", false, true, 0},
		{"long id", strings.Repeat("i", 129), "good", false, true, 0},
		{"empty id", "", "ok", true, false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newJobRegistry(nil)
			j := r.create("docker-image-pull", "fixture")
			r.updatePullProgress(j.ID, jobLayer{ID: tc.id, Status: tc.status, Current: 1, Total: 2})
			got, _ := r.get(j.ID)
			if got.ProgressLimited != tc.limited || (len(got.Layers) == 1) != tc.accept {
				t.Fatalf("unexpected result %+v", got)
			}
			for _, l := range got.Layers {
				if !utf8.ValidString(l.ID) || !utf8.ValidString(l.Status) || len(l.Status) != tc.want {
					t.Fatalf("bad bounded layer %+v", l)
				}
			}
			if !tc.accept && (got.DownloadedBytes != 0 || got.Message != "任务已进入队列") {
				t.Fatal("rejected frame changed progress")
			}
		})
	}
}

func TestPullProgressBudgetRetainedStringsAndRepeatedIDs(t *testing.T) {
	r := newJobRegistry(nil)
	j := r.create("docker-image-pull", "fixture")
	huge := strings.Repeat("x", 1<<20)
	id := huge[:128]
	status := huge[128:384]
	r.updatePullProgress(j.ID, jobLayer{ID: id, Status: status, Current: 1, Total: 2})
	for i := 0; i < 2048; i++ {
		r.updatePullProgress(j.ID, jobLayer{ID: id, Status: status, Current: int64(i + 1), Total: 3000})
	}
	got, _ := r.get(j.ID)
	l := got.Layers[id]
	if got.ProgressLimited || len(got.Layers) != 1 || l.Current != 2048 {
		t.Fatal("duplicate consumed budget")
	}
	if unsafe.StringData(l.ID) == unsafe.StringData(id) || unsafe.StringData(l.Status) == unsafe.StringData(status) {
		t.Fatal("retained backing buffer")
	}
	for key := range got.Layers {
		if unsafe.StringData(key) == unsafe.StringData(id) {
			t.Fatal("map key retained backing buffer")
		}
	}
	r.updatePullProgress(j.ID, jobLayer{ID: huge, Status: huge})
	got, _ = r.get(j.ID)
	if !got.ProgressLimited || len(got.Layers) != 1 || got.Layers[id].Current != 2048 {
		t.Fatal("giant rejected frame polluted layers")
	}
	r.updatePullProgress(j.ID, jobLayer{ID: "", Status: "empty"})
	r.updatePullProgress(j.ID, jobLayer{ID: "status", Status: "alias"})
	got, _ = r.get(j.ID)
	if len(got.Layers) != 2 || got.Layers["status"].Status != "alias" {
		t.Fatal("empty ID compatibility changed")
	}
}

func TestPullProgressBudgetCumulativeAndJSON(t *testing.T) {
	r := newJobRegistry(nil)
	j := r.create("docker-image-pull", "fixture")
	for i := 0; i < 2048; i++ {
		id := fmt.Sprintf("%03d", i) + strings.Repeat("\x00", 125)
		r.updatePullProgress(j.ID, jobLayer{ID: id, Status: strings.Repeat("\x00", 256), Current: math.MaxInt64, Total: math.MaxInt64})
	}
	got, _ := r.get(j.ID)
	if len(got.Layers) != 64 || !got.ProgressLimited || got.DownloadedBytes != math.MaxInt64 || got.TotalBytes != math.MaxInt64 {
		t.Fatalf("budget/overflow %+v", got)
	}
	reserved := 0
	for id := range got.Layers {
		reserved += 2*len(id) + 256
	}
	if reserved != pullProgressTextBudget {
		t.Fatal(reserved)
	}
	// 最大字段数值与六倍JSON转义一并计入（完整fixture快照比进度子对象更大）。
	got.SpeedBytes = math.MaxInt64
	payload, err := json.Marshal(got)
	if err != nil || len(payload) > pullProgressJSONBudget {
		t.Fatalf("JSON budget bytes=%d err=%v", len(payload), err)
	}
	t.Logf("layers=%d reserved=%d snapshot JSON=%d bytes", len(got.Layers), reserved, len(payload))
	rec := httptest.NewRecorder()
	writeJSON(rec, 200, got)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"progressLimited":true`) || rec.Body.Len() > pullProgressJSONBudget {
		t.Fatal("HTTP snapshot budget")
	}
	first := "000" + strings.Repeat("\x00", 125)
	r.updatePullProgress(j.ID, jobLayer{ID: first, Status: "updated", Current: 7, Total: 8})
	got, _ = r.get(j.ID)
	if got.Layers[first].Status != "updated" || len(got.Layers) != 64 {
		t.Fatal("existing layer blocked")
	}
}

func TestPullProgressBudgetDirtyDuringSave(t *testing.T) {
	s := &snapshotJobStore{}
	r := newJobRegistry(s)
	j := r.create("docker-image-pull", "fixture")
	s.beforeUpdate = func(controlstore.JobRecord) { r.updatePullProgress(j.ID, jobLayer{ID: strings.Repeat("x", 129)}) }
	r.update(j.ID, "running", "running", "", 0)
	got, _ := r.get(j.ID)
	if !got.ProgressLimited {
		t.Fatal("CAS publication rolled back flag")
	}
	s.beforeUpdate = nil
	r.flush()
	saved, _ := s.Job(context.Background(), j.ID)
	if !saved.ProgressLimited {
		t.Fatal("rejected-frame flag not dirtied/persisted")
	}
}

type budgetPullAgent struct {
	*pullReceiptAgent
	after func()
}

func (a *budgetPullAgent) PullDockerImageWithProgress(ctx context.Context, socket string, req docker.ImagePullRequest, cb func(docker.ImagePullProgress)) (docker.ImagePullResult, error) {
	for i := 0; i < 2048; i++ {
		cb(docker.ImagePullProgress{LayerID: fmt.Sprint(i), Status: strings.Repeat("s", 1024), Current: 1, Total: 2})
	}
	if a.after != nil {
		a.after()
	}
	return a.PullDockerImage(ctx, socket, req)
}
func TestPullProgressBudgetOutcomePersistence(t *testing.T) {
	for _, kind := range []string{"completed", "wrong-receipt", "cancelled-context"} {
		t.Run(kind, func(t *testing.T) {
			s := &snapshotJobStore{}
			r := newJobRegistry(s)
			j := r.create("docker-image-pull", "alpine:latest")
			r.update(j.ID, "running", "running", "", 0)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			agent := &budgetPullAgent{pullReceiptAgent: &pullReceiptAgent{result: docker.ImagePullResult{Reference: "alpine:latest", Completed: true}}}
			want := "completed"
			if kind == "wrong-receipt" {
				agent.result.Reference = "nginx:latest"
				want = "interrupted"
			}
			if kind == "cancelled-context" {
				agent.after = func() {
					if _, _, err := r.cancel(j.ID); err != nil {
						t.Fatal(err)
					}
					cancel()
					agent.err = context.Canceled
				}
				want = "interrupted"
			}
			after := agent.after
			agent.after = func() {
				if after != nil {
					after()
				}
				s.updateErr = errors.New("save unavailable")
			}
			api := &handler{jobs: r, dockerImages: agent}
			api.runImagePull(ctx, j.ID, docker.ImagePullRequest{Reference: "alpine:latest"})
			got, _ := r.get(j.ID)
			if !got.ProgressLimited || got.Status != "running" || got.PersistenceStatus != "pending" {
				t.Fatalf("pending %+v", got)
			}
			r.updatePullProgress(j.ID, jobLayer{ID: "late", Status: "must not publish"})
			s.updateErr = nil
			r.flush()
			r.flush()
			got, _ = r.get(j.ID)
			saved, _ := s.Job(context.Background(), j.ID)
			if got.Status != want || !got.ProgressLimited || !saved.ProgressLimited || saved.Status != want || agent.calls != 1 {
				t.Fatalf("bad saved outcome %+v / %+v", got, saved)
			}
			if _, ok := got.Layers["late"]; ok {
				t.Fatal("pending allowed progress")
			}
			if kind == "cancelled-context" && got.CancellationStatus != "unknown" {
				t.Fatal("telemetry changed cancellation")
			}
		})
	}
}

func TestPullProgressBudgetSQLiteVolume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "progress.sqlite")
	s, err := controlstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	r := newJobRegistry(s)
	j := r.create("docker-image-pull", "fixture")
	for i := 0; i < 2048; i++ {
		r.updatePullProgress(j.ID, jobLayer{ID: fmt.Sprintf("layer-%d", i), Status: strings.Repeat("x", 1024), Current: 1, Total: 2})
	}
	r.flush()
	saved, err := s.Job(context.Background(), j.ID)
	if err != nil || !saved.ProgressLimited || len(saved.LayersJSON) > pullProgressJSONBudget {
		t.Fatalf("persisted bytes=%d err=%v", len(saved.LayersJSON), err)
	}
	restored := snapshotFromRecord(saved)
	if !restored.ProgressLimited || len(restored.Layers) > pullProgressMaxLayers {
		t.Fatal("readback lost budget")
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if growth := after.Size() - before.Size(); growth > 256<<10 {
		t.Fatalf("fixture SQLite grew %d bytes", growth)
	}
	t.Logf("persisted LayersJSON=%d bytes layers=%d SQLite growth=%d bytes", len(saved.LayersJSON), len(restored.Layers), after.Size()-before.Size())
}

func TestPullProgressBudgetShortIDsAndLayerCeiling(t *testing.T) {
	r := newJobRegistry(nil)
	j := r.create("docker-image-pull", "fixture")
	for i := 1; i <= 127; i++ {
		r.updatePullProgress(j.ID, jobLayer{ID: string(rune(i)), Status: strings.Repeat("\x00", 256), Current: math.MaxInt64, Total: math.MaxInt64})
	}
	got, _ := r.get(j.ID)
	if len(got.Layers) != 127 || got.ProgressLimited {
		t.Fatalf("short ID boundary: %d %v", len(got.Layers), got.ProgressLimited)
	}
	payload, err := json.Marshal(got)
	if err != nil || len(payload) > pullProgressJSONBudget {
		t.Fatal(len(payload), err)
	}
	r.updatePullProgress(j.ID, jobLayer{ID: "overflow", Status: "x"})
	got, _ = r.get(j.ID)
	if len(got.Layers) != 127 || !got.ProgressLimited {
		t.Fatal("cumulative short-ID budget")
	}
	// 独立验证层数硬门槛，即使传入已经达到128层的快照。
	direct := jobSnapshot{Layers: map[string]jobLayer{}}
	for i := 0; i < 128; i++ {
		direct.Layers[fmt.Sprint(i)] = jobLayer{}
	}
	if _, ok := boundedPullLayer(&direct, jobLayer{ID: "new"}); ok || !direct.ProgressLimited {
		t.Fatal("layer ceiling missing")
	}
	t.Logf("short-ID snapshot JSON=%d bytes", len(payload))
}

func TestPullProgressBudgetSnapshotSurfacesAndTerminal(t *testing.T) {
	r := newJobRegistry(nil)
	j := r.create("docker-image-pull", "fixture")
	r.updatePullProgress(j.ID, jobLayer{ID: "layer", Status: strings.Repeat("x", 257), Current: 1, Total: 2})
	list := httptest.NewRecorder()
	(&handler{jobs: r}).listJobs(list, httptest.NewRequest(http.MethodGet, "/fixture", nil))
	if !strings.Contains(list.Body.String(), `"progressLimited":true`) {
		t.Fatal("HTTP list lost flag")
	}
	r.update(j.ID, "completed", "trusted completion", "", 100)
	r.updatePullProgress(j.ID, jobLayer{ID: "late", Status: "late"})
	api := &handler{jobs: r}
	route := chi.NewRouteContext()
	route.URLParams.Add("jobID", j.ID)
	req := httptest.NewRequest(http.MethodGet, "/fixture", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, route))
	get := httptest.NewRecorder()
	api.jobStatus(get, req)
	// registry完整列表也必须携带标志。
	listed := r.list("")
	if len(listed) != 1 || !listed[0].ProgressLimited {
		t.Fatal("list lost flag")
	}
	writer := &jobEventWriter{header: http.Header{}, events: make(chan string, 4)}
	api.jobEvents(writer, req)
	payload := <-writer.events
	if !strings.Contains(payload, `"progressLimited":true`) || !strings.Contains(get.Body.String(), `"progressLimited":true`) || strings.Contains(payload, `"late"`) {
		t.Fatal("snapshot flag or terminal changed")
	}
	if len(payload) > pullProgressJSONBudget {
		t.Fatal("SSE bounded fixture")
	}
}
