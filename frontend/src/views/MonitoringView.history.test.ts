// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type App } from 'vue'
import type { MetricSample } from '@/api/control'
import { NcpApiError } from '@/api/system'
import { createManualRefreshRegistry, provideManualRefreshRegistry } from '@/composables/manualRefresh'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import MonitoringView from './MonitoringView.vue'

const mocks = vi.hoisted(() => ({ requestMetricSamples: vi.fn(), date: undefined as ((event: 'update:from' | 'update:to' | 'now' | 'clear' | 'apply', value?: unknown) => void) | undefined, sensor: undefined as ((value: string[]) => void) | undefined }))
vi.mock('@/api/control', async (original) => ({ ...await original<object>(), requestMetricSamples: mocks.requestMetricSamples }))
vi.mock('@/components/ResourceAlertsPanel.vue', () => ({ default: { template: '<div data-alerts-panel />' } }))
vi.mock('@/components/RealtimeTrendChart.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return { default: defineComponent({ props: { timestamps: Array, series: Array, history: Boolean, emptyMessage: String }, setup: (props) => () => h('div', { 'data-chart': JSON.stringify(props) }, props.emptyMessage) }) }
})
vi.mock('@/components/DateTimeRangeControl.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return { default: defineComponent({ emits: ['update:from', 'update:to', 'now', 'clear', 'apply'], setup: (_, { emit }) => { mocks.date = emit; return () => h('div') } }) }
})
vi.mock('@/components/CompactSegmentedFilter.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return { default: defineComponent({ emits: ['update:modelValue'], setup: (_, { emit }) => () => h('div', ['1h', '6h', '24h', '7d'].map((value) => h('button', { 'data-range': value, onClick: () => emit('update:modelValue', value) }, value))) }) }
})
vi.mock('@/components/MonitoringSensorSelect.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return { default: defineComponent({ props: ['modelValue', 'names'], emits: ['update:modelValue'], setup: (props, { emit }) => { mocks.sensor = (value) => emit('update:modelValue', value); return () => h('div', { 'data-sensors': JSON.stringify(props) }) } }) }
})

const base = Date.parse('2026-10-13T00:00:00Z')
function sample(minute: number, overrides: Partial<MetricSample> = {}): MetricSample {
  return { collectedAt: new Date(base + minute * 60_000).toISOString(), cpuPercent: 20, memoryPercent: 37, load1: 1, diskPercent: 50, networkReceiveBytes: minute * 60 * 1024, networkTransmitBytes: minute * 120 * 1024, ...overrides }
}
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
async function settle() { for (let index = 0; index < 8; index++) await nextTick() }
let app: App | undefined
let host: HTMLDivElement
let registry: ReturnType<typeof createManualRefreshRegistry>
function mount() {
  host = document.createElement('div'); document.body.append(host)
  registry = createManualRefreshRegistry()
  app = createApp({ setup() { provideManualRefreshRegistry(registry); return () => h(MonitoringView) } })
  app.mount(host)
}
type Chart = { timestamps: number[]; history: boolean; series: { name: string; values: (number | null)[] }[]; emptyMessage?: string }
function chart(title: string): Chart { return JSON.parse(host.querySelector(`[aria-labelledby="${title}-chart-title"] [data-chart]`)!.getAttribute('data-chart')!) }
function chooseRange(value: string) { host.querySelector<HTMLButtonElement>(`[data-range="${value}"]`)!.click() }
function sensors() { return JSON.parse(host.querySelector('[data-sensors]')!.getAttribute('data-sensors')!) as { modelValue: string[]; names: string[] } }

beforeEach(() => { beginSession(); vi.clearAllMocks(); mocks.requestMetricSamples.mockResolvedValue([]) })
afterEach(() => { app?.unmount(); app = undefined; host?.remove(); vi.restoreAllMocks(); vi.useRealTimers() })

