package main

import (
	"context"
	"testing"

	"github.com/Kkwans/nas-control-plane/internal/agentsocket"
)

func TestRunAgentServerRequiresSocketGroup(t *testing.T) {
	err := runAgentServer(context.Background(), nil)
	if agentsocket.ErrorCode(err) != "AGENT_SOCKET_GROUP_REQUIRED" {
		t.Fatalf("错误码 = %q，期望 AGENT_SOCKET_GROUP_REQUIRED", agentsocket.ErrorCode(err))
	}
}
