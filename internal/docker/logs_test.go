package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"
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

func (f fakeContainerLogGateway) ReadContainerLogs(context.Context, string, int) ([]ContainerLogEntry, error) {
	return f.entries, f.err
}
