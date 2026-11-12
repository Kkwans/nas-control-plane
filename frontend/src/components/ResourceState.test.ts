// @vitest-environment jsdom

import { readFileSync } from 'node:fs'
import { createApp, h } from 'vue'
import { compileStyle, parse } from 'vue/compiler-sfc'
import { describe, expect, it, vi } from 'vitest'

import ResourceState from './ResourceState.vue'
import source from './ResourceState.vue?raw'

const scopeId = 'data-v-resource-state-target'
const { descriptor } = parse(source, { filename: 'ResourceState.vue' })
const styles = descriptor.styles.map(style => compileStyle({
  filename: 'ResourceState.vue', id: scopeId, source: style.content, scoped: style.scoped,
}))
function declarations(selector: string) {
  const values: Record<string, string> = {}
  for (const style of styles) style.rawResult!.root.walkRules(rule => {
    if (rule.selectors.includes(selector)) rule.walkDecls(declaration => { values[declaration.prop] = declaration.value })
  })
  return values
}

describe('ResourceState', () => {
  it('renders a recoverable error state with user guidance', () => {
    const host = document.createElement('div')
    const retry = vi.fn()
    const app = createApp({
      render: () => h(ResourceState, {
        state: 'error',
        title: '读取失败',
        message: 'Agent 暂不可用',
        nextStep: '确认服务状态后重试',
        code: 'AGENT_UNAVAILABLE',
        onRetry: retry,
      }),
    })
    app.mount(host)

    expect(host.querySelector('[role="alert"]')).not.toBeNull()
    expect(host.textContent).toContain('下一步：确认服务状态后重试')
    expect(host.textContent).toContain('代码 AGENT_UNAVAILABLE')
    host.querySelector('button')?.click()
    expect(retry).toHaveBeenCalledOnce()
    app.unmount()
  })

  it.each(['empty', 'loading'] as const)('keeps %s state free of a retry action', (state) => {
    const host = document.createElement('div')
    const app = createApp({ render: () => h(ResourceState, { state, title: '暂无数据' }) })
    app.mount(host)
    expect(host.querySelector('[role="status"]')).not.toBeNull()
    expect(host.querySelector('button')).toBeNull()
    app.unmount()
  })

  it.each([
    { retryLabel: undefined, expected: '重试' },
    { retryLabel: 'R', expected: 'R' },
  ])('keeps the $expected label on one native retry button', ({ retryLabel, expected }) => {
    const host = document.createElement('div')
    const retry = vi.fn()
    const app = createApp({ render: () => h(ResourceState, { state: 'error', title: '读取失败', retryLabel, onRetry: retry }) })
    app.mount(host)
    try {
      expect(host.querySelectorAll('button')).toHaveLength(1)
      const button = host.querySelector('button')!
      expect(button.type).toBe('button')
      expect(button.disabled).toBe(false)
      expect(button.textContent).toBe(expected)
      expect(retry).not.toHaveBeenCalled()
      button.click()
      expect(retry).toHaveBeenCalledExactlyOnceWith()
    } finally { app.unmount() }
  })
})

// 编译后 CSS 合同不声称 JSDOM 具有实际几何、命中或原生键盘激活能力。
describe('ResourceState 重试按钮的局部触达合同', () => {
  it('使用现有 44px token 同时限制两个最小尺寸，且无断点覆盖', () => {
    expect(styles.flatMap(style => style.errors)).toEqual([])
    expect(descriptor.styles.every(style => style.scoped)).toBe(true)
    const tokenStyle = compileStyle({ filename: 'tokens.css', id: 'tokens', source: readFileSync('src/styles/tokens.css', 'utf8') })
    expect(tokenStyle.errors).toEqual([])
    const targetValues: string[] = []
    tokenStyle.rawResult!.root.walkDecls('--ncp-touch-target', declaration => { targetValues.push(declaration.value) })
    expect(targetValues).toEqual(['44px'])
    expect(declarations(`.resource-state button[${scopeId}]`)).toMatchObject({
      'min-width': 'var(--ncp-touch-target)', 'min-height': 'var(--ncp-touch-target)',
    })
    for (const style of styles) style.rawResult!.root.walkRules(rule => {
      if (!rule.selector.includes('button')) return
      expect(rule.selectors.every(selector => selector.includes(`[${scopeId}]`))).toBe(true)
      rule.walkDecls(declaration => {
        if (['min-width', 'min-height'].includes(declaration.prop)) {
          expect(rule.parent?.type).toBe('root')
          expect(declaration.value).toBe('var(--ncp-touch-target)')
        }
      })
    })
  })

  it('保留键盘焦点环、自然文字换行和窄屏独立行', () => {
    expect(declarations(`.resource-state button[${scopeId}]:focus-visible`)).toEqual({
      outline: '2px solid var(--ncp-primary)', 'outline-offset': '2px',
    })
    const button = declarations(`.resource-state button[${scopeId}]`)
    for (const property of ['height', 'max-height', 'width', 'max-width', 'white-space', 'overflow', 'text-overflow']) {
      expect(button[property]).toBeUndefined()
    }
    const mobileRules: Record<string, string>[] = []
    for (const style of styles) style.rawResult!.root.walkAtRules('media', media => {
      if (media.params !== '(max-width: 560px)') return
      media.walkRules(rule => {
        if (rule.selector !== `.resource-state--error button[${scopeId}]`) return
        const values: Record<string, string> = {}
        rule.walkDecls(declaration => { values[declaration.prop] = declaration.value })
        mobileRules.push(values)
      })
    })
    expect(mobileRules).toEqual([{ 'grid-column': '2', 'grid-row': 'auto', 'justify-self': 'start', 'margin-top': '4px' }])
  })
})
