export interface OverviewSnapshot {
  hostname: string
  updatedAt: string
  cpu: {
    usage: number
    trend: number[]
  }
  memory: {
    usedGiB: number
    totalGiB: number
  }
  storage: {
    usedTiB: number
    totalTiB: number
  }
  network: {
    downMbps: number
    upMbps: number
  }
  docker: {
    available: boolean
    activeContainers: number
  }
}

export type OverviewStatus = 'healthy' | 'degraded' | 'attention'

export interface OverviewState {
  status: OverviewStatus
  label: string
  detail: string
}

export function deriveOverviewState(snapshot: OverviewSnapshot): OverviewState {
  if (!snapshot.docker.available) {
    return {
      status: 'degraded',
      label: 'Docker 不可用',
      detail: '系统基础信息仍可使用，容器状态会在 Docker 恢复后自动更新。',
    }
  }

  if (snapshot.cpu.usage >= 85 || memoryUsagePercent(snapshot) >= 90) {
    return {
      status: 'attention',
      label: '需要关注',
      detail: '检测到资源压力升高，建议检查高负载服务与近期任务。',
    }
  }

  return {
    status: 'healthy',
    label: '运行稳定',
    detail: '系统与容器信号均在预期范围内。',
  }
}

export function memoryUsagePercent(snapshot: OverviewSnapshot): number {
  return (snapshot.memory.usedGiB / snapshot.memory.totalGiB) * 100
}

export function storageUsagePercent(snapshot: OverviewSnapshot): number {
  return (snapshot.storage.usedTiB / snapshot.storage.totalTiB) * 100
}

export function formatOneDecimal(value: number): string {
  return new Intl.NumberFormat('zh-CN', {
    maximumFractionDigits: 1,
    minimumFractionDigits: 1,
  }).format(value)
}
