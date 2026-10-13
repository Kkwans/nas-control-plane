import type { Site, SiteDiscoveryIssue, SiteDiscoverySummary, SiteListResponse, SiteProfileResponse } from './control'
import { NcpApiError } from './system'

type RecordValue = Record<string, unknown>
const sources = ['auto', 'labels', 'built-in', 'edited', 'manual'] as const

function invalid(): never {
  throw new NcpApiError('CONTROL_RESPONSE_INVALID', '站点接口返回了无法识别的数据。')
}

function record(value: unknown): RecordValue {
  if (!value || typeof value !== 'object' || Array.isArray(value)) invalid()
  return value as RecordValue
}

function text(value: unknown, nonempty = false): string {
  if (typeof value !== 'string' || (nonempty && !value.trim())) invalid()
  return value
}

function boolean(value: unknown): boolean {
  if (typeof value !== 'boolean') invalid()
  return value
}

function integer(value: unknown, min = Number.MIN_SAFE_INTEGER, max = Number.MAX_SAFE_INTEGER): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < min || value > max) invalid()
  return value
}

function timestamp(value: unknown): string {
  const result = text(value, true)
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/i.test(result) || !Number.isFinite(Date.parse(result))) invalid()
  return result
}

function source(value: unknown): Site['source'] {
  if (!sources.some((item) => item === value)) invalid()
  return value as Site['source']
}

function ports(value: unknown): number[] {
  if (value == null) return []
  if (!Array.isArray(value)) invalid()
  return value.map((port) => integer(port, 1, 65535))
}

function profileFields(value: RecordValue, nonemptyName: boolean) {
  return {
    projectId: text(value.projectId, true),
    name: text(value.name, nonemptyName),
    description: value.description == null ? '' : text(value.description),
    iconUrl: value.iconUrl == null ? '' : text(value.iconUrl),
    category: value.category == null ? '' : text(value.category),
    launchUrl: value.launchUrl == null ? '' : text(value.launchUrl),
    primaryPort: integer(value.primaryPort, 0, 65535),
    sortOrder: integer(value.sortOrder),
    favorite: boolean(value.favorite),
    hidden: boolean(value.hidden),
  }
}

function site(value: unknown): Site {
  const item = record(value)
  return {
    ...item,
    ...profileFields(item, true),
    id: text(item.id, true),
    state: text(item.state),
    source: source(item.source),
    lastVisitedAt: item.lastVisitedAt === null ? null : timestamp(item.lastVisitedAt),
    ports: ports(item.ports),
  }
}

function issue(value: unknown): SiteDiscoveryIssue {
  const item = record(value)
  return {
    ...item,
    siteId: text(item.siteId),
    projectId: text(item.projectId),
    name: text(item.name),
    reason: text(item.reason),
    ports: ports(item.ports),
  }
}

function discovery(value: unknown, siteCount: number): SiteDiscoverySummary {
  const item = value == null ? {} : record(value)
  const status = item.status ?? 'unavailable'
  if (status !== 'complete' && status !== 'partial' && status !== 'unavailable') invalid()
  if (item.issues != null && !Array.isArray(item.issues)) invalid()
  // 旧版本缺失的诊断字段保留兼容默认值，不视为完整发现证明。
  return {
    ...item,
    status,
    probeAvailable: item.probeAvailable == null ? false : boolean(item.probeAvailable),
    candidateCount: item.candidateCount == null ? siteCount : integer(item.candidateCount, 0),
    verifiedCount: item.verifiedCount == null ? siteCount : integer(item.verifiedCount, 0),
    failedCount: item.failedCount == null ? 0 : integer(item.failedCount, 0),
    issues: item.issues == null ? [] : (item.issues as unknown[]).map(issue),
  }
}

export function parseSiteListResponse(value: unknown): SiteListResponse {
  const item = record(value)
  if (!Array.isArray(item.sites)) invalid()
  const sites = item.sites.map(site)
  return { ...item, collectedAt: timestamp(item.collectedAt), sites, discovery: discovery(item.discovery, sites.length) }
}

export type SiteProfileContext = { operation: 'ignored' | 'create' } | { operation: 'update'; siteId: string }

// 写入校验只提供解析能力；接入 POST/PUT 前必须同时提供“结果未知”的恢复状态。
export function parseSiteProfileResponse(value: unknown, context: SiteProfileContext): SiteProfileResponse {
  const item = record(value)
  const result: SiteProfileResponse = { ...item, ...profileFields(item, context.operation !== 'ignored') }
  if ('source' in item) result.source = source(item.source)
  if ('ignored' in item) result.ignored = boolean(item.ignored)
  if ('lastVisitedAt' in item) result.lastVisitedAt = item.lastVisitedAt === null ? null : timestamp(item.lastVisitedAt)
  for (const key of ['detectedTitle', 'autoIconUrl', 'localIconName'] as const) {
    if (key in item) result[key] = text(item[key])
  }
  if (context.operation === 'ignored' && result.ignored === false) invalid()
  if (context.operation === 'create' && (!result.projectId.startsWith('manual:') || !result.projectId.slice(7).trim() || (result.source !== undefined && result.source !== 'manual'))) invalid()
  if (context.operation === 'update' && result.projectId !== context.siteId) invalid()
  return result
}

export function parseIgnoredSitesResponse(value: unknown): SiteProfileResponse[] {
  if (!Array.isArray(value)) invalid()
  return value.map((item) => parseSiteProfileResponse(item, { operation: 'ignored' }))
}
