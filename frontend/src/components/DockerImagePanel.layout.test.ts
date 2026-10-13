import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { compileStyle, parse } from 'vue/compiler-sfc'

const filename = 'DockerImagePanel.vue'
const source = readFileSync(new URL(`./${filename}`, import.meta.url), 'utf8')
const { descriptor } = parse(source, { filename })
const styles = descriptor.styles.map((style) => compileStyle({ filename, id: 'data-v-image-test', source: style.content, scoped: true }))
const toolbarQuery = 'image-workspace (max-width:980px) or (max-width:61.25rem)'
const cardQuery = 'image-downloads (max-width:980px) or (max-width:61.25rem)'

function declarations(selector: string, container?: string) {
  const values: Record<string, string> = {}
  for (const style of styles) {
    style.rawResult!.root.walkRules((rule) => {
      if (!rule.selectors.includes(selector)) return
      const parent = rule.parent
      if (container ? parent?.type !== 'atrule' || parent.name !== 'container' || parent.params !== container : parent?.type !== 'root') return
      rule.walkDecls((declaration) => { values[declaration.prop] = declaration.value })
    })
  }
  return values
}

// JSDOM 不计算布局；这里验证实际编译后的作用域和降级规则，像素几何由只读 E2E fixture 覆盖。
describe('DockerImagePanel 工具栏响应式样式', () => {
  it('样式编译成功，工具栏按可用面板空间与字号切换，保留下载卡片的双阈值', () => {
    expect(styles.flatMap((style) => style.errors)).toEqual([])
    expect(declarations('.image-workspace[data-v-image-test]').container).toBe('image-workspace / inline-size')
    expect(declarations('.image-modebar[data-v-image-test]', toolbarQuery)).toMatchObject({ 'flex-direction': 'column', 'align-items': 'stretch' })
    expect(declarations('.download-workspace[data-v-image-test]').container).toBe('image-downloads / inline-size')
    expect(declarations('.download-head[data-v-image-test]', cardQuery).display).toBe('none')
    expect(declarations('.download-row[data-v-image-test]', cardQuery)['grid-template-columns']).toBe('minmax(0, 1fr)')
  })

  it('深层选择器命中真实 SectionTabs 子元素，标签和计数完整换行而非隐藏滚动', () => {
    expect(declarations('.image-modebar .section-tabs[data-v-image-test]')).toMatchObject({ 'min-width': '0', 'flex-wrap': 'wrap', overflow: 'visible' })
    expect(declarations('.image-modebar[data-v-image-test] .section-tabs button')).toMatchObject({ 'min-width': '0', 'max-width': '100%', 'white-space': 'normal' })
    expect(declarations('.image-modebar[data-v-image-test] .section-tabs button>span')['overflow-wrap']).toBe('anywhere')
    expect(declarations('.image-modebar[data-v-image-test] .section-tabs button>small').flex).toBe('0 0 auto')
    expect(declarations('.image-modebar[data-v-image-test] .section-tabs button', toolbarQuery)).toMatchObject({ flex: '1 1 auto', 'min-height': '44px' })
  })

  it('搜索、状态和刷新使用可换行布局，状态文本随字号扩展且不截断', () => {
    expect(declarations('.mode-actions[data-v-image-test]')).toMatchObject({ 'min-width': '0', 'max-width': '100%', 'flex-wrap': 'wrap' })
    expect(declarations('.mode-actions[data-v-image-test] .el-input')).toMatchObject({ 'min-width': 'min(100%, 11.25rem)', flex: '1 1 11.25rem' })
    expect(declarations('.download-mode-actions[data-v-image-test] .ncp-select')).toMatchObject({ width: 'max(150px, 9.375rem)', 'min-width': '0', flex: '0 1 max(150px, 9.375rem)' })
    expect(declarations('.download-mode-actions[data-v-image-test] .el-select__placeholder')).toMatchObject({ position: 'static', transform: 'none', 'white-space': 'normal', overflow: 'visible', 'overflow-wrap': 'anywhere', 'text-overflow': 'clip' })
    expect(declarations('.mode-actions .icon-button[data-v-image-test]', toolbarQuery)).toMatchObject({ width: '44px', 'min-height': '44px', 'flex-basis': '44px' })
  })
})
