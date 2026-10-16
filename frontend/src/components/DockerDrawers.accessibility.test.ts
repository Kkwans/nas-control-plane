// @vitest-environment jsdom
import { readFileSync } from 'node:fs'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, ref, type App } from 'vue'
import { compileStyle, parse } from 'vue/compiler-sfc'
import ProjectDetailDrawer from './ProjectDetailDrawer.vue'
import ContainerLogDrawer from './ContainerLogDrawer.vue'
import type { DockerProject } from '@/api/system'

const project: DockerProject = { id: 'demo', name: 'demo-project', kind: 'compose', workingDirectory: '/srv/demo', configFiles: [], containerCount: 0, runningCount: 0, state: 'running' }
let app: App | undefined
let host: HTMLDivElement | undefined
const detailsOpen = ref(true)
const updateDetails = vi.fn()
async function mount(state: DockerProject['state'] = 'running', pending = false) {
  detailsOpen.value = true
  host = document.createElement('div')
  document.body.append(host)
  app = createApp({ render: () => h('main', [
    h('h1', 'Docker 管理'),
    h(ProjectDetailDrawer, { modelValue: true, project: { ...project, state }, containers: [], hostName: 'nas', allowOperations: true, projectActionPending: pending ? 'restart' : null }),
    h(ContainerLogDrawer, { modelValue: detailsOpen.value, containerName: 'demo-web', loading: false, details: null, logs: { containerId: 'demo-web', collectedAt: '2026-10-15T12:00:00Z', tail: 1, entries: [{ timestamp: '2026-10-15T12:00:00Z', level: 'info', stream: 'stdout', message: 'ready' }] }, error: '', 'onUpdate:modelValue': updateDetails }),
  ]) })
  app.mount(host)
  await waitForDetailsOpen()
}
async function waitForDetailsOpen() {
  await vi.waitFor(() => {
    const drawer = document.querySelector('.container-detail-drawer')!
    const overlay = drawer.closest<HTMLElement>('.el-overlay')!
    expect(drawer.classList.contains('open')).toBe(true)
    expect(overlay.style.display).not.toBe('none')
    expect(overlay.classList.contains('el-drawer-fade-enter-active')).toBe(false)
    expect(drawer.contains(document.activeElement)).toBe(true)
  })
}
afterEach(() => { app?.unmount(); host?.remove(); app = undefined; host = undefined; updateDetails.mockClear(); vi.restoreAllMocks() })

function stylesheet(name: string, id: string) {
  const filename = `${import.meta.dirname}/${name}.vue`
  const { descriptor } = parse(readFileSync(filename, 'utf8'))
  const style = descriptor.styles.find(style => style.scoped)!
  const compiled = compileStyle({ filename, id, source: style.content, scoped: true })
  expect(compiled.errors).toEqual([])
  return compiled.rawResult!.root
}

