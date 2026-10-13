import { NcpApiError } from '@/api/system'

export type AlertCondition = 'normal' | 'pending' | 'firing' | 'recovering'
export type AlertAvailability = 'fresh' | 'unknown' | 'stale'
export interface ResourceAlertRule {
  id: 'host.cpu.high' | 'host.memory.high'
  severity: 'warning'
  triggerPercent: number
  recoveryPercent: number
  triggerSeconds: number
  recoverySeconds: number
  condition: AlertCondition
  availability: AlertAvailability
  reasonCode: string
  sampleAt: string | null
  receivedAt: string | null
  value: number | null
  pendingSince: string | null
  firingSince: string | null
  recoveringSince: string | null
  lastRecoveredAt: string | null
}
export interface ResourceAlerts {
  enabled: boolean
  observedAt: string
  startedAt: string
  rules: { cpu: ResourceAlertRule; memory: ResourceAlertRule }
}
const timestampKeys = ['sampleAt', 'receivedAt', 'pendingSince', 'firingSince', 'recoveringSince', 'lastRecoveredAt'] as const
function invalid(): never { throw new NcpApiError('MONITORING_ALERTS_RESPONSE_INVALID', '当前资源提示返回了无法识别的数据。') }
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return invalid()
  return value as Record<string, unknown>
}
function timestamp(value: unknown): value is string {
  if (typeof value !== 'string' || value.length > 40) return false
  const parts = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d{1,9})?(?:Z|[+-](\d{2}):(\d{2}))$/.exec(value)
  if (!parts) return false
  const year = Number(parts[1]), month = Number(parts[2]), day = Number(parts[3])
  const hour = Number(parts[4]), minute = Number(parts[5]), second = Number(parts[6])
  const offsetHour = Number(parts[7] ?? 0), offsetMinute = Number(parts[8] ?? 0)
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0)
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31]
  // Date.parse 会将二月 30 日等非法日期归一化，必须先检查日历与时区字段。
  return month >= 1 && month <= 12 && day >= 1 && day <= days[month - 1]! &&
    hour <= 23 && minute <= 59 && second <= 59 && offsetHour <= 23 && offsetMinute <= 59 &&
    Number.isFinite(Date.parse(value))
}
function rule(value: unknown, kind: 'cpu' | 'memory'): ResourceAlertRule {
  const item = object(value)
  if (item.id !== `host.${kind}.high` || item.severity !== 'warning' || item.triggerPercent !== 90 || item.recoveryPercent !== (kind === 'cpu' ? 80 : 85) || item.triggerSeconds !== 120 || item.recoverySeconds !== 60) return invalid()
  if (typeof item.condition !== 'string' || !['normal', 'pending', 'firing', 'recovering'].includes(item.condition) || typeof item.availability !== 'string' || !['fresh', 'unknown', 'stale'].includes(item.availability)) return invalid()
  if (typeof item.reasonCode !== 'string' || item.reasonCode.length > 128) return invalid()
  for (const key of timestampKeys) if (item[key] !== null && !timestamp(item[key])) return invalid()
  if (item.value !== null && (typeof item.value !== 'number' || !Number.isFinite(item.value) || item.value < 0 || item.value > 100)) return invalid()
  if ((item.value === null) !== (item.sampleAt === null) || (item.value === null) !== (item.receivedAt === null)) return invalid()
  if (item.availability === 'fresh' && (item.value === null || item.reasonCode !== '')) return invalid()
  if (item.condition === 'pending' && item.pendingSince === null) return invalid()
  if ((item.condition === 'firing' || item.condition === 'recovering') && item.firingSince === null) return invalid()
  if (item.condition === 'recovering' && item.recoveringSince === null) return invalid()
  return { ...item } as unknown as ResourceAlertRule
}
/** 必须字段（包括 null）全部校验；额外对象字段和未来原因码不破坏兼容。 */
export function parseResourceAlerts(value: unknown): ResourceAlerts {
  const item = object(value)
  if (typeof item.enabled !== 'boolean' || !timestamp(item.observedAt) || !timestamp(item.startedAt)) return invalid()
  const rules = object(item.rules)
  return { enabled: item.enabled, observedAt: item.observedAt, startedAt: item.startedAt, rules: { cpu: rule(rules.cpu, 'cpu'), memory: rule(rules.memory, 'memory') } }
}

/** 响应时钟不是数据时钟；静止数据和挂起请求仍在本地过期。 */
export function alertAvailability(rule: ResourceAlertRule, now: number): AlertAvailability {
  if (rule.availability === 'unknown') return 'unknown'
  if (rule.sampleAt === null || rule.receivedAt === null) return 'unknown'
  const ages = [now - Date.parse(rule.sampleAt), now - Date.parse(rule.receivedAt)]
  if (!Number.isFinite(now) || ages.some((age) => age < -5000)) return 'unknown'
  if (rule.availability === 'stale' || ages.some((age) => age > 15000)) return 'stale'
  return 'fresh'
}
export function alertPresentation(rule: ResourceAlertRule, now: number, readFailed = false) {
  const availability = alertAvailability(rule, now)
  const unavailable = availability !== 'fresh' || readFailed
  const active = rule.condition === 'firing' || rule.condition === 'recovering'
  if (unavailable) return {
    label: availability === 'stale' ? '数据已过期' : '暂不可评估', tone: 'neutral' as const,
    detail: active ? '此前已触发，数据不可用，恢复尚未确认。' : '缺少当前有效数据，不能判断本规则状态。',
    current: false,
  }
  const states = {
    normal: { label: '未达到告警条件', tone: 'healthy' as const, detail: '未达到本规则告警条件。' },
    pending: { label: '等待持续确认', tone: 'pending' as const, detail: '已达到触发阈值，等待完整的 120 秒合格观察。' },
    firing: { label: '已触发', tone: 'degraded' as const, detail: '持续高使用率已触发；尚未确认恢复。' },
    recovering: { label: '恢复观察', tone: 'pending' as const, detail: '已达到恢复阈值，等待完整的 60 秒合格观察。' },
  }
  return { ...states[rule.condition], current: true }
}
