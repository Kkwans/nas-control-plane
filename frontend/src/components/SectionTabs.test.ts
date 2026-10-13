// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, ref, type App } from 'vue'
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

  it('镜像来源的现有与窄屏样式选择器仍匹配实际分段按钮', () => {
    const { host } = mountTabs('镜像来源')
    host.className = 'image-modebar'
    const selectors = [...dockerImagePanelSource.matchAll(/(\.image-modebar \.section-tabs[^{}]*)\{/g)].map((match) => match[1]!)
    expect(selectors).toEqual([
      '.image-modebar .section-tabs',
      '.image-modebar .section-tabs button',
      '.image-modebar .section-tabs button.active',
      '.image-modebar .section-tabs span',
      '.image-modebar .section-tabs',
      '.image-modebar .section-tabs button',
    ])
    for (const selector of selectors) expect(host.querySelector(selector)).not.toBeNull()
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
