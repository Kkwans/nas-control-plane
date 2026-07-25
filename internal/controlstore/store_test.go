package controlstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestPreferencesPersistCompleteConsoleExperience(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "preferences.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	defaults, err := store.Preferences(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if defaults != DefaultPreferences() {
		t.Fatalf("defaults = %#v", defaults)
	}

	input := Preferences{
		RefreshIntervalSeconds: 15,
		InterfaceDensity:       "compact",
		BaseFontSize:           16,
		PageSize:               50,
		SidebarDefault:         "expanded",
		LinkOpenMode:           "same-tab",
		SiteDefaultProtocol:    "https",
		ChineseFont:            "noto-sans-sc",
		LatinFont:              "manrope",
	}
	if _, err := store.UpdatePreferences(context.Background(), 1, input); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Preferences(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != input {
		t.Fatalf("loaded = %#v", loaded)
	}
}

func TestMetricSamplesDeduplicateMinuteAndRespectRange(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "metrics.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 25, 15, 4, 30, 0, time.UTC)
	store.now = func() time.Time { return now }
	for _, sample := range []MetricSample{
		{CollectedAt: now.Add(-2 * time.Minute), CPUPercent: 12},
		{CollectedAt: now.Add(-30 * time.Second), CPUPercent: 25},
		{CollectedAt: now.Add(-10 * time.Second), CPUPercent: 99},
	} {
		if err := store.RecordMetricSample(context.Background(), sample); err != nil {
			t.Fatal(err)
		}
	}
	samples, err := store.MetricSamples(context.Background(), now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 || samples[0].CPUPercent != 25 {
		t.Fatalf("samples = %#v", samples)
	}
}

func TestSiteProfilePersistenceAndVisit(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "control.sqlite"))
	if err != nil {
		t.Fatalf("open control store: %v", err)
	}
	defer store.Close()
	store.now = func() time.Time {
		return time.Date(2026, 7, 25, 12, 30, 45, 0, time.UTC)
	}

	input := SiteProfile{
		ProjectID:   "compose:nas-control-plane",
		Name:        "NAS 管理面板",
		Description: "统一管理 NAS 服务",
		Category:    "管理工具",
		PrimaryPort: 8760,
		LaunchURL:   "https://nas.example.test/control/",
		Favorite:    true,
		SortOrder:   -10,
	}
	if _, err := store.UpsertSiteProfile(context.Background(), input); err != nil {
		t.Fatalf("upsert site profile: %v", err)
	}
	visitedAt, err := store.RecordSiteVisit(context.Background(), input.ProjectID)
	if err != nil {
		t.Fatalf("record site visit: %v", err)
	}

	profiles, err := store.SiteProfiles(context.Background())
	if err != nil {
		t.Fatalf("list site profiles: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("expected one profile, got %d", len(profiles))
	}
	profile := profiles[0]
	if profile.LaunchURL != input.LaunchURL || !profile.Favorite || profile.SortOrder != -10 {
		t.Fatalf("profile fields were not persisted: %#v", profile)
	}
	if profile.LastVisitedAt == nil || !profile.LastVisitedAt.Equal(visitedAt) {
		t.Fatalf("visit time was not persisted: %#v", profile.LastVisitedAt)
	}
}

func TestRecordSiteVisitCreatesActivityForAutoDiscoveredSite(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "control.sqlite"))
	if err != nil {
		t.Fatalf("open control store: %v", err)
	}
	defer store.Close()

	if _, err := store.RecordSiteVisit(context.Background(), "compose:heimdall"); err != nil {
		t.Fatalf("record auto-discovered site visit: %v", err)
	}
	profiles, err := store.SiteProfiles(context.Background())
	if err != nil {
		t.Fatalf("list site profiles: %v", err)
	}
	if len(profiles) != 1 || profiles[0].Name != "" || profiles[0].LastVisitedAt == nil {
		t.Fatalf("unexpected auto-discovered site activity: %#v", profiles)
	}
}

func TestComposeDraftPersistsContentAndHash(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "ncp.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.now = func() time.Time { return time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC) }

	saved, err := store.SaveComposeDraft(context.Background(), ComposeDraft{
		ProjectID: "compose:ncp", ConfigPath: "/volume2/Project/ncp/compose.yaml", Content: "services:\n  web:\n    image: nginx:alpine\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.ContentHash) != 64 {
		t.Fatalf("content hash = %q", saved.ContentHash)
	}
	loaded, err := store.ComposeDraft(context.Background(), saved.ProjectID, saved.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Content != saved.Content || !loaded.UpdatedAt.Equal(saved.UpdatedAt) {
		t.Fatalf("loaded draft = %#v", loaded)
	}
}
