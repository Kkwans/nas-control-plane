package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/Kkwans/nas-control-plane/internal/docker"
)

func historyTestEntry(id string) controlstore.JobHistoryEntry {
	at := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	return controlstore.JobHistoryEntry{ID: id, Type: "docker-image-pull", Status: "running", Message: "已保存的状态", CreatedAt: at, UpdatedAt: at, CreatedAtUnix: at.Unix()}
}
func historyRawCursor(raw string) string { return base64.RawURLEncoding.EncodeToString([]byte(raw)) }

func TestJobHistoryCursorCanonicalAndMaximumKeys(t *testing.T) {
	for _, piece := range []string{"a", `"`, `\`, "<", ">", "&", "界", "\u2028", "\u2029"} {
		kind := strings.Repeat(piece, 128/len(piece))
		key := controlstore.JobHistoryKey{CreatedAtUnix: time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC).Unix(), ID: strings.Repeat(piece, 256/len(piece))}
		encoded, err := encodeJobHistoryCursor(kind, key)
		if err != nil {
			t.Fatalf("piece %q: %v", piece, err)
		}
		if len(encoded) > jobHistoryCursorBytes {
			t.Fatal("valid maximum key exceeded encoded bound")
		}
		decoded, err := decodeJobHistoryCursor(encoded, kind)
		if err != nil || *decoded != key {
			t.Fatalf("roundtrip %q: %+v %v", piece, decoded, err)
		}
		if _, err := decodeJobHistoryCursor(encoded, kind+"x"); err == nil {
			t.Fatal("cross-filter cursor accepted")
		}
	}
	key := controlstore.JobHistoryKey{CreatedAtUnix: 0, ID: "legacy/non-hex id"}
	encoded, err := encodeJobHistoryCursor("", key)
	if err != nil {
		t.Fatal(err)
	}
	if decoded, err := decodeJobHistoryCursor(encoded, ""); err != nil || *decoded != key {
		t.Fatalf("legacy key rejected: %v", err)
	}
}

func TestJobHistoryCursorRejectsAmbiguousInput(t *testing.T) {
	valid := `{"v":1,"type":"","createdAtUnix":"0","id":"x"}`
	bad := []string{"", strings.Repeat("A", 2049), "%%%", historyRawCursor(valid) + "=", historyRawCursor(valid) + "\n",
		historyRawCursor(strings.Replace(valid, `"v":1`, `"v":2`, 1)),
		historyRawCursor(strings.Replace(valid, `"v":1`, `"v":1,"v":1`, 1)),
		historyRawCursor(strings.Replace(valid, `"id":"x"`, `"id":"x","extra":true`, 1)),
		historyRawCursor(valid + " {}"), historyRawCursor(" " + valid), historyRawCursor(strings.Replace(valid, `"0"`, `"00"`, 1)),
		historyRawCursor(strings.Replace(valid, `"0"`, `"+0"`, 1)), historyRawCursor(strings.Replace(valid, `"0"`, `"9223372036854775808"`, 1)),
		historyRawCursor(strings.Replace(valid, `"0"`, fmt.Sprintf(`"%d"`, int64(math.MaxInt64)), 1)),
		historyRawCursor(strings.Replace(valid, `"id":"x"`, `"id":""`, 1)), historyRawCursor(strings.Replace(valid, `"id":"x"`, `"id":"x\n"`, 1)),
		historyRawCursor(strings.Replace(valid, `"id":"x"`, `"id":"\u0078"`, 1)), historyRawCursor("\xff" + valid), historyRawCursor(strings.Repeat(" ", 1537)),
	}
	for i, value := range bad {
		if _, err := decodeJobHistoryCursor(value, ""); !errors.Is(err, controlstore.ErrJobHistoryInvalidQuery) {
			t.Errorf("bad cursor %d accepted: %v", i, err)
		}
	}
}

func TestJobHistoryQueryStrictParsing(t *testing.T) {
	for _, raw := range []string{"", "type=", "limit=1", "limit=100", "type=future-kind&limit=20", "type=%20exact%20"} {
		query, err := parseJobHistoryQuery(raw)
		if err != nil {
			t.Fatalf("valid %q: %v", raw, err)
		}
		if raw == "" && query.Limit != 50 {
			t.Fatal("wrong default")
		}
		if raw == "type=%20exact%20" && query.Type != " exact " {
			t.Fatal("type trimmed")
		}
	}
	for _, raw := range []string{"limit=", "limit=0", "limit=101", "limit=-1", "limit=%2B1", "limit=1.0", "limit=1e1", "limit= 1", "limit=999999999999999999", "type=a&type=a", "limit=1&limit=1", "cursor=&cursor=", "unknown=x", "type=%", "type=a;limit=1", "type=%FF", "type=%00", "type=" + strings.Repeat("a", 129), "cursor=", strings.Repeat("x", 8193)} {
		if _, err := parseJobHistoryQuery(raw); !errors.Is(err, controlstore.ErrJobHistoryInvalidQuery) {
			t.Errorf("bad query %q: %v", raw, err)
		}
	}
	key := controlstore.JobHistoryKey{CreatedAtUnix: 1, ID: "old-id"}
	cursor, err := encodeJobHistoryCursor("custom", key)
	if err != nil {
		t.Fatal(err)
	}
	query, err := parseJobHistoryQuery(url.Values{"type": {"custom"}, "cursor": {cursor}, "limit": {"1"}}.Encode())
	if err != nil || query.Before == nil || *query.Before != key {
		t.Fatalf("cursor query: %+v %v", query, err)
	}
	// 原始字节预算在ParseQuery前生效，包括会被解析器忽略的空字段。
	if _, err := parseJobHistoryQuery(strings.Repeat("&", 8192)); err != nil {
		t.Fatal(err)
	}
	if _, err := parseJobHistoryQuery(strings.Repeat("&", 8193)); err == nil {
		t.Fatal("overlong raw query accepted")
	}
}

// 未覆写的任何Agent方法均通过nil嵌入接口立即panic，防止真实RPC回退。
type historyAgentSentinel struct {
	AgentClient
	DockerImageAgentClient
	imageCalls int
}

func (a *historyAgentSentinel) ListDockerImages(context.Context, string) (docker.ImageInventory, error) {
	a.imageCalls++
	panic("history must not call Agent")
}

type historyTestReader struct {
	jobPersistence
	read  func(context.Context, controlstore.JobHistoryQuery) (controlstore.JobHistoryPage, error)
	calls int
}

func (s *historyTestReader) JobHistory(ctx context.Context, q controlstore.JobHistoryQuery) (controlstore.JobHistoryPage, error) {
	s.calls++
	return s.read(ctx, q)
}
func historyTestAPI(reader jobPersistence) *handler {
	return &handler{jobs: &jobRegistry{store: reader}}
}
func historyRead(api *handler, raw string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/job-history", nil)
	r.URL.RawQuery = raw
	w := httptest.NewRecorder()
	api.listJobHistory(w, r)
	return w
}

func TestJobHistoryHTTPPersistedOnlyAndBoundedContext(t *testing.T) {
	saved := historyTestEntry("saved-id")
	reader := &historyTestReader{read: func(ctx context.Context, q controlstore.JobHistoryQuery) (controlstore.JobHistoryPage, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > jobStoreTimeout {
			t.Error("missing bounded context")
		}
		if q.Limit != 50 {
			t.Error("default limit missing")
		}
		return controlstore.JobHistoryPage{Items: []controlstore.JobHistoryEntry{saved}}, nil
	}}
	api := historyTestAPI(reader)
	sentinel := &historyAgentSentinel{}
	api.agent, api.dockerImages = sentinel, sentinel
	api.jobs.jobs = map[string]*jobEntry{saved.ID: {view: jobSnapshot{ID: saved.ID, Status: "running", PersistenceStatus: "pending", Message: "live-only"}, pending: &jobSnapshot{Status: "completed"}}}
	w := historyRead(api, "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response %d %s", w.Code, w.Body.String())
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(w.Body.Bytes(), &body) != nil || string(body["source"]) != `"persisted"` {
		t.Fatalf("bad envelope %s", w.Body.String())
	}
	for _, field := range []string{"cancellable", "persistenceStatus", "layers", "artifactState", "speedBytes", "CreatedAtUnix", "live-only", "completed"} {
		if strings.Contains(w.Body.String(), field) {
			t.Errorf("leaked live/action field %s", field)
		}
	}
	if !strings.Contains(w.Body.String(), `"status":"running"`) || reader.calls != 1 {
		t.Fatal("did not return saved state once")
	}
	// 返回快照不被HTTP调整或覆盖。
	if saved.Status != "running" || saved.Message != "已保存的状态" {
		t.Fatal("mutated caller data")
	}
	saved.Status = "completed"
	if completed := historyRead(api, ""); completed.Code != 200 || sentinel.imageCalls != 0 {
		t.Fatal("completed pull history must not inspect artifacts")
	}
}

func TestJobHistoryHTTPErrorsAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reader jobPersistence
		want   int
		code   string
	}{
		{"no store", nil, 501, "JOB_HISTORY_UNSUPPORTED"},
		{"old provider", &snapshotJobStore{}, 501, "JOB_HISTORY_UNSUPPORTED"},
		{"unavailable", &historyTestReader{read: func(context.Context, controlstore.JobHistoryQuery) (controlstore.JobHistoryPage, error) {
			return controlstore.JobHistoryPage{}, errors.New("private storage path")
		}}, 503, "JOB_HISTORY_UNAVAILABLE"},
		{"corrupt", &historyTestReader{read: func(context.Context, controlstore.JobHistoryQuery) (controlstore.JobHistoryPage, error) {
			return controlstore.JobHistoryPage{}, controlstore.ErrJobHistoryCorrupt
		}}, 503, "JOB_HISTORY_CORRUPT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := historyRead(historyTestAPI(tc.reader), "")
			if w.Code != tc.want {
				t.Fatalf("status=%d", w.Code)
			}
			assertErrorCode(t, w, tc.code)
			if strings.Contains(w.Body.String(), "private") {
				t.Fatal("leaked error")
			}
		})
	}
	reader := &historyTestReader{read: func(ctx context.Context, q controlstore.JobHistoryQuery) (controlstore.JobHistoryPage, error) {
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Error("parent cancel not propagated")
		}
		return controlstore.JobHistoryPage{}, ctx.Err()
	}}
	api := historyTestAPI(reader)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/job-history", nil)
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()
	api.listJobHistory(w, r)
	if w.Code != 503 {
		t.Fatalf("cancel status=%d", w.Code)
	}
	assertErrorCode(t, w, "JOB_HISTORY_UNAVAILABLE")
	for _, raw := range []string{"type=%", "type=a;limit=1", "limit=0"} {
		before := reader.calls
		w := historyRead(api, raw)
		if w.Code != 400 || reader.calls != before {
			t.Fatal("invalid query reached reader")
		}
		assertErrorCode(t, w, "JOB_HISTORY_INVALID_QUERY")
	}
}

func TestJobHistoryPageValidationAndExactBudget(t *testing.T) {
	q := controlstore.JobHistoryQuery{Limit: 1}
	entry := historyTestEntry("z")
	key := controlstore.JobHistoryKey{ID: entry.ID, CreatedAtUnix: entry.CreatedAtUnix}
	valid := controlstore.JobHistoryPage{Items: []controlstore.JobHistoryEntry{entry}, Next: &key}
	encoded, err := encodeJobHistoryPage(q, valid)
	if err != nil || !bytes.Contains(encoded, []byte(`"nextCursor"`)) {
		t.Fatalf("valid page: %s %v", encoded, err)
	}
	empty, err := encodeJobHistoryPage(q, controlstore.JobHistoryPage{})
	if err != nil || string(empty) != `{"source":"persisted","items":[]}` {
		t.Fatalf("empty page %s %v", empty, err)
	}
	badCases := []controlstore.JobHistoryPage{
		{Items: []controlstore.JobHistoryEntry{entry, entry}},
		{Next: &key},
		{Items: []controlstore.JobHistoryEntry{entry}, Next: &controlstore.JobHistoryKey{ID: "different", CreatedAtUnix: key.CreatedAtUnix}},
	}
	badEntry := entry
	badEntry.CreatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	badCases = append(badCases, controlstore.JobHistoryPage{Items: []controlstore.JobHistoryEntry{badEntry}})
	badEntry = entry
	badEntry.Message = "\xff"
	badCases = append(badCases, controlstore.JobHistoryPage{Items: []controlstore.JobHistoryEntry{badEntry}})
	for i, page := range badCases {
		if _, err := encodeJobHistoryPage(q, page); !errors.Is(err, controlstore.ErrJobHistoryCorrupt) {
			t.Errorf("bad page%d %v", i, err)
		}
	}
	if _, err := encodeJobHistoryPage(controlstore.JobHistoryQuery{Limit: 1, Type: "other"}, valid); err == nil {
		t.Fatal("wrong type accepted")
	}
	if _, err := encodeJobHistoryPage(controlstore.JobHistoryQuery{Limit: 1, Before: &key}, valid); err == nil {
		t.Fatal("outside cursor accepted")
	}
	unsorted := entry
	unsorted.ID = "zz"
	if _, err := encodeJobHistoryPage(controlstore.JobHistoryQuery{Limit: 2}, controlstore.JobHistoryPage{Items: []controlstore.JobHistoryEntry{entry, unsorted}}); err == nil {
		t.Fatal("ascending page accepted")
	}
	// 逐条都合法，但完整JSON超过预算。HTML字符转义也必须计入。
	page := controlstore.JobHistoryPage{Items: make([]controlstore.JobHistoryEntry, 100)}
	for i := range page.Items {
		item := entry
		item.ID = fmt.Sprintf("%03d", 100-i)
		item.Message = strings.Repeat("<", 8192)
		page.Items[i] = item
	}
	if _, err := encodeJobHistoryPage(controlstore.JobHistoryQuery{Limit: 100}, page); !errors.Is(err, errJobHistoryResponseLarge) {
		t.Fatalf("large page: %v", err)
	}
	// 找到预算前后的精确一字节变化，不依赖近似行数。
	for i := range page.Items {
		page.Items[i].Message = strings.Repeat("x", 8192)
		page.Items[i].Error = strings.Repeat("x", 2000)
	}
	under, err := encodeJobHistoryPage(controlstore.JobHistoryQuery{Limit: 100}, page)
	if err != nil {
		t.Fatal(err)
	}
	remaining := jobHistoryResponseBytes - len(under)
	for i := 0; i < len(page.Items) && remaining > 0; i++ {
		extra := min(8192-len(page.Items[i].Error), remaining)
		page.Items[i].Error += strings.Repeat("x", extra)
		remaining -= extra
	}
	if remaining != 0 {
		t.Fatal("fixture cannot reach boundary")
	}
	exact, err := encodeJobHistoryPage(controlstore.JobHistoryQuery{Limit: 100}, page)
	if err != nil || len(exact) != jobHistoryResponseBytes {
		t.Fatalf("exact budget %d %v", len(exact), err)
	}
	page.Items[99].Error += "x"
	if _, err := encodeJobHistoryPage(controlstore.JobHistoryQuery{Limit: 100}, page); !errors.Is(err, errJobHistoryResponseLarge) {
		t.Fatalf("budget+1: %v", err)
	}
}

func TestJobHistoryHTTPNoPartialSuccess(t *testing.T) {
	entry := historyTestEntry("z")
	entry.Message = strings.Repeat("<", 8192)
	page := controlstore.JobHistoryPage{Items: make([]controlstore.JobHistoryEntry, 100)}
	for i := range page.Items {
		item := entry
		item.ID = fmt.Sprintf("%03d", 100-i)
		page.Items[i] = item
	}
	reader := &historyTestReader{read: func(context.Context, controlstore.JobHistoryQuery) (controlstore.JobHistoryPage, error) {
		return page, nil
	}}
	w := historyRead(historyTestAPI(reader), "limit=100")
	if w.Code != 503 || strings.Contains(w.Body.String(), `"items"`) {
		t.Fatalf("partial success %d %s", w.Code, w.Body.String())
	}
	assertErrorCode(t, w, "JOB_HISTORY_TOO_LARGE")
	page.Items = []controlstore.JobHistoryEntry{historyTestEntry("id")}
	page.Items[0].CreatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	w = historyRead(historyTestAPI(reader), "")
	if w.Code != 503 {
		t.Fatal("invalid time wrote 200")
	}
	assertErrorCode(t, w, "JOB_HISTORY_CORRUPT")
}

func TestJobHistoryAuthenticatedRoutingAndEngineStore(t *testing.T) {
	authService := openTestAuthService(t)
	password := testLoginPassword(t)
	if _, err := authService.Bootstrap(context.Background(), "history-root", password); err != nil {
		t.Fatal(err)
	}
	session, err := authService.Login(context.Background(), "history-root", password)
	if err != nil {
		t.Fatal(err)
	}
	saved := historyTestEntry("history")
	saved.Type = "compose-deploy"
	backing := &snapshotJobStore{records: map[string]controlstore.JobRecord{saved.ID: {ID: saved.ID, Type: saved.Type, Status: "completed", Message: saved.Message, CreatedAt: saved.CreatedAt, UpdatedAt: saved.UpdatedAt, LayersJSON: "{}", Revision: 1}}}
	reader := &historyTestReader{jobPersistence: backing, read: func(context.Context, controlstore.JobHistoryQuery) (controlstore.JobHistoryPage, error) {
		return controlstore.JobHistoryPage{Items: []controlstore.JobHistoryEntry{saved}}, nil
	}}
	engine := newJobRegistry(reader)
	if engine.initErr != nil {
		t.Fatal(engine.initErr)
	}
	// snapshotJobStore.Jobs有意返回空列表；显式建立既有最近列表fixture。
	engine.jobs[saved.ID] = &jobEntry{view: snapshotFromRecord(backing.records[saved.ID]), revision: 1}
	// Config.ControlStore是另一份持久层，不能被历史入口误选。
	other, err := controlstore.Open(t.TempDir() + "/other.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	sentinel := &historyAgentSentinel{}
	api := NewHandler(Config{Auth: authService, JobEngine: engine, ControlStore: other, Agent: sentinel, DockerImages: sentinel})
	for _, loggedIn := range []bool{false, true} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/job-history", nil)
		if loggedIn {
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token})
		}
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		if !loggedIn {
			if w.Code != 401 || reader.calls != 0 {
				t.Fatalf("unauthorized %d calls=%d", w.Code, reader.calls)
			}
			continue
		}
		if w.Code != 200 || reader.calls != 1 || !strings.Contains(w.Body.String(), `"id":"history"`) {
			t.Fatalf("wrong history source %d %s", w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/v1/jobs/history", "/api/v1/jobs"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Token})
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"history"`) {
			t.Fatalf("legacy route %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if reader.calls != 1 {
		t.Fatal("legacy routes consulted new reader")
	}
}

func TestJobHistoryHTTPStoredCursorRoundTrip(t *testing.T) {
	store, err := controlstore.Open(t.TempDir() + "/history.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	at := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	kind := `future<&"type`
	for _, id := range []string{"z", `legacy<&"id`, "a"} {
		record := controlstore.JobRecord{ID: id, Type: kind, Status: "future-state", Message: "持久历史", CreatedAt: at, UpdatedAt: at, LayersJSON: strings.Repeat("x", 2<<20)}
		if err := store.CreateJob(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	api := historyTestAPI(store)
	sentinel := &historyAgentSentinel{}
	api.agent, api.dockerImages = sentinel, sentinel
	var ids []string
	cursor := ""
	for page := 0; page < 3; page++ {
		params := url.Values{"type": {kind}, "limit": {"1"}}
		if cursor != "" {
			params.Set("cursor", cursor)
		}
		w := historyRead(api, params.Encode())
		if w.Code != 200 {
			t.Fatalf("page %d: %d %s", page, w.Code, w.Body.String())
		}
		var response struct {
			Source string `json:"source"`
			Items  []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"items"`
			Next string `json:"nextCursor"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Source != "persisted" || len(response.Items) != 1 || response.Items[0].Status != "future-state" {
			t.Fatalf("bad page %+v", response)
		}
		ids = append(ids, response.Items[0].ID)
		cursor = response.Next
		if (page < 2) != (cursor != "") {
			t.Fatalf("wrong next cursor page %d", page)
		}
	}
	if strings.Join(ids, "|") != `z|legacy<&"id|a` {
		t.Fatalf("wrong full ordering %q", ids)
	}
	if sentinel.imageCalls != 0 {
		t.Fatal("history queried Agent")
	}
}
