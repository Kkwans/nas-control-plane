// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type App } from 'vue'
import { mockReducedMotion } from '@/test/reducedMotion'
import RealtimeTrendChart, { type TrendSeries } from './RealtimeTrendChart.vue'

type ChartSeries = {
  name: string
  data: (number | null | [number, number | null])[]
  smooth: number | boolean
  connectNulls: boolean
  showSymbol: boolean
  symbolSize: number
  symbol?: (value: unknown, params: { dataIndex: number }) => string
}
type ChartOption = {
  animation: boolean
  tooltip: { formatter: (value: unknown) => string }
  xAxis: {
    type: string
    min?: number
    max?: number
    data?: string[]
    axisLabel: { formatter?: (value: number) => string }
  }
  grid: { left: number; right: number; top: number; bottom: number }
  legend: { show: boolean; data: string[]; selected: Record<string, boolean> }
  yAxis: { min: number | ((range: { min: number }) => number); name: string; show?: boolean }[]
  series: ChartSeries[]
}
let option: ChartOption | undefined
vi.mock('vue-echarts', () => ({ default: { props: ['option'], setup: (props: { option: ChartOption }) => () => { option = props.option; return null } } }))
let app: App | undefined
let host: HTMLDivElement
afterEach(() => { app?.unmount(); host?.remove(); app = undefined; option = undefined; vi.unstubAllGlobals() })

const start = new Date(2026, 9, 13, 0, 0, 0).valueOf()
const minute = 60_000

function mountChart(props: { timestamps: (string | number)[]; series: TrendSeries[]; history?: boolean; emptyMessage?: string; unit?: string }) {
  host = document.createElement('div'); document.body.append(host)
  app = createApp({ render: () => h(RealtimeTrendChart, props) })
  app.mount(host)
}

describe('趋势图的缺值文案', () => {
  it('最新样本不可用时不会把旧有限值宣布为当前值，null tooltip 也不会变成零', () => {
    mountChart({
      timestamps: ['2026-10-13T00:00:00Z', '2026-10-13T00:00:05Z'],
      series: [{ name: 'CPU', color: '#000000', values: [20, Number.NaN], unit: '%' }],
    })
    expect(host.textContent).toContain('CPU最新样本：暂无数据')
    expect(host.textContent).not.toContain('20.0')
    const tooltip = option!.tooltip.formatter([{ seriesName: 'CPU', dataIndex: 1, value: null }])
    expect(tooltip).toContain('CPU：暂无数据')
    expect(tooltip).not.toContain('0.0 %')
    expect(option!.series[0]!.data).toEqual([20, null])
  })

  it('历史仅有一个不可计算的速率样本时使用历史文案', () => {
    mountChart({
      history: true,
      timestamps: [start],
      series: [{ name: '接收', color: '#000000', values: [null], unit: 'KB/s' }],
    })
    expect(option).toBeUndefined()
    expect(host.textContent).toContain('一条已保存样本，暂不能计算速率。')
    expect(host.textContent).not.toContain('实时样本')
    expect(host.textContent).toContain('接收最新样本：暂无数据')
  })

  it('历史全缺值保持空态并尊重专用提示', () => {
    mountChart({
      history: true,
      timestamps: [start, start + minute],
      series: [{ name: '温度', color: '#000000', values: [null, Number.POSITIVE_INFINITY] }],
      emptyMessage: '当前范围没有可用温度读数。',
    })
    expect(option).toBeUndefined()
    expect(host.textContent).toContain('当前范围没有可用温度读数。')
    expect(host.textContent).not.toContain('实时样本')
  })
})

describe('趋势图的默认实时模式', () => {
  it('保留分类轴、平滑折线与隐藏常规圆点', () => {
    mountChart({
      timestamps: ['2026-10-13T00:00:00', '2026-10-13T00:00:05'],
      series: [{ name: 'CPU', color: '#000000', values: [20, 25], unit: '%' }],
    })
    expect(option!.xAxis.type).toBe('category')
    expect(option!.xAxis.data).toEqual(['00:00:00', '00:00:05'])
    expect(option!.xAxis.min).toBeUndefined()
    expect(option!.yAxis.map((axis) => axis.min)).toEqual([0, 0])
    expect(option!.series[0]).toMatchObject({ data: [20, 25], smooth: 0.24, connectNulls: false, showSymbol: false, symbolSize: 7 })
    expect(option!.tooltip.formatter([{ seriesName: 'CPU', dataIndex: 1, value: 25 }])).toContain('2026/10/13 00:00:05')
  })

  it('单个实时样本仍等待第二个快照', () => {
    mountChart({
      timestamps: ['2026-10-13T00:00:00Z'],
      series: [{ name: 'CPU', color: '#000000', values: [20] }],
    })
    expect(option).toBeUndefined()
    expect(host.textContent).toContain('正在积累实时样本，收到第二个有效快照后开始绘制趋势。')
    expect(host.querySelector('[aria-busy]')?.getAttribute('aria-busy')).toBe('true')
  })
})

