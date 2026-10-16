// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, shallowRef, type App } from 'vue'
import type { ComposeActionAdmission, ComposeActionCompletion, ComposeActionJobEvidence } from '@/composables/useComposeActionJobs'
import ComposeActionProtectionPanel from './ComposeActionProtectionPanel.vue'
import source from './ComposeActionProtectionPanel.vue?raw'

const paths = ['/volume/projects/demo/compose.yaml', '/volume/projects/demo/override.yaml']
const completed: ComposeActionCompletion = { projectId: 'demo', action: 'restart', state: 'running', serviceCount: 3 }
const mounted: Array<{ app: App; host: HTMLElement }> = []
afterEach(() => { for (const { app, host } of mounted.splice(0)) { app.unmount(); host.remove() } })
function entry(overrides: Partial<ComposeActionAdmission> = {}): ComposeActionAdmission {
  return { token: Symbol(), generation: 1, action: 'restart', projectId: 'demo', displayName: '冻结项目', workingDirectory: '/volume/projects/demo', configFiles: paths,
    input: { projectId: 'demo', workingDirectory: '/volume/projects/demo', configFiles: paths }, reservedAt: 0,
    phase: 'unknown', checking: false, checkVersion: 0, completionVersion: 0, checked: false, lastReadSucceeded: false,
    inventoryChecked: false, observedActive: false, notice: '', ...overrides }
}
function job(overrides: Partial<ComposeActionJobEvidence> = {}): ComposeActionJobEvidence {
  return { id: 'compose-job-id', type: 'compose-restart', status: 'running', ...overrides }
}
function mount(value: ComposeActionAdmission | null, result: ComposeActionCompletion | null = null) {
  const host = document.createElement('div'); document.body.append(host)
  const current = shallowRef(value), completion = shallowRef(result), check = vi.fn(), release = vi.fn()
  const app = createApp({ render: () => h(ComposeActionProtectionPanel, { entry: current.value, result: completion.value, onCheck: check, onRelease: release }) })
  app.mount(host); mounted.push({ app, host })
  return { host, current, completion, check, release, buttons: () => [...host.querySelectorAll<HTMLButtonElement>('button')] }
}
const releasable = { checked: true, lastReadSucceeded: true, inventoryChecked: true }

