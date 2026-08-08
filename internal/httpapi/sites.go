package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/controlstore"
	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const maxSiteIconBytes = 2 << 20

var siteIconTypes = map[string]string{
	"image/png":     ".png",
	"image/jpeg":    ".jpg",
	"image/webp":    ".webp",
	"image/svg+xml": ".svg",
}

type siteProfileStore interface {
	SiteProfiles(context.Context) ([]controlstore.SiteProfile, error)
	UpsertSiteProfile(context.Context, controlstore.SiteProfile) (controlstore.SiteProfile, error)
	DeleteSiteProfile(context.Context, string) error
	SetSiteIgnored(context.Context, string, bool) error
}

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
	hostCandidates := make([]docker.HostSiteCandidate, 0)
	if discovery, ok := api.agent.(HostSiteDiscoveryAgentClient); ok {
		discoveryContext, cancel := context.WithTimeout(request.Context(), 8*time.Second)
		if candidates, discoveryErr := discovery.DiscoverHostSiteCandidates(discoveryContext, api.agentSocketPath); discoveryErr == nil {
			hostCandidates = candidates
		}
		cancel()
	}
	writeJSON(response, http.StatusOK, SiteListResponse{
		CollectedAt: inventory.CollectedAt,
		Sites:       api.discoveredSites(request.Context(), requestHostName(request), inventory, profiles, hostCandidates),
	})
}

func requestHostName(request *http.Request) string {
	host := request.Host
	if value, _, err := net.SplitHostPort(host); err == nil {
		return value
	}
	return strings.Trim(host, "[]")
}

func (api *handler) discoveredSites(ctx context.Context, publicHost string, inventory docker.Inventory, profiles []controlstore.SiteProfile, hostCandidates []docker.HostSiteCandidate) []Site {
	candidates := mergeSites(inventory, profiles, hostCandidates)
	prober, canProbe := api.agent.(WebProbeAgentClient)
	if !canProbe {
		result := candidates[:0]
		for _, site := range candidates {
			if hasExplicitSiteLabel(inventory.Containers, site.ProjectID) || site.Source == "manual" {
				result = append(result, site)
			}
		}
		return result
	}
	type probeOutcome struct {
		index int
		probe agentsocket.WebProbeResult
		port  int
		ok    bool
	}
	outcomes := make(chan probeOutcome, len(candidates))
	limiter := make(chan struct{}, 6)
	var group sync.WaitGroup
	for index, site := range candidates {
		if siteCanSkipWebProbe(site, inventory.Containers) {
			outcomes <- probeOutcome{index: index, ok: true}
			continue
		}
		group.Add(1)
		go func(index int, site Site) {
			defer group.Done()
			limiter <- struct{}{}
			defer func() { <-limiter }()
			for _, port := range site.Ports {
				probeContext, cancel := context.WithTimeout(ctx, 4*time.Second)
				probe, err := prober.ProbeWeb(probeContext, api.agentSocketPath, fmt.Sprintf("http://127.0.0.1:%d/", port))
				cancel()
				if err == nil && probe.StatusCode >= 200 && probe.StatusCode < 500 {
					outcomes <- probeOutcome{index: index, probe: probe, port: port, ok: true}
					return
				}
			}
			outcomes <- probeOutcome{index: index}
		}(index, site)
	}
	group.Wait()
	close(outcomes)
	verified := make(map[int]probeOutcome, len(candidates))
	for outcome := range outcomes {
		verified[outcome.index] = outcome
	}
	result := make([]Site, 0, len(candidates))
	for index, site := range candidates {
		outcome := verified[index]
		if !outcome.ok {
			continue
		}
		if outcome.port > 0 {
			site.PrimaryPort = outcome.port
			if outcome.probe.Title != "" && (site.Source == "auto" || site.Source == "built-in") {
				site.Name = outcome.probe.Title
			}
			site.LaunchURL = publicSiteURL(publicHost, outcome.port, outcome.probe.URL)
			if site.IconURL == "" {
				site.IconURL = publicSiteURL(publicHost, outcome.port, outcome.probe.IconURL)
			}
		}
		result = append(result, site)
	}
	return result
}

func siteCanSkipWebProbe(site Site, containers []docker.InventoryContainer) bool {
	if site.Source == "manual" {
		return true
	}
	// Host-network entries are port-specific. A project-level label on one UI
	// container must not allow unrelated JSON backends from the same project.
	return !strings.Contains(site.ID, "@") && hasExplicitSiteLabel(containers, site.ProjectID)
}

func hasExplicitSiteLabel(containers []docker.InventoryContainer, projectID string) bool {
	for _, container := range containers {
		if container.ProjectID != projectID {
			continue
		}
		for key, value := range container.Labels {
			if strings.TrimSpace(value) != "" && (strings.HasPrefix(key, "com.ncp.site.") || strings.HasPrefix(key, "io.ncp.site.")) {
				return true
			}
		}
	}
	return false
}