describe('历史时间轴与独立缺口', () => {
  it('用实际毫秒和 null 元组保留长缺口，不压缩时间或跨缺口连线', () => {
    const timestamps = [start, start + minute, start + 30 * minute, start + 60 * minute, start + 61 * minute]
    mountChart({
      history: true,
      timestamps,
      series: [{ name: '内存', color: '#000000', values: [37, 37, null, 43, 43], unit: '%' }],
    })
    expect(option!.xAxis.type).toBe('time')
    expect(option!.xAxis.min).toBe(start)
    expect(option!.xAxis.max).toBe(start + 61 * minute)
    expect(option!.xAxis.data).toBeUndefined()
    expect(option!.xAxis.axisLabel.formatter!(start + minute)).toBe('00:01')
    expect(option!.series[0]!.data).toEqual([
      [timestamps[0], 37], [timestamps[1], 37], [timestamps[2], null], [timestamps[3], 43], [timestamps[4], 43],
    ])
    expect(option!.series[0]).toMatchObject({ smooth: false, connectNulls: false, showSymbol: true, symbolSize: 4 })
    expect(host.querySelector('[aria-busy]')?.getAttribute('aria-busy')).toBe('false')
  })

  it('历史兼容字符串时间，跨日期刻度显示月日', () => {
    mountChart({
      history: true,
      timestamps: ['2026-10-13T23:59:00', '2026-10-14T00:00:00'],
      series: [{ name: '内存', color: '#000000', values: [37, 43] }],
    })
    const last = new Date(2026, 9, 14, 0, 0, 0).valueOf()
    expect(option!.xAxis.max).toBe(last)
    expect(option!.series[0]!.data).toEqual([[last - minute, 37], [last, 43]])
    expect(option!.xAxis.axisLabel.formatter!(last)).toBe('10/14 00:00:00')
  })

  it('单个真实零值可见，并给时间轴前后各 30 秒范围', () => {
    mountChart({
      history: true,
      timestamps: [start],
      series: [{ name: '温度', color: '#000000', values: [0], unit: '°C' }],
    })
    expect(option!.xAxis).toMatchObject({ type: 'time', min: start - 30_000, max: start + 30_000 })
    expect(option!.series[0]!.data).toEqual([[start, 0]])
    expect(option!.series[0]!.symbol!([start, 0], { dataIndex: 0 })).toBe('circle')
    expect(host.textContent).toContain('温度最新样本：0.0 °C')
    expect(host.textContent).not.toContain('趋势数据未就绪')
  })

  it('两个计数样本只产生一个有效速率点时显示孤立圆点', () => {
    mountChart({
      history: true,
      timestamps: [start, start + minute],
      series: [{ name: '接收', color: '#000000', values: [null, 12], unit: 'KB/s' }],
    })
    const series = option!.series[0]!
    expect(series.data).toEqual([[start, null], [start + minute, 12]])
    expect(series.symbol!([start, null], { dataIndex: 0 })).toBe('none')
    expect(series.symbol!([start + minute, 12], { dataIndex: 1 })).toBe('circle')
    expect(option!.tooltip.formatter([{ seriesName: '接收', dataIndex: 0, value: [start, null] }])).toContain('接收：暂无数据')
  })

  it('只为有限孤立值显示圆点，连续线段和非法值不显示圆点', () => {
    const values = [10, 11, null, 20, Number.NaN, Number.POSITIVE_INFINITY, 0]
    mountChart({
      history: true,
      timestamps: values.map((_, index) => start + index * minute),
      series: [{ name: '内存', color: '#000000', values }],
    })
    const series = option!.series[0]!
    expect(values.map((_, dataIndex) => series.symbol!(series.data[dataIndex], { dataIndex }))).toEqual(['none', 'none', 'none', 'circle', 'none', 'none', 'circle'])
    expect(series.data[4]).toEqual([start + 4 * minute, null])
    expect(series.data[5]).toEqual([start + 5 * minute, null])
  })

  it('传感器各自保留缺值，最后缺读数不会回退播报旧温度', () => {
    const timestamps = [start, start + minute, start + 2 * minute]
    mountChart({
      history: true,
      timestamps,
      series: [
        { name: 'coretemp:Package id 0', color: '#000000', values: [40, 41, 42], unit: '°C' },
        { name: 'coretemp:Core 0', color: '#111111', values: [38, null, null], unit: '°C' },
        { name: 'nvme:Composite', color: '#222222', values: [null, 0, 30], unit: '°C' },
      ],
    })
    expect(option!.series.map((series) => series.data)).toEqual([
      [[timestamps[0], 40], [timestamps[1], 41], [timestamps[2], 42]],
      [[timestamps[0], 38], [timestamps[1], null], [timestamps[2], null]],
      [[timestamps[0], null], [timestamps[1], 0], [timestamps[2], 30]],
    ])
    expect(host.textContent).toContain('coretemp:Core 0最新样本：暂无数据')
    expect(host.textContent).not.toContain('38.0')
    expect(option!.series[1]!.symbol!([start, 38], { dataIndex: 0 })).toBe('circle')
    expect(option!.legend.data).toEqual(['coretemp:Package id 0', 'coretemp:Core 0', 'nvme:Composite'])
  })

  it('历史纵轴包含有效负温度，正常非负指标仍从零开始', () => {
    mountChart({
      history: true,
      timestamps: [start, start + minute],
      series: [{ name: 'ambient:Temperature', color: '#000000', values: [-12, -5], unit: '°C' }],
    })
    expect(option!.series[0]!.data).toEqual([[start, -12], [start + minute, -5]])
    for (const axis of option!.yAxis) {
      expect(typeof axis.min).toBe('function')
      if (typeof axis.min === 'function') {
        expect(axis.min({ min: -12 })).toBe(-12)
        expect(axis.min({ min: 5 })).toBe(0)
      }
    }
    expect(host.textContent).toContain('ambient:Temperature最新样本：-5.0 °C')
  })
})

