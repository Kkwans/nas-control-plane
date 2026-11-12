package openapi_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/monitoring"
	"github.com/Kkwans/nas-control-plane/internal/system"
	"github.com/getkin/kin-openapi/openapi3"
)

func TestResourceAlertsContract(t *testing.T) {
	document, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	operation := document.Paths.Value("/api/v1/monitoring/alerts").Get
	if operation.Security == nil || len(*operation.Security) != 1 {
		t.Fatal("missing explicit authentication")
	}
	for _, status := range []string{"200", "401", "500", "503"} {
		if operation.Responses.Value(status) == nil {
			t.Fatal(status)
		}
	}
	schema := document.Components.Schemas["ResourceAlertsSnapshot"].Value
	at := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	enabled := monitoring.NewAlertProcessor(true, at)
	snapshots := []monitoring.AlertsSnapshot{monitoring.NewAlertProcessor(false, at).Snapshot(at), enabled.Snapshot(at)}
	for i := 0; i <= 24; i++ {
		now := at.Add(time.Duration(i) * 5 * time.Second)
		enabled = enabled.Process(now, system.Summary{CollectedAt: now, CPU: system.CPUStats{UsagePercent: 95}, Memory: system.MemoryStats{TotalBytes: 100, UsedBytes: 95}}, "")
	}
	snapshots = append(snapshots, enabled.Snapshot(at.Add(120*time.Second)), enabled.Snapshot(at.Add(136*time.Second)))
	for _, snapshot := range snapshots {
		data, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err = json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		if err = schema.VisitJSON(value); err != nil {
			t.Fatalf("runtime contract: %v", err)
		}
	}
}

func TestResourceAlertsContractRejectsSwappedRules(t *testing.T) {
	document, err := openapi3.NewLoader().LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	schema := document.Components.Schemas["ResourceAlertsSnapshot"].Value
	at := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, rule, key string
		value           any
	}{
		{"cpu id", "cpu", "id", "host.memory.high"},
		{"memory id", "memory", "id", "host.cpu.high"},
		{"cpu recovery threshold", "cpu", "recoveryPercent", float64(85)},
		{"memory recovery threshold", "memory", "recoveryPercent", float64(80)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(monitoring.NewAlertProcessor(true, at).Snapshot(at))
			if err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err = json.Unmarshal(data, &value); err != nil {
				t.Fatal(err)
			}
			value["rules"].(map[string]any)[tc.rule].(map[string]any)[tc.key] = tc.value
			if err = schema.VisitJSON(value); err == nil {
				t.Fatal("fixed rule contract accepted swapped constant")
			}
		})
	}
}
