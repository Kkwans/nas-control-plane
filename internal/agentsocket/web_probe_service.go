package agentsocket

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"golang.org/x/net/html"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
)

const maxWebProbeResponseBytes = 1 << 20

type WebProbeResult struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	IconURL     string `json:"iconUrl"`
	ContentType string `json:"contentType"`
	StatusCode  int    `json:"statusCode"`
}

type webProbeService struct {
	client    *http.Client
	hostSites *docker.HostSiteCandidateCollector
}

func newWebProbeService() *webProbeService {
	hostSites, _ := docker.NewLiveHostSiteCandidateCollector()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &webProbeService{client: &http.Client{
		Transport: transport,
		Timeout:   3 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 4 {
				return errors.New("too many redirects")
			}
			return validateLocalProbeURL(request.URL)
		},
	}, hostSites: hostSites}
}

func (service *webProbeService) DiscoverHostSites(ctx context.Context, _ *emptypb.Empty) (*structpb.Struct, error) {
	if service.hostSites == nil {
		return nil, status.Error(codes.Unavailable, "host site discovery is unavailable")
	}
	candidates, err := service.hostSites.Collect(ctx)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "host site discovery failed")
	}
	items := make([]any, 0, len(candidates))
	for _, candidate := range candidates {
		ports := make([]any, 0, len(candidate.Ports))
		for _, port := range candidate.Ports {
			ports = append(ports, port)
		}
		items = append(items, map[string]any{
			"projectId":   candidate.ProjectID,
			"containerId": candidate.ContainerID,
			"ports":       ports,
		})
	}
	return structpb.NewStruct(map[string]any{"candidates": items})
}

func (service *webProbeService) Probe(ctx context.Context, input *structpb.Struct) (*structpb.Struct, error) {
	target := strings.TrimSpace(input.GetFields()["url"].GetStringValue())
	parsed, err := url.Parse(target)
	if err != nil || validateLocalProbeURL(parsed) != nil {
		return nil, status.Error(codes.InvalidArgument, "web probe URL is invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "web probe request is invalid")
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	request.Header.Set("User-Agent", "NCP-Web-Probe/"+BuildVersion)
	response, err := service.client.Do(request)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "web probe request failed")
	}
	defer response.Body.Close()
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if !strings.Contains(contentType, "text/html") && !strings.Contains(contentType, "application/xhtml+xml") {
		return nil, status.Error(codes.FailedPrecondition, "web probe response is not HTML")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxWebProbeResponseBytes+1))
	if err != nil || len(body) > maxWebProbeResponseBytes {
		return nil, status.Error(codes.ResourceExhausted, "web probe response is too large")
	}
	title, iconReference := parseWebMetadata(body)
	finalURL := response.Request.URL
	iconURL := ""
	if iconReference != "" {
		if resolved, err := finalURL.Parse(iconReference); err == nil {
			iconURL = resolved.String()
		}
	} else {
		iconURL = finalURL.ResolveReference(&url.URL{Path: "/favicon.ico"}).String()
	}
	return structpb.NewStruct(map[string]any{
		"url":         finalURL.String(),
		"title":       title,
		"iconUrl":     iconURL,
		"contentType": contentType,
		"statusCode":  response.StatusCode,
	})
}

func validateLocalProbeURL(target *url.URL) error {
	localAddresses, err := localInterfaceAddresses()
	if err != nil {
		localAddresses = nil
	}
	return validateLocalProbeURLWithAddresses(target, localAddresses)
}

func validateLocalProbeURLWithAddresses(target *url.URL, localAddresses []net.IP) error {
	if target == nil || (target.Scheme != "http" && target.Scheme != "https") || target.User != nil {
		return errors.New("unsupported URL")
	}
	host := target.Hostname()
	ip := net.ParseIP(host)
	if host == "localhost" || (ip != nil && ip.IsLoopback()) {
		return nil
	}
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
		return errors.New("only local interface targets are allowed")
	}
	for _, localAddress := range localAddresses {
		if localAddress != nil && localAddress.Equal(ip) {
			return nil
		}
	}
	return errors.New("only local interface targets are allowed")
}

func localInterfaceAddresses() ([]net.IP, error) {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	result := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		ip, _, parseErr := net.ParseCIDR(address.String())
		if parseErr == nil && ip != nil {
			result = append(result, ip)
		}
	}
	return result, nil
}

func parseWebMetadata(body []byte) (string, string) {
	document, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return "", ""
	}
	var title, iconURL string
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "title" && title == "" && node.FirstChild != nil {
			title = strings.TrimSpace(node.FirstChild.Data)
		}
		if node.Type == html.ElementNode && node.Data == "link" && iconURL == "" {
			var relationship, href string
			for _, attribute := range node.Attr {
				switch strings.ToLower(attribute.Key) {
				case "rel":
					relationship = strings.ToLower(attribute.Val)
				case "href":
					href = strings.TrimSpace(attribute.Val)
				}
			}
			if strings.Contains(relationship, "icon") {
				iconURL = href
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	return title, iconURL
}