func publicSiteURL(publicHost string, port int, probeURL string) string {
	parsed, err := url.Parse(probeURL)
	if err != nil || parsed.Path == "" {
		parsed = &url.URL{Path: "/"}
	}
	parsed.Scheme = "http"
	parsed.Host = net.JoinHostPort(publicHost, strconv.Itoa(port))
	return parsed.String()
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
	if existing, ok := api.siteProfile(request.Context(), input.ProjectID); ok {
		if existing.Source == "manual" {
			input.Source = "manual"
		} else {
			input.Source = "edited"
		}
		input.Ignored = existing.Ignored
		input.DetectedTitle = existing.DetectedTitle
		input.AutoIconURL = existing.AutoIconURL
		input.LocalIconName = existing.LocalIconName
	}
	profile, err := api.controlStore.UpsertSiteProfile(request.Context(), input)
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "SITE_INPUT_INVALID", "站点资料参数无效。")
		return
	}
	writeJSON(response, http.StatusOK, profile)
}

func (api *handler) createSite(response http.ResponseWriter, request *http.Request) {
	if api.controlStore == nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "SITES_UNAVAILABLE", "站点资料暂不可用。")
		return
	}
	var input controlstore.SiteProfile
	if !api.decodeControlBody(response, request, &input) {
		return
	}
	input.ProjectID = "manual:" + uuid.NewString()
	input.Source = "manual"
	input.Ignored = false
	profile, err := api.controlStore.UpsertSiteProfile(request.Context(), input)
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "SITE_INPUT_INVALID", "站点资料参数无效。")
		return
	}
	writeJSON(response, http.StatusCreated, profile)
}

func (api *handler) deleteSite(response http.ResponseWriter, request *http.Request) {
	store, ok := api.controlStore.(siteProfileStore)
	if !ok {
		api.writeError(response, request, http.StatusServiceUnavailable, "SITES_UNAVAILABLE", "站点资料暂不可用。")
		return
	}
	siteID := siteProjectID(request)
	profile, exists := api.siteProfile(request.Context(), siteID)
	var err error
	if exists && profile.Source == "manual" {
		err = store.DeleteSiteProfile(request.Context(), siteID)
	} else {
		err = store.SetSiteIgnored(request.Context(), siteID, true)
	}
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "SITE_DELETE_FAILED", "站点删除失败。")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (api *handler) ignoredSites(response http.ResponseWriter, request *http.Request) {
	if api.controlStore == nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "SITES_UNAVAILABLE", "站点资料暂不可用。")
		return
	}
	profiles, err := api.controlStore.SiteProfiles(request.Context())
	if err != nil {
		api.writeError(response, request, http.StatusInternalServerError, "SITES_READ_FAILED", "站点资料读取失败。")
		return
	}
	ignored := make([]controlstore.SiteProfile, 0)
	for _, profile := range profiles {
		if profile.Ignored {
			ignored = append(ignored, profile)
		}
	}
	writeJSON(response, http.StatusOK, ignored)
}

func (api *handler) restoreSite(response http.ResponseWriter, request *http.Request) {
	store, ok := api.controlStore.(siteProfileStore)
	if !ok {
		api.writeError(response, request, http.StatusServiceUnavailable, "SITES_UNAVAILABLE", "站点资料暂不可用。")
		return
	}
	siteID := siteProjectID(request)
	if err := store.SetSiteIgnored(request.Context(), siteID, false); err != nil {
		api.writeError(response, request, http.StatusBadRequest, "SITE_RESTORE_FAILED", "站点恢复失败。")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"siteId": siteID, "ignored": false})
}

