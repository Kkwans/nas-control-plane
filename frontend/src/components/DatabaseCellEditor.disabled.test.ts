// @vitest-environment jsdom

import { createApp, h, nextTick, reactive, type App } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { DatabaseColumn } from '@/api/database'
import DatabaseCellEditor from './DatabaseCellEditor.vue'

const column: DatabaseColumn = {
  name: 'enabled', dataType: 'BOOLEAN', nullable: true, primaryKey: false, position: 1,
}
let app: App | undefined
let host: HTMLDivElement
async function settle() { for (let i = 0; i < 20; i++) await nextTick() }
async function mount(modelValue: string, nullSelected: boolean, disabled: boolean) {
  const state = reactive({ modelValue, nullSelected, disabled })
  const updateValue = vi.fn((value: string) => { state.modelValue = value })
  const updateNull = vi.fn((value: boolean) => { state.nullSelected = value })
  host = document.createElement('div')
  document.body.append(host)
  app = createApp({ render: () => h(DatabaseCellEditor, {
    ...state, column, 'onUpdate:modelValue': updateValue, 'onUpdate:nullSelected': updateNull,
  }) })
  app.mount(host)
  await settle()
  return { state, updateValue, updateNull }
}
function input() { return host.querySelector<HTMLInputElement>('input[role="combobox"]')! }
async function open() {
  host.querySelector<HTMLElement>('.el-select__wrapper')!.click()
  await settle()
}
async function select(label: string) {
  const popup = document.getElementById(input().getAttribute('aria-controls')!)!
  const option = [...popup.querySelectorAll<HTMLElement>('[role="option"]')]
    .find((element) => element.textContent?.trim() === label)
  expect(option).toBeDefined()
  option!.click()
  await settle()
}
afterEach(async () => { app?.unmount(); app = undefined; host?.remove(); await settle() })

// 保留真实 NcpSelect / Element Plus，防止桩组件掩盖 NULL 对 disabled 的绕过。
describe('DatabaseCellEditor 布尔字段硬锁', () => {
  describe.each([
    { label: 'true', modelValue: 'true', nullSelected: false },
    { label: 'false', modelValue: 'false', nullSelected: false },
    { label: 'NULL', modelValue: '', nullSelected: true },
  ])('$label', ({ modelValue, nullSelected }) => {
    it.each([false, true])('disabled=%s 与显示值无关', async (disabled) => {
      const { updateValue, updateNull } = await mount(modelValue, nullSelected, disabled)
      expect(input().disabled).toBe(disabled)
      expect(host.querySelector('.el-select__wrapper')!.classList.contains('is-disabled')).toBe(disabled)
      await open()
      expect(input().getAttribute('aria-expanded')).toBe(String(!disabled))
      expect(updateValue).not.toHaveBeenCalled()
      expect(updateNull).not.toHaveBeenCalled()
    })
  })

  it.each(['true', 'false'])('未锁定时可以从 NULL 选择 %s 并重新选择 NULL', async (value) => {
    const { state, updateValue, updateNull } = await mount('', true, false)
    await open()
    await select(value)
    expect(state).toMatchObject({ modelValue: value, nullSelected: false })
    expect(updateValue).toHaveBeenLastCalledWith(value)
    expect(updateNull).toHaveBeenLastCalledWith(false)
    await open()
    await select('NULL（显式）')
    expect(state).toMatchObject({ modelValue: value, nullSelected: true })
    expect(updateValue).toHaveBeenCalledTimes(1)
    expect(updateNull).toHaveBeenLastCalledWith(true)
    expect(input().disabled).toBe(false)
  })

  it('请求结束解除硬锁后，NULL 可以恢复编辑', async () => {
    const { state } = await mount('', true, true)
    state.disabled = false
    await settle()
    expect(input().disabled).toBe(false)
    await open()
    await select('false')
    expect(state).toMatchObject({ modelValue: 'false', nullSelected: false })
  })

  it.each([
    { modelValue: '', nullSelected: true, choice: 'true' },
    { modelValue: 'true', nullSelected: false, choice: 'NULL（显式）' },
  ])('菜单已展开后进入硬锁，旧选项 $choice 不能修改草稿', async ({ modelValue, nullSelected, choice }) => {
    const { state, updateValue, updateNull } = await mount(modelValue, nullSelected, false)
    await open()
    state.disabled = true
    await settle()
    expect(input().disabled).toBe(true)
    await select(choice)
    expect(state).toMatchObject({ modelValue, nullSelected })
    expect(updateValue).not.toHaveBeenCalled()
    expect(updateNull).not.toHaveBeenCalled()
  })
})
