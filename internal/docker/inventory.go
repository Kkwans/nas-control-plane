package docker

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/moby/moby/client"
)

const (
	composeProjectLabel     = "com.docker.compose.project"
	composeWorkingDirLabel  = "com.docker.compose.project.working_dir"
	composeConfigFilesLabel = "com.docker.compose.project.config_files"
	composeServiceLabel     = "com.docker.compose.service"
	standaloneProjectID     = "standalone"
	standaloneProjectName   = "独立容器"
)

type ProjectKind string

const (
	ProjectKindCompose    ProjectKind = "compose"
	ProjectKindStandalone ProjectKind = "standalone"
)

type Inventory struct {
	CollectedAt time.Time            `json:"collectedAt"`
	Engine      EngineInfo           `json:"engine"`
	Containers  []InventoryContainer `json:"containers"`
	Projects    []Project            `json:"projects"`
}

type EngineInfo struct {
	ServerVersion     string `json:"serverVersion"`
	OperatingSystem   string `json:"operatingSystem"`
	Architecture      string `json:"architecture"`
	Containers        int    `json:"containers"`
	ContainersRunning int    `json:"containersRunning"`
	ContainersStopped int    `json:"containersStopped"`
	Images            int    `json:"images"`
}

type InventoryContainer struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Image       string            `json:"image"`
	State       string            `json:"state"`
	Status      string            `json:"status"`
	CreatedAt   time.Time         `json:"createdAt"`
	Labels      map[string]string `json:"-"`
	Ports       []PortMapping     `json:"ports"`
	ProjectID   string            `json:"projectId"`
	ProjectName string            `json:"projectName"`
	ServiceName string            `json:"serviceName"`
}

type PortMapping struct {
	HostIP      string `json:"hostIp"`
	PrivatePort uint16 `json:"privatePort"`
	PublicPort  uint16 `json:"publicPort"`
	Protocol    string `json:"protocol"`
}

type Project struct {
	ID               string      `json:"id"`
	Name             string      `json:"name"`
	Kind             ProjectKind `json:"kind"`
	State            string      `json:"state"`
	WorkingDirectory string      `json:"workingDirectory"`
	ConfigFiles      []string    `json:"configFiles"`
	ContainerCount   int         `json:"containerCount"`
	RunningCount     int         `json:"runningCount"`
}

type InventoryGateway interface {
	EngineInfo(context.Context) (EngineInfo, error)
	ListInventoryContainers(context.Context) ([]InventoryContainer, error)
}

type InventoryCollector struct {
	gateway InventoryGateway
	now     func() time.Time
}

func NewInventoryCollector(gateway InventoryGateway) *InventoryCollector {
	if gateway == nil {
		gateway = unavailableInventoryGateway{}
	}
	return &InventoryCollector{gateway: gateway, now: time.Now}
}

func NewLiveInventoryCollector() (*InventoryCollector, error) {
	gateway, err := NewMobyInventoryGateway()
	if err != nil {
		return nil, err
	}
	return NewInventoryCollector(gateway), nil
}

func (c *InventoryCollector) Collect(ctx context.Context) (Inventory, error) {
	if err := ctx.Err(); err != nil {
		return Inventory{}, err
	}

	engine, err := c.gateway.EngineInfo(ctx)
	if err != nil {
		return Inventory{}, coded("DOCKER_INVENTORY_UNAVAILABLE", err)
	}
	containers, err := c.gateway.ListInventoryContainers(ctx)
	if err != nil {
		return Inventory{}, coded("DOCKER_INVENTORY_UNAVAILABLE", err)
	}

	for index := range containers {
		normalizeInventoryContainer(&containers[index])
	}
	return Inventory{
		CollectedAt: c.now().UTC(),
		Engine:      engine,
		Containers:  containers,
		Projects:    groupProjects(containers),
	}, nil
}

func normalizeInventoryContainer(container *InventoryContainer) {
	container.Name = strings.TrimPrefix(strings.TrimSpace(container.Name), "/")
	container.State = strings.ToLower(strings.TrimSpace(container.State))
	container.Labels = cloneLabels(container.Labels)
	if container.Ports == nil {
		container.Ports = make([]PortMapping, 0)
	}

	projectName := strings.TrimSpace(container.Labels[composeProjectLabel])
	if projectName == "" {
		container.ProjectID = standaloneProjectID
		container.ProjectName = standaloneProjectName
		container.ServiceName = ""
		return
	}
	container.ProjectID = "compose:" + projectName
	container.ProjectName = projectName
	container.ServiceName = strings.TrimSpace(container.Labels[composeServiceLabel])
}

