package monitoring

import (
	"math"
	"math/bits"
	"time"

	"github.com/Kkwans/nas-control-plane/internal/system"
)

const (
	alertMaxAge            = 15 * time.Second
	alertFutureSkew        = 5 * time.Second
	alertTriggerDuration   = 120 * time.Second
	alertRecoveryDuration  = 60 * time.Second
	alertRecoveryRetention = 24 * time.Hour
)

// AlertsSnapshot 是当前进程的固定规则投影；读取不会推进告警计时。
type AlertsSnapshot struct {
	Enabled    bool       `json:"enabled"`
	ObservedAt time.Time  `json:"observedAt"`
	StartedAt  time.Time  `json:"startedAt"`
	Rules      AlertRules `json:"rules"`
}
type AlertRules struct {
	CPU    AlertRuleSnapshot `json:"cpu"`
	Memory AlertRuleSnapshot `json:"memory"`
}
type AlertRuleSnapshot struct {
	ID              string     `json:"id"`
	Severity        string     `json:"severity"`
	TriggerPercent  float64    `json:"triggerPercent"`
	RecoveryPercent float64    `json:"recoveryPercent"`
	TriggerSeconds  int        `json:"triggerSeconds"`
	RecoverySeconds int        `json:"recoverySeconds"`
	Condition       string     `json:"condition"`
	Availability    string     `json:"availability"`
	ReasonCode      string     `json:"reasonCode"`
	SampleAt        *time.Time `json:"sampleAt"`
	ReceivedAt      *time.Time `json:"receivedAt"`
	Value           *float64   `json:"value"`
	PendingSince    *time.Time `json:"pendingSince"`
	FiringSince     *time.Time `json:"firingSince"`
	RecoveringSince *time.Time `json:"recoveringSince"`
	LastRecoveredAt *time.Time `json:"lastRecoveredAt"`
}

// AlertProcessor 是不可变的有界值。调用方发布 Process 的返回值并保护读写；
// 已发布的副本可并发读取。运行计时保留 time.Time 的单调时钟部分。
type AlertProcessor struct {
	enabled                                bool
	startedAt, lastAttemptAt, lastSampleAt time.Time
	cpu, memory                            alertRuleState
}
type alertRuleState struct {
	condition, reason                                           string
	sampleAt, receivedAt                                        time.Time
	value                                                       float64
	hasValue                                                    bool
	pendingSince, firingSince, recoveringSince, lastRecoveredAt time.Time
}

func NewAlertProcessor(enabled bool, startedAt time.Time) AlertProcessor {
	initial := alertRuleState{condition: "normal", reason: "MONITORING_ALERTS_NO_SAMPLE"}
	return AlertProcessor{enabled: enabled, startedAt: startedAt, cpu: initial, memory: initial}
}

// Process 消费一次源结果。sourceCode 为采集器的源/全快照校验错误码；
// 空字符串表示全快照校验通过。本函数仍独立检查时间与逐规则字段。
// SQL 失败不应传入 sourceCode。禁用状态不消费数据，也不伪造恢复。
func (p AlertProcessor) Process(now time.Time, s system.Summary, sourceCode string) AlertProcessor {
	if !p.enabled {
		return p
	}
	if now.IsZero() || now.Before(p.startedAt) || (!p.lastAttemptAt.IsZero() && now.Before(p.lastAttemptAt)) {
		p.cpu.interrupt("MONITORING_ALERTS_CLOCK_INVALID")
		p.memory.interrupt("MONITORING_ALERTS_CLOCK_INVALID")
		return p
	}
	p.lastAttemptAt = now
	if sourceCode == "" && (s.CollectedAt.IsZero() || s.CollectedAt.After(now.Add(alertFutureSkew)) || now.Sub(s.CollectedAt) > alertMaxAge || (!p.lastSampleAt.IsZero() && !s.CollectedAt.After(p.lastSampleAt))) {
		sourceCode = "MONITORING_INVALID_TIMESTAMP"
	}
	if sourceCode != "" {
		switch sourceCode {
		case "SYSTEM_SUMMARY_UNAVAILABLE", "MONITORING_INVALID_TIMESTAMP", "MONITORING_SAMPLE_INVALID", "MONITORING_SAMPLE_LIMIT_EXCEEDED":
		default:
			sourceCode = "SYSTEM_SUMMARY_UNAVAILABLE"
		}
		p.cpu.interrupt(sourceCode)
		p.memory.interrupt(sourceCode)
		return p
	}
	p.lastSampleAt = s.CollectedAt
	cpuCode, memoryCode := "", ""
	for _, w := range s.Warnings {
		switch w.Source {
		case "cpu":
			cpuCode = "MONITORING_ALERTS_CPU_UNAVAILABLE"
		case "memory":
			memoryCode = "MONITORING_ALERTS_MEMORY_UNAVAILABLE"
		}
	}
	if cpuCode == "" && (math.IsNaN(s.CPU.UsagePercent) || math.IsInf(s.CPU.UsagePercent, 0) || s.CPU.UsagePercent < 0 || s.CPU.UsagePercent > 100) {
		cpuCode = "MONITORING_ALERTS_CPU_INVALID"
	}
	if memoryCode == "" && (s.Memory.TotalBytes == 0 || s.Memory.UsedBytes > s.Memory.TotalBytes) {
		memoryCode = "MONITORING_ALERTS_MEMORY_INVALID"
	}
	p.cpu.observe(now, s.CollectedAt, s.CPU.UsagePercent, s.CPU.UsagePercent >= 90, s.CPU.UsagePercent <= 80, cpuCode)
	memoryPercent := 0.0
	if memoryCode == "" {
		memoryPercent = float64(s.Memory.UsedBytes) / float64(s.Memory.TotalBytes) * 100
	}
	p.memory.observe(now, s.CollectedAt, memoryPercent, compareAlertPercent(s.Memory.UsedBytes, s.Memory.TotalBytes, 90) >= 0, compareAlertPercent(s.Memory.UsedBytes, s.Memory.TotalBytes, 85) <= 0, memoryCode)
	return p
}

