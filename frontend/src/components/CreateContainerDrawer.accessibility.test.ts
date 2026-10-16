// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, ref, type App } from 'vue'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import CreateContainerDrawer from './CreateContainerDrawer.vue'
import drawerSource from './CreateContainerDrawer.vue?raw'
import { readFileSync } from 'node:fs'
const tokens = readFileSync('src/styles/tokens.css', 'utf8')

// 真实 Element Plus DOM 与交互；只替换 fetch，不启动浏览器或服务。
// JSDOM 不验证像素布局/浏览器对比度，完整 axe 仍由 CI 执行。
const fetcher = vi.fn<typeof fetch>()
let app: App | undefined
let host: HTMLElement
let styles: HTMLStyleElement
const drawerOpen = ref(true)
async function settle() { for (let i = 0; i < 30; i++) await nextTick() }
function button(text: string) {
  const result = [...document.querySelectorAll<HTMLButtonElement>('button')].find((element) => element.textContent?.trim() === text)
  expect(result, text).toBeDefined()
  return result!
}
async function click(text: string) { const target = button(text); target.focus(); target.click(); await settle() }

beforeEach(async () => {
  drawerOpen.value = true
  beginSession()
  fetcher.mockReset()
  const collectedAt = '2026-10-14T00:00:00Z'
  fetcher.mockImplementation(async (path, init) => {
    expect(init?.method).toBeUndefined()
    let body: unknown
    if (path === '/api/v1/docker/resources') body = { collectedAt, networks: [], volumes: [] }
    else if (path === '/api/v1/docker/inventory') body = { collectedAt, engine: { serverVersion: 'fixture', operatingSystem: 'fixture', architecture: 'fixture', containers: 0, containersRunning: 0, containersStopped: 0, images: 1 }, containers: [], projects: [] }
    else if (String(path).startsWith('/api/v1/files/entries')) body = { path: '/', parent: '/', entries: [], nextCursor: '' }
    else throw new Error(`非预期请求：${String(path)}`)
    return new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } })
  })
  vi.stubGlobal('fetch', fetcher)
  host = document.createElement('div')
  document.body.append(host)
  styles = document.createElement('style')
  // 用真实 token 展开声明进行样式合同检查，不声称替代浏览器计算样式。
  const values = new Map([...tokens.matchAll(/(--ncp-[\w-]+):\s*([^;]+);/g)].map((match) => [match[1], match[2]]))
  styles.textContent = [...drawerSource.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((match) => match[1]).join('\n').replace(/var\((--ncp-[\w-]+)\)/g, (original, name: string) => values.get(name) || original)
  document.head.append(styles)
  app = createApp({ render: () => h('div', [h('header', { class: 'fixture-topbar' }, '控制台'), h(CreateContainerDrawer, { modelValue: drawerOpen.value, image: { id: 'alpine', repoTags: ['alpine:latest'], repoDigests: [], sizeBytes: 1, createdAt: collectedAt, containers: 0 } })]) })
  app.mount(host)
  await settle()
})
afterEach(async () => {
  app?.unmount()
  host.remove()
  styles.remove()
  invalidateSession()
  await settle()
  vi.unstubAllGlobals()
  expect(document.querySelector('.create-container-dialogs')).toBeNull()
})