describe('Compose 操作保护面板', () => {
  it('无保护与结果时不显示', () => { expect(mount(null).host.querySelector('section')).toBeNull() })
  it.each(['confirming', 'submitting', 'resolved'] as const)('%s 不允许核对或解除', phase => {
    const view = mount(entry({ phase }))
    view.buttons().forEach(button => { expect(button.disabled).toBe(true); button.click() })
    expect(view.check).not.toHaveBeenCalled(); expect(view.release).not.toHaveBeenCalled()
  })
  it.each(['start', 'stop', 'restart'] as const)('展示 %s 的冻结意图和有序路径，明确不是持久化证据', action => {
    const value = Object.freeze(entry({ action, configFiles: Object.freeze([...paths]) }))
    const { host } = mount(value)
    const intent = host.querySelector('[aria-label="冻结的 Compose 提交意图"]')!
    for (const text of [value.projectId, value.displayName, value.workingDirectory, { start: '启动', stop: '停止', restart: '重启' }[action]]) expect(intent.textContent).toContain(text)
    expect([...intent.querySelectorAll('ol li')].map(node => node.textContent)).toEqual(paths)
    expect(host.textContent).toContain('不是持久化执行证据')
    expect(host.textContent).toContain('项目 ID 仅用于关联，不是 Compose -p 参数')
    expect(value.configFiles).toEqual(paths)
  })
  it('未知提交保持保护，仅提供只读核对和显式风险解除', () => {
    const value = entry(); const view = mount(value)
    expect(view.host.querySelector('[role="alert"]')?.textContent).toContain('防重复保护仍保留')
    expect(view.host.textContent).toContain('接收结果未知，请勿重复提交')
    view.buttons()[0]!.click(); view.buttons()[1]!.click()
    expect(view.check).toHaveBeenCalledExactlyOnceWith(value); expect(view.release).not.toHaveBeenCalled()
    expect(view.buttons()).toHaveLength(2)
    expect(view.buttons().some(button => /重试|取消/.test(button.textContent!))).toBe(false)
  })
  it.each(['queued', 'running', 'failed', 'interrupted'] as const)('%s 即使携带聚合字段也不展示成功证据', status => {
    const view = mount(entry({ phase: 'known', job: job({ status, composeActionResult: completed }) }))
    expect(view.host.querySelector('.saved-results')).toBeNull()
    expect(view.host.textContent).not.toContain('观察到的服务数')
    expect(view.buttons()[1]!.disabled).toBe(true)
    if (status === 'failed' || status === 'interrupted') expect(view.host.querySelector('[role="alert"]')).not.toBeNull()
  })
  it('pending 终态不展示聚合成功、不解除保护', () => {
    const view = mount(entry({ phase: 'known', ...releasable, job: job({ status: 'completed', persistenceStatus: 'pending', composeActionResult: completed }) }))
    expect(view.host.textContent).toContain('结果保存中，尚未确认结果')
    expect(view.host.textContent).not.toContain('任务执行结束')
    expect(view.host.querySelector('.saved-results')).toBeNull(); expect(view.buttons()[1]!.disabled).toBe(true)
  })
  it.each(['unknown', 'failed', 'interrupted'] as const)('完整核对后 %s 才可请求风险解除', status => {
    const value = entry({ ...releasable, ...(status === 'unknown' ? {} : { phase: 'known' as const, job: job({ status }) }) })
    const view = mount(value); expect(view.buttons()[1]!.disabled).toBe(false); view.buttons()[1]!.click()
    expect(view.release).toHaveBeenCalledExactlyOnceWith(value)
    expect(view.buttons()[1]!.textContent).toContain('确认重复执行风险')
  })
  it.each([{ checking: true }, { checked: false }, { lastReadSucceeded: false }, { inventoryChecked: false }, { observedActive: true }])('核对条件不足 %j 时不能解除', overrides => {
    const view = mount(entry({ ...releasable, ...overrides }))
    expect(view.buttons()[1]!.disabled).toBe(true); view.buttons()[1]!.click(); expect(view.release).not.toHaveBeenCalled()
    if (overrides.checking) expect(view.buttons()[0]!.disabled).toBe(true)
  })
  it('更新 props 后事件携带当前 entry 而非旧快照', async () => {
    const view = mount(entry()); const current = entry({ ...releasable, checkVersion: 4 })
    view.current.value = current; await nextTick()
    view.buttons()[0]!.click(); view.buttons()[1]!.click()
    expect(view.check).toHaveBeenCalledExactlyOnceWith(current); expect(view.release).toHaveBeenCalledExactlyOnceWith(current)
  })
  it('清单失败、空清单和活动观察均不作为未执行证明，不呈现原始错误', () => {
    const value = entry({ checked: true, observedActive: true, notice: 'SECRET_RAW_NOTICE', job: job({ status: 'interrupted' }) })
    Object.assign(value.job!, { message: 'SECRET_RAW_MESSAGE', error: 'SECRET_RAW_ERROR', output: 'SECRET_RAW_OUTPUT' })
    const { host } = mount(value)
    expect(host.textContent).toContain('任务读取失败，不能判断未接收或未执行')
    expect(host.textContent).toContain('容器清单未成功刷新')
    expect(host.textContent).toContain('此观察不会自动认领未知提交')
    expect(host.textContent).toContain('当前容器清单、空清单或状态变化都不能证明先前操作未执行')
    expect(host.textContent).not.toContain('SECRET_RAW')
  })
  it.each(['running', 'stopped'] as const)('完成结果仅展示 %s 聚合观察，没有虚构逐服务结果', state => {
    const result = { ...completed, state, action: state === 'stopped' ? 'stop' as const : 'restart' as const }
    const { host } = mount(null, result)
    expect(host.textContent).toContain(`该次观察状态：${state === 'running' ? '运行' : '停止'} · 观察到的服务数：3`)
    expect(host.textContent).toContain('不保证健康、持续状态、配置服务完整性或重启次数')
    expect(host.textContent).toContain('没有逐服务结果')
    expect(host.querySelectorAll('li')).toHaveLength(0)
    expect(host.querySelectorAll('button')).toHaveLength(0)
  })
  it('领取后独立结果保留，status 使用合法容器', async () => {
    const view = mount(entry({ phase: 'resolved', job: job({ status: 'completed', composeActionResult: completed }) }), completed)
    expect(view.host.querySelector('.saved-results')?.textContent).toContain('观察到的服务数：3')
    expect([...view.host.querySelectorAll('[role="status"]')].every(node => node.tagName === 'DIV')).toBe(true)
    view.current.value = null; await nextTick()
    expect(view.host.querySelector('.completed-result')?.textContent).toContain('demo')
    expect(view.host.querySelector('.admission-status')).toBeNull()
  })
  it('32 个长路径折叠、顺序完整、原生键盘可访问且有界滚动，控件置于明细前', () => {
    const longPaths = Array.from({ length: 32 }, (_, index) => `/volume/${'路径'.repeat(500)}/${index}.yaml`)
    const { host } = mount(entry({ configFiles: longPaths, displayName: '<script>not executable</script>' }))
    const details = host.querySelector('details')!
    expect(details.open).toBe(false); expect(details.firstElementChild?.tagName).toBe('SUMMARY')
    expect(host.querySelector('.protection-actions')!.compareDocumentPosition(details)).toBe(Node.DOCUMENT_POSITION_FOLLOWING)
    details.open = true
    const region = host.querySelector<HTMLElement>('[aria-label="冻结的 Compose 提交意图"]')!
    expect(region.getAttribute('role')).toBe('region'); expect(region.tabIndex).toBe(0)
    region.focus(); expect(document.activeElement).toBe(region)
    expect([...region.querySelectorAll('li')].map(node => node.textContent)).toEqual(longPaths)
    expect(region.textContent).toContain('<script>not executable</script>'); expect(region.querySelector('script')).toBeNull()
    expect(source).toContain('max-height: 20rem; overflow: auto')
    expect(source).toContain('.frozen-intent:focus-visible { outline: 2px solid')
    expect(host.querySelector('[role="button"]')).toBeNull()
  })
  it('小范围默认展开，44px 控件、可见焦点和窄屏换行，不宣称跨客户端锁', () => {
    const { host } = mount(entry())
    expect(host.querySelector('details')!.open).toBe(true)
    expect(host.textContent).toContain('整页刷新、其他标签页和其他客户端不在保护范围内')
    expect(host.textContent).toContain('离开页面不会取消任务')
    expect(source).toContain('min-height: max(44px, var(--ncp-touch-target))')
    expect(source).toContain('button:focus-visible'); expect(source).toContain('summary:focus-visible')
    expect(source).toContain('overflow-wrap: anywhere'); expect(source).toContain('white-space: normal')
    expect(source).toContain('@media (max-width: 600px)'); expect(source).toContain('flex-direction: column')
    expect([...host.querySelectorAll('button')].every(button => button.type === 'button')).toBe(true)
  })
})
