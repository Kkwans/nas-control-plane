// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type App } from 'vue'
import type { LogEntry, LogResponse } from '@/api/control'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import LogsView from './LogsView.vue'

const mocks = vi.hoisted(() => ({ requestLogs: vi.fn() }))
vi.mock('@/api/control', async (original) => ({ ...await original<object>(), requestLogs: mocks.requestLogs }))
vi.mock('@/stores/system', () => ({ useSystemStore: () => ({ inventory: null, refreshIntervalSeconds: 5, refresh: vi.fn() }) }))
vi.mock('@/composables/useListPreference', async () => {
  const { ref } = await import('vue')
  return { useListPreference: () => ({ pageSize: ref(1) }) }
})
vi.mock('@/components/ListPageSizeControl.vue', () => ({ default: { render: () => null } }))
vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  const wrapper = defineComponent({ setup: (_, { slots }) => () => h('div', slots.default?.()) })
  return {
    ElButton: defineComponent({ props: ['disabled', 'loading'], setup: (props, { slots }) => () => h('button', { disabled: props.disabled || props.loading }, slots.default?.()) }),
    ElInput: defineComponent({ props: ['modelValue'], emits: ['update:modelValue'], setup: (props, { emit }) => () => h('input', { value: props.modelValue, onInput: (event: Event) => emit('update:modelValue', (event.target as HTMLInputElement).value) }) }),
    ElSelect: wrapper, ElOption: { render: () => null }, ElSwitch: wrapper, ElTooltip: wrapper, ElDialog: { render: () => null },
  }
})

let app: App | undefined
let host: HTMLDivElement
const createUrl = vi.fn(() => 'blob:fixture')
const revokeUrl = vi.fn()
function item(id: string): LogEntry { return { id, timestamp: `2026-10-13T00:00:0${id}Z`, source: 'system', level: 'info', unit: 'fixture', message: `记录${id}` } }
function result(entries = [item('2'), item('1')]): LogResponse { return { entries, nextCursor: '', collectedAt: '2026-10-13T00:00:00Z' } }
function mount() { host = document.createElement('div'); document.body.append(host); app = createApp({ render: () => h(LogsView) }); app.mount(host) }
async function settle() { for (let i = 0; i < 12; i++) await nextTick() }
function button() { return host.querySelector<HTMLButtonElement>('.log-export-button')! }
function refresh() { host.querySelector<HTMLButtonElement>('.log-refresh-button')!.click() }

beforeEach(() => {
  beginSession(); vi.clearAllMocks(); vi.useFakeTimers()
  mocks.requestLogs.mockResolvedValue(result())
  vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: createUrl, revokeObjectURL: revokeUrl }))
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
})
afterEach(() => { app?.unmount(); app = undefined; host?.remove(); vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe('日志当前页导出状态', () => {
  it('仅导出显示页，不请求 API，重入锁直到资源释放', async () => {
    mount(); await settle()
    expect(button().textContent).toContain('导出当前页（1 条）')
    button().click(); button().click()
    expect(createUrl).toHaveBeenCalledTimes(1)
    expect(mocks.requestLogs).toHaveBeenCalledTimes(1)
    expect(host.textContent).toContain('日志可能含敏感信息')
    await settle()
    expect(host.textContent).toContain('已发起 1 条日志下载')
    vi.runOnlyPendingTimers(); await settle(); button().click()
    expect(createUrl).toHaveBeenCalledTimes(2)
  })
  it('草稿搜索禁用，提交期间禁用，成功后可以导出', async () => {
    mount(); await settle()
    const input = host.querySelector('input')!
    input.value = 'new'; input.dispatchEvent(new Event('input')); await settle()
    expect(button().disabled).toBe(true)
    expect(host.textContent).toContain('搜索条件尚未应用')
    let resolve!: (value: LogResponse) => void
    mocks.requestLogs.mockReturnValueOnce(new Promise<LogResponse>((yes) => { resolve = yes }))
    refresh(); await settle()
    expect(button().disabled).toBe(true)
    resolve(result([item('3')])); await settle()
    expect(button().disabled).toBe(false)
    button().click(); expect(createUrl).toHaveBeenCalledTimes(1)
  })
  it('读取失败保留旧行但不能导出，重试成功恢复', async () => {
    mount(); await settle()
    mocks.requestLogs.mockRejectedValueOnce(new Error('fixture')); refresh(); await settle()
    expect(host.textContent).toContain('记录2')
    expect(button().disabled).toBe(true)
    button().click(); expect(createUrl).not.toHaveBeenCalled()
    refresh(); await settle(); expect(button().disabled).toBe(false)
  })
  it('旧页面会话撤销后不能给旧数据使用新会话背书', async () => {
    mount(); await settle(); button().click()
    beginSession(); await settle()
    expect(revokeUrl).toHaveBeenCalledTimes(1)
    expect(button().disabled).toBe(true)
    button().click(); expect(createUrl).toHaveBeenCalledTimes(1)
  })
  it('下载错误不覆盖读取数据或暴露异常详情，下一次点击可重试', async () => {
    mount(); await settle()
    createUrl.mockImplementationOnce(() => { throw new Error('private fixture detail') })
    button().click(); await settle()
    expect(host.textContent).toContain('日志下载未能发起，请重试。')
    expect(host.textContent).not.toContain('private fixture detail')
    expect(host.textContent).toContain('记录2')
    expect(button().disabled).toBe(false)
    button().click(); await settle()
    expect(host.textContent).toContain('已发起 1 条日志下载')
    expect(host.textContent).not.toContain('日志下载未能发起')
  })
  it('空页、未完成读取、卸载和会话结束不会迟到发起下载', async () => {
    let resolve!: (value: LogResponse) => void
    mocks.requestLogs.mockReturnValueOnce(new Promise<LogResponse>((yes) => { resolve = yes }))
    mount(); await settle(); expect(button().disabled).toBe(true)
    invalidateSession(); app?.unmount(); app = undefined
    resolve(result()); await settle(); expect(createUrl).not.toHaveBeenCalled()
    beginSession(); mocks.requestLogs.mockResolvedValueOnce(result([])); mount(); await settle()
    expect(button().disabled).toBe(true)
    expect(host.textContent).toContain('当前页没有可导出的日志')
  })
})
