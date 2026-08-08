// @vitest-environment jsdom

import { createApp, h } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import DateTimeRangeControl from './DateTimeRangeControl.vue'

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
})
