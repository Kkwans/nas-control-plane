import type { ResourceAlerts } from './alerts'

export function alertsFixture(): ResourceAlerts {
  const rule = { severity: 'warning' as const, triggerPercent: 90, recoveryPercent: 80, triggerSeconds: 120, recoverySeconds: 60, condition: 'normal' as const, availability: 'fresh' as const, reasonCode: '', sampleAt: '2026-10-13T00:00:00Z', receivedAt: '2026-10-13T00:00:00Z', value: 0, pendingSince: null, firingSince: null, recoveringSince: null, lastRecoveredAt: null }
  return { enabled: true, observedAt: rule.sampleAt, startedAt: rule.sampleAt, rules: { cpu: { ...rule, id: 'host.cpu.high' }, memory: { ...rule, id: 'host.memory.high', recoveryPercent: 85 } } }
}
