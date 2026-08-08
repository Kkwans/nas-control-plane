package logformat

import "testing"

func TestNormalizeMessageSupportsCommonLeadingTimestamps(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"slash date", "2026/08/08 13:06:26 Using config file", "Using config file"},
		{"dash date", "2026-08-08 13:06:26 WARN request slow", "request slow"},
		{"iso date", "2026-08-08T13:06:26.472531311Z [ERROR] request failed", "request failed"},
		{"leading level", "INFO 2026-08-08 13:06:26 service ready", "service ready"},
		{"time only", "13:06:26.123 debug cache miss", "cache miss"},
		{"body time", "request completed at 13:06:26", "request completed at 13:06:26"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := NormalizeMessage(test.input)
			if got.Text != test.want {
				t.Fatalf("NormalizeMessage(%q).Text = %q, want %q", test.input, got.Text, test.want)
			}
			if test.input != test.want && got.RawMessage != test.input {
				t.Fatalf("RawMessage = %q, want %q", got.RawMessage, test.input)
			}
		})
	}
}

func TestResolveLevelRequiresExplicitEvidence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"request completed with ERROR code", "info"},
		{"[WARN] slow request", "warning"},
		{"2026/08/08 13:06:26 ERROR failed", "error"},
		{`{"level":"fatal","message":"failed"}`, "error"},
	}
	for _, test := range tests {
		if got := ResolveLevel("", test.input); got != test.want {
			t.Fatalf("ResolveLevel(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}
