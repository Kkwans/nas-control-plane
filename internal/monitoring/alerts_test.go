package monitoring

import (
	"encoding/json"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/system"
)

var alertTestStart = time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)

func alertTestSummary(at time.Time, cpu float64, memory uint64) system.Summary {
	return system.Summary{CollectedAt: at, CPU: system.CPUStats{UsagePercent: cpu}, Memory: system.MemoryStats{TotalBytes: 1000, UsedBytes: memory}}
}
func alertTestObserve(p AlertProcessor, at time.Time, cpu float64, memory uint64) AlertProcessor {
	return p.Process(at, alertTestSummary(at, cpu, memory), "")
}
func alertTestFiring() (AlertProcessor, time.Time) {
	p := NewAlertProcessor(true, alertTestStart)
	for sec := 0; sec <= 120; sec += 5 {
		p = alertTestObserve(p, alertTestStart.Add(time.Duration(sec)*time.Second), 90, 900)
	}
	return p, alertTestStart.Add(120 * time.Second)
}
func TestAlertsExactTriggerRecovery(t *testing.T) {
	p := NewAlertProcessor(true, alertTestStart)
	p = alertTestObserve(p, alertTestStart, 90, 900)
	if p.Snapshot(alertTestStart).Rules.CPU.Condition != "pending" {
		t.Fatal("first sample counted as elapsed")
	}
	for sec := 5; sec <= 115; sec += 5 {
		p = alertTestObserve(p, alertTestStart.Add(time.Duration(sec)*time.Second), 90, 900)
	}
	before := alertTestStart.Add(120*time.Second - time.Nanosecond)
	p = alertTestObserve(p, before, 90, 900)
	if p.Snapshot(before).Rules.CPU.Condition != "pending" {
		t.Fatal("early trigger")
	}
	at := alertTestStart.Add(120 * time.Second)
	p = alertTestObserve(p, at, 90, 900)
	if p.Snapshot(at).Rules.CPU.Condition != "firing" || p.Snapshot(at).Rules.Memory.Condition != "firing" {
		t.Fatal("missing exact trigger")
	}
	firing := *p.Snapshot(at).Rules.CPU.FiringSince
	at = at.Add(5 * time.Second)
	p = alertTestObserve(p, at, 80, 850)
	for sec := 5; sec <= 55; sec += 5 {
		p = alertTestObserve(p, at.Add(time.Duration(sec)*time.Second), 80, 850)
	}
	p = alertTestObserve(p, at.Add(60*time.Second-time.Nanosecond), 80, 850)
	if p.Snapshot(at.Add(60*time.Second-time.Nanosecond)).Rules.CPU.Condition != "recovering" {
		t.Fatal("early recovery")
	}
	p = alertTestObserve(p, at.Add(60*time.Second), 80, 850)
	snap := p.Snapshot(at.Add(60 * time.Second))
	if snap.Rules.CPU.Condition != "normal" || snap.Rules.Memory.Condition != "normal" || snap.Rules.CPU.LastRecoveredAt == nil || snap.Rules.CPU.FiringSince != nil {
		t.Fatal("exact recovery missing", snap)
	}
	p = alertTestObserve(p, at.Add(65*time.Second), 90, 900)
	for sec := 70; sec <= 185; sec += 5 {
		p = alertTestObserve(p, at.Add(time.Duration(sec)*time.Second), 90, 900)
	}
	if got := p.Snapshot(at.Add(185 * time.Second)).Rules.CPU; got.FiringSince == nil || !got.FiringSince.After(firing) {
		t.Fatal("new event missing")
	}
}
func TestAlertsHysteresisAndInterruptions(t *testing.T) {
	p := NewAlertProcessor(true, alertTestStart)
	p = alertTestObserve(p, alertTestStart, 90, 900)
	p = alertTestObserve(p, alertTestStart.Add(time.Second), 89.999, 899)
	if got := p.Snapshot(alertTestStart.Add(time.Second)).Rules.CPU; got.Condition != "normal" || got.PendingSince != nil {
		t.Fatal(got)
	}
	p, at := alertTestFiring()
	original := *p.Snapshot(at).Rules.CPU.FiringSince
	p = alertTestObserve(p, at.Add(5*time.Second), 85, 880)
	if p.Snapshot(at.Add(5*time.Second)).Rules.CPU.Condition != "firing" {
		t.Fatal("hysteresis lost firing")
	}
	p = alertTestObserve(p, at.Add(10*time.Second), 80, 850)
	p = alertTestObserve(p, at.Add(15*time.Second), 80.001, 851)
	if got := p.Snapshot(at.Add(15 * time.Second)).Rules.CPU; got.Condition != "firing" || got.RecoveringSince != nil || !got.FiringSince.Equal(original) {
		t.Fatal(got)
	}
	p = alertTestObserve(p, at.Add(20*time.Second), 0, 0)
	p = p.Process(at.Add(25*time.Second), system.Summary{}, "SYSTEM_SUMMARY_UNAVAILABLE")
	got := p.Snapshot(at.Add(25 * time.Second)).Rules.CPU
	if got.Condition != "firing" || got.Availability != "unknown" || got.RecoveringSince != nil || got.LastRecoveredAt != nil {
		t.Fatal(got)
	}
	p = alertTestObserve(p, at.Add(30*time.Second), 0, 0)
	if got = p.Snapshot(at.Add(30 * time.Second)).Rules.CPU; got.Condition != "recovering" || !got.RecoveringSince.Equal(at.Add(30*time.Second)) {
		t.Fatal(got)
	}
}
func TestAlertsRuleValidity(t *testing.T) {
	cases := []struct {
		name        string
		mutate      func(*system.Summary)
		cpu, memory string
	}{
		{"zero-valid", func(s *system.Summary) { s.CPU.UsagePercent = 0; s.Memory.UsedBytes = 0 }, "fresh", "fresh"},
		{"cpu-warning", func(s *system.Summary) {
			s.Warnings = []system.SummaryWarning{{Source: "cpu", Code: "arbitrary warning"}}
		}, "unknown", "fresh"},
		{"memory-warning", func(s *system.Summary) { s.Warnings = []system.SummaryWarning{{Source: "memory"}} }, "fresh", "unknown"},
		{"other-warning", func(s *system.Summary) {
			s.Warnings = []system.SummaryWarning{{Source: "storage"}, {Source: "network"}, {Source: "temperature"}}
		}, "fresh", "fresh"},
		{"cpu-negative", func(s *system.Summary) { s.CPU.UsagePercent = -1 }, "unknown", "fresh"},
		{"cpu-over", func(s *system.Summary) { s.CPU.UsagePercent = 100.001 }, "unknown", "fresh"},
		{"cpu-nan", func(s *system.Summary) { s.CPU.UsagePercent = math.NaN() }, "unknown", "fresh"},
		{"cpu-inf", func(s *system.Summary) { s.CPU.UsagePercent = math.Inf(1) }, "unknown", "fresh"},
		{"memory-zero", func(s *system.Summary) { s.Memory.TotalBytes = 0 }, "fresh", "unknown"},
		{"memory-over", func(s *system.Summary) { s.Memory.UsedBytes = 1001 }, "fresh", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := alertTestSummary(alertTestStart, 90, 900)
			tc.mutate(&s)
			p := NewAlertProcessor(true, alertTestStart).Process(alertTestStart, s, "")
			got := p.Snapshot(alertTestStart)
			if got.Rules.CPU.Availability != tc.cpu || got.Rules.Memory.Availability != tc.memory {
				t.Fatal(got)
			}
			if _, err := json.Marshal(got); err != nil {
				t.Fatal(err)
			}
		})
	}
	// 全快照 JSON 编码失败由调用方传入，优先于逐规则有效性。
	s := alertTestSummary(alertTestStart, math.NaN(), 900)
	p := NewAlertProcessor(true, alertTestStart).Process(alertTestStart, s, "MONITORING_SAMPLE_INVALID")
	if got := p.Snapshot(alertTestStart); got.Rules.CPU.Availability != "unknown" || got.Rules.Memory.Availability != "unknown" {
		t.Fatal(got)
	}
}
func TestAlertsTimestampAndClock(t *testing.T) {
	cases := []struct {
		name             string
		sample, received time.Time
	}{
		{"zero", time.Time{}, alertTestStart.Add(time.Second)},
		{"duplicate", alertTestStart, alertTestStart.Add(time.Second)},
		{"backward-data", alertTestStart.Add(-time.Second), alertTestStart.Add(time.Second)},
		{"future", alertTestStart.Add(7 * time.Second), alertTestStart.Add(time.Second)},
		{"old", alertTestStart.Add(time.Second), alertTestStart.Add(17 * time.Second)},
		{"clock-backward", alertTestStart.Add(time.Second), alertTestStart.Add(-time.Second)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := alertTestObserve(NewAlertProcessor(true, alertTestStart), alertTestStart, 90, 900)
			p = p.Process(tc.received, alertTestSummary(tc.sample, 90, 900), "")
			got := p.Snapshot(tc.received).Rules.CPU
			if got.Availability != "unknown" || got.PendingSince != nil {
				t.Fatal(got)
			}
			resume := alertTestStart.Add(20 * time.Second)
			p = alertTestObserve(p, resume, 90, 900)
			if got = p.Snapshot(resume).Rules.CPU; got.Condition != "pending" || !got.PendingSince.Equal(resume) {
				t.Fatal(got)
			}
		})
	}
	p := NewAlertProcessor(true, alertTestStart)
	p = p.Process(alertTestStart, alertTestSummary(alertTestStart.Add(5*time.Second), 90, 900), "")
	if p.Snapshot(alertTestStart).Rules.CPU.Availability != "fresh" {
		t.Fatal("exact future skew rejected")
	}
}
func TestAlertsGapAndProjection(t *testing.T) {
	for _, gap := range []time.Duration{15 * time.Second, 15*time.Second + time.Nanosecond} {
		p := alertTestObserve(NewAlertProcessor(true, alertTestStart), alertTestStart, 90, 900)
		p = alertTestObserve(p, alertTestStart.Add(gap), 90, 900)
		got := p.Snapshot(alertTestStart.Add(gap)).Rules.CPU
		want := alertTestStart
		if gap > 15*time.Second {
			want = want.Add(gap)
		}
		if !got.PendingSince.Equal(want) {
			t.Fatal(gap, got)
		}
	}
	p := alertTestObserve(NewAlertProcessor(true, alertTestStart), alertTestStart, 90, 900)
	if p.Snapshot(alertTestStart.Add(15*time.Second)).Rules.CPU.Availability != "fresh" {
		t.Fatal("exact age stale")
	}
	for i := 0; i < 100; i++ {
		got := p.Snapshot(alertTestStart.Add(time.Hour)).Rules.CPU
		if got.Condition != "pending" || got.Availability != "stale" || got.FiringSince != nil {
			t.Fatal(got)
		}
	}
	// 数据年龄和接收年龄分别受限。
	p = NewAlertProcessor(true, alertTestStart).Process(alertTestStart, alertTestSummary(alertTestStart.Add(-15*time.Second), 90, 900), "")
	if p.Snapshot(alertTestStart.Add(time.Nanosecond)).Rules.CPU.Availability != "stale" {
		t.Fatal("data age ignored")
	}
	p, at := alertTestFiring()
	p = alertTestObserve(p, at.Add(5*time.Second), 0, 0)
	got := p.Snapshot(at.Add(time.Hour)).Rules.CPU
	if got.Condition != "recovering" || got.LastRecoveredAt != nil || got.FiringSince == nil {
		t.Fatal("read recovered event", got)
	}
	p = alertTestObserve(p, at.Add(time.Hour), 0, 0)
	if got = p.Snapshot(at.Add(time.Hour)).Rules.CPU; !got.RecoveringSince.Equal(at.Add(time.Hour)) {
		t.Fatal("gap retained recovery", got)
	}
	p = p.Process(at.Add(time.Hour+time.Second), system.Summary{}, "untrusted secret error text")
	got = p.Snapshot(at.Add(48 * time.Hour)).Rules.CPU
	if got.Availability != "unknown" || got.ReasonCode != "SYSTEM_SUMMARY_UNAVAILABLE" || got.FiringSince == nil {
		t.Fatal(got)
	}
}
func TestAlertsRetentionDisabledAndCopies(t *testing.T) {
	disabled := NewAlertProcessor(false, alertTestStart)
	disabled = alertTestObserve(disabled, alertTestStart, 99, 999)
	if got := disabled.Snapshot(alertTestStart); got.Enabled || got.Rules.CPU.Value != nil || got.Rules.CPU.Availability != "unknown" {
		t.Fatal(got)
	}
	p, at := alertTestFiring()
	for sec := 5; sec <= 65; sec += 5 {
		p = alertTestObserve(p, at.Add(time.Duration(sec)*time.Second), 0, 0)
	}
	recovered := at.Add(65 * time.Second)
	if p.Snapshot(recovered.Add(24*time.Hour-time.Nanosecond)).Rules.CPU.LastRecoveredAt == nil || p.Snapshot(recovered.Add(24*time.Hour)).Rules.CPU.LastRecoveredAt != nil {
		t.Fatal("retention boundary")
	}
	original := p.Snapshot(recovered)
	modified := p.Snapshot(recovered)
	*modified.Rules.CPU.Value = 99
	*modified.Rules.CPU.SampleAt = time.Time{}
	*modified.Rules.CPU.ReceivedAt = time.Time{}
	*modified.Rules.CPU.LastRecoveredAt = time.Time{}
	if got := p.Snapshot(recovered); *got.Rules.CPU.Value != *original.Rules.CPU.Value || !got.Rules.CPU.SampleAt.Equal(*original.Rules.CPU.SampleAt) || !got.Rules.CPU.LastRecoveredAt.Equal(recovered) {
		t.Fatal("snapshot aliases state")
	}
	data, err := json.Marshal(original)
	if err != nil || len(data) > 8192 {
		t.Fatal(len(data), err)
	}
	cold := NewAlertProcessor(true, recovered).Snapshot(recovered)
	if cold.Rules.CPU.LastRecoveredAt != nil || cold.Rules.CPU.FiringSince != nil || cold.Rules.CPU.Value != nil {
		t.Fatal("restart retained state")
	}
	data, _ = json.Marshal(cold)
	if !strings.Contains(string(data), `"value":null`) {
		t.Fatal(string(data))
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s := p.Snapshot(recovered)
				*s.Rules.CPU.Value = 99
				_ = p.Process(recovered.Add(time.Second), alertTestSummary(recovered.Add(time.Second), 90, 900), "")
			}
		}()
	}
	wg.Wait()
}

