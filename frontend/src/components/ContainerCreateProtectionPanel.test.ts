// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, type App } from 'vue'
import type { ContainerCreateAdmission } from '@/composables/useDockerContainerCreateJobs'
import ContainerCreateProtectionPanel from './ContainerCreateProtectionPanel.vue'
const mounted: Array<{ app: App; host: HTMLElement }> = []
afterEach(() => { for (const { app, host } of mounted.splice(0)) { app.unmount(); host.remove() } })
function entry(overrides: Partial<ContainerCreateAdmission> = {}): ContainerCreateAdmission {
  return { token: Symbol(), generation: 1, runContainer: true, reservedAt: 0, phase: 'unknown', checking: false, checkVersion: 0, completionVersion: 0, checked: false, lastReadSucceeded: false, inventoryChecked: false, observedActive: false, notice: '', ...overrides }
}
function mount(value: ContainerCreateAdmission | null, result: { containerId: string; created: true; started: boolean } | null = null) {
  const host = document.createElement('div'); document.body.append(host)
  const check = vi.fn(), release = vi.fn()
  const app = createApp({ render: () => h(ContainerCreateProtectionPanel, { entry: value, result, onCheck: check, onRelease: release }) })
  app.mount(host); mounted.push({ app, host }); return { host, check, release }
}
describe('容器创建保护面板', () => {
  it.each(['confirming', 'submitting'] as const)('%s 不能核对或解除', phase => {
    const { host } = mount(entry({ phase }))
    expect([...host.querySelectorAll<HTMLButtonElement>('button')].every(button => button.disabled)).toBe(true)
    expect(host.textContent).toContain('关闭抽屉或离开页面不会取消任务')
  })
  it('未知结果提供只读核对且完整核对前不能解除', () => {
    const item = entry(); const { host, check, release } = mount(item)
    const buttons = host.querySelectorAll('button'); buttons[0]!.click(); buttons[1]!.click()
    expect(check).toHaveBeenCalledExactlyOnceWith(item); expect(release).not.toHaveBeenCalled()
    expect(host.textContent).toContain('受理结果未知'); expect(host.textContent).toContain('其他标签页')
  })
  it('本轮成功核对且无活动证据才启用风险解除', () => {
    const item = entry({ checked: true, lastReadSucceeded: true, inventoryChecked: true })
    const { host, release } = mount(item); host.querySelectorAll('button')[1]!.click()
    expect(release).toHaveBeenCalledExactlyOnceWith(item)
  })
  it('完整 ID 结果卡明确创建调用确认不保证持续运行，不提供取消或重试', () => {
    const id = 'a'.repeat(64); const { host } = mount(null, { containerId: id, created: true, started: true })
    expect(host.querySelector('[aria-label="已确认的创建结果"]')?.textContent).toContain(id)
    expect(host.textContent).toContain('不保证容器持续运行'); expect(host.querySelector('button')).toBeNull()
  })
})
