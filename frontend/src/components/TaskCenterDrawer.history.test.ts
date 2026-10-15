// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type App, type VNodeChild } from 'vue'
import TaskCenterDrawer from './TaskCenterDrawer.vue'
import { beginSession } from '@/session/sessionLifecycle'
import { NcpApiError } from '@/api/systemTransport'
const api = vi.hoisted(() => ({ jobs: vi.fn(), cancel: vi.fn(), history: vi.fn(), auth: vi.fn() }))
vi.mock('@/api/docker', () => ({ requestJobs: api.jobs, cancelJob: api.cancel }))
vi.mock('@/api/jobHistory', async original => ({ ...await original<typeof import('@/api/jobHistory')>(), requestJobHistory: api.history }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ refresh: api.auth }) }))
vi.mock('element-plus', () => ({ ElDrawer: { setup: (_: unknown, { slots }: { slots: Record<string, () => VNodeChild> }) => () => h('section', {}, [slots.default?.()]) } }))
let app: App | undefined, host: HTMLDivElement
const flush = async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); await nextTick() }
function button(text: string) { return [...host.querySelectorAll('button')].find(button => button.textContent?.includes(text))! }
async function mount() { host = document.createElement('div'); document.body.append(host); app = createApp(TaskCenterDrawer); app.mount(host); await flush() }
beforeEach(() => {
  beginSession(); vi.resetAllMocks(); vi.useFakeTimers()
  api.jobs.mockResolvedValue({ jobs: [{ id: 'a', type: 'docker-image-pull', status: 'running', cancellable: true, artifactState: 'unknown', reference: 'image:tag', message: '', progress: 0, createdAt: '2026-10-15T00:00:00Z', updatedAt: '2026-10-15T00:00:00Z' }], invalidCount: 0 })
  api.history.mockResolvedValue({ source: 'persisted', items: [] })
})
afterEach(() => { app?.unmount(); host?.remove(); vi.useRealTimers() })
describe('抽屉观察 owner 切换', () => {
  it.each(['关闭', '切历史'])('%s 同步停止最近任务取消门闩，尚未卸载的按钮也不能提交', async action => {
    await mount(); button('查看详情').click(); await nextTick(); const cancel = button('请求取消')
    if (action === '关闭') host.querySelector<HTMLButtonElement>('[aria-label="关闭最近任务"]')!.click(); else button('已保存历史').click()
    cancel.click(); expect(api.cancel).not.toHaveBeenCalled(); await flush(); await vi.advanceTimersByTimeAsync(60_000); expect(api.jobs).toHaveBeenCalledTimes(1)
  })
  it.each(['关闭', '切最近'])('%s 同步撤销历史读取；旧401不能触发会话刷新', async action => {
    let reject!: (e: Error) => void; api.history.mockReturnValue(new Promise((_a, b) => { reject = b }))
    await mount(); button('已保存历史').click(); await nextTick()
    if (action === '关闭') host.querySelector<HTMLButtonElement>('[aria-label="关闭已保存历史"]')!.click(); else button('最近任务').click()
    expect((api.history.mock.calls[0]?.[1] as AbortSignal).aborted).toBe(true)
    reject(new NcpApiError('AUTH_UNAUTHORIZED', 'invalid')); await flush(); expect(api.auth).not.toHaveBeenCalled()
  })
  it('切历史停止最近轮询，返回最近重建原观察；历史不轮询', async () => {
    await mount(); button('已保存历史').click(); await flush(); await vi.advanceTimersByTimeAsync(60_000)
    expect(api.jobs).toHaveBeenCalledTimes(1); expect(api.history).toHaveBeenCalledTimes(1)
    button('最近任务').click(); await flush(); expect(api.jobs).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(15_000); expect(api.jobs).toHaveBeenCalledTimes(3); expect(api.history).toHaveBeenCalledTimes(1)
  })
})
