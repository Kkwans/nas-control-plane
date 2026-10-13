// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, ref, type App, type VNodeChild } from 'vue'
import SectionDialog from './SectionDialog.vue'

let app: App | undefined
let host: HTMLDivElement
async function flush() {
  for (let index = 0; index < 8; index++) await nextTick()
  await new Promise(resolve => setTimeout(resolve, 50))
  await nextTick()
}
async function mount(attrs: Record<string, unknown> = {}, slots: Record<string, (scope: { titleId: string; titleClass: string; close: () => void }) => VNodeChild> = {}) {
  host = document.createElement('div')
  document.body.append(host)
  const open = ref(true), pending = ref(false)
  app = createApp({ render: () => h(SectionDialog, {
    modelValue: open.value,
    'onUpdate:modelValue': (value: boolean) => { open.value = value },
    title: '编辑 DNS 服务器',
    destroyOnClose: true,
    showClose: !pending.value,
    closeOnPressEscape: !pending.value,
    closeOnClickModal: !pending.value,
    transition: { name: 'dialog-fade', css: false },
    ...attrs,
  }, {
    default: () => h('input', { 'aria-label': 'DNS 服务器' }),
    footer: () => h('button', '预览修改'),
    ...slots,
  }) })
  app.mount(host)
  await flush()
  return { open, pending }
}
afterEach(() => { app?.unmount(); app = undefined; host?.remove() })

describe('SectionDialog 原生局部语义', () => {
  it('真实 Element Plus 保留命名和标题，header/footer 位于传送到 body 的 section 内', async () => {
    await mount()
    const dialog = document.querySelector('[role="dialog"]')!
    expect(dialog.getAttribute('aria-label')).toBe('编辑 DNS 服务器')
    expect(dialog.getAttribute('aria-modal')).toBe('true')
    const section = dialog.closest('section')!
    expect(section.parentElement).toBe(document.body)
    expect(host.contains(dialog)).toBe(false)
    expect(dialog.querySelector('header')?.closest('section')).toBe(section)
    expect(dialog.querySelector('footer')?.closest('section')).toBe(section)
    expect(dialog.querySelector('[role="heading"]')?.textContent).toBe('编辑 DNS 服务器')
    expect(dialog.querySelector('[role="heading"]')?.getAttribute('aria-level')).toBe('2')
    expect(dialog.querySelector('button.el-dialog__headerbtn')).not.toBeNull()
    expect(dialog.querySelector('input')?.getAttribute('aria-label')).toBe('DNS 服务器')
    expect(dialog.querySelector('footer')?.textContent).toBe('预览修改')
  })

  it('覆盖传入的传送目标，卸载时清理完整 section', async () => {
    await mount({ appendTo: '#outside-dialog-section', appendToBody: true })
    const section = document.querySelector('[role="dialog"]')!.closest('section')!
    expect(section.parentElement).toBe(document.body)
    app!.unmount()
    app = undefined
    expect(section.isConnected).toBe(false)
    expect(document.querySelector('[role="dialog"]')).toBeNull()
  })

  it('透传打开事件和具名标题插槽的 ID、样式及关闭回调', async () => {
    const onOpen = vi.fn()
    const { open } = await mount({ title: '', onOpen }, {
      header: ({ titleId, titleClass, close }) => h('h2', { id: titleId, class: titleClass }, [
        '自定义标题', h('button', { class: 'slot-close', onClick: close }, '关闭'),
      ]),
    })
    open.value = false
    await flush()
    open.value = true
    await flush()
    expect(onOpen).toHaveBeenCalledOnce()
    const dialog = document.querySelector('[role="dialog"]')!
    const heading = dialog.querySelector('h2')!
    expect(dialog.getAttribute('aria-labelledby')).toBe(heading.id)
    expect(heading.classList.contains('el-dialog__title')).toBe(true)
    dialog.querySelector<HTMLButtonElement>('.slot-close')!.click()
    await flush()
    expect(open.value).toBe(false)
  })

  it('默认关闭按钮仍回传 modelValue，销毁后可以重开', async () => {
    const { open } = await mount()
    document.querySelector<HTMLButtonElement>('.el-dialog__headerbtn')!.click()
    await flush()
    expect(open.value).toBe(false)
    expect(document.querySelector('.el-dialog')).toBeNull()
    open.value = true
    await flush()
    expect(document.querySelector('[role="dialog"] header')?.closest('section')?.parentElement).toBe(document.body)
  })

  it('pending 隐藏关闭按钮并阻止 Escape；恢复后 Escape 可关闭', async () => {
    const { open, pending } = await mount()
    pending.value = true
    await flush()
    expect(document.querySelector('.el-dialog__headerbtn')).toBeNull()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', code: 'Escape', bubbles: true }))
    await flush()
    expect(open.value).toBe(true)
    pending.value = false
    await flush()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', code: 'Escape', bubbles: true }))
    await flush()
    expect(open.value).toBe(false)
  })
})
