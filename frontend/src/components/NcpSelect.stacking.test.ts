// @vitest-environment jsdom

import { createApp, h, nextTick, reactive, type App } from 'vue'
import { compileStyle, parse } from 'vue/compiler-sfc'
import { ElDialog } from 'element-plus'
import { afterEach, describe, expect, it, vi } from 'vitest'
import NcpSelect from './NcpSelect.vue'
import source from './NcpSelect.vue?raw'

const scopeId = 'data-v-select-stacking'
const { descriptor } = parse(source, { filename: 'NcpSelect.vue' })
const style = compileStyle({ source: descriptor.styles[0]!.content, filename: 'NcpSelect.vue', id: scopeId, scoped: true })
const options = [
  { label: 'Alpha', value: 'alpha' }, { label: 'Beta', value: 'beta' }, { label: 'Locked', value: 'locked', disabled: true },
  { label: 'Undefined word', value: 'undefined' }, { label: 'Null word', value: 'null' }, { label: 'Empty string', value: '' },
]
let app: App | undefined
let host: HTMLDivElement
async function settle() { for (let count = 0; count < 20; count++) await nextTick() }
async function mount(props: { filterable?: boolean; clearable?: boolean; disabled?: boolean; modal?: boolean; valueOnClear?: () => null } = {}, items = options) {
  const { modal, ...selectProps } = props
  const state = reactive({ modelValue: 'alpha', filterable: false, clearable: false, disabled: false, ...selectProps })
  const update = vi.fn((value: string) => { state.modelValue = value })
  host = document.createElement('div')
  document.body.append(host)
  const select = () => h(NcpSelect, { ...state, options: items, accessibleLabel: '层级验收选择器', 'onUpdate:modelValue': update })
  app = createApp({ render: () => modal ? h(ElDialog, { modelValue: true, title: '层级验收弹窗' }, { default: select }) : select() })
  app.mount(host)
  await settle()
  return { state, update }
}
function input() { return host.querySelector<HTMLInputElement>('input[role="combobox"]')! }
function wrapper() { return host.querySelector<HTMLElement>('.el-select__wrapper')! }
function popup() { return document.getElementById(input().getAttribute('aria-controls')!)! }
async function open() { wrapper().click(); await settle() }
async function choose(label: string) {
  const option = [...popup().querySelectorAll<HTMLElement>('[role="option"]')].find((node) => node.textContent?.trim() === label)
  expect(option).toBeDefined()
  option!.click()
  await settle()
}
function declarations(selector: string) {
  const values: Record<string, string> = {}
  style.rawResult!.root.walkRules((rule) => {
    if (rule.selectors.includes(selector)) rule.walkDecls((declaration) => { values[declaration.prop] = declaration.value })
  })
  return values
}
afterEach(async () => { app?.unmount(); app = undefined; host?.remove(); await settle() })

