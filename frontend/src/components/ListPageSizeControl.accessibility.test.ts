// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick, ref, type App } from 'vue'
import ListPageSizeControl from './ListPageSizeControl.vue'

vi.mock('@/composables/useListPreference', () => ({
  useListPreference: () => ({ pageSize: ref(10), saving: ref(false), error: ref(''), setPageSize: vi.fn() }),
}))

let app: App | undefined
let host: HTMLElement | undefined
async function settle() {
  for (let count = 0; count < 20; count++) await nextTick()
  await new Promise((resolve) => setTimeout(resolve, 30))
  for (let count = 0; count < 20; count++) await nextTick()
}
async function mount(markup: string) {
  host = document.createElement('div')
  host.innerHTML = markup
  document.body.append(host)
  app = createApp(ListPageSizeControl, { listKey: 'containers' })
  app.mount(host.querySelector('[data-mount]')!)
  await settle()
  return host.querySelector<HTMLElement>('.page-size-control')!
}
function help(control: HTMLElement) {
  return document.getElementById(control.getAttribute('aria-describedby')!)!
}
afterEach(async () => {
  app?.unmount()
  app = undefined
  host?.remove()
  host = undefined
  await settle()
})

// 使用真实 Element Plus 浮层验证 DOM 归属与交互；像素遮挡和 axe 由浏览器 fixture 验收。
describe('分页帮助的语义容器', () => {
  it.each(['<main>', '<section role="main">'])('悬停帮助挂在最近的主内容 %s 内，卸载移除浮层', async (opening) => {
    const closing = opening === '<main>' ? '</main>' : '</section>'
    const control = await mount(`${opening}<div data-mount></div>${closing}`)
    control.dispatchEvent(new MouseEvent('mouseenter'))
    await settle()
    const tooltip = help(control)
    expect(tooltip).not.toBeNull()
    expect(tooltip.textContent).toContain('该设置只作用于当前列表，并会保存到账号偏好中。')
    expect(tooltip.closest('main, [role="main"]')).toBe(host!.firstElementChild)
    expect(tooltip.getAttribute('aria-hidden')).not.toBe('true')
    expect(tooltip.style.display).not.toBe('none')
    const tooltipId = tooltip.id
    app!.unmount()
    app = undefined
    await settle()
    expect(document.getElementById(tooltipId)).toBeNull()
  })

  it.each(['dialog', 'alertdialog'])('嵌套 %s 中的帮助不逃逸到 main', async (role) => {
    const control = await mount(`<main><section role="${role}" aria-label="列表"><div data-mount></div></section></main>`)
    control.dispatchEvent(new MouseEvent('mouseenter'))
    await settle()
    expect(help(control).closest(`[role="${role}"]`)).toBe(host!.querySelector(`[role="${role}"]`))
    expect(help(control).getAttribute('aria-hidden')).not.toBe('true')
  })

  it('原生 dialog 优先于外层 main', async () => {
    const control = await mount('<main><dialog open><div data-mount></div></dialog></main>')
    control.dispatchEvent(new MouseEvent('mouseenter'))
    await settle()
    expect(help(control).closest('dialog')).toBe(host!.querySelector('dialog'))
  })

  it('无语义祖先时仍保留局部帮助和选择器键盘操作', async () => {
    const control = await mount('<div data-mount></div>')
    control.dispatchEvent(new MouseEvent('mouseenter'))
    await settle()
    expect(control.contains(help(control))).toBe(true)
    const input = control.querySelector<HTMLInputElement>('input[role="combobox"]')!
    input.focus()
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', bubbles: true }))
    await settle()
    expect(input.getAttribute('aria-expanded')).toBe('true')
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', code: 'Escape', bubbles: true }))
    await settle()
    expect(input.getAttribute('aria-expanded')).toBe('false')
  })
})
