package main

import (
	"context"
	"io"
	"testing"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
	"github.com/Kkwans/nas-control-plane/internal/terminal"
)

func TestRunAgentServerRequiresSocketGroup(t *testing.T) {
	err := runAgentServer(context.Background(), nil)
	if agentsocket.ErrorCode(err) != "AGENT_SOCKET_GROUP_REQUIRED" {
		t.Fatalf("错误码 = %q，期望 AGENT_SOCKET_GROUP_REQUIRED", agentsocket.ErrorCode(err))
	}
}

func TestRunAgentServerRejectsAlternateSocketPathOutsideTerminalPOC(t *testing.T) {
	err := runAgentServer(context.Background(), []string{"--socket-path", "/tmp/ncp-agent.sock"})
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