// CSS 仅核验实际编译的作用域和层级合同；JSDOM 不证明像素、命中或颜色对比度。
// 真实 Element Plus 交互不打桩，浏览器几何与完整 axe 由 schema-drift fixture 验收。
describe('NcpSelect 局部绘制层级与原生交互', () => {
  it('作用域限定的局部层级保留负层级子元素，不隐藏节点或覆盖选中值', async () => {
    await mount()
    expect(style.errors).toEqual([])
    const select = host.querySelector<HTMLElement>('.ncp-select')!
    select.setAttribute(scopeId, '')
    const outer = `.ncp-select[${scopeId}]`
    const inner = `${outer} .el-select__wrapper`
    expect(host.querySelector(outer)).toBe(select)
    expect(host.querySelector(inner)).toBe(wrapper())
    expect(declarations(outer).isolation).toBe('isolate')
    expect(declarations(inner)['z-index']).toBe('1')
    expect(declarations(inner).background).toBe('var(--ncp-control-surface)')
    for (const selector of [inner, `${outer} .el-select__placeholder`, `${outer} .el-select__input`, `${outer} .el-select__clear`]) {
      expect(declarations(selector)['pointer-events']).toBeUndefined()
      expect(declarations(selector).visibility).toBeUndefined()
      expect(declarations(selector).display).toBeUndefined()
    }
    expect(declarations(`${outer} .el-select__placeholder`)['z-index']).toBeUndefined()
    expect(host.querySelector('.el-select__selected-item')?.textContent).toBeDefined()
  })

  it('只读 combobox 保留外壳及键盘选择，禁用选项不能改变值', async () => {
    const { state, update } = await mount()
    expect(input().readOnly).toBe(true)
    await open()
    expect(input().getAttribute('aria-expanded')).toBe('true')
    await choose('Locked')
    expect(state.modelValue).toBe('alpha')
    expect(update).not.toHaveBeenCalled()
    await choose('Beta')
    expect(state.modelValue).toBe('beta')
    expect(input().getAttribute('aria-expanded')).toBe('false')
    input().dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', bubbles: true }))
    await settle()
    expect(input().getAttribute('aria-expanded')).toBe('true')
    input().dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', code: 'Escape', bubbles: true }))
    await settle()
    expect(input().getAttribute('aria-expanded')).toBe('false')
  })

  it('可搜索输入保留可编辑原生节点和选项过滤', async () => {
    const { state } = await mount({ filterable: true })
    expect(input().readOnly).toBe(false)
    await open()
    input().value = 'Beta'
    input().dispatchEvent(new Event('input', { bubbles: true }))
    await settle()
    expect(input().value).toBe('Beta')
    const visibleOptions = [...popup().querySelectorAll<HTMLElement>('[role="option"]')].filter((node) => node.style.display !== 'none')
    expect(visibleOptions.map((node) => node.textContent?.trim())).toEqual(['Beta'])
    await choose('Beta')
    expect(state.modelValue).toBe('beta')
  })

  it.each(['undefined', 'null'])('清除按钮将原生 %s 清空值转为空串，单次派发且不重开菜单', async (empty) => {
    const { state, update } = await mount({ clearable: true, ...(empty === 'null' ? { valueOnClear: () => null } : {}) })
    input().focus()
    await settle()
    const clear = host.querySelector<HTMLElement>('.el-select__clear')
    expect(clear).not.toBeNull()
    clear!.click()
    await settle()
    expect(update).toHaveBeenCalledOnce()
    expect(state.modelValue).toBe('')
    expect(input().getAttribute('aria-expanded')).toBe('false')
  })

  it.each([
    { label: 'Undefined word', value: 'undefined' }, { label: 'Null word', value: 'null' }, { label: 'Empty string', value: '' },
  ])('合法选项 $label 保留原字符串，单次派发', async ({ label, value }) => {
    const { state, update } = await mount()
    await open()
    await choose(label)
    expect(state.modelValue).toBe(value)
    expect(update).toHaveBeenCalledExactlyOnceWith(value)
  })

  it.each([0, 42])('第三方数值 %s 仍沿用 String 转换，零不能当成空值', async (value) => {
    // NcpSelect 对外是字符串合同；防御第三方 ElSelect 运行时仍可能派发数值。
    const { state, update } = await mount({}, [{ label: 'Numeric option', value: value as unknown as string }])
    await open()
    await choose('Numeric option')
    expect(state.modelValue).toBe(String(value))
    expect(update).toHaveBeenCalledExactlyOnceWith(String(value))
  })

  it('禁用选择器不展开且不显示清除入口，恢复后才能更改', async () => {
    const { state, update } = await mount({ filterable: true, clearable: true, disabled: true })
    expect(input().disabled).toBe(true)
    expect(wrapper().classList.contains('is-disabled')).toBe(true)
    await open()
    expect(input().getAttribute('aria-expanded')).toBe('false')
    expect(host.querySelector('.el-select__clear')).toBeNull()
    expect(update).not.toHaveBeenCalled()
    state.disabled = false
    await settle()
    await open()
    await choose('Beta')
    expect(state.modelValue).toBe('beta')
  })

  it('弹窗内菜单仍传送到 body，不被选择器隔离层或弹窗内容包裹', async () => {
    const { state } = await mount({ modal: true })
    await open()
    const listbox = popup()
    expect(listbox.getAttribute('role')).toBe('listbox')
    expect(document.body.contains(listbox)).toBe(true)
    expect(host.contains(listbox)).toBe(false)
    expect(listbox.closest('.ncp-select, .el-dialog')).toBeNull()
    expect(host.querySelector('.el-dialog')?.contains(wrapper())).toBe(true)
    await choose('Beta')
    expect(state.modelValue).toBe('beta')
  })
})
