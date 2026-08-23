package main

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/terminal"
)

func TestWriteBuildVersion(t *testing.T) {
	var output bytes.Buffer
	if err := writeBuildVersion(&output); err != nil {
		t.Fatalf("writeBuildVersion error = %v", err)
	}
	if got, want := output.String(), "dev\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func TestRunAgentServerRequiresSocketGroup(t *testing.T) {
	err := runAgentServer(context.Background(), nil)
	if agentsocket.ErrorCode(err) != "AGENT_SOCKET_GROUP_REQUIRED" {
		t.Fatalf("错误码 = %q，期望 AGENT_SOCKET_GROUP_REQUIRED", agentsocket.ErrorCode(err))
	}
}

func TestRunAgentServerRejectsAlternateSocketPathOutsideTerminalPOC(t *testing.T) {
	err := runAgentServer(context.Background(), []string{"--terminal-poc=false", "--socket-path", "/tmp/ncp-agent.sock"})
	if agentsocket.ErrorCode(err) != "AGENT_SOCKET_PATH_POC_ONLY" {
		t.Fatalf("错误码 = %q，期望 AGENT_SOCKET_PATH_POC_ONLY", agentsocket.ErrorCode(err))
	}
}

func TestRunTerminalPOCRejectsUnsupportedTargetBeforeOpeningSession(t *testing.T) {
	err := runTerminalPOC(context.Background(), []string{"--target", "root"}, io.Discard)
	if terminal.ErrorCode(err) != "TERMINAL_TARGET_REJECTED" {
		t.Fatalf("错误码 = %q，期望 TERMINAL_TARGET_REJECTED", terminal.ErrorCode(err))
	}
}

func TestRunJournalPOCRejectsUnexpectedArguments(t *testing.T) {
	err := runJournalPOC(context.Background(), []string{"unexpected"}, io.Discard)
	if err == nil {
		t.Fatal("journal POC accepted an unexpected argument")
	}
}
