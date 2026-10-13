// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, type App } from 'vue'
import RealtimeTrendChart from './RealtimeTrendChart.vue'

type ChartOption = { tooltip: { formatter: (value: unknown) => string } }
let option: ChartOption | undefined
vi.mock('vue-echarts', () => ({ default: { props: ['option'], setup: (props: { option: ChartOption }) => () => { option = props.option; return null } } }))
let app: App | undefined
let host: HTMLDivElement
afterEach(() => { app?.unmount(); host?.remove(); option = undefined })

describe('趋势图的缺值文案', () => {
  it('最新样本不可用时不会把旧有限值宣布为当前值，null tooltip 也不会变成零', () => {
    host = document.createElement('div'); document.body.append(host)
    app = createApp({ render: () => h(RealtimeTrendChart, {
      timestamps: ['2026-10-13T00:00:00Z', '2026-10-13T00:00:05Z'],
      series: [{ name: 'CPU', color: '#000000', values: [20, Number.NaN], unit: '%' }],
    }) })
    app.mount(host)
    expect(host.textContent).toContain('CPU最新样本：暂无数据')
    expect(host.textContent).not.toContain('20.0')
    const tooltip = option!.tooltip.formatter([{ seriesName: 'CPU', dataIndex: 1, value: null }])
    expect(tooltip).toContain('CPU：暂无数据')
    expect(tooltip).not.toContain('0.0 %')
  })
})
