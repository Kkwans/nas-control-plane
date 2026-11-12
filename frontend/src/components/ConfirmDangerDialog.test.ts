// @vitest-environment jsdom

import { createApp, h, nextTick, reactive, type App } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ConfirmDangerDialog from './ConfirmDangerDialog.vue'

vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    ElButton: defineComponent({
      props: { disabled: Boolean, loading: Boolean },
      setup: (props, { slots }) => () => h('button', { disabled: props.disabled || props.loading }, slots.default?.()),
    }),
    ElDialog: defineComponent({
      props: { modelValue: Boolean, title: String }, emits: ['update:modelValue', 'close'],
      setup: (props, { slots }) => () => props.modelValue
        ? h('section', { role: 'dialog' }, [h('h2', props.title), slots.default?.(), slots.footer?.()]) : null,
    }),
    ElInput: defineComponent({
      props: { modelValue: String }, emits: ['update:modelValue'],
      setup: (props, { emit }) => () => h('input', {
        value: props.modelValue,
        onInput: (event: Event) => emit('update:modelValue', (event.target as HTMLInputElement).value),
      }),
    }),
  }
})

let app: App | undefined
let host: HTMLDivElement
let props: {
  modelValue: boolean; title: string; description: string; impact: string[];
  retained: string[]; busy: boolean; confirmationTarget: string
}
const confirm = vi.fn()
const update = vi.fn()
async function mount(details?: string) {
  host = document.createElement('div')
  document.body.append(host)
  app = createApp({ render: () => h(ConfirmDangerDialog, {
    ...props,
    onConfirm: confirm,
    'onUpdate:modelValue': (value: boolean) => { update(value); props.modelValue = value },
  }, details === undefined ? undefined : { details: () => h('pre', { 'data-test': 'details' }, details) }) })
  app.mount(host)
  await nextTick()
}
function button(text: string) {
  return [...host.querySelectorAll('button')].find((element) => element.textContent === text)!
}

beforeEach(() => {
  vi.clearAllMocks()
  props = reactive({ modelValue: true, title: '确认删除', description: '请确认删除对象。', impact: ['删除目标对象'], retained: ['保留其他对象'], busy: false, confirmationTarget: '' })
})
afterEach(() => { app?.unmount(); app = undefined; host?.remove() })

describe('ConfirmDangerDialog 向后兼容', () => {
  it('不提供新插槽时保留说明、影响、保留范围及原确认和取消行为', async () => {
    await mount()
    expect(host.textContent).toContain('确认删除')
    expect(host.querySelector('.confirm-danger-dialog__notice')?.textContent).toContain('请确认删除对象。')
    expect([...host.querySelectorAll('li')].map((element) => element.textContent)).toEqual(['删除目标对象', '保留其他对象'])
    expect(host.querySelector('[data-test="details"]')).toBeNull()
    button('确认操作').click()
    expect(confirm).toHaveBeenCalledTimes(1)
    expect(update).not.toHaveBeenCalled()
    expect(props.modelValue).toBe(true)
    button('取消').click()
    await nextTick()
    expect(update).toHaveBeenCalledExactlyOnceWith(false)
    expect(host.querySelector('[role="dialog"]')).toBeNull()
  })

  it('可选详情插槽不改变原有提示和操作', async () => {
    const details = '第一行\n  第二行'
    await mount(details)
    expect(host.querySelector('[data-test="details"]')?.textContent).toBe(details)
    expect([...host.querySelectorAll('li')].map((element) => element.textContent)).toEqual(['删除目标对象', '保留其他对象'])
    button('确认操作').click()
    expect(confirm).toHaveBeenCalledTimes(1)
    button('取消').click()
    expect(update).toHaveBeenCalledExactlyOnceWith(false)
  })

  it('忙碌状态保持确认和取消禁用', async () => {
    props.busy = true
    await mount()
    expect(button('确认操作').disabled).toBe(true)
    expect(button('取消').disabled).toBe(true)
    button('确认操作').click()
    button('取消').click()
    expect(confirm).not.toHaveBeenCalled()
    expect(update).not.toHaveBeenCalled()
  })

  it('目标名称校验及关闭后清空确认输入保持不变', async () => {
    props.confirmationTarget = 'sample'
    await mount()
    expect(button('确认操作').disabled).toBe(true)
    const input = host.querySelector('input')!
    input.value = ' sample '
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()
    expect(button('确认操作').disabled).toBe(false)
    button('确认操作').click()
    expect(confirm).toHaveBeenCalledTimes(1)
    button('取消').click()
    await nextTick()
    props.modelValue = true
    await nextTick()
    expect(host.querySelector('input')?.value).toBe('')
    expect(button('确认操作').disabled).toBe(true)
  })

  it('无说明和影响列表时仍使用默认提示且不增加空白详情区', async () => {
    props.description = ''
    props.impact = []
    props.retained = []
    await mount()
    expect(host.textContent).toContain('这是一个可能影响数据或运行状态的操作')
    expect(host.querySelector('.confirm-danger-dialog__section')).toBeNull()
    expect(host.querySelector('[data-test="details"]')).toBeNull()
  })
})
