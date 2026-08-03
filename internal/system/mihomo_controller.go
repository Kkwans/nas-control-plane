package system

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// MihomoInvokeRequest intentionally has no token, endpoint or raw path field.
// The Agent keeps those values server-side and accepts only the allowlisted operations.
type MihomoInvokeRequest struct {
	Operation MihomoOperation `json:"operation"`
	Group     string          `json:"group"`
	Proxy     string          `json:"proxy"`
}

type MihomoInvokeResult struct {
	Operation  MihomoOperation `json:"operation"`
	StatusCode int             `json:"statusCode"`
	Data       json.RawMessage `json:"data"`
}

type MihomoControllerClient struct {
	Endpoint string
	Token    string
	Client   *http.Client
}

func NewMihomoControllerClient(endpoint, token string) (*MihomoControllerClient, error) {
	if _, err := parseControllerEndpoint(endpoint); err != nil {
		return nil, errors.New("MIHOMO_CONTROLLER_ENDPOINT_INVALID")
	}
	return &MihomoControllerClient{
		Endpoint: strings.TrimRight(endpoint, "/"),
		Token:    token,
		Client:   &http.Client{Timeout: commandTimeout},
	}, nil
}

func ValidateMihomoInvokeRequest(request MihomoInvokeRequest) error {
	switch request.Operation {
	case MihomoOperationVersion, MihomoOperationHealth, MihomoOperationTraffic, MihomoOperationConnections, MihomoOperationProxies:
		if request.Group != "" || request.Proxy != "" {
			return errors.New("MIHOMO_OPERATION_ARGUMENTS_INVALID")
		}
	case MihomoOperationSelectProxy:
		if !safeControllerSegment(request.Group) || !safeControllerSegment(request.Proxy) {
			return errors.New("MIHOMO_PROXY_SELECTION_INVALID")
		}
	default:
		return errors.New("MIHOMO_OPERATION_NOT_ALLOWED")
	}
	return nil
}

func (c *MihomoControllerClient) Invoke(ctx context.Context, request MihomoInvokeRequest) (MihomoInvokeResult, error) {
	if c == nil {
		return MihomoInvokeResult{}, errors.New("MIHOMO_CONTROLLER_UNAVAILABLE")
	}
	if err := ValidateMihomoInvokeRequest(request); err != nil {
		return MihomoInvokeResult{}, err
	}
	endpoint, err := parseControllerEndpoint(c.Endpoint)
	if err != nil {
		return MihomoInvokeResult{}, errors.New("MIHOMO_CONTROLLER_ENDPOINT_INVALID")
	}
	method := http.MethodGet
	path := "/version"
	var body io.Reader
	switch request.Operation {
	case MihomoOperationHealth:
		path = "/version"
	case MihomoOperationTraffic:
		path = "/traffic"
	case MihomoOperationConnections:
		path = "/connections"
	case MihomoOperationProxies:
		path = "/proxies"
	case MihomoOperationSelectProxy:
		method = http.MethodPut
		path = "/proxies/" + url.PathEscape(request.Group)
		encoded, encodeErr := json.Marshal(map[string]string{"name": request.Proxy})
		if encodeErr != nil {
			return MihomoInvokeResult{}, errors.New("MIHOMO_REQUEST_INVALID")
		}
		body = bytes.NewReader(encoded)
	}
	parsed := *endpoint
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	requestHTTP, err := http.NewRequestWithContext(ctx, method, parsed.String(), body)
	if err != nil {
		return MihomoInvokeResult{}, errors.New("MIHOMO_REQUEST_INVALID")
	}
	requestHTTP.Header.Set("Accept", "application/json")
	if body != nil {
		requestHTTP.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(c.Token) != "" {
		requestHTTP.Header.Set("Authorization", "Bearer "+c.Token)
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: commandTimeout}
	}
	response, err := client.Do(requestHTTP)
	if err != nil {
		return MihomoInvokeResult{}, errors.New("MIHOMO_CONTROLLER_UNAVAILABLE")
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, 256*1024))
	if err != nil {
		return MihomoInvokeResult{}, errors.New("MIHOMO_RESPONSE_INVALID")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return MihomoInvokeResult{Operation: request.Operation, StatusCode: response.StatusCode, Data: json.RawMessage("null")}, fmt.Errorf("MIHOMO_CONTROLLER_HTTP_%d", response.StatusCode)
	}
	if len(bytes.TrimSpace(content)) == 0 {
		content = []byte("null")
	}
	if !json.Valid(content) {
		return MihomoInvokeResult{}, errors.New("MIHOMO_RESPONSE_INVALID")
	}
	return MihomoInvokeResult{Operation: request.Operation, StatusCode: response.StatusCode, Data: json.RawMessage(content)}, nil
}

func safeControllerSegment(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 || strings.ContainsAny(value, "/\\\x00\r\n") {
		return false
	}
	return true
}
