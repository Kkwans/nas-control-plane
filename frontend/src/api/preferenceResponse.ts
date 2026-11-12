import type { UserPreferences } from './control'
import { NcpApiError } from './system'

/** 成功状态不代表响应可用；保存可能已经生效，只能读取核对，不能自动重放。 */
export class PreferencesWriteUnknownError extends NcpApiError {
  constructor() {
    super('PREFERENCES_WRITE_UNKNOWN', '保存请求返回成功状态，但偏好设置响应无效，保存结果未确认。请重新读取设置后核对。')
    this.name = 'PreferencesWriteUnknownError'
  }
}

export function parsePreferencesResponse(value: unknown, operation: 'read' | 'write' = 'read'): UserPreferences {
  function invalid(): never {
    if (operation === 'write') throw new PreferencesWriteUnknownError()
    throw new NcpApiError('CONTROL_RESPONSE_INVALID', '偏好设置接口返回了无法识别的数据。')
  }
  function integer(value: unknown, min: number, max: number): number {
    if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < min || value > max) invalid()
    return value
  }
  function option<T extends string>(value: unknown, options: readonly T[]): T {
    if (typeof value !== 'string' || !options.includes(value as T)) invalid()
    return value as T
  }

  if (!value || typeof value !== 'object' || Array.isArray(value)) invalid()
  const item = value as Record<string, unknown>
  const order = item.navigationOrder
  // 路由按稳定字符串 ID 传递，保留未来服务端新增的 ID；不在响应边界补齐或删减顺序。
  if (!Array.isArray(order) || new Set(order).size !== order.length) invalid()
  for (const id of order) if (typeof id !== 'string') invalid()
  return {
    ...item,
    refreshIntervalSeconds: integer(item.refreshIntervalSeconds, 2, 300),
    interfaceDensity: option(item.interfaceDensity, ['comfortable', 'compact']),
    baseFontSize: integer(item.baseFontSize, 13, 18),
    pageSize: integer(item.pageSize, 1, 200),
    sidebarDefault: option(item.sidebarDefault, ['collapsed', 'expanded']),
    linkOpenMode: option(item.linkOpenMode, ['new-tab', 'same-tab']),
    siteDefaultProtocol: option(item.siteDefaultProtocol, ['http', 'https']),
    chineseFont: option(item.chineseFont, ['system', 'noto-sans-sc', 'microsoft-yahei', 'source-han-sans-sc', 'misans', 'harmonyos-sans-sc']),
    latinFont: option(item.latinFont, ['system', 'manrope', 'inter', 'ibm-plex-sans']),
    navigationOrder: [...order] as string[],
  }
}