// compareAlertPercent 精确比较 100*used 与 threshold*total；128 位乘积避免
// uint64 溢出，也避免百分比浮点显示值在阈值附近舍入后改变状态。
func compareAlertPercent(used, total, threshold uint64) int {
	usedHi, usedLo := bits.Mul64(used, 100)
	thresholdHi, thresholdLo := bits.Mul64(total, threshold)
	if usedHi < thresholdHi || (usedHi == thresholdHi && usedLo < thresholdLo) {
		return -1
	}
	if usedHi > thresholdHi || (usedHi == thresholdHi && usedLo > thresholdLo) {
		return 1
	}
	return 0
}

func (r *alertRuleState) interrupt(reason string) {
	r.reason = reason
	r.pendingSince = time.Time{}
	r.recoveringSince = time.Time{}
	if !r.firingSince.IsZero() {
		r.condition = "firing"
	} else {
		r.condition = "normal"
	}
}
func (r *alertRuleState) observe(now, sampleAt time.Time, value float64, trigger, recovered bool, code string) {
	if code != "" {
		r.interrupt(code)
		return
	}
	if r.hasValue && (now.Sub(r.receivedAt) > alertMaxAge || sampleAt.Sub(r.sampleAt) > alertMaxAge) {
		r.interrupt("")
	}
	r.reason = ""
	r.value, r.hasValue, r.sampleAt, r.receivedAt = value, true, sampleAt, now
	switch r.condition {
	case "normal", "pending":
		if !trigger {
			r.condition = "normal"
			r.pendingSince = time.Time{}
			return
		}
		if r.pendingSince.IsZero() {
			r.pendingSince = now
			r.condition = "pending"
		}
		if now.Sub(r.pendingSince) >= alertTriggerDuration {
			r.condition = "firing"
			r.firingSince = now
			r.pendingSince = time.Time{}
		}
	case "firing", "recovering":
		if !recovered {
			r.condition = "firing"
			r.recoveringSince = time.Time{}
			return
		}
		if r.recoveringSince.IsZero() {
			r.recoveringSince = now
			r.condition = "recovering"
		}
		if now.Sub(r.recoveringSince) >= alertRecoveryDuration {
			r.condition = "normal"
			r.lastRecoveredAt = now
			r.firingSince, r.recoveringSince = time.Time{}, time.Time{}
		}
	}
}

func (p AlertProcessor) Snapshot(now time.Time) AlertsSnapshot {
	return AlertsSnapshot{Enabled: p.enabled, ObservedAt: now, StartedAt: p.startedAt,
		Rules: AlertRules{CPU: p.cpu.snapshot(now, p.startedAt, p.lastAttemptAt, "host.cpu.high", 80), Memory: p.memory.snapshot(now, p.startedAt, p.lastAttemptAt, "host.memory.high", 85)}}
}
func alertTimePointer(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
func (r alertRuleState) snapshot(now, startedAt, lastAttemptAt time.Time, id string, recovery float64) AlertRuleSnapshot {
	out := AlertRuleSnapshot{ID: id, Severity: "warning", TriggerPercent: 90, RecoveryPercent: recovery,
		TriggerSeconds: 120, RecoverySeconds: 60, Condition: r.condition, Availability: "fresh", ReasonCode: r.reason,
		SampleAt: alertTimePointer(r.sampleAt), ReceivedAt: alertTimePointer(r.receivedAt),
		PendingSince: alertTimePointer(r.pendingSince), FiringSince: alertTimePointer(r.firingSince), RecoveringSince: alertTimePointer(r.recoveringSince)}
	if r.hasValue {
		value := r.value
		out.Value = &value
	}
	if now.IsZero() || now.Before(startedAt) || (!lastAttemptAt.IsZero() && now.Before(lastAttemptAt)) {
		out.Availability, out.ReasonCode = "unknown", "MONITORING_ALERTS_CLOCK_INVALID"
	} else if r.reason != "" || !r.hasValue {
		out.Availability = "unknown"
	} else if now.Sub(r.sampleAt) > alertMaxAge || now.Sub(r.receivedAt) > alertMaxAge {
		out.Availability, out.ReasonCode = "stale", "MONITORING_ALERTS_STALE"
	}
	if !r.lastRecoveredAt.IsZero() && now.Sub(r.lastRecoveredAt) >= 0 && now.Sub(r.lastRecoveredAt) < alertRecoveryRetention {
		out.LastRecoveredAt = alertTimePointer(r.lastRecoveredAt)
	}
	return out
}
