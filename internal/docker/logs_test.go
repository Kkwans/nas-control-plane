package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

func TestContainerLogCollectorNormalizesDefaultTailAndPreservesEntries(t *testing.T) {
	collector := NewContainerLogCollector(fakeContainerLogGateway{
		entries: []ContainerLogEntry{{Stream: "stdout", Message: "ready"}},
	})
	result, err := collector.Read(context.Background(), ContainerLogsRequest{ContainerID: "abc123"})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if result.ContainerID != "abc123" || result.Tail != DefaultContainerLogTail || result.CollectedAt.IsZero() {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Entries) != 1 || result.Entries[0].Message != "ready" {
		t.Fatalf("entries = %#v", result.Entries)
	}
	if result.Entries[0].Level != "info" {
		t.Fatalf("entry level = %q, want info", result.Entries[0].Level)
	}
}

func TestContainerLogCollectorRejectsTailOutsideRange(t *testing.T) {
	collector := NewContainerLogCollector(fakeContainerLogGateway{})
	_, err := collector.Read(context.Background(), ContainerLogsRequest{ContainerID: "abc123", Tail: MaxContainerLogTail + 1})
	if ErrorCode(err) != "DOCKER_LOGS_INPUT_INVALID" {
		t.Fatalf("error code = %q, want DOCKER_LOGS_INPUT_INVALID", ErrorCode(err))
	}
}

func TestContainerLogCollectorReturnsStableUnavailableError(t *testing.T) {
	collector := NewContainerLogCollector(fakeContainerLogGateway{err: errors.New("docker unavailable")})
	_, err := collector.Read(context.Background(), ContainerLogsRequest{ContainerID: "abc123", Tail: 10})
	if ErrorCode(err) != "DOCKER_LOGS_UNAVAILABLE" {
		t.Fatalf("error code = %q, want DOCKER_LOGS_UNAVAILABLE", ErrorCode(err))
	}
}

func TestDecodeDockerLogEntriesSupportsMultiplexedStreams(t *testing.T) {
	payload := bytes.NewBuffer(nil)
	writeDockerLogFrame(t, payload, 1, "ready\n")
	writeDockerLogFrame(t, payload, 2, "warning\n")

	entries := decodeDockerLogEntries(payload.Bytes())
	if len(entries) != 2 || entries[0].Stream != "stdout" || entries[0].Message != "ready" || entries[1].Stream != "stderr" || entries[1].Message != "warning" {
		t.Fatalf("entries = %#v", entries)
	}
	if entries[0].Level != "info" || entries[1].Level != "warning" {
		t.Fatalf("levels = %q, %q; want info, warning", entries[0].Level, entries[1].Level)
	}
}

func TestDecodeDockerLogEntriesFallsBackToPlainText(t *testing.T) {
	entries := decodeDockerLogEntries([]byte("ready\nnext\n"))
	if len(entries) != 2 || entries[0].Message != "ready" || entries[1].Message != "next" {
		t.Fatalf("entries = %#v", entries)
	}
}

func writeDockerLogFrame(t *testing.T, buffer *bytes.Buffer, stream byte, message string) {
	t.Helper()
	header := make([]byte, 8)
	header[0] = stream
	binary.BigEndian.PutUint32(header[4:], uint32(len(message)))
	if _, err := buffer.Write(header); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := buffer.WriteString(message); err != nil {
		t.Fatalf("write message: %v", err)
	}
}

type fakeContainerLogGateway struct {
	entries []ContainerLogEntry
	err     error
}

func (f fakeContainerLogGateway) ReadContainerLogs(context.Context, string, int, string) ([]ContainerLogEntry, error) {
	return f.entries, f.err
}

func TestDecodeDockerLogEntriesPreservesRealTimestamp(t *testing.T) {
	entries := textLogEntries("stdout", "2026-07-26T09:15:39.123456789Z service ready\n")
	if len(entries) != 1 {
		t.Fatalf("entries = %#v", entries)
	}
	if entries[0].Timestamp.Format(time.RFC3339Nano) != "2026-07-26T09:15:39.123456789Z" || entries[0].Message != "service ready" {
		t.Fatalf("entry = %#v", entries[0])
	}
	if entries[0].Level != "info" {
		t.Fatalf("entry level = %q, want info", entries[0].Level)
	}
}

func TestDecodeDockerLogEntriesRemovesApplicationTimestamp(t *testing.T) {
	entries := textLogEntries("stdout", "2026-08-08T21:06:26.472531311Z 2026/08/08 13:06:26 Using config file\n")
	if len(entries) != 1 {
		t.Fatalf("entries = %#v", entries)
	}
	entry := entries[0]
	if entry.Message != "Using config file" || entry.RawMessage != "2026/08/08 13:06:26 Using config file" {
		t.Fatalf("entry = %#v", entry)
	}
	if entry.Timestamp.Format(time.RFC3339Nano) != "2026-08-08T21:06:26.472531311Z" {
		t.Fatalf("timestamp = %s", entry.Timestamp.Format(time.RFC3339Nano))
	}
}

func TestResolveContainerLogLevelRequiresAnExplicitPrefix(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    string
	}{
		{name: "stderr content does not become error", message: "request completed with ERROR code", want: "info"},
		{name: "error prefix", message: "ERROR request failed", want: "error"},
		{name: "bracketed warning prefix", message: "[WARN] slow request", want: "warning"},
		{name: "debug prefix", message: "debug cache miss", want: "debug"},
		{name: "explicit info prefix", message: "INFO service ready", want: "info"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ResolveContainerLogLevel("", test.message); got != test.want {
				t.Fatalf("ResolveContainerLogLevel(%q) = %q, want %q", test.message, got, test.want)
			}
		})
	}
}

func TestContainerLogCollectorNormalizesExplicitAndMissingLevels(t *testing.T) {
	collector := NewContainerLogCollector(fakeContainerLogGateway{entries: []ContainerLogEntry{
		{Stream: "stderr", Message: "service failed"},
		{Stream: "stdout", Level: "ERROR", Message: "explicit failure"},
	}})
	result, err := collector.Read(context.Background(), ContainerLogsRequest{ContainerID: "abc123", Tail: 2})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := result.Entries[0].Level; got != "info" {
		t.Fatalf("missing level = %q, want info", got)
	}
	if got := result.Entries[1].Level; got != "error" {
		t.Fatalf("explicit level = %q, want error", got)
	}
}
