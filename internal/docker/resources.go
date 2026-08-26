package docker

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	mobynetwork "github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

type Resources struct {
	CollectedAt time.Time `json:"collectedAt"`
	Networks    []Network `json:"networks"`
	Volumes     []Volume  `json:"volumes"`
}

type Network struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Driver   string   `json:"driver"`
	Scope    string   `json:"scope"`
	Internal bool     `json:"internal"`
	Subnets  []string `json:"subnets"`
	Gateways []string `json:"gateways"`
}

type Volume struct {
	Name       string `json:"name"`
	Driver     string `json:"driver"`
	Scope      string `json:"scope"`
	Mountpoint string `json:"mountpoint"`
}

type ResourceProvider interface {
	ListResources(context.Context) (Resources, error)
}

type ResourceCollector struct {
	client *client.Client
	now    func() time.Time
}

func NewLiveDockerResourceCollector() (*ResourceCollector, error) {
	apiClient, err := client.New(client.WithHost(localDockerHost), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &ResourceCollector{client: apiClient, now: time.Now}, nil
}

func NewResourceCollector(apiClient *client.Client) *ResourceCollector {
	return &ResourceCollector{client: apiClient, now: time.Now}
}

func (c *ResourceCollector) ListResources(ctx context.Context) (Resources, error) {
	if c == nil || c.client == nil {
		return Resources{}, errors.New("docker resource collector is not configured")
	}
	networks, err := c.client.NetworkList(ctx, client.NetworkListOptions{})
	if err != nil {
		return Resources{}, coded("DOCKER_RESOURCES_NETWORKS_UNAVAILABLE", err)
	}
	volumes, err := c.client.VolumeList(ctx, client.VolumeListOptions{})
	if err != nil {
		return Resources{}, coded("DOCKER_RESOURCES_VOLUMES_UNAVAILABLE", err)
	}
	result := Resources{CollectedAt: c.now().UTC(), Networks: make([]Network, 0, len(networks.Items)), Volumes: make([]Volume, 0, len(volumes.Items))}
	for _, item := range networks.Items {
		result.Networks = append(result.Networks, networkResource(item))
	}
	for _, item := range volumes.Items {
		result.Volumes = append(result.Volumes, Volume{Name: item.Name, Driver: item.Driver, Scope: item.Scope, Mountpoint: item.Mountpoint})
	}
	sort.Slice(result.Networks, func(i, j int) bool {
		return strings.ToLower(result.Networks[i].Name) < strings.ToLower(result.Networks[j].Name)
	})
	sort.Slice(result.Volumes, func(i, j int) bool {
		return strings.ToLower(result.Volumes[i].Name) < strings.ToLower(result.Volumes[j].Name)
	})
	return result, nil
}

func networkResource(item mobynetwork.Summary) Network {
	result := Network{ID: item.ID, Name: item.Name, Driver: item.Driver, Scope: item.Scope, Internal: item.Internal, Subnets: []string{}, Gateways: []string{}}
	for _, config := range item.IPAM.Config {
		if config.Subnet.IsValid() {
			result.Subnets = append(result.Subnets, config.Subnet.String())
		}
		if config.Gateway.IsValid() {
			result.Gateways = append(result.Gateways, config.Gateway.String())
		}
	}
	return result
}
