// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, shallowRef, type App } from 'vue'
import type { ProjectActionAdmission, ProjectActionCompletion, ProjectActionJobEvidence } from '@/composables/useDockerProjectActionJobs'
import ProjectActionProtectionPanel from './ProjectActionProtectionPanel.vue'
import source from './ProjectActionProtectionPanel.vue?raw'

const ids = ['a'.repeat(64), 'b'.repeat(64), 'c'.repeat(64)]
const completed: ProjectActionCompletion = { projectId: 'standalone', kind: 'standalone', action: 'restart', items: [{ containerId: ids[0]!, outcome: 'succeeded', state: 'running' }] }
const partial: ProjectActionCompletion = { ...completed, items: [...completed.items, { containerId: ids[1]!, outcome: 'state-mismatch', state: 'stopped', errorCode: 'DOCKER_CONTAINER_STATE_MISMATCH' }, { containerId: ids[2]!, outcome: 'unknown', state: 'unknown', errorCode: 'JOB_EXECUTION_INTERRUPTED' }] }
const mounted: Array<{ app: App; host: HTMLElement }> = []
afterEach(() => { for (const { app, host } of mounted.splice(0)) { app.unmount(); host.remove() } })
function entry(overrides: Partial<ProjectActionAdmission> = {}): ProjectActionAdmission {
  return { token: Symbol(), generation: 1, action: 'restart', containerIds: ids, reservedAt: 0, phase: 'unknown', checking: false, checkVersion: 0, completionVersion: 0, checked: false, lastReadSucceeded: false, inventoryChecked: false, observedActive: false, notice: '', ...overrides }
}
function job(overrides: Partial<ProjectActionJobEvidence> = {}): ProjectActionJobEvidence {
  return { id: 'job-full-id', type: 'docker-project-restart', status: 'running', ...overrides }
}
function mount(value: ProjectActionAdmission | null, result: ProjectActionCompletion | null = null) {
  const host = document.createElement('div'); document.body.append(host)
  const current = shallowRef(value), completion = shallowRef(result), check = vi.fn(), release = vi.fn()
  const app = createApp({ render: () => h(ProjectActionProtectionPanel, { entry: current.value, result: completion.value, onCheck: check, onRelease: release }) })
  app.mount(host); mounted.push({ app, host })
  return { host, current, completion, check, release, buttons: () => [...host.querySelectorAll<HTMLButtonElement>('button')] }
}
const releasable = { checked: true, lastReadSucceeded: true, inventoryChecked: true }

