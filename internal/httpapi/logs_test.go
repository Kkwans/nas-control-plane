package httpapi

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/docker"
	"github.com/Kkwans/nas-control-plane/internal/logformat"
)

func TestNormalizeLogMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		wantMessage string
		wantRaw     string
	}{
		{
			name:        "strict structured prefix",
			input:       "INFO 2026-07-28 18:28:15.234596 entry_serv/grpcServ/server.go:60 [100.924µs]\t/desktop",
			wantMessage: "entry_serv/grpcServ/server.go:60 [100.924µs]\t/desktop",
			wantRaw:     "INFO 2026-07-28 18:28:15.234596 entry_serv/grpcServ/server.go:60 [100.924µs]\t/desktop",
		},
		{
			name:        "rfc3339 prefix",
			input:       "2026-08-01T11:24:34.846634623Z [diagnostic] heartbeat",
			wantMessage: "[diagnostic] heartbeat",
			wantRaw:     "2026-08-01T11:24:34.846634623Z [diagnostic] heartbeat",
		},
		{
			name:        "slash date prefix",
			input:       "2026/08/08 13:06:26 Using config file: /config/settings.json",
			wantMessage: "Using config file: /config/settings.json",
			wantRaw:     "2026/08/08 13:06:26 Using config file: /config/settings.json",
		},
		{
			name:        "time only prefix",
			input:       "19:24:34 [diagnostic] heartbeat",
			wantMessage: "[diagnostic] heartbeat",
			wantRaw:     "19:24:34 [diagnostic] heartbeat",
		},
		{
			name:        "level and time only prefix",
			input:       "WARN 19:24:34.123 warning body\nnext line",
			wantMessage: "warning body\nnext line",
			wantRaw:     "WARN 19:24:34.123 warning body\nnext line",
		},
		{
			name:        "body timestamp remains",
			input:       "request completed at 19:24:34 with code 200",
			wantMessage: "request completed at 19:24:34 with code 200",
			wantRaw:     "",
		},
		{
			name:        "ordinary numbers remain untouched",
			input:       `172.27.0.1 requested http://192.168.5.110 with Firefox/154.0`,
			wantMessage: `172.27.0.1 requested http://192.168.5.110 with Firefox/154.0`,
			wantRaw:     "",
		},
		{
			name:        "level without timestamp is content",
			input:       "INFO worker completed successfully",
			wantMessage: "INFO worker completed successfully",
			wantRaw:     "",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			normalized := logformat.NormalizeMessage(test.input)
			if normalized.Text != test.wantMessage || normalized.RawMessage != test.wantRaw {
				t.Fatalf("NormalizeMessage(%q) = (%q, %q), want (%q, %q)", test.input, normalized.Text, normalized.RawMessage, test.wantMessage, test.wantRaw)
			}
		})
	}
}

func TestReadContainerLogCenterKeepsStreamAndDefaultsStderrToInfo(t *testing.T) {
	collectedAt := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	agent := &fakeAgentClient{logsResult: docker.ContainerLogsResult{
		ContainerID: "abc123",
		Tail:        10,
		CollectedAt: collectedAt,
		Entries: []docker.ContainerLogEntry{
			{Timestamp: collectedAt, Stream: "stderr", Message: "request completed with ERROR code"},
			{Timestamp: collectedAt.Add(time.Second), Stream: "stdout", Message: "ERROR request failed"},
		},
	}}
	api := &handler{agent: agent, agentSocketPath: "/run/ncp/test.sock"}

	result, err := api.readContainerLogCenter(context.Background(), 10, "abc123", httptest.NewRequest("GET", "/api/v1/logs?source=container", nil))
	if err != nil {
		t.Fatalf("readContainerLogCenter() error = %v", err)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("entries = %#v", result.Entries)
	}
	if result.Entries[0].Level != "info" || result.Entries[0].Stream != "stderr" {
		t.Fatalf("stderr entry = %#v, want info/stderr", result.Entries[0])
	}
	if result.Entries[1].Level != "error" || result.Entries[1].Stream != "stdout" {
		t.Fatalf("explicit-level entry = %#v, want error/stdout", result.Entries[1])
	}
}
