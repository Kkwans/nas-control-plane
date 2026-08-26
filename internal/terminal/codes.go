package terminal

// NormalizeErrorCode keeps the first-generation POC names readable by older
// clients while exposing the formal terminal contract to new callers.
func NormalizeErrorCode(code string) string {
	switch code {
	case "TERMINAL_POC_UNAVAILABLE":
		return "TERMINAL_UNAVAILABLE"
	case "TERMINAL_POC_INITIALIZATION_FAILED":
		return "TERMINAL_INITIALIZATION_FAILED"
	case "TERMINAL_POC_TIMEOUT":
		return "TERMINAL_INITIALIZATION_TIMEOUT"
	case "TERMINAL_POC_OUTPUT_CLOSED":
		return "TERMINAL_OUTPUT_CLOSED"
	case "TERMINAL_POC_OUTPUT_FAILED":
		return "TERMINAL_OUTPUT_FAILED"
	default:
		return code
	}
}
