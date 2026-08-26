import { describe, expect, it } from 'vitest'

import type { MetricSample } from '@/api/control'

import { mergeMetricSampleWindow } from './series'

function sample(collectedAt: string, cpuPercent: number): MetricSample {
  return { collectedAt, cpuPercent, memoryPercent: 20, load1: 1, diskPercent: 40, networkReceiveBytes: 1, networkTransmitBytes: 1 }
}

describe('mergeMetricSampleWindow', () => {
  it('sorts samples, replaces duplicate timestamps, and trims outside the active range', () => {
    const result = mergeMetricSampleWindow([
      sample('2026-08-27T00:00:02Z', 2),
      sample('2026-08-27T00:00:00Z', 0),
      sample('2026-08-26T23:59:59Z', -1),
    ], sample('2026-08-27T00:00:02Z', 22), Date.parse('2026-08-27T00:00:00Z'), Date.parse('2026-08-27T00:00:03Z'))

    expect(result.map((item) => item.cpuPercent)).toEqual([0, 22])
  })
})
