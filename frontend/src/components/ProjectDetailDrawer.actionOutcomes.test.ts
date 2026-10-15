// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick, type App } from 'vue'
import type { DockerInventory, DockerProject } from '@/api/system'
import ProjectDetailDrawer from './ProjectDetailDrawer.vue'

vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    // 保留具名标题与正文插槽，真实抽屉内容参与断言；不测传送和动画。
    ElDrawer: defineComponent({ setup: (_, { slots }) => () => h('aside', [slots.header?.(), slots.default?.()]) }),
    ElTooltip: defineComponent({ setup: (_, { slots }) => () => h('div', slots.default?.()) }),
  }
})

const project: DockerProject = { id: 'demo', name: 'demo-project', kind: 'compose', workingDirectory: '/srv/demo', configFiles: ['compose.yaml'], containerCount: 1, runningCount: 1, state: 'running' }
const containers: DockerInventory['containers'] = [{ id: 'demo-container', name: 'demo-web', image: 'demo:latest', state: 'running', status: 'Up', createdAt: '2026-10-13T00:00:00.000Z', projectId: 'demo', projectName: 'demo-project', serviceName: 'web', ports: [] }]
let app: App | undefined
let host: HTMLDivElement | undefined
async function mount(overrides: Partial<InstanceType<typeof ProjectDetailDrawer>['$props']> = {}) {
  host = document.createElement('div')
  document.body.append(host)
  app = createApp(ProjectDetailDrawer, { modelValue: true, project, containers, hostName: 'nas', allowOperations: true, ...overrides })
  app.mount(host)
  await nextTick()
  return host
}
afterEach(() => { app?.unmount(); host?.remove(); app = undefined; host = undefined })

describe('项目详情抽屉结果文案', () => {
  it.each([
    { label: '项目未知', scope: 'project' as const, name: '项目操作', message: '操作结果未确认，可能已经执行；请先只读核对，勿重复提交。' },
    { label: '服务状态未达目标', scope: undefined, name: 'web', message: '状态复核尚未达到目标；请先只读核对，勿重复提交。' },
  ])('$label 使用核对标题且保留正文', async ({ scope, name, message }) => {
    const root = await mount({ projectActionErrors: [{ containerId: 'result', name, message, scope }] })
    const alert = root.querySelector('.project-lifecycle [role="alert"]')!
    expect(alert.querySelector('strong')?.textContent).toBe('项目操作需核对')
    expect(alert.querySelector('li')?.textContent).toBe(`${name}：${message}`)
    expect(root.querySelector('.project-lifecycle header p')?.textContent).toBe('通过 Compose 执行，需核对的结果会保留在这里。')
    expect(root.textContent).not.toMatch(/项目操作失败|部分容器操作失败|按容器逐项执行/)
    expect(root.querySelectorAll('.project-lifecycle__actions button')).toHaveLength(3)
  })

  it('独立容器组保留逐项执行说明但结果标题仍需核对', async () => {
    const root = await mount({ project: { ...project, kind: 'standalone' }, projectActionErrors: [{ containerId: 'demo-container', name: 'demo-web', message: '项目回执未确认完成，请先只读核对状态，勿重复提交。' }] })
    expect(root.querySelector('.project-lifecycle header p')?.textContent).toBe('按容器逐项执行，失败项会保留在这里。')
    expect(root.querySelector('.project-lifecycle [role="alert"] strong')?.textContent).toBe('项目操作需核对')
    expect(root.textContent).not.toContain('通过 Compose 执行')
  })

  it('没有错误时不显示结果告警', async () => {
    const root = await mount()
    expect(root.querySelector('[role="alert"]')).toBeNull()
    expect(root.querySelector('.project-lifecycle header p')?.textContent).toBe('通过 Compose 执行，需核对的结果会保留在这里。')
  })

  it('不允许操作时隐藏项目操作区及其告警', async () => {
    const root = await mount({ allowOperations: false, projectActionErrors: [{ containerId: 'result', name: '项目操作', message: '请先只读核对', scope: 'project' }] })
    expect(root.querySelector('.project-lifecycle')).toBeNull()
    expect(root.querySelector('.container-card__actions')).toBeNull()
    expect(root.querySelector('[role="alert"]')).toBeNull()
    expect(root.querySelector('.drawer-title')?.textContent).toContain('demo-project')
  })

  it('单容器未知结果使用核对标题并原样保留正文', async () => {
    const message = '容器操作结果未确认，可能已经生效；请先只读核对，勿重复提交。'
    const root = await mount({ containerActionError: message })
    const alert = root.querySelector('.detail-section [role="alert"]')!
    expect(alert.querySelector('strong')?.textContent).toBe('容器操作需核对')
    expect(alert.querySelector('p')?.textContent).toBe(message)
    expect(root.querySelector('.project-lifecycle [role="alert"]')).toBeNull()
    expect(root.textContent).not.toContain('容器操作失败')
  })
})
