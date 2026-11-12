package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/go-chi/chi/v5"
)

type iconOwnershipStore struct {
	ControlStore
	mu              sync.Mutex
	profile         controlstore.SiteProfile
	exists          bool
	entered         chan struct{}
	release         chan struct{}
	casCalls        atomic.Int32
	casErr          error
	commitThenError bool
	input           controlstore.SiteProfile
}

func (s *iconOwnershipStore) SiteProfiles(context.Context) ([]controlstore.SiteProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.exists {
		return nil, nil
	}
	return []controlstore.SiteProfile{s.profile}, nil
}
func (s *iconOwnershipStore) CompareAndSwapSiteIcon(_ context.Context, id, expected, replacement string) (bool, error) {
	if s.casCalls.Add(1) == 1 && s.entered != nil {
		close(s.entered)
		<-s.release
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.casErr != nil {
		if s.commitThenError && s.exists && s.profile.ProjectID == id && s.profile.LocalIconName == expected {
			s.profile.LocalIconName = replacement
		}
		return false, s.casErr
	}
	if !s.exists || s.profile.ProjectID != id || s.profile.LocalIconName != expected {
		return false, nil
	}
	s.profile.LocalIconName = replacement
	return true, nil
}
func (s *iconOwnershipStore) UpsertSiteProfile(_ context.Context, p controlstore.SiteProfile) (controlstore.SiteProfile, error) {
	s.input = p
	p.LocalIconName = s.profile.LocalIconName
	s.profile = p
	return p, nil
}
func ownershipIconRequest(t *testing.T, method string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	contentType := ""
	if method == http.MethodPost {
		w := multipart.NewWriter(&body)
		part, err := w.CreateFormFile("icon", "fixture.svg")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(part, `<svg xmlns="http://www.w3.org/2000/svg"/>`); err != nil {
			t.Fatal(err)
		}
		if err = w.Close(); err != nil {
			t.Fatal(err)
		}
		contentType = w.FormDataContentType()
	}
	req := httptest.NewRequest(method, "/api/v1/sites/fixture/icon", &body)
	req.Header.Set("Content-Type", contentType)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("siteID", "fixture")
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
}
func runOwnershipIcon(api *handler, res http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodPost {
		api.uploadSiteIcon(res, req)
	} else {
		api.deleteSiteIcon(res, req)
	}
}
func newOwnershipFixture(t *testing.T) (*handler, *iconOwnershipStore) {
	t.Helper()
	s := &iconOwnershipStore{exists: true, profile: controlstore.SiteProfile{ProjectID: "fixture", Name: "original", LocalIconName: "old.svg"}}
	api := &handler{controlStore: s, siteAssetsDirectory: t.TempDir()}
	if err := os.WriteFile(filepath.Join(api.siteAssetsDirectory, "old.svg"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	return api, s
}
func assertOwnershipFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, e := range entries {
		found[e.Name()] = true
	}
	if len(found) != len(names) {
		t.Fatalf("files=%v want=%v", found, names)
	}
	for _, name := range names {
		if !found[name] {
			t.Fatalf("missing %q; files=%v", name, found)
		}
	}
}

func TestSiteIconOwnershipConcurrentMutations(t *testing.T) {
	for _, first := range []string{http.MethodPost, http.MethodDelete} {
		for _, second := range []string{http.MethodPost, http.MethodDelete} {
			t.Run(first+"_then_"+second, func(t *testing.T) {
				api, s := newOwnershipFixture(t)
				s.entered = make(chan struct{})
				s.release = make(chan struct{})
				req := ownershipIconRequest(t, first)
				loser := httptest.NewRecorder()
				done := make(chan struct{})
				go func() { defer close(done); runOwnershipIcon(api, loser, req) }()
				<-s.entered
				winner := httptest.NewRecorder()
				runOwnershipIcon(api, winner, ownershipIconRequest(t, second))
				close(s.release)
				<-done
				want := http.StatusOK
				if second == http.MethodDelete {
					want = http.StatusNoContent
				}
				if winner.Code != want {
					t.Fatalf("winner=%d %s", winner.Code, winner.Body.String())
				}
				if loser.Code != http.StatusConflict || !strings.Contains(loser.Body.String(), "SITE_ICON_CONFLICT") {
					t.Fatalf("loser=%d %s", loser.Code, loser.Body.String())
				}
				if second == http.MethodPost {
					assertOwnershipFiles(t, api.siteAssetsDirectory, s.profile.LocalIconName)
				} else {
					assertOwnershipFiles(t, api.siteAssetsDirectory)
				}
			})
		}
	}
}

func TestSiteIconOwnershipConcurrentProfileChanges(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		for _, deleted := range []bool{false, true} {
			name := method + "_metadata"
			if deleted {
				name = method + "_deleted"
			}
			t.Run(name, func(t *testing.T) {
				api, s := newOwnershipFixture(t)
				s.entered = make(chan struct{})
				s.release = make(chan struct{})
				req := ownershipIconRequest(t, method)
				res := httptest.NewRecorder()
				done := make(chan struct{})
				go func() { defer close(done); runOwnershipIcon(api, res, req) }()
				<-s.entered
				s.mu.Lock()
				s.profile.Name = "concurrent edit"
				s.exists = !deleted
				s.mu.Unlock()
				close(s.release)
				<-done
				if deleted {
					if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "SITE_ICON_CONFLICT") {
						t.Fatalf("status=%d %s", res.Code, res.Body.String())
					}
					if s.exists {
						t.Fatal("deleted profile resurrected")
					}
					assertOwnershipFiles(t, api.siteAssetsDirectory, "old.svg")
				} else {
					want := http.StatusOK
					if method == http.MethodDelete {
						want = http.StatusNoContent
					}
					if res.Code != want || s.profile.Name != "concurrent edit" {
						t.Fatalf("status=%d profile=%+v", res.Code, s.profile)
					}
					if method == http.MethodPost {
						assertOwnershipFiles(t, api.siteAssetsDirectory, s.profile.LocalIconName)
					} else {
						assertOwnershipFiles(t, api.siteAssetsDirectory)
					}
				}
			})
		}
	}
}

