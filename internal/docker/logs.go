package docker

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

const (
	DefaultContainerLogTail = 120
	MaxContainerLogTail     = 500
	maxContainerLogBytes    = 256 * 1024
)

type ContainerLogsRequest struct {
	ContainerID string `json:"containerId"`
	Tail        int    `json:"tail"`
	Since       string `json:"since,omitempty"`
}

func (r ContainerLogsRequest) Normalize() (ContainerLogsRequest, error) {
	r.ContainerID = strings.TrimSpace(r.ContainerID)
	if r.ContainerID == "" {
		return ContainerLogsRequest{}, coded("DOCKER_LOGS_INPUT_INVALID", errors.New("container id is required"))
	}
	if r.Tail == 0 {
		r.Tail = DefaultContainerLogTail
	}
	if r.Tail < 1 || r.Tail > MaxContainerLogTail {
		return ContainerLogsRequest{}, coded("DOCKER_LOGS_INPUT_INVALID", errors.New("tail is outside the supported range"))
	}
	r.Since = strings.TrimSpace(r.Since)
	if r.Since != "" {
		if _, err := time.Parse(time.RFC3339Nano, r.Since); err != nil {
			return ContainerLogsRequest{}, coded("DOCKER_LOGS_INPUT_INVALID", errors.New("since is invalid"))
		}
	}
	return r, nil
}

type ContainerLogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Stream    string    `json:"stream"`
	Message   string    `json:"message"`
}

type ContainerLogsResult struct {
	ContainerID string              `json:"containerId"`
	Tail        int                 `json:"tail"`
	CollectedAt time.Time           `json:"collectedAt"`
	Entries     []ContainerLogEntry `json:"entries"`
}

type ContainerLogGateway interface {
	ReadContainerLogs(context.Context, string, int, string) ([]ContainerLogEntry, error)
}

type ContainerLogCollector struct {
	gateway ContainerLogGateway
	now     func() time.Time
}

func NewContainerLogCollector(gateway ContainerLogGateway) *ContainerLogCollector {
	if gateway == nil {
		gateway = unavailableContainerLogGateway{}
	}
	return &ContainerLogCollector{gateway: gateway, now: time.Now}
}

func NewLiveContainerLogCollector() (*ContainerLogCollector, error) {
	gateway, err := NewMobyContainerLogGateway()
	if err != nil {
		return nil, err
	}
	return NewContainerLogCollector(gateway), nil
}

func (c *ContainerLogCollector) Read(ctx context.Context, request ContainerLogsRequest) (ContainerLogsResult, error) {
	request, err := request.Normalize()
	if err != nil {
		return ContainerLogsResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ContainerLogsResult{}, err
	}
	entries, err := c.gateway.ReadContainerLogs(ctx, request.ContainerID, request.Tail, request.Since)
	if err != nil {
		return ContainerLogsResult{}, coded("DOCKER_LOGS_UNAVAILABLE", err)
	}
	if entries == nil {
		entries = make([]ContainerLogEntry, 0)
	}
	return ContainerLogsResult{
		ContainerID: request.ContainerID,
		Tail:        request.Tail,
		CollectedAt: c.now().UTC(),
		Entries:     entries,
	}, nil
}

type mobyContainerLogGateway struct {
	client *client.Client
}

func NewMobyContainerLogGateway() (ContainerLogGateway, error) {
	apiClient, err := client.New(client.WithHost(localDockerHost), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &mobyContainerLogGateway{client: apiClient}, nil
}

func (g *mobyContainerLogGateway) ReadContainerLogs(ctx context.Context, containerID string, tail int, since string) ([]ContainerLogEntry, error) {
	response, err := g.client.ContainerLogs(ctx, containerID, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       strconv.Itoa(tail),
		Since:      since,
		Timestamps: true,
	})
	if err != nil {
		return nil, err
	}
	defer response.Close()
	payload, err := io.ReadAll(io.LimitReader(response, maxContainerLogBytes+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > maxContainerLogBytes {
		return nil, errors.New("container logs exceeded response limit")
	}
	return decodeDockerLogEntries(payload), nil
}

func decodeDockerLogEntries(payload []byte) []ContainerLogEntry {
	accumulator := &logAccumulator{}
	_, err := stdcopy.StdCopy(accumulator.writer("stdout"), accumulator.writer("stderr"), strings.NewReader(string(payload)))
	if err != nil {
		return textLogEntries("stdout", string(payload))
	}
	accumulator.flush()
	return accumulator.entries
}

type logAccumulator struct {
	entries []ContainerLogEntry
	pending map[string]string
}

func (a *logAccumulator) writer(stream string) io.Writer {
	if a.pending == nil {
		a.pending = make(map[string]string)
	}
	return logStreamWriter{accumulator: a, stream: stream}
}

func (a *logAccumulator) append(stream, text string) {
	if text == "" {
		return
	}
	timestamp, message := parseDockerLogLine(text)
	a.entries = append(a.entries, ContainerLogEntry{Timestamp: timestamp, Stream: stream, Message: message})
}

func (a *logAccumulator) flush() {
	for _, stream := range []string{"stdout", "stderr"} {
		if pending := a.pending[stream]; pending != "" {
			a.append(stream, pending)
		}
	}
}

type logStreamWriter struct {
	accumulator *logAccumulator
	stream      string
}

func (w logStreamWriter) Write(data []byte) (int, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = w.accumulator.pending[w.stream] + text
	lines := strings.Split(text, "\n")
	w.accumulator.pending[w.stream] = lines[len(lines)-1]
	for _, line := range lines[:len(lines)-1] {
		w.accumulator.append(w.stream, line)
	}
	return len(data), nil
}

func textLogEntries(stream, text string) []ContainerLogEntry {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	entries := make([]ContainerLogEntry, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			timestamp, message := parseDockerLogLine(line)
			entries = append(entries, ContainerLogEntry{Timestamp: timestamp, Stream: stream, Message: message})
		}
	}
	return entries
}

func parseDockerLogLine(line string) (time.Time, string) {
	timestampText, message, found := strings.Cut(line, " ")
	if !found {
		return time.Time{}, line
	}
	timestamp, err := time.Parse(time.RFC3339Nano, timestampText)
	if err != nil {
		return time.Time{}, line
	}
	return timestamp.UTC(), message
}

type unavailableContainerLogGateway struct{}

func (unavailableContainerLogGateway) ReadContainerLogs(context.Context, string, int, string) ([]ContainerLogEntry, error) {
	return nil, errors.New("container log gateway is not configured")
}