func (api *handler) uploadSiteIcon(response http.ResponseWriter, request *http.Request) {
	if api.siteAssetsDirectory == "" || api.controlStore == nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "SITE_ICON_UNAVAILABLE", "站点图标存储暂不可用。")
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, maxSiteIconBytes+64*1024)
	if err := request.ParseMultipartForm(maxSiteIconBytes); err != nil {
		api.writeError(response, request, http.StatusBadRequest, "SITE_ICON_INVALID", "图标文件无效或超过 2 MB。")
		return
	}
	file, _, err := request.FormFile("icon")
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "SITE_ICON_REQUIRED", "请选择要上传的图标文件。")
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxSiteIconBytes+1))
	if err != nil || len(content) == 0 || len(content) > maxSiteIconBytes {
		api.writeError(response, request, http.StatusBadRequest, "SITE_ICON_INVALID", "图标文件无效或超过 2 MB。")
		return
	}
	contentType := http.DetectContentType(content)
	if len(content) >= 5 && strings.Contains(strings.ToLower(string(content[:min(len(content), 256)])), "<svg") {
		contentType = "image/svg+xml"
	}
	extension, ok := siteIconTypes[contentType]
	if !ok {
		api.writeError(response, request, http.StatusBadRequest, "SITE_ICON_TYPE_UNSUPPORTED", "仅支持 PNG、JPEG、WebP 和 SVG 图标。")
		return
	}
	siteID := siteProjectID(request)
	profile, exists := api.siteProfile(request.Context(), siteID)
	if !exists {
		api.writeError(response, request, http.StatusNotFound, "SITE_NOT_FOUND", "站点不存在。")
		return
	}
	if err := os.MkdirAll(api.siteAssetsDirectory, 0o750); err != nil {
		api.writeError(response, request, http.StatusInternalServerError, "SITE_ICON_SAVE_FAILED", "图标保存失败。")
		return
	}
	fileName := uuid.NewString() + extension
	target := filepath.Join(api.siteAssetsDirectory, fileName)
	if err := os.WriteFile(target, content, 0o640); err != nil {
		api.writeError(response, request, http.StatusInternalServerError, "SITE_ICON_SAVE_FAILED", "图标保存失败。")
		return
	}
	previous := profile.LocalIconName
	profile.LocalIconName = fileName
	if _, err := api.controlStore.UpsertSiteProfile(request.Context(), profile); err != nil {
		_ = os.Remove(target)
		api.writeError(response, request, http.StatusInternalServerError, "SITE_ICON_SAVE_FAILED", "图标资料保存失败。")
		return
	}
	if previous != "" {
		_ = removeSiteIconFile(api.siteAssetsDirectory, previous)
	}
	writeJSON(response, http.StatusOK, map[string]string{
		"siteId":  siteID,
		"iconUrl": "/api/v1/sites/" + url.PathEscape(siteID) + "/icon",
	})
}

func (api *handler) siteIcon(response http.ResponseWriter, request *http.Request) {
	profile, ok := api.siteProfile(request.Context(), siteProjectID(request))
	if !ok || profile.LocalIconName == "" || api.siteAssetsDirectory == "" {
		api.writeError(response, request, http.StatusNotFound, "SITE_ICON_NOT_FOUND", "站点图标不存在。")
		return
	}
	target, err := safeSiteIconPath(api.siteAssetsDirectory, profile.LocalIconName)
	if err != nil {
		api.writeError(response, request, http.StatusNotFound, "SITE_ICON_NOT_FOUND", "站点图标不存在。")
		return
	}
	response.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(response, request, target)
}

func (api *handler) deleteSiteIcon(response http.ResponseWriter, request *http.Request) {
	if api.controlStore == nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "SITE_ICON_UNAVAILABLE", "站点图标存储暂不可用。")
		return
	}
	profile, ok := api.siteProfile(request.Context(), siteProjectID(request))
	if !ok {
		api.writeError(response, request, http.StatusNotFound, "SITE_NOT_FOUND", "站点不存在。")
		return
	}
	previous := profile.LocalIconName
	profile.LocalIconName = ""
	if _, err := api.controlStore.UpsertSiteProfile(request.Context(), profile); err != nil {
		api.writeError(response, request, http.StatusInternalServerError, "SITE_ICON_DELETE_FAILED", "图标删除失败。")
		return
	}
	if previous != "" && api.siteAssetsDirectory != "" {
		_ = removeSiteIconFile(api.siteAssetsDirectory, previous)
	}
	response.WriteHeader(http.StatusNoContent)
}

func safeSiteIconPath(directory, fileName string) (string, error) {
	if fileName != filepath.Base(fileName) || fileName == "." || fileName == "" {
		return "", errors.New("site icon filename is invalid")
	}
	return filepath.Join(directory, fileName), nil
}

func removeSiteIconFile(directory, fileName string) error {
	target, err := safeSiteIconPath(directory, fileName)
	if err != nil {
		return err
	}
	err = os.Remove(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (api *handler) siteProfile(ctx context.Context, siteID string) (controlstore.SiteProfile, bool) {
	if api.controlStore == nil {
		return controlstore.SiteProfile{}, false
	}
	profiles, err := api.controlStore.SiteProfiles(ctx)
	if err != nil {
		return controlstore.SiteProfile{}, false
	}
	for _, profile := range profiles {
		if profile.ProjectID == siteID {
			return profile, true
		}
	}
	return controlstore.SiteProfile{}, false
}

func (api *handler) recordSiteVisit(response http.ResponseWriter, request *http.Request) {
	if api.controlStore == nil {
		api.writeError(response, request, http.StatusServiceUnavailable, "SITES_UNAVAILABLE", "站点资料暂不可用。")
		return
	}
	siteID := siteProjectID(request)
	visitedAt, err := api.controlStore.RecordSiteVisit(request.Context(), siteID)
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "SITE_VISIT_INVALID", "站点访问记录保存失败。")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"siteId":        siteID,
		"lastVisitedAt": visitedAt,
	})
}

