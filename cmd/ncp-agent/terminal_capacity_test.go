package main

import (
	"strconv"
	"testing"
)

func TestTerminalCapacityCLI(t *testing.T) {
	for _, value := range []string{"", "1", "2", "3", "4"} {
		t.Run("valid_"+value, func(t *testing.T) {
			args := []string{"--socket-group", "ncp"}
			want := 1
			if value != "" {
				args = append(args, "--terminal-max-sessions", value)
				want, _ = strconv.Atoi(value)
			}
			config, err := parseAgentServerConfig(args)
			if err != nil || config.TerminalMaxSessions != want {
				t.Fatalf("config=%+v err=%v", config, err)
			}
		})
	}
	for _, value := range []string{"0", "-1", "5", "9999999999999999999999999", "foo", "1.5", ""} {
		t.Run("invalid_"+value, func(t *testing.T) {
			for _, enabled := range []string{"true", "false"} {
				if _, err := parseAgentServerConfig([]string{"--terminal-poc=" + enabled, "--terminal-max-sessions=" + value}); err == nil {
					t.Fatal("invalid capacity accepted")
				}
			}
		})
	}
}

func TestTerminalCapacityCLIErrorCode(t *testing.T) {
	_, err := parseAgentServerConfig([]string{"--terminal-max-sessions=0"})
	if code := agentSocketErrorCode(err); code != "TERMINAL_MAX_SESSIONS_INVALID" {
		t.Fatalf("CLI capacity error code = %q", code)
	}
}

func TestTerminalCapacityCLIExistingFlags(t *testing.T) {
	config, err := parseAgentServerConfig([]string{
		"--socket-group=example-group", "--socket-path=/tmp/example-agent.sock", "--terminal-poc=true",
		"--outbound-proxy=http://127.0.0.1:7890", "--compose-registry-path=/tmp/example-registry.db",
	})
	if err != nil || config.SocketGroup != "example-group" || config.SocketPath != "/tmp/example-agent.sock" || !config.EnableTerminalPOC || config.OutboundProxy != "http://127.0.0.1:7890" || config.ComposeRegistryPath != "/tmp/example-registry.db" || config.TerminalMaxSessions != 1 {
		t.Fatalf("原有配置未保留：%+v %v", config, err)
	}
	config, err = parseAgentServerConfig([]string{"--terminal-poc=false", "--terminal-max-sessions=4"})
	if err != nil || config.EnableTerminalPOC || config.TerminalMaxSessions != 4 {
		t.Fatalf("关闭终端时配置未保留：%+v %v", config, err)
	}
	for _, args := range [][]string{{"unexpected"}, {"--unknown-flag"}, {"--terminal-poc=false", "--socket-path=/tmp/example-agent.sock"}} {
		if _, err := parseAgentServerConfig(args); err == nil {
			t.Fatalf("意外接受参数：%v", args)
		}
	}
}
