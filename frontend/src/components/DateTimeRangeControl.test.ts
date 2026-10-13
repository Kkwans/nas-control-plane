// @vitest-environment jsdom

import { createApp, h } from 'vue'
import { compileStyle, parse } from 'vue/compiler-sfc'
import { describe, expect, it, vi } from 'vitest'

import DateTimeRangeControl from './DateTimeRangeControl.vue'
import source from './DateTimeRangeControl.vue?raw'
import monitoringSource from '../views/MonitoringView.vue?raw'

describe('DateTimeRangeControl', () => {
  it('renders one compact range picker and keeps apply disabled until the range is complete', () => {
    const host = document.createElement('div')
    const onNow = vi.fn()
    const app = createApp({
      render: () => h(DateTimeRangeControl, {
        from: null,
        to: null,
        onNow,
      }),
    })

    app.mount(host)

    expect(host.querySelectorAll('.el-date-editor--datetimerange')).toHaveLength(1)
    expect(host.querySelector('input[placeholder="开始时间"]')).not.toBeNull()
    expect(host.querySelector('input[placeholder="结束时间"]')).not.toBeNull()

    const buttons = [...host.querySelectorAll<HTMLButtonElement>('button')]
    expect(buttons.map((button) => button.textContent?.trim())).toEqual(['现在', '清除', '应用'])
    expect(buttons[2]?.disabled).toBe(true)

    buttons[0]?.click()
    expect(onNow).toHaveBeenCalledOnce()

    app.unmount()
  })
  it('anchors deep editor styles on a native wrapper and keeps both date fields and the icon', () => {
    const host = document.createElement('div')
    const app = createApp({ render: () => h(DateTimeRangeControl, {
      from: new Date('2026-10-13T00:00:00Z'), to: new Date('2026-10-13T01:00:00Z'),
    }) })
    app.mount(host)
    const frame = host.querySelector('.date-time-range__picker-frame')!
    frame.setAttribute('data-v-layout', '')
    const { descriptor } = parse(source)
    const css = compileStyle({ source: descriptor.styles[0]!.content, filename: 'DateTimeRangeControl.vue', id: 'data-v-layout', scoped: true })
    expect(css.errors).toEqual([])
    const selector = '.date-time-range__picker-frame[data-v-layout] .date-time-range__picker'
    expect(css.code).toContain(selector)
    expect(host.querySelector(selector)).not.toBeNull()
    expect(frame.querySelectorAll('input')).toHaveLength(2)
    expect([...frame.querySelectorAll('input')].every((input) => /^\d{4}\/\d{2}\/\d{2} \d{2}:\d{2}$/.test(input.value))).toBe(true)
    expect(frame.querySelector('.el-range__icon')).not.toBeNull()
    expect(css.code).toContain('@container monitoring-date-picker (max-width: 26rem)')
    expect(css.code).not.toContain('display: none')
    app.unmount()
  })

  it('allows intrinsic-width actions to wrap rather than clipping enlarged labels', () => {
    const actions = source.match(/\.date-time-range__actions > \.action-button \{([^}]+)\}/)?.[1]
    expect(actions).toContain('min-width: max-content')
    expect(actions).toContain('height: auto')
    const mobile = source.split('@media (max-width: 760px)')[1]!.split('@media')[0]!
    expect(mobile).toMatch(/\.date-time-range__actions \{[^}]*flex-wrap: wrap/)
    expect(mobile).toMatch(/\.date-time-range__actions > \.action-button \{[^}]*flex: 1 0 auto/)
    expect(mobile).not.toContain('min-width: 0')
  })

  it('uses the font-relative tool width budget only while the toolbar lays out horizontally', () => {
    expect(monitoringSource).toMatch(/\.monitor-header :deep\(\.workspace-tools\) \{ flex: 0 0 auto; \}/)
    expect(monitoringSource).toMatch(/@media \(min-width: 641px\) \{\s*\.monitor-header :deep\(\.workspace-tools\) \{ flex: 1 1 36rem; \}\s*\}/)
    expect(monitoringSource.match(/flex: 1 1 36rem/g)).toHaveLength(1)
  })

  it.each([false, true])('preserves clear/apply events and loading protection (loading=%s)', (loading) => {
    const host = document.createElement('div')
    const onNow = vi.fn(), onClear = vi.fn(), onApply = vi.fn()
    const app = createApp({ render: () => h(DateTimeRangeControl, {
      from: new Date('2026-10-13T00:00:00Z'), to: new Date('2026-10-13T01:00:00Z'),
      loading, onNow, onClear, onApply,
    }) })
    app.mount(host)
    const buttons = [...host.querySelectorAll<HTMLButtonElement>('.date-time-range__actions button')]
    expect(buttons).toHaveLength(3)
    expect(buttons.every((button) => button.disabled === loading)).toBe(true)
    for (const button of buttons) button.click()
    for (const handler of [onNow, onClear, onApply]) expect(handler).toHaveBeenCalledTimes(loading ? 0 : 1)
    expect(host.querySelector('.date-time-range')?.getAttribute('aria-busy')).toBe(String(loading))
    app.unmount()
  })

})
