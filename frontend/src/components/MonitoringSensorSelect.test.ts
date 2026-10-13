// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, ref, type App } from 'vue'
import MonitoringSensorSelect from './MonitoringSensorSelect.vue'

const captured = vi.hoisted(() => ({ select: undefined as Record<string, unknown> | undefined }))
vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    ElSelect: defineComponent({
      props: ['modelValue', 'multipleLimit', 'filterMethod'],
      emits: ['update:modelValue'],
      setup(props, { slots, emit }) {
        captured.select = { filter: props.filterMethod, update: (value: string[]) => emit('update:modelValue', value), limit: props.multipleLimit }
        return () => h('div', { 'data-select': true }, slots.default?.())
      },
    }),
    ElOption: defineComponent({ props: ['label', 'value'], setup: (props) => () => h('div', { 'data-option': props.value }, props.label) }),
  }
})
let app: App | undefined
let host: HTMLDivElement
afterEach(() => { app?.unmount(); host?.remove(); captured.select = undefined })
function mount(names: string[], initial: string[] = names.slice(0, 8)) {
  const selected = ref(initial)
  host = document.createElement('div'); document.body.append(host)
  app = createApp({ render: () => h(MonitoringSensorSelect, { names, modelValue: selected.value, 'onUpdate:modelValue': (value) => selected.value = value }) })
  app.mount(host)
  return selected
}

describe('有界温度传感器选择', () => {
  it('keeps only 50 matching options plus at most 8 selected from a 65,536-name catalogue', async () => {
    const names = Array.from({ length: 65_536 }, (_, index) => `sensor-${String(index).padStart(5, '0')}`)
    mount(names, names.slice(-8))
    expect(host.querySelectorAll('[data-option]').length).toBe(58)
    expect(host.textContent).toContain('已显示 8 / 65536 个传感器')
    expect(host.textContent).toContain('匹配 65536 项，仅显示前 50 项')
    expect(captured.select?.limit).toBe(8)
    const filter = captured.select?.filter as (value: string) => void
    filter('sensor-12345')
    await nextTick()
    expect(host.querySelectorAll('[data-option]').length).toBe(9)
    expect(host.querySelector('[data-option="sensor-12345"]')).not.toBeNull()
    expect(host.textContent).not.toContain('匹配 65536 项')
  })
  it('limits the ninth selection, deduplicates names, and allows an intentional empty selection', async () => {
    const names = Array.from({ length: 10 }, (_, index) => `sensor-${index}`)
    const selected = mount(names)
    const update = captured.select?.update as (value: string[]) => void
    update([...names, names[0]!, 'unknown'])
    await nextTick()
    expect(selected.value).toEqual(names.slice(0, 8))
    update([])
    await nextTick()
    expect(selected.value).toEqual([])
    expect(host.textContent).toContain('已显示 0 / 10 个传感器')
  })
  it('keeps raw names distinct and escapes their displayed labels', () => {
    mount(['<img src=x onerror=1>', 'CPU&1'], [])
    expect(host.querySelector('img')).toBeNull()
    expect(host.textContent).toContain('<img src=x onerror=1>')
    expect(host.textContent).toContain('CPU&1')
  })
})
