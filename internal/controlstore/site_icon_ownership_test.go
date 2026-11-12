package controlstore

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func iconOwnershipStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "icons.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func iconOwnershipProfile(t *testing.T, s *Store) SiteProfile {
	t.Helper()
	profiles, err := s.SiteProfiles(context.Background())
	if err != nil || len(profiles) != 1 {
		t.Fatalf("profiles=%#v error=%v", profiles, err)
	}
	return profiles[0]
}

func iconOwnershipSwap(t *testing.T, s *Store, id, old, next string, want bool) {
	t.Helper()
	got, err := s.CompareAndSwapSiteIcon(context.Background(), id, old, next)
	if err != nil || got != want {
		t.Fatalf("swap %q -> %q: changed=%v error=%v", old, next, got, err)
	}
}

func TestSiteIconOwnershipLateDeleteCannotRecreate(t *testing.T) {
	s := iconOwnershipStore(t)
	ctx := context.Background()
	const id = "manual:fixture"
	if _, err := s.UpsertSiteProfile(ctx, SiteProfile{ProjectID: id, Name: "Original", Source: "manual"}); err != nil {
		t.Fatal(err)
	}
	iconOwnershipSwap(t, s, id, "", "old.svg", true)
	if err := s.DeleteSiteProfile(ctx, id); err != nil {
		t.Fatal(err)
	}
	iconOwnershipSwap(t, s, id, "old.svg", "late.svg", false)
	iconOwnershipSwap(t, s, id, "old.svg", "", false)
	profiles, err := s.SiteProfiles(ctx)
	if err != nil || len(profiles) != 0 {
		t.Fatalf("late icon recreated row: %#v %v", profiles, err)
	}
}

func TestSiteIconOwnershipPreservesConcurrentMetadataAndVisit(t *testing.T) {
	s := iconOwnershipStore(t)
	ctx := context.Background()
	const id = "compose:fixture"
	p := SiteProfile{ProjectID: id, Name: "Original"}
	if _, err := s.UpsertSiteProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	iconOwnershipSwap(t, s, id, "", "old.svg", true)
	p.Name = "New name"
	p.Description = "New description"
	p.Favorite = true
	p.Hidden = true
	p.Source = "edited"
	p.Category = "New category"
	p.DetectedTitle = "Detected"
	p.AutoIconURL = "https://example.test/icon.png"
	p.LaunchURL = "https://example.test"
	p.PrimaryPort = 443
	p.SortOrder = 12
	if _, err := s.UpsertSiteProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSiteIgnored(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordSiteVisit(ctx, id); err != nil {
		t.Fatal(err)
	}
	before := iconOwnershipProfile(t, s)
	iconOwnershipSwap(t, s, id, "old.svg", "new.svg", true)
	after := iconOwnershipProfile(t, s)
	before.LocalIconName = "new.svg"
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("icon changed metadata: before=%#v after=%#v", before, after)
	}
}

func TestSiteIconOwnershipConcurrentUploadAndRemoval(t *testing.T) {
	for _, winner := range []string{"winner.svg", ""} {
		t.Run("winner="+winner, func(t *testing.T) {
			s := iconOwnershipStore(t)
			const id = "manual:fixture"
			if _, err := s.UpsertSiteProfile(context.Background(), SiteProfile{ProjectID: id, Name: "Site", Source: "manual"}); err != nil {
				t.Fatal(err)
			}
			iconOwnershipSwap(t, s, id, "", "old.svg", true)
			// 两个请求持有同一个旧快照，先完成的写入获得唯一归属。
			iconOwnershipSwap(t, s, id, "old.svg", winner, true)
			iconOwnershipSwap(t, s, id, "old.svg", "loser.svg", false)
			iconOwnershipSwap(t, s, id, "old.svg", "", false)
			if got := iconOwnershipProfile(t, s).LocalIconName; got != winner {
				t.Fatalf("icon=%q", got)
			}
		})
	}
}