describe('历史 tooltip 的时间和值', () => {
  it('优先使用元组中的真实时间，不把投影后的下标当作原样本时间', () => {
    mountChart({
      history: true,
      timestamps: [start, start + minute],
      series: [{ name: 'CPU', color: '#000000', values: [20, 25], unit: '%' }],
    })
    const tooltip = option!.tooltip.formatter([{ seriesName: 'CPU', dataIndex: 0, value: [start + minute, 25] }])
    expect(tooltip).toContain('2026/10/13 00:01:00')
    expect(tooltip).not.toContain('2026/10/13 00:00:00')
    expect(tooltip).toContain('CPU：25.0 %')
  })

  it('没有元组时可用 axisValue 定位，空值不显示为真实零', () => {
    mountChart({
      history: true,
      timestamps: [start, start + minute],
      series: [{ name: 'CPU', color: '#000000', values: [20, null], unit: '%' }],
    })
    const tooltip = option!.tooltip.formatter([{ seriesName: 'CPU', dataIndex: 0, axisValue: start + minute, value: null }])
    expect(tooltip).toContain('2026/10/13 00:01:00')
    expect(tooltip).toContain('CPU：暂无数据')
    expect(tooltip).not.toContain('0.0 %')
  })

  it('转义传感器原名的 HTML，同时保留 legend 身份和真实零读数', () => {
    const name = 'temp<&"\'id><img src=x onerror=alert(1)>'
    mountChart({
      history: true,
      timestamps: [start],
      series: [{ name, color: '#000000', values: [0], unit: '°C' }],
    })
    const marker = '<span style="color:red">●</span>'
    const tooltip = option!.tooltip.formatter([{ seriesName: name, marker, value: [start, 0] }])
    expect(tooltip).toContain('temp&lt;&amp;&quot;&#39;id&gt;&lt;img src=x onerror=alert(1)&gt;：0.0 °C')
    expect(tooltip).toContain(marker)
    expect(tooltip).not.toContain('<img')
    expect(host.querySelector('img')).toBeNull()
    expect(option!.legend.data).toEqual([name])
  })
})


