package httpapi

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/go-chi/chi/v5"
)

type Site struct {
	ID            string     `json:"id"`
	ProjectID     string     `json:"projectId"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	IconURL       string     `json:"iconUrl"`
	Category      string     `json:"category"`
	State         string     `json:"state"`
	PrimaryPort   int        `json:"primaryPort"`
	Ports         []int      `json:"ports"`
	LaunchURL     string     `json:"launchUrl"`
	Favorite      bool       `json:"favorite"`
	SortOrder     int        `json:"sortOrder"`
	LastVisitedAt *time.Time `json:"lastVisitedAt"`
	Hidden        bool       `json:"hidden"`
	Source        string     `json:"source"`
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
	input.ProjectID = siteProjectID(request)
	profile, err := api.controlStore.UpsertSiteProfile(request.Context(), input)
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "SITE_INPUT_INVALID", "站点资料参数无效。")
		return
	}
	writeJSON(response, http.StatusOK, profile)
}

func (api *handler) recordSiteVisit(response http.ResponseWriter, request *http.Request) {
	if api.controlStore == nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "SITES_UNAVAILABLE", "站点资料暂不可用。")
		return
	}
	projectID := siteProjectID(request)
	visitedAt, err := api.controlStore.RecordSiteVisit(request.Context(), projectID)
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "SITE_VISIT_INVALID", "站点访问记录保存失败。")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"projectId":     projectID,
		"lastVisitedAt": visitedAt,
	})
}

func siteProjectID(request *http.Request) string {
	projectID := chi.URLParam(request, "projectID")
	if decodedProjectID, err := url.PathUnescape(projectID); err == nil {
		return decodedProjectID
	}
	return projectID
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
		applySiteProfile(&site, siteProfileFromLabels(inventory.Containers, project.ID), "labels")
		if profile, ok := builtInSiteProfile(project.Name); ok {
			applySiteProfile(&site, profile, "built-in")
		}
		if profile, ok := profileByProject[project.ID]; ok {
			applySiteProfile(&site, profile, "edited")
		}
		result = append(result, site)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Favorite != result[right].Favorite {
			return result[left].Favorite
		}
		if result[left].SortOrder != result[right].SortOrder {
			return result[left].SortOrder < result[right].SortOrder
		}
		if result[left].LastVisitedAt != nil || result[right].LastVisitedAt != nil {
			if result[left].LastVisitedAt == nil {
				return false
			}
			if result[right].LastVisitedAt == nil {
				return true
			}
			if !result[left].LastVisitedAt.Equal(*result[right].LastVisitedAt) {
				return result[left].LastVisitedAt.After(*result[right].LastVisitedAt)
			}
		}
		if result[left].State != result[right].State {
			return result[left].State == "running"
		}
		return strings.ToLower(result[left].Name) < strings.ToLower(result[right].Name)
	})
	return result
}

func applySiteProfile(site *Site, profile controlstore.SiteProfile, source string) {
	changed := false
	if profile.Name != "" {
		site.Name = profile.Name
		changed = true
	}
	if profile.Description != "" {
		site.Description = profile.Description
		changed = true
	}
	if profile.IconURL != "" {
		site.IconURL = profile.IconURL
		changed = true
	}
	if profile.Category != "" {
		site.Category = profile.Category
		changed = true
	}
	if containsPort(site.Ports, profile.PrimaryPort) {
		site.PrimaryPort = profile.PrimaryPort
		changed = true
	}
	if profile.LaunchURL != "" {
		site.LaunchURL = profile.LaunchURL
		changed = true
	}
	site.Favorite = profile.Favorite
	site.SortOrder = profile.SortOrder
	site.LastVisitedAt = profile.LastVisitedAt
	site.Hidden = profile.Hidden
	if changed {
		site.Source = source
	}
}

func siteProfileFromLabels(containers []docker.InventoryContainer, projectID string) controlstore.SiteProfile {
	result := controlstore.SiteProfile{}
	for _, container := range containers {
		if container.ProjectID != projectID {
			continue
		}
		if result.Name == "" {
			result.Name = firstLabel(container.Labels, "com.ncp.site.name", "io.ncp.site.name")
		}
		if result.Description == "" {
			result.Description = firstLabel(container.Labels, "com.ncp.site.description", "io.ncp.site.description", "org.opencontainers.image.description")
		}
		if result.IconURL == "" {
			result.IconURL = firstLabel(container.Labels, "com.ncp.site.icon", "io.ncp.site.icon")
		}
		if result.Category == "" {
			result.Category = firstLabel(container.Labels, "com.ncp.site.category", "io.ncp.site.category")
		}
		if result.LaunchURL == "" {
			result.LaunchURL = firstLabel(container.Labels, "com.ncp.site.url", "io.ncp.site.url")
		}
		if result.PrimaryPort == 0 {
			value, _ := strconv.Atoi(firstLabel(container.Labels, "com.ncp.site.port", "io.ncp.site.port"))
			result.PrimaryPort = value
		}
	}
	return result
}

func firstLabel(labels map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(labels[key]); value != "" {
			return value
		}
	}
	return ""
}

func builtInSiteProfile(name string) (controlstore.SiteProfile, bool) {
	profiles := map[string]controlstore.SiteProfile{
		"nas-control-plane": {
			Name:        "NAS 管理面板",
			Description: "统一查看 NAS 资源、Docker、数据库与系统运行状态。",
			Category:    "管理工具",
		},
		"nas-file-browser": {
			Name:        "NAS 文件浏览器",
			Description: "浏览、预览和编辑 NAS 文件，集中管理多个存储卷。",
			Category:    "文件与 NAS",
		},
		"heimdall": {
			Name:        "Heimdall",
			Description: "管理 AI 模型路由，并查看请求、Token 与延迟统计。",
			Category:    "AI 工具",
			PrimaryPort: 8889,
		},
		"mihomo": {
			Name:        "Mihomo",
			Description: "管理 NAS 网络代理配置、节点、规则与实时连接。",
			Category:    "网络服务",
		},
		"claude-code": {
			Name:        "Claude Code",
			Description: "在浏览器中使用 Claude Code 处理 NAS 上的开发任务。",
			Category:    "AI 工具",
		},
		"film-forest": {
			Name:        "影视森林",
			Description: "整理影视资源、浏览影片资料并维护个人媒体库。",
			Category:    "影音服务",
		},
		"ddns-go": {
			Name:        "DDNS-GO",
			Description: "自动同步公网地址到 DNS 服务商，维护 NAS 的远程访问域名。",
			Category:    "网络服务",
		},
		"openclaw": {
			Name:        "OpenClaw",
			Description: "统一运行与管理本地 AI 助手、渠道接入和自动化任务。",
			Category:    "AI 工具",
		},
		"hermes": {
			Name:        "Hermes",
			Description: "面向 NAS 的 AI Agent 工作台，用于执行开发与自动化任务。",
			Category:    "AI 工具",
		},
		"firefox": {
			Name:        "Firefox",
			Description: "运行在 NAS 上的远程浏览器，用于局域网网页访问和调试。",
			Category:    "效率工具",
		},
	}
	profile, ok := profiles[strings.ToLower(strings.TrimSpace(name))]
	return profile, ok
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
	switch inferSiteCategory(project.Name) {
	case "文件与 NAS":
		return "浏览和管理该项目提供的 NAS 文件与存储功能。"
	case "影音服务":
		return "访问该项目的媒体浏览、整理与播放界面。"
	case "AI 工具":
		return "进入该项目的 AI 工作台、任务或模型管理界面。"
	case "网络服务":
		return "查看该项目的网络状态、规则与连接配置。"
	default:
		return project.Name + " 的 Web 管理入口。"
	}
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