describe('Docker 抽屉真实 Element Plus 标题合同', () => {
  it('传送到 body 的容器详情保留 dialog 标题但不创建页面 banner', async () => {
    await mount()
    const drawer = document.querySelector('.container-detail-drawer')!
    expect(drawer.closest('main')).toBeNull()
    expect(drawer.getAttribute('role')).toBe('dialog')
    expect(drawer.getAttribute('aria-modal')).toBe('true')
    const heading = drawer.querySelector('.el-drawer__header')!
    expect(heading.tagName).toBe('HEADER')
    const section = heading.closest('section')!
    expect(section.className).toBe('container-detail-dialogs')
    expect(section.parentElement).toBe(document.body)
    expect(section.hasAttribute('role')).toBe(false)
    expect(section.hasAttribute('aria-label')).toBe(false)
    expect(section.hasAttribute('aria-labelledby')).toBe(false)
    expect(heading.querySelector('h2')?.id).toBe(drawer.getAttribute('aria-labelledby'))
    // 内部日志 header 保留在 section 中；无 sectioning 祖先的原生 header 才会成为 banner。
    const banners = [...drawer.querySelectorAll('header')].filter(header => !header.parentElement?.closest('article, aside, main, nav, section'))
    expect(banners).toEqual([])
    expect(drawer.querySelector('[role="banner"]')).toBeNull()
    const close = drawer.querySelector<HTMLButtonElement>('.el-drawer__close-btn')!
    expect(close.tagName).toBe('BUTTON')
    expect(close.type).toBe('button')
    expect(close.getAttribute('aria-label')).toBeTruthy()
    expect(close.disabled).toBe(false)
    close.focus()
    expect(document.activeElement).toBe(close)
    close.click()
    // ElDrawer 在真实 Transition 的 afterLeave 中回传 false；固定延时不能证明两帧已执行。
    await vi.waitFor(() => {
      expect(drawer.closest<HTMLElement>('.el-overlay')!.style.display).toBe('none')
      expect(updateDetails.mock.calls).toEqual([[false]])
    })
  })

  it('关闭按钮等待真实退出动画完成后才回传一次 false', async () => {
    await mount()
    const drawer = document.querySelector('.container-detail-drawer')!
    const overlay = drawer.closest<HTMLElement>('.el-overlay')!
    const frames: FrameRequestCallback[] = []
    // 只控制帧调度，保留真实按钮、ElDrawer、Teleport 和 Vue Transition。
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation(callback => frames.push(callback))
    drawer.querySelector<HTMLButtonElement>('.el-drawer__close-btn')!.click()
    await nextTick()
    expect(drawer.classList.contains('open')).toBe(false)
    expect(overlay.classList.contains('el-drawer-fade-leave-active')).toBe(true)
    expect(overlay.style.display).not.toBe('none')
    expect(updateDetails.mock.calls).toEqual([])
    expect(frames).toHaveLength(1)
    frames.shift()!(performance.now())
    await nextTick()
    expect(updateDetails.mock.calls).toEqual([])
    expect(overlay.style.display).not.toBe('none')
    expect(frames).toHaveLength(1)
    frames.shift()!(performance.now())
    await nextTick()
    expect(frames).toHaveLength(0)
    expect(overlay.classList.contains('el-drawer-fade-leave-active')).toBe(false)
    expect(overlay.style.display).toBe('none')
    expect(updateDetails.mock.calls).toEqual([[false]])
  })

  it('真实 Teleport 在关闭、重开与卸载后不遗留可见遮罩或独立标题', async () => {
    await mount()
    const section = document.querySelector('.container-detail-dialogs')!
    const overlay = section.querySelector<HTMLElement>('.el-overlay')!
    const titleId = section.querySelector('h2')!.id
    expect(overlay.style.display).not.toBe('none')
    detailsOpen.value = false
    await vi.waitFor(() => {
      expect(overlay.style.display).toBe('none')
      expect(updateDetails.mock.calls).toEqual([[false]])
    })
    detailsOpen.value = true
    await waitForDetailsOpen()
    expect(overlay.style.display).not.toBe('none')
    expect(section.querySelector('h2')!.id).toBe(titleId)
    expect(document.querySelectorAll('.container-detail-dialogs')).toHaveLength(1)
    app!.unmount()
    app = undefined
    expect(document.querySelector('.container-detail-dialogs')).toBeNull()
    expect(document.getElementById(titleId)).toBeNull()
    expect(overlay.isConnected).toBe(false)
  })

  it('INFO 使用强信息色并满足普通文字 4.5:1 对比度', async () => {
    await mount()
    const info = document.querySelector('.container-detail-drawer .log-level--info')!
    const scope = [...info.attributes].find(attr => attr.name.startsWith('data-v-'))!.name
    const colors: Record<string, string> = {}
    stylesheet('ContainerLogDrawer', scope).walkRules(rule => {
      if (rule.selectors.some(selector => info.matches(selector))) rule.walkDecls(declaration => { colors[declaration.prop] = declaration.value })
    })
    expect(colors.color).toBe('var(--ncp-info-strong)')
    expect(colors.background).toBe('var(--ncp-info-soft)')
    const tokens = readFileSync(`${import.meta.dirname}/../styles/tokens.css`, 'utf8')
    function luminance(property: string) {
      const name = /var\((--[\w-]+)\)/.exec(colors[property]!)![1]!
      const hex = new RegExp(`${name}:\\s*(#[\\da-f]{6});`, 'i').exec(tokens)![1]!
      const channels = [1, 3, 5].map(start => {
        const value = Number.parseInt(hex.slice(start, start + 2), 16) / 255
        return value <= .04045 ? value / 12.92 : ((value + .055) / 1.055) ** 2.4
      })
      return channels[0]! * .2126 + channels[1]! * .7152 + channels[2]! * .0722
    }
    expect((luminance('background') + .05) / (luminance('color') + .05)).toBeGreaterThanOrEqual(4.5)
  })

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
