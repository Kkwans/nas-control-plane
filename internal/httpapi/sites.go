package httpapi

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/go-chi/chi/v5"
)

type Site struct {
	ID          string `json:"id"`
	ProjectID   string `json:"projectId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IconURL     string `json:"iconUrl"`
	Category    string `json:"category"`
	State       string `json:"state"`
	PrimaryPort int    `json:"primaryPort"`
	Ports       []int  `json:"ports"`
	Hidden      bool   `json:"hidden"`
	Source      string `json:"source"`
}

type SiteListResponse struct {
	CollectedAt time.Time `json:"collectedAt"`
	Sites       []Site    `json:"sites"`
}

func (api *handler) sites(response http.ResponseWriter, request *http.Request) {
	if api.controlStore == nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "SITES_UNAVAILABLE", "站点资料暂不可用。")
		return
	}
	inventory, ok := api.collectDockerInventory(response, request)
	if !ok {
		return
	}
	profiles, err := api.controlStore.SiteProfiles(request.Context())
	if err != nil {
		api.writeError(response, request, http.StatusInternalServerError, "SITES_READ_FAILED", "站点资料读取失败。")
		return
	}
	writeJSON(response, http.StatusOK, SiteListResponse{
		CollectedAt: inventory.CollectedAt,
		Sites:       mergeSites(inventory, profiles),
	})
}

func (api *handler) updateSite(response http.ResponseWriter, request *http.Request) {
	if api.controlStore == nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "SITES_UNAVAILABLE", "站点资料暂不可用。")
		return
	}
	var input controlstore.SiteProfile
	if !api.decodeControlBody(response, request, &input) {
		return
	}
	input.ProjectID = chi.URLParam(request, "projectID")
	profile, err := api.controlStore.UpsertSiteProfile(request.Context(), input)
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "SITE_INPUT_INVALID", "站点资料参数无效。")
		return
	}
	writeJSON(response, http.StatusOK, profile)
}

func mergeSites(inventory docker.Inventory, profiles []controlstore.SiteProfile) []Site {
	profileByProject := make(map[string]controlstore.SiteProfile, len(profiles))
	for _, profile := range profiles {
		profileByProject[profile.ProjectID] = profile
	}
	result := make([]Site, 0, len(inventory.Projects))
	for _, project := range inventory.Projects {
		ports := sitePorts(inventory.Containers, project.ID)
		if len(ports) == 0 {
			continue
		}
		site := Site{
			ID:          project.ID,
			ProjectID:   project.ID,
			Name:        project.Name,
			Description: defaultSiteDescription(project),
			Category:    inferSiteCategory(project.Name),
			State:       project.State,
			PrimaryPort: ports[0],
			Ports:       ports,
			Source:      "auto",
		}
		if profile, ok := profileByProject[project.ID]; ok {
			site.Name = profile.Name
			site.Description = profile.Description
			site.IconURL = profile.IconURL
			site.Category = profile.Category
			site.Hidden = profile.Hidden
			if containsPort(ports, profile.PrimaryPort) {
				site.PrimaryPort = profile.PrimaryPort
			}
			site.Source = "edited"
		}
		result = append(result, site)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].State != result[right].State {
			return result[left].State == "running"
		}
		return strings.ToLower(result[left].Name) < strings.ToLower(result[right].Name)
	})
	return result
}

func sitePorts(containers []docker.InventoryContainer, projectID string) []int {
	seen := make(map[int]struct{})
	for _, container := range containers {
		if container.ProjectID != projectID {
			continue
		}
		for _, port := range container.Ports {
			value := int(port.PublicPort)
			if value == 0 || !likelyHTTPPort(value) {
				continue
			}
			seen[value] = struct{}{}
		}
	}
	result := make([]int, 0, len(seen))
	for port := range seen {
		result = append(result, port)
	}
	sort.Ints(result)
	return result
}

func likelyHTTPPort(port int) bool {
	switch port {
	case 20, 21, 22, 23, 25, 53, 110, 143, 445, 465, 587, 993, 995,
		1883, 3306, 5432, 6379, 9092, 27017:
		return false
	default:
		return true
	}
}

func containsPort(ports []int, target int) bool {
	for _, port := range ports {
		if port == target {
			return true
		}
	}
	return false
}

func defaultSiteDescription(project docker.Project) string {
	return "由 Docker 自动发现的局域网站点，共 " + strconv.Itoa(project.ContainerCount) + " 个容器。"
}

func inferSiteCategory(name string) string {
	value := strings.ToLower(name)
	switch {
	case strings.Contains(value, "file"), strings.Contains(value, "nas"):
		return "文件与 NAS"
	case strings.Contains(value, "film"), strings.Contains(value, "media"), strings.Contains(value, "movie"):
		return "影音服务"
	case strings.Contains(value, "claw"), strings.Contains(value, "claude"), strings.Contains(value, "hermes"):
		return "AI 工具"
	case strings.Contains(value, "mihomo"), strings.Contains(value, "ddns"), strings.Contains(value, "tailscale"):
		return "网络服务"
	default:
		return "管理工具"
	}
}
