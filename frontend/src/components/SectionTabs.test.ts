// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, ref, type App } from 'vue'
import { compileStyle, parse } from 'vue/compiler-sfc'
import SectionTabs, { type SectionTab } from './SectionTabs.vue'
import dockerImagePanelSource from './DockerImagePanel.vue?raw'

const mounted: { app: App; host: HTMLDivElement }[] = []
const tabs: SectionTab[] = [
  { value: 'local', label: '本地镜像', count: 2 },
  { value: 'disabled', label: '不可用', disabled: true },
  { value: 'hub', label: '镜像仓库' },
  { value: 'downloads', label: '下载任务', count: 0 },
]

function mountTabs(accessibleLabel?: string, items = tabs) {
  const value = ref('local')
  const update = vi.fn((next: string) => { value.value = next })
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render: () => h(SectionTabs, {
      modelValue: value.value,
      tabs: items,
      accessibleLabel,
      'onUpdate:modelValue': update,
    }),
  })
  app.mount(host)
  mounted.push({ app, host })
  return { host, value, update, buttons: [...host.querySelectorAll('button')] }
}

async function press(button: HTMLButtonElement, key: string) {
  const event = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true })
  button.dispatchEvent(event)
  await nextTick()
  return event
}

afterEach(() => {
  for (const { app, host } of mounted.splice(0)) {
    app.unmount()
    host.remove()
  }
})

