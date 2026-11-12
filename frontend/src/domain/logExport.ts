import type { LogEntry } from '../api/control'

export const LOG_EXPORT_MAX_ROWS = 200
export const LOG_EXPORT_MAX_BYTES = 2 * 1024 * 1024
export const LOG_EXPORT_TOO_LARGE = '当前页日志超过 2 MiB，请减少每页数量后重试。'

export interface LogExportInput {
  entries: readonly LogEntry[]
  source: LogEntry['source']
  page: number
  pageSize: number
  loadedCount: number
  exportedAt: Date
}

export interface LogExportFile { text: string; filename: string; entryCount: number; byteLength: number }

// 逐行 JSON 保留原始字符串语义，同时避免终端控制、双向控制或换行伪造记录。
function safeLine(value: object): string {
  return JSON.stringify(value).replace(/[\u007f-\u009f\u061c\u200e\u200f\u2028-\u202e\u2066-\u2069]/g,
    (character) => `\\u${character.charCodeAt(0).toString(16).padStart(4, '0')}`) + '\n'
}

export function formatLogExport(input: LogExportInput): LogExportFile {
  const { entries, source, page, pageSize, loadedCount } = input
  if (!['system', 'agent', 'container'].includes(source) ||
      !Number.isSafeInteger(page) || page < 1 || !Number.isSafeInteger(pageSize) || pageSize < 1 || pageSize > LOG_EXPORT_MAX_ROWS ||
      !Number.isSafeInteger(loadedCount) || loadedCount < entries.length || entries.length < 1 || entries.length > pageSize || entries.length > LOG_EXPORT_MAX_ROWS) {
    throw new Error('当前页日志范围无效，请刷新后重试。')
  }
  const exportedAt = input.exportedAt.toISOString()
  const encoder = new TextEncoder()
  const lines: string[] = []
  let byteLength = 0
  function append(value: object) {
    const line = safeLine(value)
    byteLength += encoder.encode(line).byteLength
    if (byteLength > LOG_EXPORT_MAX_BYTES) throw new Error(LOG_EXPORT_TOO_LARGE)
    lines.push(line)
  }
  append({ formatVersion: 1, scope: 'current-page', exportedAt, page, pageSize, entryCount: entries.length, loadedCount, order: 'newest-first', source })
  for (const entry of entries) {
    if (!['system', 'agent', 'container'].includes(entry.source) ||
        !['error', 'warning', 'info', 'debug'].includes(entry.level) ||
        (entry.stream !== undefined && !['stdout', 'stderr'].includes(entry.stream))) {
      throw new Error('日志字段无效，请刷新后重试。')
    }
    // 仅检查/复制白名单；不读取 rawMessage、游标或未知字段。长度粗预算先于序列化分配。
    const values = [entry.timestamp, entry.source, entry.unit, entry.level, entry.stream ?? '', entry.message]
    let rawLength = 0
    for (const value of values) {
      if (typeof value !== 'string') throw new Error('日志字段无效，请刷新后重试。')
      rawLength += value.length
      if (rawLength > LOG_EXPORT_MAX_BYTES - byteLength) throw new Error(LOG_EXPORT_TOO_LARGE)
    }
    append({ timestamp: entry.timestamp, source: entry.source, unit: entry.unit, level: entry.level,
      ...(entry.stream ? { stream: entry.stream } : {}), message: entry.message })
  }
  return { text: lines.join(''), filename: `ncp-logs-${source}-page-${page}-${exportedAt.replace(/[-:.]/g, '')}.txt`, entryCount: entries.length, byteLength }
}
