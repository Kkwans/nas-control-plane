package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/Kkwans/nas-control-plane/internal/docker"
)

type artifactInventoryAgent struct {
	DockerImageAgentClient
	inventory docker.ImageInventory
	err       error
	calls     int
}

func (a *artifactInventoryAgent) ListDockerImages(context.Context, string) (docker.ImageInventory, error) {
	a.calls++
	return a.inventory, a.err
}

func TestDockerArtifactPresenceHubAlias(t *testing.T) {
	agent := &artifactInventoryAgent{inventory: docker.ImageInventory{Images: []docker.ImageSummary{{RepoTags: []string{"alpine:latest"}}}}}
	api := &handler{dockerImages: agent}
	jobs := api.resolveDockerArtifactStates(context.Background(), []jobSnapshot{{Type: "docker-image-pull", Status: "completed", Reference: "docker.io/library/alpine:latest"}})
	if jobs[0].ArtifactState != jobArtifactPresent {
		t.Fatalf("artifact state = %q, want present", jobs[0].ArtifactState)
	}
}

func TestDockerArtifactReferenceSharedCases(t *testing.T) {
	data, err := os.ReadFile("../../testdata/docker-artifact-reference-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name      string `json:"name"`
		Reference string `json:"reference"`
		Candidate string `json:"candidate"`
		Present   bool   `json:"present"`
		Valid     bool   `json:"valid"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			key := dockerArtifactReferenceKey(tc.Reference)
			if (key != "") != tc.Valid {
				t.Fatalf("key validity = %q, want valid=%v", key, tc.Valid)
			}
			if got := key != "" && key == dockerArtifactReferenceKey(tc.Candidate); got != tc.Present {
				t.Fatalf("match = %v, want %v", got, tc.Present)
			}
			for _, digestInventory := range []bool{false, true} {
				image := docker.ImageSummary{}
				if digestInventory {
					image.RepoDigests = []string{tc.Candidate}
				} else {
					image.RepoTags = []string{tc.Candidate}
				}
				agent := &artifactInventoryAgent{inventory: docker.ImageInventory{Images: []docker.ImageSummary{image}}}
				api := &handler{dockerImages: agent}
				jobs := api.resolveDockerArtifactStates(context.Background(), []jobSnapshot{{Type: "docker-image-pull", Status: "completed", Reference: tc.Reference}})
				want := jobArtifactDeleted
				if !tc.Valid {
					want = jobArtifactUnknown
				} else if tc.Present {
					want = jobArtifactPresent
				}
				if jobs[0].ArtifactState != want || agent.calls != 1 {
					t.Fatalf("artifact = %q, want %q; calls=%d", jobs[0].ArtifactState, want, agent.calls)
				}
			}
		})
	}
}

func TestDockerArtifactPresenceInventoryFailureAndJobScope(t *testing.T) {
	agent := &artifactInventoryAgent{err: errors.New("unavailable")}
	api := &handler{dockerImages: agent}
	untouched := []jobSnapshot{{Type: "docker-image-pull", Status: "running", ArtifactState: jobArtifactUnknown}, {Type: "docker-image-remove", Status: "completed", ArtifactState: jobArtifactUnknown}}
	api.resolveDockerArtifactStates(context.Background(), untouched)
	if agent.calls != 0 {
		t.Fatal("unneeded inventory call")
	}
	jobs := append(untouched, jobSnapshot{Type: "docker-image-pull", Status: "completed", Reference: "alpine:latest", ArtifactState: jobArtifactPresent})
	got := api.resolveDockerArtifactStates(context.Background(), jobs)
	for _, job := range got {
		if job.ArtifactState != jobArtifactUnknown {
			t.Fatalf("failure or out-of-scope changed state: %+v", job)
		}
	}
	if agent.calls != 1 {
		t.Fatalf("calls=%d", agent.calls)
	}
}

func TestDockerArtifactPresenceEmptyInventoryKeepsNonPullJobs(t *testing.T) {
	agent := &artifactInventoryAgent{}
	api := &handler{dockerImages: agent}
	jobs := []jobSnapshot{
		{Type: "docker-image-pull", Status: "running", ArtifactState: jobArtifactPresent},
		{Type: "docker-image-remove", Status: "completed", ArtifactState: jobArtifactPresent},
		{Type: "docker-image-pull", Status: "failed", ArtifactState: jobArtifactUnknown},
		{Type: "docker-image-pull", Status: "completed", Reference: "alpine:latest", ArtifactState: jobArtifactPresent},
		{Type: "docker-image-pull", Status: "completed", Reference: "", ArtifactState: jobArtifactPresent},
	}
	got := api.resolveDockerArtifactStates(context.Background(), jobs)
	wants := []string{jobArtifactPresent, jobArtifactPresent, jobArtifactUnknown, jobArtifactDeleted, jobArtifactUnknown}
	for index, want := range wants {
		if got[index].ArtifactState != want {
			t.Fatalf("job %d = %q, want %q", index, got[index].ArtifactState, want)
		}
	}
}
