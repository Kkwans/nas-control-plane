package docker

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/moby/moby/client"
)

// HostSiteCandidate is the deliberately narrow result exposed by the Root
// Agent. It contains no environment values, labels, commands, or credentials.
type HostSiteCandidate struct {
	ProjectID   string `json:"projectId"`
	ContainerID string `json:"containerId"`
	Ports       []int  `json:"ports"`
}

type HostSiteCandidateCollector struct {
	client *client.Client
}

func NewLiveHostSiteCandidateCollector() (*HostSiteCandidateCollector, error) {
	apiClient, err := client.New(client.WithHost(localDockerHost), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &HostSiteCandidateCollector{client: apiClient}, nil
}

func (c *HostSiteCandidateCollector) Collect(ctx context.Context) ([]HostSiteCandidate, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("host site candidate collector is unavailable")
	}
	containers, err := c.client.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, err
	}
	result := make([]HostSiteCandidate, 0)
	for _, item := range containers.Items {
		response, err := c.client.ContainerInspect(ctx, item.ID, client.ContainerInspectOptions{})
		inspect := response.Container
		if err != nil || inspect.State == nil || !inspect.State.Running || inspect.HostConfig == nil || inspect.Config == nil {
			continue
		}
		if string(inspect.HostConfig.NetworkMode) != "host" {
			continue
		}
		exposedPorts := make([]string, 0, len(inspect.Config.ExposedPorts))
		for port := range inspect.Config.ExposedPorts {
			exposedPorts = append(exposedPorts, port.Port())
		}
		ports := safeHostSitePorts(inspect.Config.Env, inspect.Config.Entrypoint, inspect.Config.Cmd, exposedPorts)
		if len(ports) == 0 {
			continue
		}
		projectID := standaloneProjectID
		if projectName := strings.TrimSpace(inspect.Config.Labels[composeProjectLabel]); projectName != "" {
			projectID = "compose:" + projectName
		}
		result = append(result, HostSiteCandidate{ProjectID: projectID, ContainerID: item.ID, Ports: ports})
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].ProjectID != result[right].ProjectID {
			return result[left].ProjectID < result[right].ProjectID
		}
		return result[left].ContainerID < result[right].ContainerID
	})
	return result, nil
}

var commandPortPattern = regexp.MustCompile(`(?i)(?:^|\s)--?(?:server\.)?(?:http-)?port(?:=|\s+)([0-9]{2,5})(?:\s|$)`)

func safeHostSitePorts(environment, entrypoint, command, exposedPorts []string) []int {
	seen := make(map[int]struct{})
	add := func(value string) {
		port, err := strconv.Atoi(strings.TrimSpace(strings.SplitN(value, "/", 2)[0]))
		if err == nil && port > 0 && port <= 65535 {
			seen[port] = struct{}{}
		}
	}
	for _, port := range exposedPorts {
		add(port)
	}
	for _, variable := range environment {
		key, value, found := strings.Cut(variable, "=")
		key = strings.ToUpper(strings.TrimSpace(key))
		if found && (key == "PORT" || strings.HasSuffix(key, "_PORT")) {
			add(value)
		}
	}
	arguments := strings.Join(append(append([]string{}, entrypoint...), command...), " ")
	for _, match := range commandPortPattern.FindAllStringSubmatch(arguments, -1) {
		if len(match) > 1 {
			add(match[1])
		}
	}
	ports := make([]int, 0, len(seen))
	for port := range seen {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports
}
