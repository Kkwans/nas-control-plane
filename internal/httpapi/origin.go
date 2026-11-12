package httpapi

import (
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// protectBrowserWrites 在认证和业务处理前执行；不为任何来源或路由添加豁免。
// 缺少浏览器来源头的命令行客户端仍需通过后续的会话认证。
func (api *handler) protectBrowserWrites(next http.Handler) http.Handler {
	protection := http.NewCrossOriginProtection()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		safe := r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions
		if !safe && (!validRequestOrigin(r.Header) || protection.Check(r) != nil) {
			api.writeError(w, r, http.StatusForbidden, "HTTP_CROSS_ORIGIN_REJECTED", "请求来源校验失败，请从当前控制台页面重试。")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// validRequestOrigin 仅检查浏览器 Origin 的语法，不替代标准库的来源判定。
// 必须区分头不存在与空值；null、重复值和 URL 的非 authority 部分均拒绝。
func validRequestOrigin(header http.Header) bool {
	values, exists := header["Origin"]
	if !exists {
		return true
	}
	if len(values) != 1 {
		return false
	}
	origin := values[0]
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		u.Opaque != "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(origin, "?#") ||
		origin != u.Scheme+"://"+u.Host {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	if strings.HasPrefix(u.Host, "[") {
		address, err := netip.ParseAddr(host)
		if err != nil || !address.Is6() || address.Zone() != "" {
			return false
		}
		suffix := strings.TrimPrefix(u.Host, "["+host+"]")
		if suffix != "" && !strings.HasPrefix(suffix, ":") {
			return false
		}
	} else {
		for _, c := range host {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_') {
				return false
			}
		}
	}
	if strings.HasSuffix(u.Host, ":") {
		return false
	}
	if port := u.Port(); port != "" {
		number, err := strconv.ParseUint(port, 10, 16)
		if err != nil || number > 65535 {
			return false
		}
	}
	return true
}
