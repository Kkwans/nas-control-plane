import { describe, expect, it } from 'vitest'

import { deriveOverviewState, formatBytes, usagePercent } from './overview'

describe('deriveOverviewState', () => {
  it('keeps the UI honest when Root Agent has not returned a system snapshot', () => {
    expect(deriveOverviewState(null, null)).toEqual({
      status: 'pending',
      label: '等待实时数据',
      detail: 'Root Agent 尚未返回系统快照，页面不会展示虚构指标。',
    })
  })

  it('shows a recoverable degraded state when Docker inventory is unavailable', () => {
    expect(
      deriveOverviewState(
        {
          collectedAt: '2026-07-23T12:00:00Z',
          host: { hostname: 'DH4300-PLUS', operatingSystem: 'Debian', kernelVersion: '6.1', architecture: 'arm64', uptimeSeconds: 1, processCount: 1 },
          cpu: { usagePercent: 22, logicalCores: 4, load1: 0, load5: 0, load15: 0 },
          memory: { totalBytes: 100, usedBytes: 30, availableBytes: 70 },
          storage: [],
          network: [],
          warnings: [],
        },
        null,
      ),
    ).toMatchObject({ status: 'degraded' })
  })
})

describe('overview formatters', () => {
  it('formats ratios and byte values from the live API units', () => {
    expect(usagePercent(25, 100)).toBe(25)
    expect(formatBytes(1024 * 1024 * 1024)).toBe('1.0 GB')
  })
})
