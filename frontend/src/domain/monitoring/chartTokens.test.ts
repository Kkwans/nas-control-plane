import { describe, expect, it } from 'vitest'

import { monitoringChartTokens } from './chartTokens'

describe('monitoringChartTokens', () => {
  it('keeps a stable semantic palette for charts', () => {
    expect(monitoringChartTokens.cpu).toMatch(/^#[0-9a-f]{6}$/i)
    expect(monitoringChartTokens.temperature).toHaveLength(4)
    expect(monitoringChartTokens.tooltipText).toBe('#ffffff')
  })
})