describe('创建容器无障碍 DOM', () => {
  it.each(['仅创建', '创建并启动'])('%s 的发起按钮在浏览器禁用失焦后仍能在取消时恢复，并支持再次打开', async action => {
    for (const method of ['escape', 'close', 'back']) {
      const opener = button(action)
      opener.focus()
      opener.click()
      // JSDOM 不会像浏览器一样在按钮禁用后失焦；在焦点陷阱异步记录前模拟此行为。
      opener.blur()
      await settle()
      expect(opener.disabled).toBe(true)
      const dialog = document.querySelector<HTMLElement>('.create-confirmation-dialog')!
      await vi.waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true))
      if (method === 'escape') document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', code: 'Escape', bubbles: true }))
      else if (method === 'close') dialog.querySelector<HTMLButtonElement>('.el-dialog__headerbtn')!.click()
      else button('返回修改').click()
      await settle()
      await vi.waitFor(() => expect(document.activeElement).toBe(opener))
      expect(opener.disabled).toBe(false)
      expect((dialog.closest('.el-overlay') as HTMLElement).style.display).toBe('none')
    }
    expect(fetcher.mock.calls.every(([, init]) => !init?.method)).toBe(true)
  })

  it.each(['draft', 'session', 'drawer'])('%s 改变后不再主动恢复旧确认发起者焦点', async change => {
    const opener = button('创建并启动')
    opener.focus()
    opener.click()
    opener.blur()
    await settle()
    const dialog = document.querySelector<HTMLElement>('.create-confirmation-dialog')!
    await vi.waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true))
    const focus = vi.spyOn(opener, 'focus')
    if (change === 'draft') {
      const name = document.querySelector<HTMLInputElement>('input[placeholder="例如 mysql-1"]')!
      name.value = 'changed-draft'
      name.dispatchEvent(new Event('input', { bubbles: true }))
    } else if (change === 'session') invalidateSession()
    else drawerOpen.value = false
    await settle()
    await vi.waitFor(() => expect((dialog.closest('.el-overlay') as HTMLElement).style.display).toBe('none'))
    expect(focus).not.toHaveBeenCalledWith({ preventScroll: true })
    focus.mockRestore()
  })

  it.each([
    ['添加环境变量', 'env', '环境变量'],
    ['添加挂载', 'mount', '挂载'],
    ['添加端口映射', 'port', '端口映射'],
    ['添加设备', 'device', '设备'],
  ])('%s 的删除按钮有明确行号、支持移除并重新编号', async (add, kind, label) => {
    document.querySelector<HTMLButtonElement>('.advanced-toggle')!.click()
    await settle()
    await click(add)
    await click(add)
    const rows = () => [...document.querySelectorAll(`.repeat-row--${kind}`)]
    const names = () => rows().map((row) => row.querySelector('button.row-remove')?.getAttribute('aria-label'))
    expect(names()).toEqual([`删除第 1 个${label}`, `删除第 2 个${label}`])
    const survivingInput = rows()[1]!.querySelector<HTMLInputElement>('input.el-input__inner')!
    survivingInput.value = 'remaining'
    survivingInput.dispatchEvent(new Event('input', { bubbles: true }))
    await settle()
    rows()[0]!.querySelector<HTMLButtonElement>('.row-remove')!.click()
    await settle()
    expect(names()).toEqual([`删除第 1 个${label}`])
    expect(rows()[0]!.querySelector<HTMLInputElement>('input.el-input__inner')?.value).toBe('remaining')
    expect(fetcher.mock.calls.every(([, init]) => !init?.method)).toBe(true)
  })

  it('高级开关明确使用白色控件背景，展开/收起状态保持可读', async () => {
    const toggle = document.querySelector<HTMLButtonElement>('.advanced-toggle')!
    const rule = [...styles.sheet!.cssRules].find((entry) => entry instanceof CSSStyleRule && entry.selectorText === '.advanced-toggle') as CSSStyleRule
    expect(toggle.matches(rule.selectorText)).toBe(true)
    expect(rule.style.background).toBe('rgb(255, 255, 255)')
    expect(tokens).toContain('--ncp-control-surface: #ffffff;')
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    toggle.click()
    await settle()
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    expect(document.querySelector('.advanced-body')).not.toBeNull()
    toggle.click()
    await settle()
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    expect(document.querySelector('.advanced-body')).toBeNull()
  })

  it('确认配置独立滚动且保留标题、全部字段与外部操作区', async () => {
    await click('仅创建')
    const dialog = document.querySelector<HTMLElement>('.create-confirmation-dialog')!
    const header = dialog.querySelector<HTMLElement>('.el-dialog__header')!
    const body = dialog.querySelector<HTMLElement>('.el-dialog__body')!
    const footer = dialog.querySelector<HTMLElement>('.el-dialog__footer')!
    const region = body.querySelector<HTMLElement>('[role="region"][aria-label="容器配置详情"]')!
    expect(region.tabIndex).toBe(0)
    region.focus()
    expect(document.activeElement).toBe(region)
    expect(region.contains(header)).toBe(false)
    expect(region.contains(footer)).toBe(false)
    expect([...region.querySelectorAll('dt')].map((node) => node.textContent)).toEqual([
      '镜像', '容器名称', 'CPU 限制', '内存限制', '重启策略', '挂载', '网络', '端口', '启动参数', '设备映射', '特权模式',
      '增加 Capabilities', '移除 Capabilities', '硬件请求', 'GPU 配置',
    ])
    expect(footer.contains(button('返回修改'))).toBe(true)
    expect(footer.contains(button('确认创建'))).toBe(true)
    expect(dialog.style.getPropertyValue('--el-dialog-margin-top')).toBe('16px')

    // JSDOM 只验证布局声明合同；390px/桌面实际边界及滚动由 CI 浏览器检查。
    function rule(selector: string) {
      const found = [...styles.sheet!.cssRules].find((entry) => entry instanceof CSSStyleRule && entry.selectorText === selector) as CSSStyleRule
      expect(found, selector).toBeDefined()
      return found.style
    }
    const modal = rule('.create-confirmation-dialog.el-dialog')
    expect(modal.display).toBe('flex')
    expect(modal.flexDirection).toBe('column')
    expect(modal.maxHeight).toBe('calc(100dvh - 32px)')
    expect(drawerSource).toContain('max-height:calc(100vh - 32px);max-height:calc(100dvh - 32px)')
    expect(modal.boxSizing).toBe('border-box')
    expect(modal.marginBottom).toBe('16px')
    expect(rule('.create-confirmation-dialog .el-dialog__header,.create-confirmation-dialog .el-dialog__footer').flex).toBe('0 0 auto')
    const bodyStyle = rule('.create-confirmation-dialog .el-dialog__body')
    expect(bodyStyle.display).toBe('flex')
    expect(bodyStyle.minHeight).toBe('0px')
    expect(bodyStyle.overflow).toBe('hidden')
    const scrollStyle = rule('.create-confirmation-dialog .create-preview')
    expect(scrollStyle.gridAutoRows).toBe('max-content')
    expect(scrollStyle.minHeight).toBe('0px')
    expect(scrollStyle.overflow).toBe('auto')
    expect(scrollStyle.getPropertyValue('overscroll-behavior')).toBe('contain')
    expect(rule('.create-confirmation-dialog .create-preview:focus-visible').outlineOffset).toBe('-2px')
    expect(rule('.create-confirmation-dialog .dialog-actions').flexWrap).toBe('wrap')
    await click('返回修改')
    expect(fetcher.mock.calls.every(([, init]) => !init?.method)).toBe(true)
  })

  it.each([
    ['确认容器配置', 'button'], ['确认容器配置', 'escape'],
    ['选择 NAS 路径', 'button'], ['选择 NAS 路径', 'escape'],
  ])('%s 保留局部 header、焦点与模态；通过 %s 关闭', async (title, closeMethod) => {
    if (title === '选择 NAS 路径') await click('添加挂载')
    const opener = button(title === '选择 NAS 路径' ? '浏览' : '仅创建')
    opener.focus()
    opener.click()
    await settle()
    const dialog = document.querySelector<HTMLElement>(`[role="dialog"][aria-label="${title}"]`)!
    expect(dialog).not.toBeNull()
    expect(dialog.getAttribute('aria-modal')).toBe('true')
    await vi.waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true))
    const header = dialog.querySelector('header.el-dialog__header')!
    expect(header.textContent).toContain(title)
    const section = header.closest('section')!
    expect(section.classList.contains('create-container-dialogs')).toBe(true)
    expect(section.parentElement).toBe(document.body)
    expect(dialog.closest('[inert], [aria-hidden="true"]')).toBeNull()
    const drawerOverlay = document.querySelector('.create-container-drawer')!.closest<HTMLElement>('.el-overlay')!
    expect(Number((dialog.closest('.el-overlay') as HTMLElement).style.zIndex)).toBeGreaterThan(Number(drawerOverlay.style.zIndex))
    expect(header.getAttribute('aria-hidden')).toBeNull()
    expect(dialog.querySelector('[role="heading"]')?.textContent).toBe(title)
    expect(document.querySelector('.fixture-topbar')).not.toBeNull()
    const close = header.querySelector<HTMLButtonElement>('button')!
    expect(close.getAttribute('aria-label')).toBeTruthy()
    const enabledButtons = [...dialog.querySelectorAll<HTMLButtonElement>('button:not(:disabled)')]
    const lastButton = enabledButtons.at(-1)!
    lastButton.focus()
    const tab = new KeyboardEvent('keydown', { key: 'Tab', code: 'Tab', bubbles: true, cancelable: true })
    lastButton.dispatchEvent(tab)
    await settle()
    expect(tab.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(close)
    const shiftTab = new KeyboardEvent('keydown', { key: 'Tab', code: 'Tab', shiftKey: true, bubbles: true, cancelable: true })
    close.dispatchEvent(shiftTab)
    await settle()
    expect(shiftTab.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(lastButton)
    if (closeMethod === 'button') close.click()
    else document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', code: 'Escape', bubbles: true }))
    await settle()
    await vi.waitFor(() => expect((dialog.closest('.el-overlay') as HTMLElement).style.display).toBe('none'))
    await vi.waitFor(() => expect(document.activeElement).toBe(opener))
    expect(fetcher.mock.calls.every(([, init]) => !init?.method)).toBe(true)
  })
})
