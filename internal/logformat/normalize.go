package logformat

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Message contains the display-safe log body and the original text when a
// leading timestamp was removed. RawMessage stays empty for ordinary content
// so clients only offer the raw view when it adds information.
type Message struct {
	Text       string
	RawMessage string
}

var timestampPrefix = regexp.MustCompile(`^(?:(?:\d{4}[-/]\d{2}[-/]\d{2})(?:T|\s)\d{2}:\d{2}:\d{2}(?:[.,]\d{1,9})?(?:Z|[+-]\d{2}:?\d{2})?|\d{2}:\d{2}:\d{2}(?:[.,]\d{1,9})?)`)

// NormalizeMessage removes only a structured timestamp at the beginning of a
// log line, plus an adjacent explicit level token. Timestamps in the message
// body are intentionally preserved.
func NormalizeMessage(value string) Message {
	original := value
	text := strings.TrimLeft(value, " \t")

	_, afterLeadingLevel, hasLeadingLevel := consumeLevel(text)
	if hasLeadingLevel {
		if afterTimestamp, ok := consumeTimestamp(afterLeadingLevel); ok {
			return normalized(original, afterTimestamp)
		}
	}

	if afterTimestamp, ok := consumeTimestamp(text); ok {
		_, afterTimestamp, _ = consumeLevel(afterTimestamp)
		return normalized(original, afterTimestamp)
	}

	return Message{Text: original}
}

// ResolveLevel is deliberately conservative. stderr is transport metadata,
// not a severity signal; only an explicit level or structured level field can
// raise the severity.
func ResolveLevel(explicit, message string) string {
	if level := NormalizeLevel(explicit); level != "" {
		return level
	}
	if level, _, ok := consumeLevel(strings.TrimLeft(message, " \t")); ok {
		return level
	}
	if level := structuredLevel(message); level != "" {
		return level
	}
	if afterTimestamp, ok := consumeTimestamp(strings.TrimLeft(message, " \t")); ok {
		if level, _, ok := consumeLevel(afterTimestamp); ok {
			return level
		}
	}
	return "info"
}

func NormalizeLevel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "trace", "debug":
		return "debug"
	case "info", "notice":
		return "info"
	case "warn", "warning":
		return "warning"
	case "error", "fatal", "panic", "critical", "crit":
		return "error"
	default:
		return ""
	}
}

func consumeTimestamp(value string) (string, bool) {
	match := timestampPrefix.FindString(value)
	if match == "" {
		return value, false
	}
	return trimSeparator(value[len(match):]), true
}

func consumeLevel(value string) (string, string, bool) {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return "", value, false
	}
	token := fields[0]
	level := NormalizeLevel(strings.Trim(token, "[](){}:;,|"))
	if level == "" {
		return "", value, false
	}
	remainder := strings.TrimLeft(value[len(token):], " \t")
	return level, trimSeparator(remainder), true
}

func trimSeparator(value string) string {
	value = strings.TrimLeft(value, " \t")
	value = strings.TrimLeft(value, ":|-—")
	return strings.TrimLeft(value, " \t")
}

func normalized(original, body string) Message {
	if body == "" || body == original {
		return Message{Text: original}
	}
	return Message{Text: body, RawMessage: original}
}

func structuredLevel(message string) string {
	trimmed := strings.TrimSpace(message)
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return ""
	}
	var fields map[string]any
	if json.Unmarshal([]byte(trimmed), &fields) != nil {
		return ""
	}
	for _, key := range []string{"level", "severity", "log.level"} {
		if value, ok := fields[key].(string); ok {
			if level := NormalizeLevel(value); level != "" {
				return level
			}
		}
	}
	return ""
}
