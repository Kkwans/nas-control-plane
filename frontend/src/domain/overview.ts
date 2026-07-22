import type { DockerInventory, SystemSummary } from '@/api/system'

export type OverviewStatus = 'healthy' | 'degraded' | 'attention' | 'pending'

export interface OverviewState {
  status: OverviewStatus
  label: string
  detail: string
}

export function deriveOverviewState(summary: SystemSummary | null, inventory: DockerInventory | null): OverviewState {
  if (!summary) {
    return {
      status: 'pending',
      label: '等待实时数据',
      detail: 'Root Agent 尚未返回系统快照，页面不会展示虚构指标。',
    }
  }
  if (!inventory) {
    return {
      status: 'degraded',
      label: 'Docker 数据待恢复',
      detail: '系统快照可用，Docker Engine 清单将在 Root Agent 恢复后自动刷新。',
    }
  }
  if (summary.cpu.usagePercent >= 85 || usagePercent(summary.memory.usedBytes, summary.memory.totalBytes) >= 90) {
    return {
      status: 'attention',
      label: '资源压力升高',
      detail: '检测到 CPU 或内存使用偏高，建议优先检查运行中的服务。',
    }
  }
  return {
    status: 'healthy',
    label: '运行稳定',
    detail: '系统快照与 Docker 资源清单均已由 Root Agent 同步。',
  }
}

export function usagePercent(used: number, total: number): number {
  if (total <= 0) return 0
  return Math.min(100, Math.max(0, (used / total) * 100))
}

export function formatOneDecimal(value: number): string {
  return new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1, minimumFractionDigits: 1 }).format(value)
}

export function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1)
  const amount = value / 1024 ** index
  return `${amount >= 10 || index === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`
}

export function formatUptime(seconds: number): string {
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  if (days > 0) return `${days} 天 ${hours} 小时`
  return `${hours} 小时`
}

export function totalStorage(summary: SystemSummary | null) {
  return (summary?.storage ?? []).reduce(
    (total, disk) => ({ used: total.used + disk.usedBytes, capacity: total.capacity + disk.totalBytes }),
    { used: 0, capacity: 0 },
  )
}

export function projectStateTone(state: DockerInventory['projects'][number]['state']): OverviewStatus {
  if (state === 'running') return 'healthy'
  if (state === 'degraded') return 'attention'
  return 'degraded'
}
