import type { LogEntry, LogResponse } from './control'
import { NcpApiError } from './systemTransport'

function invalid(): never {
  throw new NcpApiError('CONTROL_RESPONSE_INVALID', '日志接口返回了无法识别的数据。')
}

function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) invalid()
  return value as Record<string, unknown>
}

function text(value: unknown): string {
  if (typeof value !== 'string') invalid()
  return value
}

function member<const T extends readonly string[]>(value: unknown, values: T): T[number] {
  if (typeof value !== 'string' || !values.includes(value)) invalid()
  return value
}

function timestamp(value: unknown): string {
  const result = text(value)
  const parts = /^(\d{4})-(\d{2})-(\d{2})T([01]\d|2[0-3]):[0-5]\d:[0-5]\d(?:\.\d+)?(?:Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$/i.exec(result)
  if (!parts || !Number.isFinite(Date.parse(result))) invalid()
  const year = Number(parts[1])
  const month = Number(parts[2])
  const day = Number(parts[3])
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0)
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31][month - 1]
  // 不让 Date.parse 的日期进位掩盖坏响应，也不截断 Go RFC3339Nano 的精度。
  if (!days || day < 1 || day > days) invalid()
  return result
}

function entry(value: unknown): LogEntry {
  const input = record(value)
  const result: LogEntry = {
    id: text(input.id),
    timestamp: timestamp(input.timestamp),
    source: member(input.source, ['system', 'agent', 'container']),
    unit: text(input.unit),
    level: member(input.level, ['error', 'warning', 'info', 'debug']),
    message: text(input.message),
  }
  // Go omitempty 字段允许缺省；可选 null 与空 stream 宽容视为缺省，不传递未知属性。
  if (input.rawMessage != null) result.rawMessage = text(input.rawMessage)
  if (input.stream != null && input.stream !== '') result.stream = member(input.stream, ['stdout', 'stderr'])
  return result
}

export function parseLogResponse(value: unknown): LogResponse {
  const input = record(value)
  // HTTP filterLogEntries 与 SSE incremental 均显式创建空数组，null 不是空页。
  if (!Array.isArray(input.entries)) invalid()
  return {
    collectedAt: timestamp(input.collectedAt),
    entries: Array.from(input.entries, entry),
    nextCursor: text(input.nextCursor),
  }
}
