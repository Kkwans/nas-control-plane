package docker

import (
	"encoding/json"
	"strings"
	"testing"

	mobycontainer "github.com/moby/moby/api/types/container"
)

func TestContainerDetailsExcludeSensitiveInspectFields(t *testing.T) {
	inspect := mobycontainer.InspectResponse{
		ID:           "abc123",
		Name:         "/demo",
		Image:        "sha256:image-id",
		Created:      "2026-08-08T10:00:00.000000000Z",
		Path:         "/bin/secret-command",
		Args:         []string{"--token", "command-secret"},
		RestartCount: 2,
		Config: &mobycontainer.Config{
			Image:  "demo:latest",
			Env:    []string{"DATABASE_PASSWORD=environment-secret"},
			Labels: map[string]string{"private.token": "label-secret"},
			Cmd:    []string{"serve", "--api-key", "command-secret"},
		},
		State: &mobycontainer.State{
			Status: mobycontainer.ContainerState("running"), Running: true,
			StartedAt: "2026-08-08T10:01:00Z", ExitCode: 0,
			Health: &mobycontainer.Health{Status: mobycontainer.Healthy},
		},
		HostConfig: &mobycontainer.HostConfig{
			NetworkMode:   "host",
			RestartPolicy: mobycontainer.RestartPolicy{Name: mobycontainer.RestartPolicyUnlessStopped},
			Resources:     mobycontainer.Resources{NanoCPUs: 2_000_000_000, Memory: 1 << 30},
		},
	}

	details := containerDetailsFromInspect(inspect)
	if details.ID != "abc123" || details.Name != "demo" || details.Image != "demo:latest" {
		t.Fatalf("identity = %#v", details)
	}
	if details.State != "running" || details.Health != "healthy" || details.RestartCount != 2 {
		t.Fatalf("state = %#v", details)
	}
	if details.NetworkMode != "host" || details.NanoCPUs != 2_000_000_000 || details.MemoryBytes != 1<<30 {
		t.Fatalf("runtime settings = %#v", details)
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, secret := range []string{"environment-secret", "label-secret", "command-secret", "DATABASE_PASSWORD", "private.token"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("safe details leaked %q: %s", secret, encoded)
		}
	}
}
