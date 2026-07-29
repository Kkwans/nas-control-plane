package httpapi

import (
	"testing"
	"time"
)

func TestJobRegistryCreateDefaultsArtifactState(t *testing.T) {
	registry := newJobRegistry(nil)
	job := registry.create("docker-image-pull", "redis:latest")

	if job.ArtifactState != jobArtifactUnknown {
		t.Fatalf("artifact state = %q, want %q", job.ArtifactState, jobArtifactUnknown)
	}
}

func TestNormalizeJobSnapshotRepairsLegacyArtifactState(t *testing.T) {
	job := normalizeJobSnapshot(jobSnapshot{
		ID:        "legacy-job",
		Type:      "docker-image-pull",
		Status:    "completed",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})

	if job.ArtifactState != jobArtifactUnknown {
		t.Fatalf("artifact state = %q, want %q", job.ArtifactState, jobArtifactUnknown)
	}
	if job.Layers == nil {
		t.Fatal("layers must be normalized to an empty map")
	}
}

func TestNormalizeJobSnapshotKeepsValidArtifactState(t *testing.T) {
	for _, state := range []string{jobArtifactPresent, jobArtifactDeleted, jobArtifactUnknown} {
		t.Run(state, func(t *testing.T) {
			job := normalizeJobSnapshot(jobSnapshot{ArtifactState: state})
			if job.ArtifactState != state {
				t.Fatalf("artifact state = %q, want %q", job.ArtifactState, state)
			}
		})
	}
}
