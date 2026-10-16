// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, ref, type App } from 'vue'
import SegmentedTabs from './SegmentedTabs.vue'
const mounted: { app: App; host: HTMLDivElement }[] = []
const tabs = [
  { value: 'running', label: '运行中', count: 2, icon: { render: () => h('svg') } },
  { value: 'disabled', label: '不可用', disabled: true },
  { value: 'all', label: '全部站点', count: 3 },
  { value: 'hidden', label: '已隐藏', count: 0 },
]
function mountTabs(mode?: 'tabs' | 'filter', disabled = false) {
  const value = ref('running')
  const update = vi.fn((next: string) => { value.value = next })
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({ render: () => h(SegmentedTabs, {
    modelValue: value.value, tabs, mode, disabled, panelId: 'test-panel', accessibleLabel: '站点状态筛选',
    'onUpdate:modelValue': update,
  }) })
  app.mount(host); mounted.push({ app, host })
  return { host, value, update, buttons: [...host.querySelectorAll('button')] }
}
async function press(button: HTMLButtonElement, key: string) {
  const event = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true })
  button.dispatchEvent(event); await nextTick(); return event
}
afterEach(() => { for (const { app, host } of mounted.splice(0)) { app.unmount(); host.remove() } })
describe('SegmentedTabs 显式筛选语义', () => {
  it('默认保持标签页角色、选中状态和面板关联', () => {
    const { host, buttons } = mountTabs()
    expect(host.querySelector('[role="tablist"]')?.getAttribute('aria-label')).toBe('站点状态筛选')
    expect(buttons.map((button) => button.getAttribute('role'))).toEqual(tabs.map(() => 'tab'))
    expect(buttons.map((button) => button.getAttribute('aria-selected'))).toEqual(['true', 'false', 'false', 'false'])
    expect(buttons.every((button) => button.getAttribute('aria-controls') === 'test-panel')).toBe(true)
    expect(host.querySelector('[aria-checked]')).toBeNull()
  })
  it('筛选使用单选组，保留文字、计数、图标和点击选择，不声明面板关联', async () => {
    const { host, buttons, value } = mountTabs('filter')
    expect(host.querySelector('[role="radiogroup"]')?.getAttribute('aria-label')).toBe('站点状态筛选')
    expect(host.querySelector('[role="tablist"], [role="tab"], [aria-selected], [aria-controls]')).toBeNull()
    expect(buttons.map((button) => button.getAttribute('role'))).toEqual(tabs.map(() => 'radio'))
    expect(buttons.map((button) => button.getAttribute('aria-checked'))).toEqual(['true', 'false', 'false', 'false'])
    expect(buttons.map((button) => button.tabIndex)).toEqual([0, -1, -1, -1])
    expect(buttons[0]!.textContent).toBe('运行中2')
    expect(buttons[0]!.querySelector('svg')?.getAttribute('aria-hidden')).toBe('true')
    expect(buttons[3]!.querySelector('.segmented-tabs__count')?.textContent).toBe('0')
    buttons[2]!.click(); await nextTick()
    expect(value.value).toBe('all')
    expect(buttons[2]!.getAttribute('aria-checked')).toBe('true')
    expect(buttons[2]!.classList.contains('is-active')).toBe(true)
    expect(buttons[0]!.getAttribute('aria-checked')).toBe('false')
  })
  it.each(['tabs', 'filter'] as const)('%s 方向键循环且跳过禁用项，Home/End 选首末项', async (mode) => {
    const { buttons, value, update } = mountTabs(mode)
    buttons[1]!.click(); expect(update).not.toHaveBeenCalled()
    for (const [from, key, to] of [[0, 'ArrowRight', 2], [2, 'ArrowDown', 3], [3, 'ArrowRight', 0], [0, 'ArrowLeft', 3], [3, 'ArrowUp', 2], [2, 'ArrowLeft', 0], [0, 'End', 3], [3, 'Home', 0]] as const) {
      expect((await press(buttons[from]!, key)).defaultPrevented).toBe(true)
      expect(document.activeElement).toBe(buttons[to])
      expect(value.value).toBe(tabs[to]!.value)
      expect(buttons[to]!.tabIndex).toBe(0)
      expect(buttons[to]!.getAttribute(mode === 'filter' ? 'aria-checked' : 'aria-selected')).toBe('true')
    }
    expect((await press(buttons[0]!, 'Tab')).defaultPrevented).toBe(false)
  })
  it.each(['tabs', 'filter'] as const)('%s 整组禁用不更新值或焦点', async (mode) => {
    const { buttons, update } = mountTabs(mode, true)
    const focus = document.activeElement
    for (const button of buttons) {
      expect(button.disabled).toBe(true); button.click()
      expect((await press(button, 'Home')).defaultPrevented).toBe(false)
    }
    expect(document.activeElement).toBe(focus); expect(update).not.toHaveBeenCalled()
  })
})
