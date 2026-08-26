package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"log/slog"
)

func TestWithRequestIDEmitsStructuredSafeRequestRecord(t *testing.T) {
	var output bytes.Buffer
	handler := NewHandler(Config{
		RequestID: func() string { return "req-structured" },
		Logger:    slog.New(slog.NewJSONHandler(&output, nil)),
	})
	request := httptest.NewRequest(http.MethodGet, "/healthz?password=do-not-log", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", response.Code)
	}
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("decode structured log: %v; output=%q", err, output.String())
	}
	for key, want := range map[string]string{
		"component":  "httpapi",
		"request_id": "req-structured",
		"actor":      "anonymous",
		"action":     "GET",
		"route":      "/healthz",
	} {
		if got := record[key]; got != want {
			t.Fatalf("log %s = %v, want %q", key, got, want)
		}
	}
	if _, exists := record["password"]; exists || bytes.Contains(output.Bytes(), []byte("do-not-log")) {
		t.Fatalf("structured request log leaked query data: %s", output.String())
	}
	if status, ok := record["status"].(float64); !ok || int(status) != http.StatusOK {
		t.Fatalf("log status = %v, want 200", record["status"])
	}
}