func TestAlertsIndependentContinuityAndDualGaps(t *testing.T) {
	p := NewAlertProcessor(true, alertTestStart)
	for sec := 0; sec <= 120; sec += 5 {
		at := alertTestStart.Add(time.Duration(sec) * time.Second)
		s := alertTestSummary(at, 90, 900)
		if sec == 60 {
			s.Warnings = []system.SummaryWarning{{Source: "cpu"}}
		}
		p = p.Process(at, s, "")
	}
	got := p.Snapshot(alertTestStart.Add(120 * time.Second))
	if got.Rules.CPU.Condition != "pending" || got.Rules.Memory.Condition != "firing" {
		t.Fatal("one invalid rule interrupted the other", got)
	}
	// 源时间跨度 16 秒、接收跨度仅 1 秒，仍须重置连续性。
	p = NewAlertProcessor(true, alertTestStart)
	p = p.Process(alertTestStart, alertTestSummary(alertTestStart.Add(-15*time.Second), 90, 900), "")
	p = p.Process(alertTestStart.Add(time.Second), alertTestSummary(alertTestStart.Add(time.Second), 90, 900), "")
	if got := p.Snapshot(alertTestStart.Add(time.Second)).Rules.CPU; !got.PendingSince.Equal(alertTestStart.Add(time.Second)) {
		t.Fatal("source gap ignored", got)
	}
	// 接收跨度 16 秒、源时间跨度仅 1 秒（当前数据年龄恰好 15 秒）。
	p = alertTestObserve(NewAlertProcessor(true, alertTestStart), alertTestStart, 90, 900)
	p = p.Process(alertTestStart.Add(16*time.Second), alertTestSummary(alertTestStart.Add(time.Second), 90, 900), "")
	if got := p.Snapshot(alertTestStart.Add(16 * time.Second)).Rules.CPU; !got.PendingSince.Equal(alertTestStart.Add(16 * time.Second)) {
		t.Fatal("receive gap ignored", got)
	}
	// 持续事件不会每 tick 重新开立；失联 48 小时不等于恢复。
	p, at := alertTestFiring()
	firing := *p.Snapshot(at).Rules.CPU.FiringSince
	for sec := 5; sec <= 600; sec += 5 {
		p = alertTestObserve(p, at.Add(time.Duration(sec)*time.Second), 95, 950)
	}
	if got := p.Snapshot(at.Add(48 * time.Hour)).Rules.CPU; got.FiringSince == nil || !got.FiringSince.Equal(firing) || got.LastRecoveredAt != nil || got.Availability != "stale" {
		t.Fatal(got)
	}
}