func siteProjectID(request *http.Request) string {
	projectID := chi.URLParam(request, "siteID")
	if decodedProjectID, err := url.PathUnescape(projectID); err == nil {
		return decodedProjectID
	}
	return projectID
}

func mergeSites(inventory docker.Inventory, profiles []controlstore.SiteProfile, hostCandidates []docker.HostSiteCandidate) []Site {
	profileByProject := make(map[string]controlstore.SiteProfile, len(profiles))
	for _, profile := range profiles {
		profileByProject[profile.ProjectID] = profile
	}
	hostPortsByProject := make(map[string][]int)
	for _, candidate := range hostCandidates {
		for _, port := range candidate.Ports {
			if port > 0 && port <= 65535 && likelyHTTPPort(port) && !containsPort(hostPortsByProject[candidate.ProjectID], port) {
				hostPortsByProject[candidate.ProjectID] = append(hostPortsByProject[candidate.ProjectID], port)
			}
		}
	}
	for projectID := range hostPortsByProject {
		sort.Ints(hostPortsByProject[projectID])
	}

	result := make([]Site, 0, len(inventory.Projects)+len(hostCandidates))
	for _, project := range inventory.Projects {
		ports := sitePorts(inventory.Containers, project.ID)
		if len(ports) > 0 {
			site, ignored := mergedProjectSite(project, project.ID, ports, inventory.Containers, profileByProject, project.ID)
			if !ignored {
				result = append(result, site)
			}
			delete(profileByProject, project.ID)
		}
		hostPorts := hostPortsByProject[project.ID]
		for index, port := range hostPorts {
			siteID := siteEntryID(project.ID, port)
			profileID := siteID
			if _, exists := profileByProject[siteID]; !exists && index == 0 && len(ports) == 0 {
				if _, legacyExists := profileByProject[project.ID]; legacyExists {
					profileID = project.ID
				}
			}
			site, ignored := mergedProjectSite(project, siteID, []int{port}, inventory.Containers, profileByProject, profileID)
			if !ignored {
				result = append(result, site)
			}
			delete(profileByProject, profileID)
		}
	}
	for _, profile := range profileByProject {
		if profile.Source != "manual" || profile.Ignored {
			continue
		}
		site := Site{
			ID:            profile.ProjectID,
			ProjectID:     profile.ProjectID,
			Name:          profile.Name,
			Description:   profile.Description,
			IconURL:       preferredSiteIcon(profile),
			Category:      profile.Category,
			State:         "stopped",
			PrimaryPort:   profile.PrimaryPort,
			Ports:         make([]int, 0),
			LaunchURL:     profile.LaunchURL,
			Favorite:      profile.Favorite,
			SortOrder:     profile.SortOrder,
			LastVisitedAt: profile.LastVisitedAt,
			Hidden:        profile.Hidden,
			Source:        "manual",
		}
		if profile.PrimaryPort > 0 {
			site.Ports = []int{profile.PrimaryPort}
		}
		if profile.PrimaryPort > 0 || strings.TrimSpace(profile.LaunchURL) != "" {
			site.State = "running"
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

func mergedProjectSite(project docker.Project, siteID string, ports []int, containers []docker.InventoryContainer, profiles map[string]controlstore.SiteProfile, profileID string) (Site, bool) {
	site := Site{
		ID:          siteID,
		ProjectID:   project.ID,
		Name:        project.Name,
		Description: defaultSiteDescription(project),
		Category:    inferSiteCategory(project.Name),
		State:       project.State,
		PrimaryPort: ports[0],
		Ports:       ports,
		Source:      "auto",
	}
	applySiteProfile(&site, siteProfileFromLabels(containers, project.ID), "labels")
	if profile, ok := builtInSiteProfile(project.Name); ok {
		applySiteProfile(&site, profile, "built-in")
	}
	if profile, ok := profiles[profileID]; ok {
		if profile.Ignored {
			return Site{}, true
		}
		applySiteProfile(&site, profile, "edited")
	}
	return site, false
}

func siteEntryID(projectID string, port int) string {
	return projectID + "@" + strconv.Itoa(port)
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
	if iconURL := preferredSiteIcon(profile); iconURL != "" {
		site.IconURL = iconURL
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

func preferredSiteIcon(profile controlstore.SiteProfile) string {
	if profile.LocalIconName != "" {
		return "/api/v1/sites/" + url.PathEscape(profile.ProjectID) + "/icon"
	}
	if profile.IconURL != "" {
		return profile.IconURL
	}
	return profile.AutoIconURL
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
