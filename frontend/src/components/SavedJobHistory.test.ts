// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, ref, type App } from 'vue'
import SavedJobHistory from './SavedJobHistory.vue'
import { NcpApiError } from '@/api/systemTransport'
import { beginSession } from '@/session/sessionLifecycle'
const api = vi.hoisted(() => ({ read: vi.fn(), auth: vi.fn() }))
vi.mock('@/api/jobHistory', async original => ({ ...await original<typeof import('@/api/jobHistory')>(), requestJobHistory: api.read }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ refresh: api.auth }) }))
let app: App | undefined, host: HTMLDivElement
const flush = async () => { for (let i = 0; i < 10; i++) await Promise.resolve(); await nextTick() }
const item = (status = 'running') => ({ id: '<img src=x onerror=alert(1)>', type: ' future-kind ', status, message: '保存消息', updatedAt: '2026-10-15T00:00:00Z', createdAt: '2026-10-15T00:00:00Z' })
const page = (status?: string) => ({ source: 'persisted', items: [item(status)], nextCursor: 'YWJj' })
async function mount() { const active = ref(true); host = document.createElement('div'); document.body.append(host); app = createApp({ render: () => h(SavedJobHistory, { active: () => active.value }) }); app.mount(host); await flush(); return active }
function button(text: string) { return [...host.querySelectorAll('button')].find(button => button.textContent?.includes(text))! }
beforeEach(() => { beginSession(); vi.resetAllMocks(); api.read.mockResolvedValue(page()); api.auth.mockResolvedValue(undefined) })
afterEach(() => { app?.unmount(); host?.remove() })
describe('保存历史只读呈现', () => {
  it.each(['running', 'constructor', '__proto__', 'toString', 'unknown-state'])('状态 %s 仅来自保存值，未知标签纯文本', async status => {
    api.read.mockResolvedValue(page(status)); await mount()
    expect(host.textContent).toContain('仅显示最后保存的记录，可能落后于当前执行；不代表当前资源状态')
    expect(host.textContent).toContain('记录更新时间'); expect(host.querySelector('img')).toBeNull()
    expect(host.querySelector('.history-status')?.textContent).toBe(`保存状态：${status === 'running' ? '执行中' : status}`)
    expect(host.querySelector('progress')).toBeNull(); expect(host.textContent).not.toContain('镜像当前状态'); expect(host.textContent).not.toContain('请求取消')
  })
  it('筛选未知类型保留空格、页大小回最新，标签名称独立', async () => {
    await mount(); const controls = [...host.querySelectorAll('input, select')]
    expect(controls.map(control => document.getElementById(control.getAttribute('aria-labelledby')!)?.textContent)).toEqual(['已保存任务类型', '每页条数', '其他类型精确查询（保留空格）'])
    const input = host.querySelector('input')!; input.value = ' unknown '; input.dispatchEvent(new Event('input')); host.querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true })); await flush()
    expect(api.read.mock.calls.at(-1)?.[0]).toEqual({ type: ' unknown ', limit: 50 }); expect(host.textContent).toContain('类型「 unknown 」')
    const size = host.querySelectorAll('select')[1]!; size.value = '20'; size.dispatchEvent(new Event('change')); await flush()
    expect(api.read.mock.calls.at(-1)?.[0]).toEqual({ type: ' unknown ', limit: 20 })
  })
  it('失败保留页和标签并可重试同一请求；没有假总数或页码', async () => {
    await mount(); api.read.mockRejectedValueOnce(new NcpApiError('JOB_HISTORY_UNAVAILABLE', '读取失败')); button('下一页').click(); await flush()
    expect(host.querySelectorAll('.history-record')).toHaveLength(1); expect(host.textContent).toContain('未切页'); expect(button('下一页').disabled).toBe(true)
    button('重试同一读取').click(); await flush(); expect(api.read.mock.calls[2]?.[0]).toEqual(api.read.mock.calls[1]?.[0]); expect(host.textContent).toContain('本页 1 条'); expect(host.textContent).not.toContain('总共')
  })
  it.each(['JOB_HISTORY_UNSUPPORTED', 'JOB_HISTORY_TOO_LARGE', 'JOB_HISTORY_RESPONSE_INVALID'])('错误 %s 保留来源说明', async code => {
    api.read.mockRejectedValue(new NcpApiError(code, '固定错误')); await mount(); expect(host.textContent).toContain('固定错误'); expect(host.textContent).toContain('最后保存'); expect(host.textContent).not.toContain('没有更多')
    expect(!!button('重试同一读取')).toBe(code !== 'JOB_HISTORY_UNSUPPORTED')
  })
  it.each(['JOB_HISTORY_UNSUPPORTED', 'JOB_HISTORY_UNAVAILABLE', 'empty'])('空列表 %s 仍使用可命名 region 语义', async code => {
    if (code === 'empty') api.read.mockResolvedValue({ source: 'persisted', items: [] })
    else api.read.mockRejectedValue(new NcpApiError(code, '读取失败'))
    await mount()
    const list = host.querySelector('[aria-label="已保存历史列表"]')!
    expect(list.tagName).toBe('SECTION')
    expect(list.getAttribute('aria-busy')).toBe('false')
    expect(list.querySelectorAll('article')).toHaveLength(0)
  })
  it('关闭的同一时刻先撤销所有权，迟到401不触发会话刷新', async () => {
    let reject!: (e: Error) => void; api.read.mockReturnValue(new Promise((_a, b) => { reject = b }))
    const active = await mount(); active.value = false; reject(new NcpApiError('AUTH_UNAUTHORIZED', 'invalid')); await flush()
    expect(api.auth).not.toHaveBeenCalled(); expect((api.read.mock.calls[0]?.[1] as AbortSignal).aborted).toBe(true)
  })
  it('当前401仅刷新会话一次，刷新失败不循环', async () => {
    api.read.mockRejectedValue(new NcpApiError('AUTH_UNAUTHORIZED', '会话已失效')); api.auth.mockResolvedValue(undefined)
    await mount(); expect(api.auth).toHaveBeenCalledOnce(); expect(button('重试同一读取')).toBeUndefined(); expect(host.textContent).toContain('会话已失效')
  })
})
