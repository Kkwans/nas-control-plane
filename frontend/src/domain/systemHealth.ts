import type { SystemSummary } from '@/api/system'

export const SNAPSHOT_FRESHNESS_MS = 15_000
export const SNAPSHOT_FUTURE_TOLERANCE_MS = 5_000

export type SummarySource = 'host' | 'cpu' | 'memory' | 'storage' | 'network' | 'disk-io' | 'temperature'

export function summarySourceAvailable(summary: SystemSummary | null, source: SummarySource): boolean {
  if (!summary || summary.warnings?.some((warning) => warning.source === source)) return false
  if (source === 'cpu') return finiteNonnegative(summary.cpu.usagePercent) && summary.cpu.usagePercent <= 100 && finiteNonnegative(summary.cpu.load1)
  if (source === 'memory') return finiteNonnegative(summary.memory.usedBytes) && Number.isFinite(summary.memory.totalBytes) && summary.memory.totalBytes > 0 && summary.memory.usedBytes <= summary.memory.totalBytes
  if (source === 'network') return Array.isArray(summary.network) && summary.network.length > 0 && summary.network.every((item) => item && finiteNonnegative(item.receiveBytes) && finiteNonnegative(item.transmitBytes))
  if (source === 'storage') return Array.isArray(summary.storage) && summary.storage.length > 0 && summary.storage.every((item) => item && finiteNonnegative(item.usedBytes) && Number.isFinite(item.totalBytes) && item.totalBytes > 0 && item.usedBytes <= item.totalBytes)
  if (source === 'disk-io') return Boolean(summary.diskIO && finiteNonnegative(summary.diskIO.readBytes) && finiteNonnegative(summary.diskIO.writeBytes))
  return true
}

export function summaryHasUnavailableSources(summary: SystemSummary): boolean {
  const requiredSources = ['cpu', 'memory', 'storage', 'network'] as const
  // 旧版协议的磁盘 I/O 字段可缺省；一旦提供，必须验证其真实数值。
  return Boolean(summary.warnings?.length) || requiredSources.some((source) => !summarySourceAvailable(summary, source)) ||
    (summary.diskIO !== undefined && !summarySourceAvailable(summary, 'disk-io'))
}

export function finiteNonnegative(value: number): boolean {
  return Number.isFinite(value) && value >= 0
}

// 以浏览器时间验证源时间；两端时钟偏差会保守地显示过期/时间无效，
// 不使用事件外层发送时间给旧数据续期。接收后的寿命另由单调时钟限制。
export function snapshotTimestamp(value: string, now = Date.now()): number | null {
  const timestamp = Date.parse(value)
  return Number.isFinite(timestamp) && timestamp > 0 && timestamp <= now + SNAPSHOT_FUTURE_TOLERANCE_MS ? timestamp : null
}

export function snapshotErrorMessage(code: string | null): { label: string; detail: string } | null {
  switch (code) {
    case 'SYSTEM_SUMMARY_STALE':
    case 'DOCKER_INVENTORY_STALE':
      return { label: '显示上次数据', detail: '已显示的数据超过 15 秒，正在等待下一次刷新；这不代表主机已停止运行。' }
    case 'SYSTEM_SUMMARY_UNAVAILABLE':
      return { label: '系统数据待恢复', detail: '最新系统快照暂不可用，保留上次更新的可用数据。' }
    case 'MONITORING_WRITE_FAILED':
      return { label: '历史写入失败', detail: '实时快照仍可用，但最近的监控历史写入失败。' }
    case 'MONITORING_SAMPLE_INCOMPLETE':
      return { label: '部分指标不可用', detail: '部分采集源未返回有效数据，本次快照不会写入监控历史。' }
    case 'MONITORING_SAMPLE_INVALID':
      return { label: '历史样本无效', detail: '最近的快照不符合历史样本校验要求，本次监控历史未记录。' }
    case 'MONITORING_SAMPLE_LIMIT_EXCEEDED':
      return { label: '历史样本超限', detail: '最近的历史样本超过容量限制，本次监控历史未记录。' }
    case 'SYSTEM_SUMMARY_PARTIAL':
      return { label: '部分指标不可用', detail: '部分采集源暂不可用，仅显示其余有效指标。' }
    case 'REALTIME_STREAM_FAILED':
      return { label: '实时连接中断', detail: '正在等待实时连接恢复，保留上次更新的可用数据。' }
    default:
      return code ? { label: '数据同步受限', detail: '最新数据未能完整同步，保留上次更新的可用数据。' } : null
  }
}