func TestSiteIconOwnershipFailureCleanup(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		for _, missing := range []bool{false, true} {
			name := method + "_store_error"
			if missing {
				name = method + "_initially_missing"
			}
			t.Run(name, func(t *testing.T) {
				api, s := newOwnershipFixture(t)
				s.exists = !missing
				s.casErr = errors.New("fixture failure")
				res := httptest.NewRecorder()
				runOwnershipIcon(api, res, ownershipIconRequest(t, method))
				want := http.StatusInternalServerError
				if missing {
					want = http.StatusNotFound
				}
				if res.Code != want {
					t.Fatalf("status=%d %s", res.Code, res.Body.String())
				}
				if method == http.MethodPost && !missing {
					entries, err := os.ReadDir(api.siteAssetsDirectory)
					if err != nil {
						t.Fatal(err)
					}
					if len(entries) != 2 {
						t.Fatalf("uncertain write must retain old and candidate files: %v", entries)
					}
					if _, err := os.Stat(filepath.Join(api.siteAssetsDirectory, "old.svg")); err != nil {
						t.Fatal(err)
					}
				} else {
					assertOwnershipFiles(t, api.siteAssetsDirectory, "old.svg")
				}
				if missing && s.casCalls.Load() != 0 {
					t.Fatal("CAS called for initially missing profile")
				}
			})
		}
	}
}

func TestSiteIconOwnershipEmptyDelete(t *testing.T) {
	api, s := newOwnershipFixture(t)
	s.profile.LocalIconName = ""
	res := httptest.NewRecorder()
	api.deleteSiteIcon(res, ownershipIconRequest(t, http.MethodDelete))
	if res.Code != http.StatusNoContent {
		t.Fatalf("status=%d %s", res.Code, res.Body.String())
	}
	assertOwnershipFiles(t, api.siteAssetsDirectory, "old.svg")
}

func TestSiteIconOwnershipProfileInputIsServerOwned(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			api, s := newOwnershipFixture(t)
			if method == http.MethodPost {
				s.profile.LocalIconName = ""
			}
			req := httptest.NewRequest(method, "/api/v1/sites/fixture", strings.NewReader(`{"name":"edited","localIconName":"attacker.svg"}`))
			req.Header.Set("Content-Type", "application/json")
			rc := chi.NewRouteContext()
			rc.URLParams.Add("siteID", "fixture")
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
			res := httptest.NewRecorder()
			if method == http.MethodPost {
				api.createSite(res, req)
			} else {
				api.updateSite(res, req)
			}
			want := http.StatusOK
			if method == http.MethodPost {
				want = http.StatusCreated
			}
			if res.Code != want || s.input.LocalIconName != "" {
				t.Fatalf("status=%d input=%+v body=%s", res.Code, s.input, res.Body.String())
			}
			if method == http.MethodPut && !strings.Contains(res.Body.String(), `"localIconName":"old.svg"`) {
				t.Fatalf("missing stored filename: %s", res.Body.String())
			}
		})
	}
}

func TestSiteIconOwnershipStaleSnapshotDoesNotRemoveFiles(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			api, s := newOwnershipFixture(t)
			s.entered = make(chan struct{})
			s.release = make(chan struct{})
			req := ownershipIconRequest(t, method)
			res := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { defer close(done); runOwnershipIcon(api, res, req) }()
			<-s.entered
			s.mu.Lock()
			s.profile.LocalIconName = "winner.svg"
			s.mu.Unlock()
			writeErr := os.WriteFile(filepath.Join(api.siteAssetsDirectory, "winner.svg"), []byte("winner"), 0600)
			close(s.release)
			<-done
			if writeErr != nil {
				t.Fatal(writeErr)
			}
			if res.Code != http.StatusConflict {
				t.Fatalf("status=%d %s", res.Code, res.Body.String())
			}
			assertOwnershipFiles(t, api.siteAssetsDirectory, "old.svg", "winner.svg")
		})
	}
}

func TestSiteIconOwnershipDiscoveredProfileHasNoNameRequirement(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			api, s := newOwnershipFixture(t)
			s.profile.Name = ""
			s.profile.Source = "auto"
			res := httptest.NewRecorder()
			runOwnershipIcon(api, res, ownershipIconRequest(t, method))
			want := http.StatusOK
			if method == http.MethodDelete {
				want = http.StatusNoContent
			}
			if res.Code != want || s.profile.Name != "" || s.profile.Source != "auto" {
				t.Fatalf("status=%d profile=%+v", res.Code, s.profile)
			}
		})
	}
}

func TestSiteIconOwnershipCommitThenErrorRetainsReferencedFile(t *testing.T) {
	api, s := newOwnershipFixture(t)
	s.casErr = context.Canceled
	s.commitThenError = true
	res := httptest.NewRecorder()
	api.uploadSiteIcon(res, ownershipIconRequest(t, http.MethodPost))
	if res.Code != http.StatusInternalServerError || !strings.Contains(res.Body.String(), "SITE_ICON_SAVE_FAILED") {
		t.Fatalf("status=%d %s", res.Code, res.Body.String())
	}
	if s.profile.LocalIconName == "old.svg" || s.profile.LocalIconName == "" {
		t.Fatal("fixture did not simulate committed write")
	}
	assertOwnershipFiles(t, api.siteAssetsDirectory, "old.svg", s.profile.LocalIconName)
}
