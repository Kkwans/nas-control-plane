// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, ref, shallowRef, type App } from 'vue'
import ResourceAlertsPanel from './ResourceAlertsPanel.vue'
import { alertsFixture } from '@/domain/monitoring/alerts.fixture'
import type { AlertCondition, ResourceAlerts } from '@/domain/monitoring/alerts'
const state = vi.hoisted(() => ({ model: undefined as unknown }))
vi.mock('@/composables/useResourceAlerts', () => ({ useResourceAlerts: () => state.model }))
let app: App | undefined
let host: HTMLDivElement
let model: { snapshot: ReturnType<typeof shallowRef<ResourceAlerts | null>>; loading: ReturnType<typeof ref<boolean>>; errorCode: ReturnType<typeof ref<string>>; now: ReturnType<typeof ref<number>>; refresh: ReturnType<typeof vi.fn> }
function mount() { host = document.createElement('div'); document.body.append(host); app = createApp({ render: () => h(ResourceAlertsPanel) }); app.mount(host) }
function fixture(condition: AlertCondition) {
  const result = alertsFixture()
  Object.assign(result.rules.cpu, { condition, pendingSince: condition === 'pending' ? result.observedAt : null, firingSince: ['firing', 'recovering'].includes(condition) ? result.observedAt : null, recoveringSince: condition === 'recovering' ? result.observedAt : null })
  return result
}
beforeEach(() => { model = { snapshot: shallowRef<ResourceAlerts | null>(null), loading: ref(true), errorCode: ref(''), now: ref(Date.parse('2026-10-13T00:00:00Z')), refresh: vi.fn() }; state.model = model })
afterEach(() => { app?.unmount(); host?.remove() })
describe('当前资源提示面板', () => {
  it('distinguishes first load and disabled without a fake toggle or health claim', async () => {
    mount(); expect(host.textContent).toContain('正在读取当前资源提示')
    const result = alertsFixture(); result.enabled = false; model.snapshot.value = result; model.loading.value = false; await nextTick()
    expect(host.textContent).toContain('未启用不表示资源正常'); expect(host.querySelector('[role="switch"]')).toBeNull(); expect(host.querySelectorAll('article')).toHaveLength(0)
  })
  it.each([['normal', '未达到告警条件'], ['pending', '等待持续确认'], ['firing', '已触发'], ['recovering', '恢复观察']] as const)('renders %s independently of history', (condition, text) => {
    model.snapshot.value = fixture(condition); model.loading.value = false; mount()
    expect(host.textContent).toContain(text); expect(host.textContent).toContain('与下方历史范围无关'); expect(host.textContent).toContain('仅本次 Server 运行期')
    expect(host.querySelectorAll('article')).toHaveLength(2)
  })
  it.each(['unknown', 'stale'] as const)('retains fired event during %s using neutral last-known value', (availability) => {
    const result = fixture('firing'); result.rules.cpu.availability = availability; result.rules.cpu.value = 98
    model.snapshot.value = result; mount()
    const cpu = host.querySelector('article')!
    expect(cpu.textContent).toContain('此前已触发'); expect(cpu.textContent).toContain('恢复尚未确认'); expect(cpu.textContent).toContain('上次有效值'); expect(cpu.textContent).toContain('98.0%')
    expect(cpu.querySelector('.status-pill--neutral')).not.toBeNull(); expect(cpu.textContent).toContain('2026')
  })
  it('renders missing cold-start values as unavailable rather than zero or healthy', () => {
    const result = fixture('normal'); Object.assign(result.rules.cpu, { availability: 'unknown', value: null, sampleAt: null, receivedAt: null })
    model.snapshot.value = result; mount()
    const cpu = host.querySelector('article')!; expect(cpu.textContent).toContain('暂无有效值'); expect(cpu.textContent).not.toContain('0.0%'); expect(cpu.textContent).not.toContain('未达到告警条件')
  })
  it('shows separate first-read and stale-data refresh failure with independent retry', async () => {
    model.loading.value = false; model.errorCode.value = 'MONITORING_ALERTS_UNAVAILABLE'; mount()
    expect(host.textContent).toContain('当前提示读取失败')
    host.querySelector<HTMLButtonElement>('.resource-alerts__error button')!.click(); expect(model.refresh).toHaveBeenCalledTimes(1)
    model.snapshot.value = fixture('firing'); await nextTick(); expect(host.textContent).toContain('保留上次读取结果'); expect(host.textContent).toContain('恢复尚未确认')
    expect(host.querySelector('[role="alert"]')).toBeNull()
  })
  it('announces only stable status changes, not value/response-time polling', async () => {
    mount(); model.snapshot.value = fixture('pending'); await nextTick()
    const live = host.querySelector('[aria-live="polite"]')!; const previous = live.textContent
    const result = fixture('pending'); result.observedAt = '2026-10-13T00:00:05Z'; result.rules.cpu.value = 95; model.snapshot.value = result; await nextTick()
    expect(live.textContent).toBe(previous); expect(host.querySelectorAll('[role="status"]')).toHaveLength(1)
    model.snapshot.value = fixture('firing'); await nextTick(); expect(live.textContent).not.toBe(previous); expect(live.textContent).toContain('已触发')
  })
})