describe('持久化监控历史页面', () => {
  it('renders mutually exclusive loading, successful empty, and charts states', async () => {
    const pending = deferred<MetricSample[]>()
    mocks.requestMetricSamples.mockReturnValueOnce(pending.promise)
    mount()
    expect(host.querySelector('[aria-label="正在加载监控图表"]')).not.toBeNull()
    expect(host.querySelectorAll('[data-chart]')).toHaveLength(0)
    expect(host.textContent).not.toContain('当前范围暂无样本')
    pending.resolve([]); await settle()
    expect(host.textContent).toContain('当前范围暂无样本')
    expect(host.querySelectorAll('[data-chart]')).toHaveLength(0)
    mocks.requestMetricSamples.mockResolvedValueOnce([sample(0)])
    await registry.refresh(); await settle()
    expect(host.querySelectorAll('[data-chart]')).toHaveLength(6)
    expect(host.textContent).not.toContain('当前范围暂无样本')
    expect(chart('network').emptyMessage).toContain('一条已保存样本，暂不能计算速率')
  })
  it.each([
    ['MONITORING_HISTORY_LIMIT_EXCEEDED', '超过读取上限'],
    ['MONITORING_HISTORY_INVALID', '包含无效数据'],
    ['MONITORING_READ_FAILED', '读取失败'],
  ])('shows %s as an error rather than six empty charts, then retries', async (code, message) => {
    mocks.requestMetricSamples.mockRejectedValueOnce(new NcpApiError(code, 'fixture'))
    mount(); await settle()
    expect(host.textContent).toContain(message)
    expect(host.textContent).toContain(code)
    expect(host.textContent).not.toContain('Agent')
    expect(host.textContent).not.toContain('当前范围暂无样本')
    expect(host.querySelectorAll('[data-chart]')).toHaveLength(0)
    mocks.requestMetricSamples.mockResolvedValueOnce([sample(0), sample(1)])
    host.querySelector<HTMLButtonElement>('.resource-state button')!.click()
    await settle()
    expect(host.querySelectorAll('[data-chart]')).toHaveLength(6)
    expect(host.querySelector('.resource-state--error')).toBeNull()
  })
  it('uses epoch time and exactly one null gap, without treating warm-up or reset as zero', async () => {
    mocks.requestMetricSamples.mockResolvedValue([sample(0), sample(1), sample(60), sample(61, { networkReceiveBytes: 0 })])
    mount(); await settle()
    expect(chart('memory')).toMatchObject({ history: true, timestamps: [base, base + 60_000, base + 30.5 * 60_000, base + 60 * 60_000, base + 61 * 60_000] })
    expect(chart('memory').series[0]?.values).toEqual([37, 37, null, 37, 37])
    expect(chart('network').series[0]?.values).toEqual([null, 1, null, null, null])
    expect(chart('network').series[1]?.values).toEqual([null, 2, null, null, 2])
    expect(host.textContent).toContain('采样数量4')
  })
  it.each([
    [sample(0), sample(0)],
    [sample(1), sample(0)],
    [sample(0, { collectedAt: 'invalid' })],
  ])('reports an invalid timeline as a read error', async (...samples) => {
    mocks.requestMetricSamples.mockResolvedValue(samples)
    mount(); await settle()
    expect(host.textContent).toContain('MONITORING_HISTORY_INVALID')
    expect(host.querySelectorAll('[data-chart]')).toHaveLength(0)
  })
  it('prevents an aborted older response from overwriting the newer range', async () => {
    const old = deferred<MetricSample[]>(); const current = deferred<MetricSample[]>()
    mocks.requestMetricSamples.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    mount()
    const signal = mocks.requestMetricSamples.mock.calls[0]![1] as AbortSignal
    chooseRange('1h'); await settle()
    expect(signal.aborted).toBe(true)
    current.resolve([sample(0, { memoryPercent: 51 })]); await settle()
    old.resolve([sample(0, { memoryPercent: 99 })]); await settle()
    expect(chart('memory').series[0]?.values).toEqual([51])
    expect(host.textContent).toContain('已读取范围：1 小时')
  })
  it('keeps unapplied invalid draft ranges separate from the last successful read and refresh', async () => {
    mocks.requestMetricSamples.mockResolvedValue([sample(0)])
    mount(); await settle()
    mocks.date?.('update:from', new Date(base + 60_000)); mocks.date?.('update:to', new Date(base))
    mocks.date?.('apply'); await settle()
    expect(mocks.requestMetricSamples).toHaveBeenCalledTimes(1)
    expect(host.textContent).toContain('请选择不超过 7 天')
    expect(host.textContent).toContain('已读取范围：6 小时')
    expect(host.querySelector('.resource-state--error')).toBeNull()
    expect(host.querySelectorAll('[data-chart]')).toHaveLength(6)
    await registry.refresh(); await settle()
    expect(mocks.requestMetricSamples.mock.calls[1]![0]).toBe('6h')
  })
  it('initially selects only eight raw sensor names, preserves surviving choices, and respects clearing', async () => {
    const temperatures = Array.from({ length: 60 }, (_, index) => ({ name: `sensor-${String(index).padStart(3, '0')}`, temperatureCelsius: index }))
    mocks.requestMetricSamples.mockResolvedValue([sample(0, { temperatures }), sample(1, { temperatures: [temperatures[0]!] })])
    mount(); await settle()
    expect(sensors().names).toHaveLength(60)
    expect(chart('temperature').series).toHaveLength(8)
    expect(chart('temperature').series[0]?.values).toEqual([0, 0])
    expect(chart('temperature').series[1]?.values).toEqual([1, null])
    mocks.sensor?.(['sensor-059', 'sensor-001']); await settle()
    mocks.requestMetricSamples.mockResolvedValue([sample(0, { temperatures: [temperatures[59]!, temperatures[0]!] })])
    chooseRange('1h'); await settle()
    expect(sensors().modelValue).toEqual(['sensor-059'])
    mocks.sensor?.([]); await settle()
    expect(chart('temperature').emptyMessage).toBe('请选择传感器。')
    await registry.refresh(); await settle()
    expect(sensors().modelValue).toEqual([])
    expect(chart('temperature').series).toEqual([])
  })
  it('distinguishes missing counts and temperature readings without claiming an unsupported server', async () => {
    mocks.requestMetricSamples.mockResolvedValue([sample(0), sample(1)])
    mount(); await settle()
    expect(chart('disk-io').emptyMessage).toBe('当前范围没有可用的磁盘 I/O 计数。')
    expect(chart('temperature').emptyMessage).toBe('当前范围没有可用温度读数。')
    mocks.requestMetricSamples.mockResolvedValue([sample(0, { diskReadBytes: 10 }), sample(60, { diskReadBytes: 20 })])
    await registry.refresh(); await settle()
    expect(chart('disk-io').emptyMessage).toBe('尚无可计算的相邻分钟样本。')
    expect(chart('disk-io').series[0]?.values).toEqual([null, null, null])
  })
  it('keeps chart nodes, projections, sensors, count and read metadata while refreshing and after failure', async () => {
    mocks.requestMetricSamples.mockResolvedValueOnce([sample(0, { temperatures: [{ name: 'cpu', temperatureCelsius: 40 }] }), sample(1)])
    mount(); await settle()
    mocks.sensor?.([]); await settle()
    const nodes = [...host.querySelectorAll('[data-chart]')]
    const projections = nodes.map((node) => node.getAttribute('data-chart'))
    const readAt = host.querySelector('time')!.getAttribute('datetime')
    const pending = deferred<MetricSample[]>()
    mocks.requestMetricSamples.mockReturnValueOnce(pending.promise)
    const refresh = registry.refresh(); await settle()
    expect(host.textContent).toContain('正在刷新，暂时显示上次成功读取的数据')
    expect(host.querySelector('[aria-label="正在加载监控图表"]')).toBeNull()
    expect(host.querySelectorAll('[data-chart]')).toHaveLength(nodes.length)
    host.querySelectorAll('[data-chart]').forEach((node, index) => expect(node).toBe(nodes[index]))
    expect(nodes.map((node) => node.getAttribute('data-chart'))).toEqual(projections)
    expect(host.textContent).toContain('采样数量2')
    pending.reject(new Error('offline'))
    expect((await refresh).failed).toHaveLength(1); await settle()
    expect(host.textContent).toContain('刷新失败，当前显示上次成功读取的旧数据')
    expect(host.querySelector('time')!.getAttribute('datetime')).toBe(readAt)
    expect(host.querySelectorAll('[data-chart]')).toHaveLength(nodes.length)
    host.querySelectorAll('[data-chart]').forEach((node, index) => expect(node).toBe(nodes[index]))
    expect(nodes.map((node) => node.getAttribute('data-chart'))).toEqual(projections)
    expect(sensors().modelValue).toEqual([])
    mocks.requestMetricSamples.mockResolvedValueOnce([sample(2, { memoryPercent: 80, temperatures: [{ name: 'cpu', temperatureCelsius: 45 }] })])
    host.querySelector<HTMLButtonElement>('.resource-state button')!.click(); await settle()
    expect(chart('memory').series[0]?.values).toEqual([80])
    expect(sensors().modelValue).toEqual([])
    expect(host.querySelectorAll('[data-chart]')).toHaveLength(nodes.length)
    host.querySelectorAll('[data-chart]').forEach((node, index) => expect(node).toBe(nodes[index]))
    expect(host.querySelector('.resource-state--error')).toBeNull()
  })
  it.each([
    [sample(0), sample(0)],
    [sample(1), sample(0)],
    [sample(0, { collectedAt: 'invalid' })],
    [sample(0, { temperatures: {} as MetricSample['temperatures'] })],
  ])('does not publish an invalid candidate over a successful snapshot', async (...invalid) => {
    mocks.requestMetricSamples.mockResolvedValueOnce([sample(0), sample(1)])
    mount(); await settle()
    const before = chart('memory')
    mocks.requestMetricSamples.mockResolvedValueOnce(invalid)
    expect((await registry.refresh()).failed).toHaveLength(1); await settle()
    expect(chart('memory')).toEqual(before)
    expect(host.textContent).toContain('刷新失败')
  })
  it('replaces old data with a valid empty snapshot and retains that empty result on refresh failure', async () => {
    mocks.requestMetricSamples.mockResolvedValueOnce([sample(0)])
    mount(); await settle()
    mocks.requestMetricSamples.mockResolvedValueOnce([])
    await registry.refresh(); await settle()
    expect(host.querySelectorAll('[data-chart]')).toHaveLength(0)
    expect(host.textContent).toContain('采样数量0')
    expect(host.textContent).toContain('当前范围暂无样本')
    const pending = deferred<MetricSample[]>()
    mocks.requestMetricSamples.mockReturnValueOnce(pending.promise)
    const refresh = registry.refresh(); await settle()
    expect(host.textContent).toContain('当前范围暂无样本')
    expect(host.querySelector('[aria-label="正在加载监控图表"]')).toBeNull()
    pending.reject(new Error('offline')); await refresh; await settle()
    expect(host.textContent).toContain('当前范围暂无样本')
    expect(host.textContent).toContain('刷新失败')
  })
  it('clears successful data on an explicit range switch and isolates superseded manual outcomes', async () => {
    mocks.requestMetricSamples.mockResolvedValueOnce([sample(0)])
    mount(); await settle()
    const old = deferred<MetricSample[]>()
    mocks.requestMetricSamples.mockReturnValueOnce(old.promise)
    const refresh = registry.refresh(); await settle()
    const signal = mocks.requestMetricSamples.mock.calls[1]![1] as AbortSignal
    mocks.requestMetricSamples.mockRejectedValueOnce(new Error('new range failed'))
    chooseRange('1h'); await settle()
    expect(signal.aborted).toBe(true)
    expect(host.querySelectorAll('[data-chart]')).toHaveLength(0)
    expect(host.querySelector('time')).toBeNull()
    expect(host.textContent).not.toContain('已读取范围')
    old.resolve([sample(1, { memoryPercent: 99 })])
    expect((await refresh).failed).toHaveLength(0); await settle()
    expect(host.querySelectorAll('[data-chart]')).toHaveLength(0)
    expect(host.textContent).toContain('监控历史加载失败')
  })
  it('keeps the successful rolling custom label until the new request succeeds', async () => {
    vi.useFakeTimers(); vi.setSystemTime(new Date(base + 6 * 60 * 60_000))
    mocks.requestMetricSamples.mockResolvedValue([sample(0)])
    mount(); await settle()
    mocks.date?.('update:from', new Date(base)); mocks.date?.('update:to', new Date()); mocks.date?.('apply'); await settle()
    const oldLabel = host.querySelector('.monitor-history-note')!.textContent
    const oldRead = host.querySelector('time')!.getAttribute('datetime')
    vi.setSystemTime(new Date(base + 7 * 60 * 60_000))
    const pending = deferred<MetricSample[]>()
    mocks.requestMetricSamples.mockReturnValueOnce(pending.promise)
    const refresh = registry.refresh(); await settle()
    expect(mocks.requestMetricSamples.mock.calls[2]![0]).toEqual({ from: new Date(base + 60 * 60_000).toISOString(), to: new Date().toISOString() })
    expect(host.querySelector('.monitor-history-note')!.textContent).toBe(oldLabel)
    pending.reject(new Error('offline')); await refresh; await settle()
    expect(host.querySelector('.monitor-history-note')!.textContent).toBe(oldLabel)
    mocks.requestMetricSamples.mockResolvedValueOnce([sample(1)])
    host.querySelector<HTMLButtonElement>('.resource-state button')!.click(); await settle()
    expect(host.querySelector('.monitor-history-note')!.textContent).not.toBe(oldLabel)
    expect(host.querySelector('time')!.getAttribute('datetime')).not.toBe(oldRead)
  })
  it('clears all historical data and messages at logout and ignores late results', async () => {
    mocks.requestMetricSamples.mockResolvedValueOnce([sample(0)])
    mount(); await settle()
    mocks.requestMetricSamples.mockRejectedValueOnce(new Error('offline'))
    await registry.refresh(); await settle()
    expect(host.textContent).toContain('刷新失败')
    const pending = deferred<MetricSample[]>()
    mocks.requestMetricSamples.mockReturnValueOnce(pending.promise)
    const refresh = registry.refresh(); await settle()
    const signal = mocks.requestMetricSamples.mock.calls[2]![1] as AbortSignal
    invalidateSession(); await settle()
    expect(signal.aborted).toBe(true)
    expect(host.querySelectorAll('[data-chart]')).toHaveLength(0)
    expect(host.querySelector('time')).toBeNull()
    expect(host.textContent).not.toContain('已读取范围')
    expect(host.textContent).not.toContain('刷新失败')
    expect(host.textContent).not.toContain('监控历史加载失败')
    expect(host.querySelector('[aria-label="正在加载监控图表"]')).toBeNull()
    pending.resolve([sample(1)]); await refresh; await settle()
    await registry.refresh(); await settle()
    expect(mocks.requestMetricSamples).toHaveBeenCalledTimes(3)
    expect(host.querySelectorAll('[data-chart]')).toHaveLength(0)
  })
  it('clears an already displayed refresh error and invalid range draft at logout', async () => {
    mocks.requestMetricSamples.mockResolvedValueOnce([sample(0)])
    mount(); await settle()
    mocks.requestMetricSamples.mockRejectedValueOnce(new Error('offline'))
    await registry.refresh(); await settle()
    mocks.date?.('update:from', new Date(base + 60_000)); mocks.date?.('update:to', new Date(base))
    mocks.date?.('apply'); await settle()
    expect(host.textContent).toContain('MONITORING_READ_FAILED')
    expect(host.textContent).toContain('请选择不超过 7 天')
    invalidateSession(); await settle()
    expect(host.querySelector('[role="alert"]')).toBeNull()
    expect(host.textContent).not.toContain('MONITORING_READ_FAILED')
    expect(host.textContent).not.toContain('请选择不超过 7 天')
    expect(host.querySelector('time')).toBeNull()
    expect(host.querySelectorAll('[data-chart]')).toHaveLength(0)
  })
  it('aborts pending work and unregisters manual refresh on unmount', async () => {
    const pending = deferred<MetricSample[]>()
    mocks.requestMetricSamples.mockReturnValueOnce(pending.promise)
    mount()
    const signal = mocks.requestMetricSamples.mock.calls[0]![1] as AbortSignal
    app!.unmount(); app = undefined
    expect(signal.aborted).toBe(true)
    pending.resolve([sample(1)]); await settle()
    expect((await registry.refresh()).requested).toBe(0)
    expect(host.querySelectorAll('[data-chart]')).toHaveLength(0)
  })

})