describe('SectionTabs 分段按钮组', () => {
  it.each(['Docker 管理视图', '镜像来源', '系统信息分类'])('使用允许 group 角色的原生容器并保留名称：%s', (label) => {
    const { host, buttons } = mountTabs(label)
    const group = host.querySelector('[role="group"]')!
    expect(group.tagName).toBe('DIV')
    expect(group.getAttribute('aria-label')).toBe(label)
    expect(host.querySelector('nav[role="group"]')).toBeNull()
    expect(host.querySelector('[role="tablist"], [role="tab"]')).toBeNull()
    expect(group.querySelectorAll('button')).toHaveLength(tabs.length)
    expect(buttons.map((button) => button.type)).toEqual(tabs.map(() => 'button'))
    expect(buttons.map((button) => button.getAttribute('aria-pressed'))).toEqual(['true', 'false', 'false', 'false'])
    expect(buttons.map((button) => button.tabIndex)).toEqual([0, -1, -1, -1])
    expect(buttons[0]!.textContent).toBe('本地镜像2')
    expect(buttons[3]!.textContent).toBe('下载任务0')
  })

  it('保留默认组名称与点击后的按下状态', async () => {
    const { host, buttons, value, update } = mountTabs()
    expect(host.querySelector('[role="group"]')?.getAttribute('aria-label')).toBe('分区标签')
    buttons[2]!.click()
    await nextTick()
    expect(update).toHaveBeenCalledExactlyOnceWith('hub')
    expect(value.value).toBe('hub')
    expect(buttons[0]!.getAttribute('aria-pressed')).toBe('false')
    expect(buttons[0]!.tabIndex).toBe(-1)
    expect(buttons[2]!.getAttribute('aria-pressed')).toBe('true')
    expect(buttons[2]!.tabIndex).toBe(0)
    expect(buttons[2]!.classList.contains('active')).toBe(true)
  })

  it('镜像来源的编译后作用域与窄屏深层选择器匹配真实子组件结构', () => {
    const scopeId = 'data-v-image-test'
    const icon = { render: () => h('svg') }
    const parent = {
      __scopeId: scopeId,
      render: () => h('header', { class: 'image-modebar' }, [h(SectionTabs, {
        modelValue: 'local', tabs: tabs.map((tab) => ({ ...tab, icon })), accessibleLabel: '镜像来源',
      })]),
    }
    const host = document.createElement('div')
    document.body.append(host)
    const app = createApp(parent)
    app.mount(host)
    mounted.push({ app, host })

    const group = host.querySelector('.section-tabs')!
    expect(group.hasAttribute(scopeId)).toBe(true)
    // Vue 仅将父组件 scope 传给子组件根节点；普通后代选择器曾在这里静默失效。
    for (const element of group.querySelectorAll('button, button>span, button>small, button>svg')) {
      expect(element.hasAttribute(scopeId)).toBe(false)
    }
    const { descriptor } = parse(dockerImagePanelSource, { filename: 'DockerImagePanel.vue' })
    const styles = descriptor.styles.map((style) => compileStyle({ filename: 'DockerImagePanel.vue', id: scopeId, source: style.content, scoped: true }))
    expect(styles.flatMap((style) => style.errors)).toEqual([])
    const selectors = new Set<string>()
    const responsiveButtons: Record<string, string> = {}
    for (const style of styles) {
      style.rawResult!.root.walkRules((rule) => {
        for (const selector of rule.selectors) selectors.add(selector)
        const container = rule.parent
        if (container?.type === 'atrule' && container.name === 'container'
          && container.params === 'image-workspace (max-width:980px) or (max-width:61.25rem)'
          && rule.selectors.includes(`.image-modebar[${scopeId}] .section-tabs button`)) {
          rule.walkDecls((declaration) => { responsiveButtons[declaration.prop] = declaration.value })
        }
      })
    }
    const required = [
      [`.image-modebar .section-tabs[${scopeId}]`, 1],
      [`.image-modebar[${scopeId}] .section-tabs button`, tabs.length],
      [`.image-modebar[${scopeId}] .section-tabs button>span`, tabs.length],
      [`.image-modebar[${scopeId}] .section-tabs button>small`, 2],
      [`.image-modebar[${scopeId}] .section-tabs button>svg`, tabs.length],
    ] as const
    for (const [selector, count] of required) {
      expect(selectors.has(selector), `缺少实际编译选择器：${selector}`).toBe(true)
      expect(host.querySelectorAll(selector), `选择器必须命中真实子组件：${selector}`).toHaveLength(count)
    }
    expect(responsiveButtons).toMatchObject({ flex: '1 1 auto', 'min-height': '44px' })
    expect(dockerImagePanelSource).not.toContain('.image-modebar nav')
  })

  it.each(['ArrowRight', 'ArrowDown'])('%s 跳过禁用按钮并循环到首项', async (key) => {
    const { buttons, value, update } = mountTabs()
    buttons[0]!.focus()
    for (const [from, to] of [[0, 2], [2, 3], [3, 0]]) {
      expect((await press(buttons[from!]!, key)).defaultPrevented).toBe(true)
      expect(document.activeElement).toBe(buttons[to!]!)
      expect(value.value).toBe(tabs[to!]!.value)
      expect(buttons[to!]!.getAttribute('aria-pressed')).toBe('true')
      expect(buttons[to!]!.tabIndex).toBe(0)
    }
    expect(update).toHaveBeenCalledTimes(3)
  })

  it.each(['ArrowLeft', 'ArrowUp'])('%s 循环到末项并跳过禁用按钮', async (key) => {
    const { buttons, value } = mountTabs()
    buttons[0]!.focus()
    for (const [from, to] of [[0, 3], [3, 2], [2, 0]]) {
      expect((await press(buttons[from!]!, key)).defaultPrevented).toBe(true)
      expect(document.activeElement).toBe(buttons[to!]!)
      expect(value.value).toBe(tabs[to!]!.value)
    }
  })

  it('Home 与 End 聚焦并选择首末可用项，忽略两端禁用项', async () => {
    const { buttons, value } = mountTabs(undefined, [
      { value: 'first-disabled', label: '首项禁用', disabled: true },
      ...tabs,
      { value: 'last-disabled', label: '末项禁用', disabled: true },
    ])
    buttons[1]!.focus()
    expect((await press(buttons[1]!, 'End')).defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(buttons[4])
    expect(value.value).toBe('downloads')
    expect((await press(buttons[4]!, 'Home')).defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(buttons[1])
    expect(value.value).toBe('local')
  })

  it('禁用按钮不响应点击或键盘，不截获普通按键', async () => {
    const { buttons, value, update } = mountTabs()
    expect(buttons[1]!.disabled).toBe(true)
    buttons[1]!.click()
    expect((await press(buttons[1]!, 'ArrowRight')).defaultPrevented).toBe(false)
    buttons[0]!.focus()
    expect((await press(buttons[0]!, 'Tab')).defaultPrevented).toBe(false)
    expect(document.activeElement).toBe(buttons[0])
    expect(value.value).toBe('local')
    expect(update).not.toHaveBeenCalled()
  })

  it('全部禁用时不选择也不移动焦点', async () => {
    const { buttons, update } = mountTabs(undefined, tabs.map((tab) => ({ ...tab, disabled: true })))
    const previousFocus = document.activeElement
    for (const button of buttons) {
      button.click()
      expect((await press(button, 'Home')).defaultPrevented).toBe(false)
    }
    expect(document.activeElement).toBe(previousFocus)
    expect(update).not.toHaveBeenCalled()
  })
})