func TestAlertsMemoryExactUint64Thresholds(t *testing.T) {
	const total = uint64(math.MaxUint64)
	tests := []struct {
		name            string
		used, threshold uint64
		want            int
	}{
		{"trigger-below-rounded", 16602069666338596453, 90, -1},
		{"trigger-above-rounded", 16602069666338596454, 90, 1},
		{"recovery-below-rounded", 15679732462653118872, 85, -1},
		{"recovery-above-rounded", 15679732462653118873, 85, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := compareAlertPercent(tc.used, total, tc.threshold); got != tc.want {
				t.Fatalf("comparison %d, want %d", got, tc.want)
			}
			// 这些不同侧的整数在显示百分比中全部舍入至阈值。
			if got := float64(tc.used) / float64(total) * 100; got != float64(tc.threshold) {
				t.Fatalf("fixture did not round to threshold: %v", got)
			}
			p := NewAlertProcessor(true, alertTestStart)
			if tc.threshold == 85 {
				p, _ = alertTestFiring()
			}
			start := alertTestStart.Add(125 * time.Second)
			for sec := 0; sec <= 120; sec += 5 {
				at := start.Add(time.Duration(sec) * time.Second)
				s := alertTestSummary(at, 0, 0)
				s.Memory.TotalBytes = total
				s.Memory.UsedBytes = tc.used
				p = p.Process(at, s, "")
			}
			want := "normal"
			if (tc.threshold == 90 && tc.want > 0) || (tc.threshold == 85 && tc.want > 0) {
				want = "firing"
			}
			if got := p.Snapshot(start.Add(120 * time.Second)).Rules.Memory; got.Condition != want {
				t.Fatalf("condition %s, want %s", got.Condition, want)
			}
		})
	}
	// 可精确整除的最大量级边界仍包括等号。
	exactTotal := uint64(math.MaxUint64/100) * 100
	for _, threshold := range []uint64{85, 90} {
		used := exactTotal / 100 * threshold
		for _, delta := range []int{-1, 0, 1} {
			candidate := used
			if delta < 0 {
				candidate--
			}
			if delta > 0 {
				candidate++
			}
			if got := compareAlertPercent(candidate, exactTotal, threshold); got != delta {
				t.Fatalf("threshold %d delta %d: %d", threshold, delta, got)
			}
		}
	}
}
