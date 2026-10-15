// @vitest-environment jsdom
import { readFileSync } from 'node:fs'
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, type App } from 'vue'
import { compileStyle, parse } from 'vue/compiler-sfc'
import ProjectDetailDrawer from './ProjectDetailDrawer.vue'
import ContainerLogDrawer from './ContainerLogDrawer.vue'
import type { DockerProject } from '@/api/system'

const project: DockerProject = { id: 'demo', name: 'demo-project', kind: 'compose', workingDirectory: '/srv/demo', configFiles: [], containerCount: 0, runningCount: 0, state: 'running' }
let app: App | undefined
let host: HTMLDivElement | undefined
async function mount(state: DockerProject['state'] = 'running', pending = false) {
  host = document.createElement('div')
  document.body.append(host)
  app = createApp({ render: () => h('main', [
    h('h1', 'Docker 管理'),
    h(ProjectDetailDrawer, { modelValue: true, project: { ...project, state }, containers: [], hostName: 'nas', allowOperations: true, projectActionPending: pending ? 'restart' : null }),
    h(ContainerLogDrawer, { modelValue: true, containerName: 'demo-web', loading: false, details: null, logs: null, error: '' }),
  ]) })
  app.mount(host)
  for (let index = 0; index < 8; index++) await nextTick()
  await new Promise(resolve => setTimeout(resolve, 50))
  await nextTick()
}
afterEach(() => { app?.unmount(); host?.remove(); app = undefined; host = undefined })

function stylesheet(name: string, id: string) {
  const filename = `${import.meta.dirname}/${name}.vue`
  const { descriptor } = parse(readFileSync(filename, 'utf8'))
  const style = descriptor.styles.find(style => style.scoped)!
  const compiled = compileStyle({ filename, id, source: style.content, scoped: true })
  expect(compiled.errors).toEqual([])
  return compiled.rawResult!.root
}

describe('Docker 抽屉真实 Element Plus 标题合同', () => {
  it.each([
    ['running', false, '运行中'], ['degraded', false, '需关注'], ['stopped', false, '已停止'], ['running', true, '正在重启'],
  ] as const)('%s pending=%s 保留命名、层级和状态', async (state, pending, label) => {
    await mount(state, pending)
    const titles = [] as string[]
    for (const [selector, name] of [['.project-drawer', 'demo-project'], ['.container-detail-drawer', 'demo-web']]) {
      const drawer = document.querySelector(selector!)!
      expect(drawer.getAttribute('role')).toBe('dialog')
      const id = drawer.getAttribute('aria-labelledby')!
      const title = document.getElementById(id)!
      expect(id).toBeTruthy()
      expect(title?.tagName).toBe('H2')
      expect(title?.textContent).toBe(name)
      expect(drawer.contains(title)).toBe(true)
      expect(title?.closest('[aria-hidden="true"], [hidden]')).toBeNull()
      titles.push(id)
    }
    expect(new Set(titles).size).toBe(2)
    expect([...document.querySelectorAll('.project-drawer h2, .project-drawer h3')].map(node => [node.tagName, node.textContent])).toEqual([
      ['H2', 'demo-project'], ['H3', '项目操作'], ['H3', '访问入口'], ['H3', '容器'],
    ])
    expect(document.querySelector('.project-drawer [role="status"]')?.textContent).toBe(label)
  })

  it('编译后的标题副文本颜色不匹配 StatusPill 根节点及其后代', async () => {
    await mount()
    const drawer = document.querySelector('.project-drawer')!
    const pill = drawer.querySelector('.status-pill')!
    // Vue 会把父 scope ID 加到子组件根节点；保留真实 DOM 验证此边界。
    const parentScope = [...drawer.querySelector('.drawer-title')!.attributes].find(attr => attr.name.startsWith('data-v-'))!.name
    expect(pill.hasAttribute(parentScope)).toBe(true)
    const sheet = stylesheet('ProjectDetailDrawer', parentScope)
    const subtitle = drawer.querySelector('.drawer-title>div>span')!
    let subtitleColor = ''
    sheet.walkRules(rule => {
      rule.walkDecls('color', declaration => {
        for (const selector of rule.selectors) {
          if (subtitle.matches(selector)) subtitleColor = declaration.value
          for (const node of [pill, ...pill.querySelectorAll('*')]) expect(node.matches(selector), selector).toBe(false)
        }
      })
    })
    expect(subtitleColor).toBe('var(--ncp-text-subtle)')
    const pillSheet = stylesheet('StatusPill', [...pill.attributes].find(attr => attr.name.startsWith('data-v-') && attr.name !== parentScope)!.name)
    let healthyColor = ''
    pillSheet.walkRules(rule => {
      if (rule.selectors.some(selector => pill.matches(selector))) rule.walkDecls('color', declaration => { healthyColor = declaration.value })
    })
    expect(healthyColor).toBe('var(--ncp-success-strong)')
  })
})
