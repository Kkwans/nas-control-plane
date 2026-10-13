package httpapi

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	maxSiteIconBytes    = 2 << 20
	siteIconFallbackSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48"><rect width="48" height="48" rx="12" fill="#edf3fc"/><circle cx="24" cy="24" r="12" fill="none" stroke="#6f86a3" stroke-width="2"/><path d="M12 24h24M24 12c4 4 6 8 6 12s-2 8-6 12c-4-4-6-8-6-12s2-8 6-12Z" fill="none" stroke="#6f86a3" stroke-width="2" stroke-linecap="round"/></svg>`
)

var siteIconTypes = map[string]string{
	"image/png":     ".png",
	"image/jpeg":    ".jpg",
	"image/webp":    ".webp",
	"image/svg+xml": ".svg",
}

type siteIconHTTPClient interface {
	Do(*http.Request) (*http.Response, error)
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
	swapped, err := api.controlStore.CompareAndSwapSiteIcon(request.Context(), siteID, previous, fileName)
	if err != nil {
		// 提交结果可能不确定，保留候选文件，避免删除数据库已引用的图标。
		api.writeError(response, request, http.StatusInternalServerError, "SITE_ICON_SAVE_FAILED", "图标资料保存失败。")
		return
	}
	if !swapped {
		_ = os.Remove(target)
		api.writeError(response, request, http.StatusConflict, "SITE_ICON_CONFLICT", "站点或图标已变更，请刷新后重试。")
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
	protectSiteIconResponse(response)
	http.ServeFile(response, request, target)
}

func (api *handler) siteIconProxy(response http.ResponseWriter, request *http.Request) {
	target, err := url.Parse(strings.TrimSpace(request.URL.Query().Get("url")))
	if err != nil || !validSiteIconProxyTarget(target, requestHostName(request)) {
		api.writeError(response, request, http.StatusBadRequest, "SITE_ICON_SOURCE_INVALID", "站点图标地址无效。")
		return
	}
	client := api.siteIconClient
	if client == nil {
		productionClient := &http.Client{
			Timeout: 4 * time.Second,
			Transport: &http.Transport{
				Proxy:       nil,
				DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
			},
			CheckRedirect: func(next *http.Request, _ []*http.Request) error {
				if !validSiteIconProxyTarget(next.URL, requestHostName(request)) {
					return errors.New("site icon redirect target is invalid")
				}
				return nil
			},
		}
		defer productionClient.CloseIdleConnections()
		client = productionClient
	}
	upstreamRequest, err := http.NewRequestWithContext(request.Context(), http.MethodGet, target.String(), nil)
	if err != nil {
		api.writeError(response, request, http.StatusBadRequest, "SITE_ICON_SOURCE_INVALID", "站点图标地址无效。")
		return
	}
	upstreamRequest.Header.Set("Accept", "image/avif,image/webp,image/svg+xml,image/png,image/jpeg,image/*;q=0.8")
	upstreamResponse, err := client.Do(upstreamRequest)
	if err != nil {
		writeSiteIconFallback(response)
		return
	}
	defer upstreamResponse.Body.Close()
	if upstreamResponse.StatusCode < http.StatusOK || upstreamResponse.StatusCode >= http.StatusMultipleChoices {
		writeSiteIconFallback(response)
		return
	}
	content, err := io.ReadAll(io.LimitReader(upstreamResponse.Body, maxSiteIconBytes+1))
	if err != nil || len(content) == 0 || len(content) > maxSiteIconBytes {
		writeSiteIconFallback(response)
		return
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(upstreamResponse.Header.Get("Content-Type"), ";")[0]))
	if contentType == "" || siteIconTypes[contentType] == "" {
		contentType = http.DetectContentType(content)
	}
	if len(content) >= 5 && strings.Contains(strings.ToLower(string(content[:min(len(content), 256)])), "<svg") {
		contentType = "image/svg+xml"
	}
	if siteIconTypes[contentType] == "" {
		writeSiteIconFallback(response)
		return
	}
	response.Header().Set("Cache-Control", "private, max-age=3600")
	response.Header().Set("Content-Type", contentType)
	protectSiteIconResponse(response)
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(content)
}

func writeSiteIconFallback(response http.ResponseWriter) {
	response.Header().Set("Cache-Control", "private, max-age=300")
	response.Header().Set("Content-Type", "image/svg+xml")
	protectSiteIconResponse(response)
	response.Header().Set("X-NCP-Icon-Fallback", "true")
	response.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(response, siteIconFallbackSVG)
}

// 图标即使被作为独立文档打开，也不能获得控制台同源脚本权限。
// 对所有图标统一设置，避免上传、代理和回退路径的策略再次分叉。
func protectSiteIconResponse(response http.ResponseWriter) {
	response.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	response.Header().Set("X-Content-Type-Options", "nosniff")
}

func validSiteIconProxyTarget(target *url.URL, publicHost string) bool {
	if target == nil || (target.Scheme != "http" && target.Scheme != "https") || target.User != nil {
		return false
	}
	if target.Hostname() == "" || !strings.EqualFold(strings.Trim(target.Hostname(), "[]"), strings.Trim(publicHost, "[]")) {
		return false
	}
	if value := target.Port(); value != "" {
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return false
		}
	}
	return true
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
	swapped, err := api.controlStore.CompareAndSwapSiteIcon(request.Context(), profile.ProjectID, previous, "")
	if err != nil {
		api.writeError(response, request, http.StatusInternalServerError, "SITE_ICON_DELETE_FAILED", "图标删除失败。")
		return
	}
	if !swapped {
		api.writeError(response, request, http.StatusConflict, "SITE_ICON_CONFLICT", "站点或图标已变更，请刷新后重试。")
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
