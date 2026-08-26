package terminal

import "testing"

func TestNormalizeErrorCodeKeepsLegacyPOCNamesCompatible(t *testing.T) {
	tests := map[string]string{
		"TERMINAL_POC_UNAVAILABLE":           "TERMINAL_UNAVAILABLE",
		"TERMINAL_POC_INITIALIZATION_FAILED": "TERMINAL_INITIALIZATION_FAILED",
		"TERMINAL_POC_TIMEOUT":               "TERMINAL_INITIALIZATION_TIMEOUT",
		"TERMINAL_POC_OUTPUT_CLOSED":         "TERMINAL_OUTPUT_CLOSED",
		"TERMINAL_POC_OUTPUT_FAILED":         "TERMINAL_OUTPUT_FAILED",
		"TERMINAL_TARGET_UNAVAILABLE":        "TERMINAL_TARGET_UNAVAILABLE",
	}
	for input, want := range tests {
		if got := NormalizeErrorCode(input); got != want {
			t.Errorf("NormalizeErrorCode(%q) = %q, want %q", input, got, want)
		}
	}
}
