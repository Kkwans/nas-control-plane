// @vitest-environment jsdom

import { createApp, h, nextTick, ref, type App } from 'vue'
import { compileStyle, parse } from 'vue/compiler-sfc'
import { afterEach, describe, expect, it, vi } from 'vitest'

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

describe('DateTimeRangeControl 真实 tooltip 交互', () => {
  let app: App | undefined
  let host: HTMLElement
  let main: HTMLElement | undefined

  async function flush() {
    await nextTick()
    await vi.advanceTimersByTimeAsync(250)
    await nextTick()
  }
  async function mount(inMain = true) {
    vi.useFakeTimers()
    host = document.createElement('div')
    if (inMain) {
      main = document.createElement('main')
      main.id = 'date-help-main'
      main.append(host)
      document.body.append(main)
    }
    const onNow = vi.fn(), onClear = vi.fn(), loading = ref(false)
    app = createApp({ render: () => h(DateTimeRangeControl, { from: null, to: null, onNow, onClear, loading: loading.value }) })
    app.mount(host)
    await flush()
    return {
      buttons: [...host.querySelectorAll<HTMLButtonElement>('.date-time-range__actions button')],
      onNow, onClear, loading,
    }
  }
  function popper(button: HTMLButtonElement) {
    const id = button.getAttribute('aria-describedby')
    expect(id).toBeTruthy()
    const element = (main ?? host).querySelector<HTMLElement>(`[id="${id}"]`)
    expect(element?.getAttribute('role')).toBe('tooltip')
    expect(element?.getAttribute('aria-hidden')).toBe('false')
    return element!
  }
  function mouse(element: HTMLElement, type: 'mouseenter' | 'mouseleave') {
    element.dispatchEvent(new MouseEvent(type))
  }
  function escape() {
    const event = new KeyboardEvent('keydown', { key: 'Escape', code: 'Escape', bubbles: true, cancelable: true })
    document.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(false)
  }
  afterEach(() => {
    app?.unmount()
    app = undefined
    main?.remove()
    main = undefined
    host?.remove()
    vi.useRealTimers()
  })

  it.each([0, 1])('将按钮 %s 的真实提示放入所属 main，保持关联并允许 Escape 后重新聚焦', async (index) => {
    const { buttons } = await mount()
    const button = buttons[index]!
    button.focus()
    await flush()
    const tooltip = popper(button)
    expect(tooltip.parentElement).toBe(main)
    expect(tooltip.textContent).toBe(button.getAttribute('aria-label'))
    expect(host.contains(tooltip)).toBe(false)
    expect(document.activeElement).toBe(button)
    escape()
    await flush()
    expect(button.hasAttribute('aria-describedby')).toBe(false)
    expect(tooltip.isConnected).toBe(false)
    expect(document.activeElement).toBe(button)
    button.blur()
    button.focus()
    await flush()
    expect(popper(button).parentElement).toBe(main)
  })

  it('待执行的导航帧移走焦点会关闭帮助，先完成导航帧后聚焦则持续显示', async () => {
    const { buttons } = await mount()
    const button = buttons[0]!
    main!.tabIndex = -1
    // router.afterEach 在 requestAnimationFrame 中将焦点交给 main。
    // 冻结时钟下若先聚焦按钮，再推进时钟，就会实际触发 blur。
    requestAnimationFrame(() => main!.focus())
    button.focus()
    await flush()
    expect(document.activeElement).toBe(main)
    expect(button.hasAttribute('aria-describedby')).toBe(false)

    requestAnimationFrame(() => main!.focus())
    await vi.advanceTimersByTimeAsync(32)
    expect(document.activeElement).toBe(main)
    button.focus()
    await flush()
    expect(document.activeElement).toBe(button)
    popper(button)
  })

  it('独立脱离文档挂载时回退到自身，保留帮助且不创建 landmark', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    try {
      const { buttons } = await mount(false)
      mouse(buttons[0]!, 'mouseenter')
      await flush()
      expect(popper(buttons[0]!).parentElement).toBe(host.querySelector('.date-time-range'))
      expect(host.querySelector('main, [role="main"], [role="region"]')).toBeNull()
      expect(warn).not.toHaveBeenCalled()
    } finally { warn.mockRestore() }
  })

  it('焦点、按钮悬停及提示悬停之间切换时保留帮助，全部离开后关闭', async () => {
    const { buttons } = await mount()
    const button = buttons[0]!
    button.focus()
    mouse(button, 'mouseenter')
    await flush()
    mouse(button, 'mouseleave')
    await flush()
    let tooltip = popper(button)
    mouse(tooltip, 'mouseenter')
    button.blur()
    await flush()
    tooltip = popper(button)
    mouse(tooltip, 'mouseleave')
    await flush()
    expect(button.hasAttribute('aria-describedby')).toBe(false)

    mouse(button, 'mouseenter')
    button.focus()
    await flush()
    button.blur()
    await flush()
    popper(button)
    mouse(button, 'mouseleave')
    await flush()
    expect(button.hasAttribute('aria-describedby')).toBe(false)
  })

  it('仅鼠标悬停也可 Escape 关闭；未离开前不重开，重新进入后可显示', async () => {
    const { buttons } = await mount()
    const button = buttons[0]!
    mouse(button, 'mouseenter')
    await flush()
    popper(button)
    expect(document.activeElement).not.toBe(button)
    escape()
    await flush()
    expect(button.hasAttribute('aria-describedby')).toBe(false)
    await flush()
    expect(button.hasAttribute('aria-describedby')).toBe(false)
    mouse(button, 'mouseleave')
    mouse(button, 'mouseenter')
    await flush()
    popper(button)
  })

  it('Escape 取消尚未显示的提示，卸载清除传送内容和自身键盘监听', async () => {
    const remove = vi.spyOn(document, 'removeEventListener')
    const { buttons } = await mount()
    mouse(buttons[0]!, 'mouseenter')
    escape()
    await flush()
    expect(buttons[0]!.hasAttribute('aria-describedby')).toBe(false)
    mouse(buttons[0]!, 'mouseleave')
    mouse(buttons[0]!, 'mouseenter')
    await flush()
    const tooltip = popper(buttons[0]!)
    app!.unmount()
    app = undefined
    expect(tooltip.isConnected).toBe(false)
    expect(remove.mock.calls.some(([type, listener]) => type === 'keydown' && typeof listener === 'function' && listener.name === 'dismissHelp')).toBe(true)
    remove.mockRestore()
  })

  it('Enter/Space 不被 tooltip 拦截，按钮仍保留原来的点击动作', async () => {
    const { buttons, onNow, onClear } = await mount()
    for (const button of buttons.slice(0, 2)) {
      button.focus()
      await flush()
      for (const [key, code] of [['Enter', 'Enter'], [' ', 'Space'], ['Enter', 'NumpadEnter']]) {
        const event = new KeyboardEvent('keydown', { key, code, bubbles: true, cancelable: true })
        button.dispatchEvent(event)
        expect(event.defaultPrevented).toBe(false)
      }
      button.click()
    }
    expect(onNow).toHaveBeenCalledOnce()
    expect(onClear).toHaveBeenCalledOnce()
  })

  it.each([0, 1])('loading 禁用已聚焦按钮 %s 时清理帮助，恢复后只由新交互打开', async (index) => {
    const { buttons, loading, onNow, onClear } = await mount()
    const button = buttons[index]!
    button.focus()
    await flush()
    const tooltip = popper(button)
    loading.value = true
    await flush()
    expect(button.disabled).toBe(true)
    expect(button.hasAttribute('aria-describedby')).toBe(false)
    expect(tooltip.isConnected).toBe(false)
    mouse(button, 'mouseenter')
    button.click()
    await flush()
    expect(button.hasAttribute('aria-describedby')).toBe(false)
    expect(onNow).not.toHaveBeenCalled()
    expect(onClear).not.toHaveBeenCalled()
    loading.value = false
    await flush()
    expect(button.disabled).toBe(false)
    expect(button.hasAttribute('aria-describedby')).toBe(false)
    button.blur()
    button.focus()
    await flush()
    popper(button)
  })
})