describe('图例、单位与绘图区分层', () => {
  it('网络单位拥有独立行，canvas 不再叠画图例或轴名', () => {
    mountChart({
      history: true,
      timestamps: [start, start + minute],
      series: [
        { name: '接收', color: '#16866a', values: [1, 2], unit: 'KB/s' },
        { name: '发送', color: '#2468d8', values: [2, 3], unit: 'KB/s' },
      ],
    })
    expect(option!.legend.show).toBe(false)
    expect(option!.yAxis.every((axis) => axis.name === '')).toBe(true)
    expect(host.querySelector('.trend-chart__legend')?.textContent).toContain('接收')
    expect(host.querySelector('.trend-chart__legend')?.textContent).not.toContain('KB/s')
    expect(host.querySelector('.trend-chart__axis-units')?.textContent).toBe('KB/s')
    expect(option!.grid).toMatchObject({ top: 10, right: 12, bottom: 26, left: 45 })
  })

  it('双纵轴独立标识百分比和无单位负载，并预留右侧刻度空间', () => {
    mountChart({
      timestamps: [start, start + minute],
      series: [
        { name: 'CPU', color: '#2468d8', values: [20, 25], unit: '%' },
        { name: '负载', color: '#d28a1b', values: [1, 2], unit: '', axis: 'right' },
      ],
    })
    expect(host.querySelector('.trend-chart__axis-units')?.textContent).toContain('左轴 · %')
    expect(host.querySelector('.trend-chart__axis-units')?.textContent).toContain('右轴 · 负载')
    expect(option!.grid.right).toBe(42)
    expect(option!.yAxis[1]!.show).toBe(true)
  })

  it('八条长传感器名称完整保留且安全，图例按钮切换原名称对应的折线', async () => {
    const names = Array.from({ length: 8 }, (_, index) => `sensor-${index}-<img src=x onerror=1>-很长的原始传感器名称-${'name'.repeat(20)}`)
    mountChart({
      history: true,
      timestamps: [start, start + minute],
      unit: '°C',
      series: names.map((name, index) => ({ name, color: '#d28a1b', values: [30 + index, 31 + index] })),
    })
    const buttons = [...host.querySelectorAll<HTMLButtonElement>('.trend-chart__legend-item')]
    expect(buttons).toHaveLength(8)
    expect(buttons.map((button) => button.title)).toEqual(names)
    expect(buttons.map((button) => button.textContent?.trim())).toEqual(names)
    expect(host.querySelector('img')).toBeNull()
    expect(option!.legend.data).toEqual(names)
    expect(host.querySelector('.trend-chart__axis-units')?.textContent).toBe('°C')
    buttons[0]!.click()
    await nextTick()
    expect(buttons[0]!.getAttribute('aria-pressed')).toBe('false')
    expect(option!.legend.selected[names[0]!]).toBe(false)
    expect(option!.legend.selected[names[1]!]).toBe(true)
    buttons[0]!.click()
    await nextTick()
    expect(buttons[0]!.getAttribute('aria-pressed')).toBe('true')
    expect(option!.legend.selected[names[0]!]).toBe(true)
  })

  it('单点历史的短时间窗口显示秒，避免多个不同刻度重复同一分钟', () => {
    mountChart({ history: true, timestamps: [start], series: [{ name: '温度', color: '#d28a1b', values: [30] }] })
    const format = option!.xAxis.axisLabel.formatter!
    expect([0, 10_000, 20_000].map((offset) => format(start + offset))).toEqual(['00:00:00', '00:00:10', '00:00:20'])
  })
})


describe('趋势图动态减少动效', () => {
  it.each([true, false])('初始偏好 %s 与页面打开后的变化同步到图表，数据保持不变', async (initial) => {
    const mock = mockReducedMotion(initial)
    mountChart({ timestamps: [start, start + minute], series: [{ name: 'CPU', color: '#000000', values: [20, 25] }] })
    expect(option!.animation).toBe(!initial)
    for (const reduce of [!initial, initial]) {
      mock.change(reduce)
      await nextTick()
      expect(option!.animation).toBe(!reduce)
      expect(option!.series[0]!.data).toEqual([20, 25])
    }
    app!.unmount()
    app = undefined
    expect(mock.media.removeEventListener).toHaveBeenCalledTimes(1)
  })
})
