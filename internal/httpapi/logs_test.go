package httpapi

import "testing"

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
			message, raw := normalizeLogMessage(test.input)
			if message != test.wantMessage || raw != test.wantRaw {
				t.Fatalf("normalizeLogMessage(%q) = (%q, %q), want (%q, %q)", test.input, message, raw, test.wantMessage, test.wantRaw)
			}
		})
	}
}
