// Package caddy contains the narrowly scoped P0 Caddy Admin API boundary.
package caddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	pocRouteID   = "ncp-p0-caddy-route"
	pocRoutePath = "/ncp-p0-caddy"
	pocRouteBody = "NCP_P0_CADDY_ROUTE"
)

type pathMatcher struct {
	Path []string `json:"path"`
}

type staticResponse struct {
	Handler string `json:"handler"`
	Body    string `json:"body"`
}

type route struct {
	ID       string           `json:"@id"`
	Match    []pathMatcher    `json:"match"`
	Handle   []staticResponse `json:"handle"`
	Terminal bool             `json:"terminal"`
}

// Client can only target a loopback Caddy Admin API endpoint.
type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
}

func NewClient(endpoint string, httpClient *http.Client) (*Client, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || !isLoopbackHost(parsed.Hostname()) {
		return nil, coded("CADDY_ADMIN_ENDPOINT_INVALID", errors.New("caddy admin endpoint must be loopback http"))
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: parsed, httpClient: httpClient}, nil
}

// AddPOCRoute appends only the fixed P0 static-response route to Caddy's
// isolated P0 server. It does not accept hostnames, upstreams, or route input.
func (c *Client) AddPOCRoute(ctx context.Context) error {
	payload, err := json.Marshal(pocRoute())
	if err != nil {
		return coded("CADDY_REQUEST_FAILED", err)
	}
	return c.request(ctx, http.MethodPost, "/config/apps/http/servers/ncp-p0/routes", payload)
}

// DeletePOCRoute removes only the route whose fixed @id belongs to P0.
func (c *Client) DeletePOCRoute(ctx context.Context) error {
	return c.request(ctx, http.MethodDelete, "/id/"+pocRouteID, nil)
}

func (c *Client) request(ctx context.Context, method, path string, payload []byte) error {
	if c == nil || c.baseURL == nil || c.httpClient == nil {
		return coded("CADDY_CLIENT_UNAVAILABLE", errors.New("caddy client is unavailable"))
	}
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return coded("CADDY_REQUEST_FAILED", err)
	}
	if len(payload) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return coded("CADDY_REQUEST_CANCELED", ctx.Err())
		}
		return coded("CADDY_REQUEST_FAILED", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return coded("CADDY_RESPONSE_INVALID", errors.New("caddy admin request was rejected"))
	}
	return nil
}

func pocRoute() route {
	return route{
		ID:       pocRouteID,
		Match:    []pathMatcher{{Path: []string{pocRoutePath}}},
		Handle:   []staticResponse{{Handler: "static_response", Body: pocRouteBody}},
		Terminal: true,
	}
}

func isLoopbackHost(host string) bool {
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}

type codedError struct {
	code string
	err  error
}

func (e *codedError) Error() string {
	return e.code
}

func (e *codedError) Unwrap() error {
	return e.err
}

func coded(code string, err error) error {
	return &codedError{code: code, err: err}
}

func ErrorCode(err error) string {
	var codedErr *codedError
	if errors.As(err, &codedErr) {
		return codedErr.code
	}
	return ""
}
