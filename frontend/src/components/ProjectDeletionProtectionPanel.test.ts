// @vitest-environment jsdom
import { createApp, h, nextTick, type App } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ProjectDeletionAdmission } from '@/composables/useDockerProjectDeletionProtection'
import ProjectDeletionProtectionPanel from './ProjectDeletionProtectionPanel.vue'
let app: App | undefined
let host: HTMLDivElement
const check = vi.fn(), release = vi.fn()
const entry = (fields: Partial<ProjectDeletionAdmission> = {}): ProjectDeletionAdmission => ({ token: Symbol(), projectId: 'long-project-id-'.repeat(30), name: 'demo', kind: 'compose', phase: 'unknown', checking: false, inventoryChecked: false, checkVersion: 0, notice: '', ...fields })
async function mount(entries: ProjectDeletionAdmission[]) {
  host = document.createElement('div'); document.body.append(host)
  app = createApp({ render: () => h(ProjectDeletionProtectionPanel, { entries, onCheck: check, onRelease: release }) }); app.mount(host); await nextTick()
}
afterEach(() => { app?.unmount(); host?.remove(); vi.clearAllMocks() })
describe('项目删除防重复独立面板', () => {
  it('没有保护项不占位', async () => { await mount([]); expect(host.querySelector('section')).toBeNull() })
  it('保留完整 ID、人工核对及会话范围说明，不依赖清单', async () => {
    const value = entry(); await mount([value])
    expect(host.textContent).toContain(value.projectId)
    expect(host.textContent).toContain('项目注册状态可能仍需人工核对')
    expect(host.textContent).toContain('整页刷新、其他标签页')
    expect(host.textContent).toContain('不会补偿、重发或取消')
    const buttons = host.querySelectorAll('button'); buttons[0]!.click(); buttons[1]!.click()
    expect(check).toHaveBeenCalledExactlyOnceWith(value); expect(release).not.toHaveBeenCalled()
  })
  it.each(['confirming', 'submitting'] as const)('%s 不允许核对或解除', async phase => {
    await mount([entry({ phase, inventoryChecked: true })]); expect([...host.querySelectorAll('button')].every(button => button.disabled)).toBe(true)
  })
  it('本轮成功只允许发出解除请求，不自行删除保护', async () => {
    const value = entry({ inventoryChecked: true, checkVersion: 2 }); await mount([value]); host.querySelectorAll('button')[1]!.click()
    expect(release).toHaveBeenCalledExactlyOnceWith(value); expect(host.querySelector('article')).not.toBeNull()
  })
})
