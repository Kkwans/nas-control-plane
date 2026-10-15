import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { compileStyle } from 'vue/compiler-sfc'
import { describe, expect, it } from 'vitest'

const require = createRequire(import.meta.url)
function stylesheet(filename: string) {
  const result = compileStyle({ filename, id: 'success-message-contract', source: readFileSync(filename, 'utf8') })
  expect(result.errors).toEqual([])
  return result.rawResult!.root
}
const base = stylesheet(`${import.meta.dirname}/base.css`)
const tokens = stylesheet(`${import.meta.dirname}/tokens.css`)
const element = stylesheet(require.resolve('element-plus/dist/index.css'))

function declarations(sheet: typeof base, selector: string) {
  const values: Record<string, string> = {}
  sheet.walkRules((rule) => {
    if (!rule.selectors.includes(selector)) return
    rule.walkDecls((declaration) => { values[declaration.prop] = declaration.value })
  })
  return values
}
function resolveColor(value: string): string {
  const name = /^var\((--[\w-]+)\)$/.exec(value)?.[1]
  if (!name) {
    expect(value).toMatch(/^#[\da-f]{6}$/i)
    return value
  }
  const resolved = declarations(tokens, ':root')[name] ?? declarations(element, ':root')[name]
  if (!resolved) throw new Error(`缺少颜色 token：${name}`)
  return resolveColor(resolved)
}
function luminance(color: string) {
  const channels = [1, 3, 5].map((start) => {
    const value = Number.parseInt(color.slice(start, start + 2), 16) / 255
    return value <= .04045 ? value / 12.92 : ((value + .055) / 1.055) ** 2.4
  })
  return channels[0]! * .2126 + channels[1]! * .7152 + channels[2]! * .0722
}

describe('Element 成功消息颜色合同', () => {
  it('以双类限定覆盖组件局部 token，不改全局成功色或背景', () => {
    expect(declarations(base, '.el-message.el-message--success')).toEqual({
      '--el-message-text-color': 'var(--ncp-success-strong)',
    })
    expect(declarations(tokens, ':root')['--el-color-success']).toBe('var(--ncp-success)')
    expect(declarations(base, ':root')['--el-color-success']).toBeUndefined()
    // 双类选择器优先于 Element 单类的局部声明，不依赖异步 CSS 注入顺序。
    expect(declarations(element, '.el-message--success')['--el-message-text-color']).toBe('var(--el-color-success)')
  })

  it('真实组件文字与图标消费覆盖 token，背景链上的普通字号对比度达到 4.5:1', () => {
    expect(declarations(element, '.el-message--success .el-message__content').color).toBe('var(--el-message-text-color)')
    expect(declarations(element, '.el-message .el-message-icon--success').color).toBe('var(--el-message-text-color)')
    expect(declarations(element, '.el-message')['background-color']).toBe('var(--el-message-bg-color)')
    const foreground = resolveColor(declarations(base, '.el-message.el-message--success')['--el-message-text-color']!)
    const background = resolveColor(declarations(element, '.el-message--success')['--el-message-bg-color']!)
    expect(background).toBe('#f0f9eb')
    const values = [luminance(foreground), luminance(background)].sort((a, b) => b - a)
    expect((values[0]! + .05) / (values[1]! + .05)).toBeGreaterThanOrEqual(4.5)
  })
})
