import type { LogEntry } from '../api/control'

/** 游标/容器散列仅在所属来源与资源内作为身份，不按消息文本去重。 */
export function logEntryKey(entry: LogEntry): string {
  return JSON.stringify([entry.source, entry.unit, entry.stream ?? '', entry.id])
}

export function mergeLogEntries(previous: LogEntry[], incoming: LogEntry[], limit?: number): LogEntry[] {
  const merged = new Map(previous.map((entry) => [logEntryKey(entry), entry]))
  for (const entry of incoming) merged.set(logEntryKey(entry), entry)
  const ordered = [...merged.values()].sort((left, right) => Date.parse(right.timestamp) - Date.parse(left.timestamp))
  return limit === undefined ? ordered : ordered.slice(0, limit)
}
