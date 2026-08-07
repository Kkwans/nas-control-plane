package httpapi

import (
	"errors"
	"net"
	"strings"

	"github.com/Kkwans/nas-control-plane/internal/system"
)

func validateDNSChangeRequest(request system.DNSChangeRequest) error {
	if len(request.Nameservers) == 0 || len(request.Nameservers) > 6 {
		return errors.New("nameservers 必须包含 1 到 6 个 IP 地址。")
	}
	seen := map[string]struct{}{}
	for _, value := range request.Nameservers {
		value = strings.TrimSpace(value)
		if net.ParseIP(value) == nil {
			return errors.New("nameservers 只能包含合法 IP 地址。")
		}
		if _, ok := seen[value]; ok {
			return errors.New("nameservers 不能重复。")
		}
		seen[value] = struct{}{}
	}
	if len(request.SearchDomains) > 8 {
		return errors.New("searchDomains 最多包含 8 个域名。")
	}
	for _, domain := range request.SearchDomains {
		if !validDNSDomain(domain) {
			return errors.New("searchDomains 包含无效域名。")
		}
	}
	return nil
}

func validDNSDomain(value string) bool {
	value = strings.TrimSuffix(strings.TrimSpace(value), ".")
	if value == "" || len(value) > 253 || strings.ContainsAny(value, "/\\\x00\r\n") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if character != '-' && character != '_' && (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') {
				return false
			}
		}
	}
	return true
}
