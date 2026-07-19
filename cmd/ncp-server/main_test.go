package main

import (
	"context"
	"io"
	"testing"
)

func TestRunAgentProbeRejectsUnexpectedArguments(t *testing.T) {
	if err := runAgentProbe(context.Background(), []string{"unexpected"}, io.Discard); err == nil {
		t.Fatal("agent-probe 应拒绝额外位置参数")
	}
}
