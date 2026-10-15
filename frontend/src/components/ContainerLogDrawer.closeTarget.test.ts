import { readFileSync } from 'node:fs'
import { compileStyle, parse } from 'vue/compiler-sfc'
import { describe, expect, it } from 'vitest'

const filename = 'ContainerLogDrawer.vue'
const { descriptor } = parse(readFileSync(new URL(`./${filename}`, import.meta.url), 'utf8'), { filename })
const styles = descriptor.styles.map(style => compileStyle({ filename, id: 'data-v-close-target', source: style.content, scoped: style.scoped }))
function declarations(selector: string) {
  const values: Record<string, string> = {}
  for (const style of styles) style.rawResult!.root.walkRules(rule => {
    if (rule.selectors.includes(selector)) rule.walkDecls(declaration => { values[declaration.prop] = declaration.value })
  })
  return values
}

// 纯 CSS 合同不声称实际像素尺寸；真实几何、命中、键盘与恢复由只读 E2E 留证。
describe('容器详情原生关闭按钮局部样式', () => {
  it('所有断点保留至少 44×44 的非收缩按钮及居中图标', () => {
    expect(styles.flatMap(style => style.errors)).toEqual([])
    expect(declarations('.container-detail-drawer .el-drawer__close-btn')).toEqual({
      'box-sizing': 'border-box', 'min-width': '44px', 'min-height': '44px', flex: '0 0 auto', 'align-items': 'center', 'justify-content': 'center',
    })
    for (const style of styles) style.rawResult!.root.walkRules(rule => {
      if (rule.selector.includes('.el-drawer__close-btn')) {
        expect(rule.selectors.every(selector => selector.startsWith('.container-detail-drawer '))).toBe(true)
        expect(rule.parent?.type).toBe('root')
      }
    })
  })

  it('标题保留收缩截断能力，间隔不侵占关闭按钮或移动正文', () => {
    expect(declarations('.container-detail-drawer .el-drawer__header')).toMatchObject({ gap: '12px', padding: '17px 20px 14px' })
    expect(declarations('.drawer-heading[data-v-close-target]')['min-width']).toBe('0')
    expect(declarations('.drawer-heading h2[data-v-close-target]')).toMatchObject({ overflow: 'hidden', 'text-overflow': 'ellipsis', 'white-space': 'nowrap' })
    expect(declarations('.container-detail-drawer .el-drawer__body').padding).toBe('15px 14px')
  })

  it('局部键盘焦点环覆盖组件默认 outline:none 而不修改其它抽屉', () => {
    expect(declarations('.container-detail-drawer .el-drawer__close-btn:focus-visible')).toEqual({ outline: '2px solid var(--ncp-primary)', 'outline-offset': '2px' })
    expect(declarations('.el-drawer__close-btn')).toEqual({})
    expect(declarations('.el-drawer__close-btn:focus-visible')).toEqual({})
  })
})
