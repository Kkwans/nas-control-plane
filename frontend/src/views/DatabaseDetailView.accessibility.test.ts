import { readFileSync } from 'node:fs'
import { compileStyle, parse } from 'vue/compiler-sfc'
import { describe, expect, it } from 'vitest'
import viewSource from './DatabaseDetailView.vue?raw'

const { descriptor } = parse(viewSource, { filename: 'DatabaseDetailView.vue' })
const styles = descriptor.styles.map((style) => compileStyle({
  filename: 'DatabaseDetailView.vue', id: 'database-detail-accessibility', source: style.content,
}))
const tokensSource = readFileSync(`${import.meta.dirname}/../styles/tokens.css`, 'utf8')

function declarations(selector: string) {
  const values: Record<string, string> = {}
  for (const style of styles) {
    style.rawResult!.root.walkRules((rule) => {
      if (!rule.selectors.includes(selector)) return
      rule.walkDecls((declaration) => { values[declaration.prop] = declaration.value })
    })
  }
  return values
}
function token(reference: string) {
  const name = /^var\((--[\w-]+)\)$/.exec(reference)?.[1]
  const color = name && new RegExp(`${name}:\\s*(#[\\da-f]{6});`, 'i').exec(tokensSource)?.[1]
  if (!color) throw new Error(`缺少颜色 token：${reference}`)
  return color
}
function luminance(color: string) {
  const channels = [1, 3, 5].map((start) => {
    const value = Number.parseInt(color.slice(start, start + 2), 16) / 255
    return value <= .04045 ? value / 12.92 : ((value + .055) / 1.055) ** 2.4
  })
  return channels[0]! * .2126 + channels[1]! * .7152 + channels[2]! * .0722
}

describe('DatabaseDetailView 诊断状态码可读性', () => {
  // 静态回归保护不替代浏览器全量 axe；不允许 opacity 再把语义色混浅。
  it.each(['success', 'previous', 'failure'])('%s 状态码保持不透明且普通字号对比度至少 4.5:1', (state) => {
    expect(styles.flatMap((style) => style.errors)).toEqual([])
    const result = declarations('.diagnostic-result')
    const variant = declarations(`.diagnostic-result--${state}`)
    const code = declarations('.diagnostic-result code')
    const variantCode = declarations(`.diagnostic-result--${state} code`)
    for (const rule of [result, variant, code, variantCode]) expect(rule.opacity ?? '1').toBe('1')
    const foreground = token(variantCode.color ?? code.color ?? variant.color!)
    const background = token(variant.background!)
    const values = [luminance(foreground), luminance(background)].sort((a, b) => b - a)
    expect((values[0]! + .05) / (values[1]! + .05)).toBeGreaterThanOrEqual(4.5)
  })
})
