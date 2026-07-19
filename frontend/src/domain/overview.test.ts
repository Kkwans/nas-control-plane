import { describe, expect, it } from 'vitest'

import { deriveOverviewState, type OverviewSnapshot } from './overview'

describe('deriveOverviewState', () => {
  it('在 Docker 不可用时保留系统总览，并给出可恢复的降级说明', () => {
    const snapshot: OverviewSnapshot = {
      hostname: 'DH4300-PLUS',
      updatedAt: '2026-07-19T09:30:00+08:00',
      cpu: { usage: 36.8, trend: [28, 31, 35, 33, 36.8] },
      memory: { usedGiB: 8.7, totalGiB: 15.6 },
      storage: { usedTiB: 11.4, totalTiB: 24 },
      network: { downMbps: 13.8, upMbps: 4.2 },
      docker: { available: false, activeContainers: 0 },
    }

    expect(deriveOverviewState(snapshot)).toEqual({
      status: 'degraded',
      label: 'Docker 不可用',
      detail: '系统基础信息仍可使用，容器状态会在 Docker 恢复后自动更新。',
    })
  })

  it('在核心信号正常时给出健康状态', () => {
    const snapshot: OverviewSnapshot = {
      hostname: 'DH4300-PLUS',
      updatedAt: '2026-07-19T09:30:00+08:00',
      cpu: { usage: 36.8, trend: [28, 31, 35, 33, 36.8] },
      memory: { usedGiB: 8.7, totalGiB: 15.6 },
      storage: { usedTiB: 11.4, totalTiB: 24 },
      network: { downMbps: 13.8, upMbps: 4.2 },
      docker: { available: true, activeContainers: 14 },
    }

    expect(deriveOverviewState(snapshot)).toEqual({
      status: 'healthy',
      label: '运行稳定',
      detail: '系统与容器信号均在预期范围内。',
    })
  })
})
