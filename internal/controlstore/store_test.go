package controlstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

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
