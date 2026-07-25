package docker

import (
	"context"
	"errors"
	"testing"
)

func TestInventoryCollectorGroupsComposeProjectsAndStandaloneContainers(t *testing.T) {
	collector := NewInventoryCollector(fakeInventoryGateway{
		engine: EngineInfo{
			ServerVersion:     "28.0.1",
			OperatingSystem:   "Debian 12",
			Architecture:      "aarch64",
			Containers:        3,
			ContainersRunning: 2,
			ContainersStopped: 1,
			Images:            14,
		},
		containers: []InventoryContainer{
			{
				ID: "aaa111", Name: "/ncp-web", Image: "ncp:latest", State: "running", Status: "Up 2 minutes",
				Labels: map[string]string{
					"com.docker.compose.project":              "nas-control-plane",
					"com.docker.compose.project.working_dir":  "/volume2/Project/nas-control-plane",
					"com.docker.compose.project.config_files": "/volume2/Project/nas-control-plane/compose.yaml,/volume2/Project/nas-control-plane/compose.override.yaml",
					"com.docker.compose.service":              "web",
				},
				Ports: []PortMapping{{PrivatePort: 80, PublicPort: 8760, Protocol: "tcp"}},
			},
			{
				ID: "bbb222", Name: "/ncp-worker", Image: "ncp:latest", State: "exited", Status: "Exited (1)",
				Labels: map[string]string{
					"com.docker.compose.project":             "nas-control-plane",
					"com.docker.compose.project.working_dir": "/volume2/Project/nas-control-plane",
					"com.docker.compose.service":             "worker",
				},
			},
			{ID: "ccc333", Name: "/standalone", Image: "alpine:3.21", State: "running", Status: "Up 5 minutes"},
		},
	})

	inventory, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if inventory.Engine.ContainersRunning != 2 || inventory.Engine.Images != 14 {
		t.Fatalf("engine = %#v", inventory.Engine)
	}
	if len(inventory.Containers) != 3 || inventory.Containers[0].Name != "ncp-web" {
		t.Fatalf("containers = %#v", inventory.Containers)
	}
	if len(inventory.Containers[0].Ports) != 1 || inventory.Containers[0].Ports[0].PublicPort != 8760 {
		t.Fatalf("ports = %#v", inventory.Containers[0].Ports)
	}
	if len(inventory.Projects) != 2 {
		t.Fatalf("projects = %#v", inventory.Projects)
	}

	project := inventory.Projects[0]
	if project.Name != "nas-control-plane" || project.ContainerCount != 2 || project.RunningCount != 1 || project.State != "degraded" {
		t.Fatalf("compose project = %#v", project)
	}
	if project.WorkingDirectory != "/volume2/Project/nas-control-plane" {
		t.Fatalf("working directory = %q", project.WorkingDirectory)
	}
	if len(project.ConfigFiles) != 2 || project.ConfigFiles[0] != "/volume2/Project/nas-control-plane/compose.yaml" {
		t.Fatalf("config files = %#v", project.ConfigFiles)
	}
	if inventory.Projects[1].Kind != ProjectKindStandalone || inventory.Projects[1].ContainerCount != 1 {
		t.Fatalf("standalone project = %#v", inventory.Projects[1])
	}
}

func TestInventoryCollectorReturnsStableErrorWhenDockerIsUnavailable(t *testing.T) {
	collector := NewInventoryCollector(fakeInventoryGateway{engineErr: errors.New("socket unavailable")})

	_, err := collector.Collect(context.Background())
	if ErrorCode(err) != "DOCKER_INVENTORY_UNAVAILABLE" {
		t.Fatalf("error code = %q, want DOCKER_INVENTORY_UNAVAILABLE", ErrorCode(err))
	}
}

type fakeInventoryGateway struct {
	engine        EngineInfo
	engineErr     error
	containers    []InventoryContainer
	containersErr error
}

func (f fakeInventoryGateway) EngineInfo(context.Context) (EngineInfo, error) {
	return f.engine, f.engineErr
}

func (f fakeInventoryGateway) ListInventoryContainers(context.Context) ([]InventoryContainer, error) {
	return f.containers, f.containersErr
}
