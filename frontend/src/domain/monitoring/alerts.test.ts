import { describe, expect, it } from 'vitest'
import { alertAvailability, alertPresentation, parseResourceAlerts } from './alerts'
import { alertsFixture } from './alerts.fixture'

const base = Date.parse('2026-10-13T00:00:00Z')
describe('当前资源提示契约与投影', () => {
  it('accepts zero, explicit nullable cold start, disabled and extra future fields', () => {
    expect(parseResourceAlerts({ ...alertsFixture(), future: true }).rules.cpu.value).toBe(0)
    const value = alertsFixture(); value.enabled = false
    for (const rule of Object.values(value.rules)) Object.assign(rule, { availability: 'unknown', reasonCode: 'FUTURE_REASON', value: null, sampleAt: null, receivedAt: null })
    expect(parseResourceAlerts(value).enabled).toBe(false)
  })
  it.each(['sampleAt', 'receivedAt', 'value', 'pendingSince', 'firingSince', 'recoveringSince', 'lastRecoveredAt', 'reasonCode', 'condition', 'availability', 'severity', 'triggerSeconds'])('rejects missing %s in a successful response', (key) => {
    const value = alertsFixture(); delete (value.rules.cpu as unknown as Record<string, unknown>)[key]
    expect(() => parseResourceAlerts(value)).toThrow()
  })
  it.each([NaN, Infinity, -1, 101, '0', undefined])('rejects invalid value %s', (bad) => {
    const value = alertsFixture(); Object.assign(value.rules.memory, { value: bad })
    expect(() => parseResourceAlerts(value)).toThrow()
  })
  it('rejects missing rules, invalid enums and incoherent active states', () => {
    expect(() => parseResourceAlerts({ ...alertsFixture(), rules: {} })).toThrow()
    const value = alertsFixture(); Object.assign(value.rules.cpu, { condition: 'resolved' }); expect(() => parseResourceAlerts(value)).toThrow()
    Object.assign(value.rules.cpu, { condition: 'firing' }); expect(() => parseResourceAlerts(value)).toThrow()
    Object.assign(value.rules.cpu, { condition: 'normal', availability: 'healthy' }); expect(() => parseResourceAlerts(value)).toThrow()
  })
  it.each([
    { condition: ['normal'] }, { condition: { toString: (): string => 'normal' } },
    { availability: ['fresh'] }, { availability: { toString: (): string => 'fresh' } },
  ])('rejects non-string state enums: %s', (bad) => {
    const value = alertsFixture(); Object.assign(value.rules.cpu, bad)
    expect(() => parseResourceAlerts(value)).toThrow()
  })
  it.each([
    '2026-02-30T00:00:00Z', '2026-02-29T00:00:00Z', '2100-02-29T00:00:00Z',
    '2026-04-31T00:00:00Z', '2026-00-01T00:00:00Z', '2026-13-01T00:00:00Z',
    '2026-10-00T00:00:00Z', '2026-10-13T24:00:00Z', '2026-10-13T00:60:00Z',
    '2026-10-13T00:00:60Z', '2026-10-13T00:00:00+24:00', '2026-10-13T00:00:00+00:60',
    '2026-10-13T00:00:00.1234567890Z',
  ])('rejects invalid RFC3339 calendar or offset fields: %s', (bad) => {
    const value = alertsFixture(); value.observedAt = bad
    expect(() => parseResourceAlerts(value)).toThrow()
    value.observedAt = '2026-10-13T00:00:00Z'; value.rules.cpu.sampleAt = bad
    expect(() => parseResourceAlerts(value)).toThrow()
  })
  it.each([
    '2024-02-29T23:59:59Z', '2000-02-29T00:00:00Z',
    '2026-10-13T12:30:45.123456789+08:00', '2026-10-13T12:30:45.1-05:30',
    '2026-10-13T12:30:45+23:59',
  ])('accepts valid leap days, timezone offsets and nanosecond fractions: %s', (time) => {
    const value = alertsFixture(); value.observedAt = time; value.rules.cpu.sampleAt = time
    expect(parseResourceAlerts(value).observedAt).toBe(time)
  })
  it('uses both data timestamps at the exact 15 second boundary, never response clock', () => {
    const value = alertsFixture(); value.observedAt = '2026-10-13T00:10:00Z'
    expect(alertAvailability(value.rules.cpu, base + 15000)).toBe('fresh')
    expect(alertAvailability(value.rules.cpu, base + 15001)).toBe('stale')
    value.rules.cpu.sampleAt = '2026-10-13T00:00:10Z'
    expect(alertAvailability(value.rules.cpu, base + 15001)).toBe('stale')
    expect(alertAvailability(value.rules.cpu, base - 5001)).toBe('unknown')
  })
  it('never converts a fired unknown/stale/read-failed rule to healthy or resolved', () => {
    const rule = alertsFixture().rules.cpu; Object.assign(rule, { condition: 'firing', firingSince: rule.sampleAt })
    for (const availability of ['unknown', 'stale', 'fresh'] as const) {
      rule.availability = availability
      const state = alertPresentation(rule, base, availability === 'fresh')
      expect(state.current).toBe(false); expect(state.tone).toBe('neutral'); expect(state.detail).toContain('恢复尚未确认')
    }
  })
})