describe('项目操作保护面板', () => {
  it('无保护和结果时不显示', () => { expect(mount(null).host.innerHTML).not.toContain('<section') })
  it.each(['confirming', 'submitting', 'resolved'] as const)('%s 不允许核对或解除', phase => {
    const view = mount(entry({ phase })); view.buttons().forEach(button => { expect(button.disabled).toBe(true); button.click() })
    expect(view.check).not.toHaveBeenCalled(); expect(view.release).not.toHaveBeenCalled()
  })
  it.each(['start', 'stop', 'restart'] as const)('显示 %s 操作、冻结数量和完整 ID', action => {
    const { host } = mount(entry({ action }))
    expect(host.textContent).toContain({ start: '启动', stop: '停止', restart: '重启' }[action])
    expect(host.textContent).toContain('冻结范围：3 个容器')
    expect([...host.querySelectorAll('[aria-label="冻结的容器 ID"] li')].map(node => node.textContent)).toEqual(ids)
  })
  it('未知提交仅提供只读核对，不提供重试或取消', () => {
    const value = entry(); const view = mount(value)
    view.buttons()[0]!.click(); view.buttons()[1]!.click()
    expect(view.check).toHaveBeenCalledExactlyOnceWith(value); expect(view.release).not.toHaveBeenCalled()
    expect(view.host.textContent).toContain('接收结果未知')
    expect(view.buttons()).toHaveLength(2)
    expect(view.buttons().some(button => /重试|取消/.test(button.textContent!))).toBe(false)
  })
  it.each(['queued', 'running'] as const)('%s 即使含结果也不呈现为成功', status => {
    const view = mount(entry({ phase: 'known', job: job({ status, projectActionResult: completed }), ...releasable }))
    expect(view.host.querySelector('.saved-results')).toBeNull()
    expect(view.host.textContent).not.toContain('该次调用回读符合目标状态')
    expect(view.buttons()[1]!.disabled).toBe(true)
  })
  it('保存中不得展示终态或逐项成功，也不得解除', () => {
    const view = mount(entry({ phase: 'known', job: job({ status: 'completed', persistenceStatus: 'pending', projectActionResult: completed }), ...releasable }))
    expect(view.host.textContent).toContain('结果保存中，尚未确认结果')
    expect(view.host.textContent).not.toContain('任务执行结束')
    expect(view.host.querySelector('.saved-results')).toBeNull(); expect(view.buttons()[1]!.disabled).toBe(true)
  })
  it('中断结果保留成功、不符与未知逐项证据，不呈现原始名称或错误', () => {
    const value = entry({ phase: 'known', job: job({ status: 'interrupted', projectActionResult: partial }), notice: 'RAW_SERVER_ERROR' })
    Object.assign(value.job!, { message: 'RAW_SERVER_MESSAGE', error: 'RAW_SERVER_ERROR', name: 'SECRET_NAME' })
    const { host } = mount(value)
    const items = host.querySelectorAll('[aria-label="任务逐项结果"] li')
    expect(items).toHaveLength(3)
    expect(items[0]!.textContent).toContain('该次调用回读符合目标状态')
    expect(items[1]!.textContent).toContain('该次调用回读与目标状态不符')
    expect(items[2]!.textContent).toContain('执行结果未知，可能已执行')
    ids.forEach((id, index) => expect(items[index]!.textContent).toContain(id))
    expect(host.textContent).not.toMatch(/RAW_SERVER|SECRET_NAME|JOB_EXECUTION_INTERRUPTED/)
    expect(host.textContent).toContain('部分符合目标不代表全部成功')
  })
  it.each(['unknown', 'failed', 'interrupted'] as const)('完整核对后的 %s 可发出风险解除事件', status => {
    const value = entry({ ...releasable, ...(status === 'unknown' ? {} : { phase: 'known' as const, job: job({ status }) }) })
    const view = mount(value); expect(view.buttons()[1]!.disabled).toBe(false); view.buttons()[1]!.click()
    expect(view.release).toHaveBeenCalledExactlyOnceWith(value)
    expect(view.buttons()[1]!.textContent).toContain('重复执行风险')
  })
  it.each([{ checking: true }, { checked: false }, { lastReadSucceeded: false }, { inventoryChecked: false }, { observedActive: true }])('核对条件不足 %j 时不能解除', overrides => {
    const view = mount(entry({ ...releasable, ...overrides }))
    expect(view.buttons()[1]!.disabled).toBe(true); view.buttons()[1]!.click(); expect(view.release).not.toHaveBeenCalled()
    if (overrides.checking) expect(view.buttons()[0]!.disabled).toBe(true)
  })
  it('清单与读取失败不作为未执行证明', () => {
    const { host } = mount(entry({ checked: true, observedActive: true }))
    expect(host.textContent).toContain('任务读取失败，不能判断未接收或未执行')
    expect(host.textContent).toContain('容器清单未成功刷新')
    expect(host.textContent).toContain('此观察不会自动认领未知提交')
    expect(host.textContent).toContain('当前容器清单、空清单或状态变化都不能证明先前操作未执行')
  })
  it('领取后独立完成卡保留，使用合法 status 容器且不保证持续健康', async () => {
    const view = mount(entry({ phase: 'resolved', job: job({ status: 'completed', projectActionResult: completed }) }), completed)
    const statuses = [...view.host.querySelectorAll('[role="status"]')]
    expect(statuses).toHaveLength(2)
    expect(statuses.every(node => node.tagName === 'DIV' && node.classList.contains('protection-status'))).toBe(true)
    view.current.value = null; await nextTick()
    expect(view.host.querySelector('[aria-label="已确认的项目操作结果"]')?.textContent).toContain(ids[0])
    expect(view.host.textContent).toContain('不保证容器持续运行、持续停止或健康')
    expect(view.host.querySelector('.admission-status')).toBeNull(); expect(view.buttons()).toHaveLength(0)
  })
  it('128 个目标默认折叠明细且核对操作在前，展开后保留完整逐项证据', () => {
    const largeIds = Array.from({ length: 128 }, (_, index) => index.toString(16).padStart(64, '0'))
    const largeResult: ProjectActionCompletion = { ...completed, items: largeIds.map((containerId, index) => ({ containerId, ...(index < 126 ? { outcome: 'succeeded' as const, state: 'running' as const } : index === 126 ? { outcome: 'state-mismatch' as const, state: 'stopped' as const, errorCode: 'DOCKER_CONTAINER_STATE_MISMATCH' as const } : { outcome: 'unknown' as const, state: 'unknown' as const, errorCode: 'JOB_EXECUTION_INTERRUPTED' as const }) })) }
    const value = entry({ ...releasable, phase: 'known', containerIds: largeIds, job: job({ status: 'interrupted', projectActionResult: largeResult }) })
    const view = mount(value)
    const details = [...view.host.querySelectorAll('details')]
    expect(details).toHaveLength(2)
    expect(details.every(node => !node.open && node.firstElementChild?.tagName === 'SUMMARY')).toBe(true)
    expect(view.host.querySelector('.protection-actions')!.compareDocumentPosition(details[0]!)).toBe(Node.DOCUMENT_POSITION_FOLLOWING)
    expect(details[0]!.querySelector('summary')!.textContent).toContain('128 个')
    expect(details[1]!.querySelector('summary')!.textContent).toContain('符合目标 126 · 状态不符 1 · 结果未知 1')
    details.forEach(node => { node.open = true })
    expect([...view.host.querySelectorAll('[aria-label="冻结的容器 ID"] li')].map(node => node.textContent)).toEqual(largeIds)
    const rows = view.host.querySelectorAll('[aria-label="任务逐项结果"] li')
    expect(rows).toHaveLength(128)
    largeIds.forEach((id, index) => expect(rows[index]!.textContent).toContain(id))
    view.buttons()[0]!.click(); view.buttons()[1]!.click()
    expect(view.check).toHaveBeenCalledExactlyOnceWith(value); expect(view.release).toHaveBeenCalledExactlyOnceWith(value)
    const completionView = mount(null, largeResult)
    expect(completionView.host.querySelector('details')!.open).toBe(false)
    expect(completionView.host.querySelector('summary')!.textContent).toContain('逐项回读（128 项）')
    expect([...completionView.host.querySelectorAll('[role="status"]')].every(node => node.tagName === 'DIV')).toBe(true)
  })
  it('小范围明细默认展开，原生 summary 提供键盘焦点样式', () => {
    const { host } = mount(entry({ phase: 'known', job: job({ status: 'interrupted', projectActionResult: partial }) }), completed)
    expect([...host.querySelectorAll('details')].every(node => node.open)).toBe(true)
    expect(source).toContain('summary:focus-visible')
    expect(host.querySelector('[role="button"]')).toBeNull()
  })
  it('明确会话限制、键盘焦点、44px 目标及窄屏换行样式', () => {
    const { host } = mount(entry())
    expect(host.textContent).toContain('整页刷新、其他标签页和其他客户端不在保护范围内')
    expect(host.textContent).toContain('离开页面不会取消任务')
    expect(source).toContain('min-height: max(44px, var(--ncp-touch-target))')
    expect(source).toContain('button:focus-visible'); expect(source).toContain('overflow-wrap: anywhere')
    expect(source).toContain('@media (max-width: 600px)'); expect(source).toContain('flex-direction: column')
    expect([...host.querySelectorAll('button')].every(button => button.type === 'button')).toBe(true)
  })
})