func groupProjects(containers []InventoryContainer) []Project {
	projects := make(map[string]*Project)
	for _, container := range containers {
		project, exists := projects[container.ProjectID]
		if !exists {
			project = &Project{
				ID:   container.ProjectID,
				Name: container.ProjectName,
				Kind: projectKindFor(container.ProjectID),
			}
			if project.Kind == ProjectKindCompose {
				project.WorkingDirectory = strings.TrimSpace(container.Labels[composeWorkingDirLabel])
				project.ConfigFiles = splitComposeConfigFiles(container.Labels[composeConfigFilesLabel])
			}
			projects[container.ProjectID] = project
		}
		project.ContainerCount++
		if container.State == "running" {
			project.RunningCount++
		}
	}

	result := make([]Project, 0, len(projects))
	for _, project := range projects {
		if project.ConfigFiles == nil {
			project.ConfigFiles = make([]string, 0)
		}
		project.State = projectState(project.ContainerCount, project.RunningCount)
		result = append(result, *project)
	}
	sort.Slice(result, func(left, right int) bool {
		leftRank := projectKindRank(result[left].Kind)
		rightRank := projectKindRank(result[right].Kind)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return result[left].Name < result[right].Name
	})
	return result
}

func splitComposeConfigFiles(value string) []string {
	result := make([]string, 0)
	for _, item := range strings.Split(value, ",") {
		path := strings.TrimSpace(item)
		if path != "" {
			result = append(result, path)
		}
	}
	return result
}

func projectKindFor(projectID string) ProjectKind {
	if projectID == standaloneProjectID {
		return ProjectKindStandalone
	}
	return ProjectKindCompose
}

func projectKindRank(kind ProjectKind) int {
	if kind == ProjectKindCompose {
		return 0
	}
	return 1
}

func projectState(containerCount, runningCount int) string {
	if runningCount == 0 {
		return "stopped"
	}
	if runningCount == containerCount {
		return "running"
	}
	return "degraded"
}

func cloneLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return make(map[string]string)
	}
	result := make(map[string]string, len(labels))
	for key, value := range labels {
		result[key] = value
	}
	return result
}

type mobyInventoryGateway struct {
	client *client.Client
}

func NewMobyInventoryGateway() (InventoryGateway, error) {
	apiClient, err := client.New(client.WithHost(localDockerHost), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &mobyInventoryGateway{client: apiClient}, nil
}

func (g *mobyInventoryGateway) EngineInfo(ctx context.Context) (EngineInfo, error) {
	response, err := g.client.Info(ctx, client.InfoOptions{})
	if err != nil {
		return EngineInfo{}, err
	}
	info := response.Info
	return EngineInfo{
		ServerVersion:     info.ServerVersion,
		OperatingSystem:   info.OperatingSystem,
		Architecture:      info.Architecture,
		Containers:        info.Containers,
		ContainersRunning: info.ContainersRunning,
		ContainersStopped: info.ContainersStopped,
		Images:            info.Images,
	}, nil
}

func (g *mobyInventoryGateway) ListInventoryContainers(ctx context.Context) ([]InventoryContainer, error) {
	response, err := g.client.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, err
	}

	result := make([]InventoryContainer, 0, len(response.Items))
	for _, item := range response.Items {
		name := ""
		if len(item.Names) > 0 {
			name = item.Names[0]
		}
		ports := make([]PortMapping, 0, len(item.Ports))
		for _, port := range item.Ports {
			hostIP := ""
			if port.IP.IsValid() {
				hostIP = port.IP.String()
			}
			ports = append(ports, PortMapping{
				HostIP:      hostIP,
				PrivatePort: port.PrivatePort,
				PublicPort:  port.PublicPort,
				Protocol:    port.Type,
			})
		}
		result = append(result, InventoryContainer{
			ID:        item.ID,
			Name:      name,
			Image:     item.Image,
			State:     string(item.State),
			Status:    item.Status,
			CreatedAt: time.Unix(item.Created, 0).UTC(),
			Labels:    cloneLabels(item.Labels),
			Ports:     ports,
		})
	}
	return result, nil
}

type unavailableInventoryGateway struct{}

func (unavailableInventoryGateway) EngineInfo(context.Context) (EngineInfo, error) {
	return EngineInfo{}, errors.New("inventory gateway is not configured")
}

func (unavailableInventoryGateway) ListInventoryContainers(context.Context) ([]InventoryContainer, error) {
	return nil, errors.New("inventory gateway is not configured")
}
