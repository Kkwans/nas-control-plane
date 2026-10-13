import { describe, expect, it } from 'vitest'
import type { LogEntry } from '../api/control'
import { logEntryKey, mergeLogEntries } from './logEntries'

const item: LogEntry = { id: 'id', source: 'container', unit: 'container-A', stream: 'stdout', timestamp: '2026-10-13T00:00:00Z', level: 'info', message: '相同消息' }

describe('历史/实时日志身份', () => {
  it('相同事件去重，独立来源、容器、流及重复消息事件分别保留', () => {
    const distinct = [item, { ...item, source: 'agent' as const }, { ...item, unit: 'container-B' }, { ...item, stream: 'stderr' as const }, { ...item, id: 'other-event' }]
    expect(new Set(distinct.map(logEntryKey)).size).toBe(5)
    expect(mergeLogEntries(distinct, [{ ...item, message: '新版本' }])).toHaveLength(5)
    expect(mergeLogEntries([item], [{ ...item, message: '新版本' }])[0]?.message).toBe('新版本')
  })

  it('实时缓冲保留最新 500 条，历史分页不隐式裁剪', () => {
    const entries = Array.from({ length: 600 }, (_, index) => ({ ...item, id: String(index), timestamp: new Date(index * 1000).toISOString() }))
    expect(mergeLogEntries([], entries, 500)).toHaveLength(500)
    expect(mergeLogEntries([], entries, 500)[0]?.id).toBe('599')
    expect(mergeLogEntries([], entries, 500).at(-1)?.id).toBe('100')
    expect(mergeLogEntries([], entries)).toHaveLength(600)
  })
})