func TestSiteIconOwnershipUpsertCannotAssignOrRestoreLocalFile(t *testing.T) {
	s := iconOwnershipStore(t)
	ctx := context.Background()
	p := SiteProfile{ProjectID: "manual:fixture", Name: "Site", Source: "manual", LocalIconName: "client.svg"}
	saved, err := s.UpsertSiteProfile(ctx, p)
	if err != nil || saved.LocalIconName != "" {
		t.Fatalf("insert accepted client icon: %#v %v", saved, err)
	}
	iconOwnershipSwap(t, s, p.ProjectID, "", "old.svg", true)
	stale := iconOwnershipProfile(t, s)
	iconOwnershipSwap(t, s, p.ProjectID, "old.svg", "new.svg", true)
	stale.Name = "New name"
	saved, err = s.UpsertSiteProfile(ctx, stale)
	if err != nil || saved.LocalIconName != "new.svg" || saved.Name != "New name" {
		t.Fatalf("upsert returned stale icon: %#v %v", saved, err)
	}
	if got := iconOwnershipProfile(t, s); got.LocalIconName != "new.svg" || got.Name != "New name" {
		t.Fatalf("upsert overwrote icon: %#v", got)
	}
	iconOwnershipSwap(t, s, p.ProjectID, "new.svg", "", true)
	saved, err = s.UpsertSiteProfile(ctx, stale)
	if err != nil || saved.LocalIconName != "" {
		t.Fatalf("upsert restored removed icon: %#v %v", saved, err)
	}
}

func TestSiteIconOwnershipDiscoveredAndPlaceholderCompatibility(t *testing.T) {
	for _, kind := range []string{"first-edit", "ignored", "visited"} {
		t.Run(kind, func(t *testing.T) {
			s := iconOwnershipStore(t)
			ctx := context.Background()
			const id = "compose:fixture"
			switch kind {
			case "first-edit":
				if _, err := s.UpsertSiteProfile(ctx, SiteProfile{ProjectID: id, Name: "Discovered"}); err != nil {
					t.Fatal(err)
				}
			case "ignored":
				if err := s.SetSiteIgnored(ctx, id, true); err != nil {
					t.Fatal(err)
				}
			case "visited":
				if _, err := s.RecordSiteVisit(ctx, id); err != nil {
					t.Fatal(err)
				}
			}
			before := iconOwnershipProfile(t, s)
			iconOwnershipSwap(t, s, id, "", "new.svg", true)
			got := iconOwnershipProfile(t, s)
			before.LocalIconName = "new.svg"
			if !reflect.DeepEqual(before, got) {
				t.Fatalf("placeholder changed: %#v %#v", before, got)
			}
		})
	}
}

func TestSiteIconOwnershipInvalidInputAndCancelledContext(t *testing.T) {
	s := iconOwnershipStore(t)
	ctx := context.Background()
	const id = "manual:fixture"
	if _, err := s.UpsertSiteProfile(ctx, SiteProfile{ProjectID: id, Name: "Site"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".", "..", "../icon.svg", "nested/icon.svg", "bad\x00.svg", strings.Repeat("x", 161)} {
		for _, expected := range []bool{false, true} {
			old, next := "", name
			if expected {
				old, next = name, ""
			}
			if changed, err := s.CompareAndSwapSiteIcon(ctx, id, old, next); err == nil || changed {
				t.Fatalf("accepted invalid filename %q", name)
			}
		}
	}
	for _, badID := range []string{"", strings.Repeat("x", 257)} {
		if changed, err := s.CompareAndSwapSiteIcon(ctx, badID, "", "new.svg"); err == nil || changed {
			t.Fatal("accepted invalid ID")
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if changed, err := s.CompareAndSwapSiteIcon(canceled, id, "", "new.svg"); err == nil || changed {
		t.Fatalf("cancelled changed=%v error=%v", changed, err)
	}
	if got := iconOwnershipProfile(t, s).LocalIconName; got != "" {
		t.Fatalf("rejected operation wrote %q", got)
	}
	// 空到空的移除仍是有效的现有行操作。
	iconOwnershipSwap(t, s, id, "", "", true)
}
