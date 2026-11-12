// @vitest-environment jsdom
import { createApp, h, nextTick, shallowRef, type App } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { JobSnapshot } from '@/api/system'
import type { ContainerActionAdmission } from '@/composables/useDockerContainerActionJobs'
import ContainerActionProtectionPanel from './ContainerActionProtectionPanel.vue'

const containerId = 'a'.repeat(64)
const job = (fields: Partial<JobSnapshot> = {}): JobSnapshot => ({ id: 'known-job-123', type: 'docker-container-restart', reference: containerId, status: 'queued', artifactState: 'unknown', cancellable: false, message: '', progress: 0, createdAt: '2026-10-16T00:00:00Z', updatedAt: '2026-10-16T00:00:00Z', downloadedBytes: 0, totalBytes: 0, speedBytes: 0, layers: {}, ...fields })
const entry = (fields: Partial<ContainerActionAdmission> = {}): ContainerActionAdmission => ({ token: Symbol(), containerId, name: '旧容器名称', action: 'restart', phase: 'unknown', checking: false, checkVersion: 0, checked: false, inventoryChecked: false, observedActive: false, notice: '', ...fields })
let app: App | undefined
let host: HTMLDivElement
const entries = shallowRef<ContainerActionAdmission[]>([])
const check = vi.fn(), release = vi.fn(), fetcher = vi.fn()
async function mount(value: ContainerActionAdmission[] = []) {
  entries.value = value
  host = document.createElement('div')
  document.body.append(host)
  app = createApp({ render: () => h(ContainerActionProtectionPanel, { entries: entries.value, onCheck: check, onRelease: release }) })
  app.mount(host)
  await nextTick()
}
function buttons() { return [...host.querySelectorAll('button')] }
function releaseButton() { return buttons().find(button => button.textContent === '解除本页防重复保护')! }
beforeEach(() => { vi.clearAllMocks(); vi.stubGlobal('fetch', fetcher) })
afterEach(() => { app?.unmount(); app = undefined; host?.remove(); vi.unstubAllGlobals() })

describe('单容器操作保护独立面板', () => {
  it('无保护项时不显示', async () => {
    await mount()
    expect(host.querySelector('section')).toBeNull()
    expect(fetcher).not.toHaveBeenCalled()
  })
  it('不依赖容器清单，仍显示完整身份、任务 ID、状态及跨路由保护范围', async () => {
    await mount([entry({ phase: 'known', job: job(), notice: '已接收不代表完成。' })])
    expect(host.querySelector('[aria-label="单容器操作防重复保护"]')).not.toBeNull()
    expect(host.textContent).toContain(`容器 ID：${containerId}`)
    expect(host.textContent).toContain('任务 ID：known-job-123')
    expect(host.textContent).toContain('旧容器名称 · 重启 · 已接收，排队中')
    expect(host.textContent).toContain('本登录会话跨路由保留保护')
    expect(host.textContent).toContain('整页重载、多标签页及其他客户端不在保护范围内')
    expect(host.querySelector('[role="status"]')?.textContent).toBe('已接收不代表完成。')
    expect(fetcher).not.toHaveBeenCalled()
  })
  it.each(['confirming', 'submitting'] as const)('%s 禁止核对及解除', async phase => {
    await mount([entry({ phase, checked: true, inventoryChecked: true })])
    expect(buttons().every(button => button.disabled)).toBe(true)
    buttons().forEach(button => button.click())
    expect(check).not.toHaveBeenCalled()
    expect(release).not.toHaveBeenCalled()
    expect(fetcher).not.toHaveBeenCalled()
  })
  it.each([
    { status: 'queued' }, { status: 'running' },
    { status: 'queued', persistenceStatus: 'pending' }, { status: 'running', persistenceStatus: 'pending' },
    { status: 'completed', persistenceStatus: 'pending' }, { status: 'failed', persistenceStatus: 'pending' }, { status: 'interrupted', persistenceStatus: 'pending' },
  ] as Partial<JobSnapshot>[])('活动或待落盘任务不可解除：%j', async fields => {
    await mount([entry({ phase: 'known', job: job(fields), checked: true, inventoryChecked: true })])
    expect(releaseButton().disabled).toBe(true)
    releaseButton().click()
    expect(release).not.toHaveBeenCalled()
    if (fields.persistenceStatus) expect(host.textContent).toContain('结果保存中')
  })
  it('unknown 核对前不可解除，核对后仅发出解除请求，不自动执行或重发', async () => {
    const original = entry()
    await mount([original])
    expect(host.textContent).toContain('接收未知，勿重复提交')
    expect(host.textContent).not.toContain('任务 ID：')
    expect(releaseButton().disabled).toBe(true)
    buttons()[0]!.click()
    expect(check).toHaveBeenCalledExactlyOnceWith(original)
    expect(releaseButton().disabled).toBe(true)
    const checked = { ...original, checked: true, checkVersion: 1, inventoryChecked: false }
    entries.value = [checked]
    await nextTick()
    expect(releaseButton().disabled).toBe(false)
    releaseButton().click()
    expect(release).toHaveBeenCalledExactlyOnceWith(checked)
    expect(host.querySelector('article')).not.toBeNull()
    expect(fetcher).not.toHaveBeenCalled()
  })
  it('unknown 观察到同容器活动任务时，即使核对后也禁解除且不认领任务', async () => {
    await mount([entry({ checked: true, inventoryChecked: true, observedActive: true })])
    expect(releaseButton().disabled).toBe(true)
    expect(host.textContent).toContain('不会自动绑定为本次提交')
    expect(host.textContent).not.toContain('任务 ID：')
    releaseButton().click()
    expect(release).not.toHaveBeenCalled()
  })
  it.each(['failed', 'interrupted'] as const)('%s 必须核对且成功刷新清单才允许请求解除', async status => {
    const failed = entry({ phase: 'known', job: job({ status, errorCode: 'DOCKER_CONTAINER_STATE_MISMATCH' }) })
    await mount([failed])
    expect(host.textContent).toContain('结果代码：DOCKER_CONTAINER_STATE_MISMATCH')
    expect(releaseButton().disabled).toBe(true)
    entries.value = [{ ...failed, checked: true, inventoryChecked: false }]
    await nextTick()
    expect(releaseButton().disabled).toBe(true)
    entries.value = [{ ...failed, checked: true, inventoryChecked: true }]
    await nextTick()
    expect(releaseButton().disabled).toBe(false)
    expect(fetcher).not.toHaveBeenCalled()
  })
  it('核对进行中禁用两个按钮，核对按钮只 emit 不 POST', async () => {
    const active = entry({ phase: 'known', job: job({ status: 'running' }) })
    await mount([active])
    buttons()[0]!.click()
    expect(check).toHaveBeenCalledExactlyOnceWith(active)
    entries.value = [{ ...active, checking: true, checked: true, inventoryChecked: true }]
    await nextTick()
    expect(buttons().every(button => button.disabled)).toBe(true)
    expect(buttons()[0]!.textContent).toBe('正在只读核对…')
    buttons()[0]!.click()
    expect(check).toHaveBeenCalledOnce()
    expect(fetcher).not.toHaveBeenCalled()
  })
})
