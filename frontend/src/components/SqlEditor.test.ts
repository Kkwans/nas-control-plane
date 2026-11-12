// @vitest-environment jsdom

import { EditorView } from 'codemirror'
import { createApp, h, nextTick, reactive, type App } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'

import SqlEditor from './SqlEditor.vue'

const mounted: { app: App; host: HTMLDivElement }[] = []

function mountEditor(disabled = false) {
  const state = reactive({ modelValue: 'SELECT 1;', disabled, generation: 0 })
  const update = vi.fn((value: string) => { state.modelValue = value })
  const execute = vi.fn()
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render: () => h(SqlEditor, {
      key: state.generation,
      modelValue: state.modelValue,
      disabled: state.disabled,
      'onUpdate:modelValue': update,
      onExecute: execute,
    }),
  })
  app.mount(host)
  mounted.push({ app, host })
  const view = () => EditorView.findFromDOM(host.querySelector<HTMLElement>('.cm-content')!)!
  return { state, host, view, update, execute }
}

function expectNamedTextbox(view: EditorView) {
  expect(view.contentDOM.classList.contains('cm-content')).toBe(true)
  expect(view.contentDOM.getAttribute('role')).toBe('textbox')
  expect(view.contentDOM.getAttribute('aria-label')).toBe('SQL 编辑器')
  expect(view.contentDOM.getAttribute('aria-multiline')).toBe('true')
}

function expectEditingState(view: EditorView, disabled: boolean) {
  expect(view.state.facet(EditorView.editable)).toBe(!disabled)
  expect(view.state.readOnly).toBe(disabled)
  expect(view.contentDOM.getAttribute('contenteditable')).toBe(String(!disabled))
  expect(view.contentDOM.getAttribute('aria-readonly')).toBe(disabled ? 'true' : null)
  expectNamedTextbox(view)
}

function dispatchEditorInput(view: EditorView, action: 'Enter' | 'Backspace' | 'paste' | 'undo') {
  if (action === 'paste') {
    const event = new Event('paste', { bubbles: true, cancelable: true })
    Object.defineProperty(event, 'clipboardData', {
      value: { getData: (type: string) => type === 'text/plain' ? 'INSERTED' : '' },
    })
    view.contentDOM.dispatchEvent(event)
    return
  }
  view.contentDOM.dispatchEvent(new KeyboardEvent('keydown', {
    key: action === 'undo' ? 'z' : action,
    code: action === 'undo' ? 'KeyZ' : action,
    ctrlKey: action === 'undo',
    bubbles: true,
    cancelable: true,
  }))
}

afterEach(() => {
  for (const { app, host } of mounted.splice(0)) {
    app.unmount()
    host.remove()
  }
})

// 使用真实 CodeMirror，避免仅断言外层 group 而遗漏实际 textbox 的可访问名称。
describe('SqlEditor 实际编辑区域', () => {
  it.each([false, true])('初始 disabled=%s 的 textbox 有可访问名称', (disabled) => {
    const { view } = mountEditor(disabled)
    expectNamedTextbox(view())
    expect(view().contentDOM.getAttribute('contenteditable')).toBe(String(!disabled))
  })

  it('外部值更新和编辑器事务均保留 textbox 名称及双向绑定', async () => {
    const { state, view, update } = mountEditor()
    const original = view()
    state.modelValue = 'SELECT 2;\nSELECT 3;'
    await nextTick()
    expect(view()).toBe(original)
    expect(view().state.doc.toString()).toBe(state.modelValue)
    expectNamedTextbox(view())

    view().dispatch({ changes: { from: 0, to: view().state.doc.length, insert: 'SELECT 4;' } })
    await nextTick()
    expect(state.modelValue).toBe('SELECT 4;')
    expect(update).toHaveBeenLastCalledWith('SELECT 4;')
    expectNamedTextbox(view())
  })

  it.each([false, true])('初始 disabled=%s 后反复切换和更新文档仍保留名称', async (disabled) => {
    const { state, host, view } = mountEditor(disabled)
    for (const nextDisabled of [!disabled, disabled, !disabled]) {
      state.disabled = nextDisabled
      await nextTick()
      expect(host.querySelector('[role="group"]')!.getAttribute('aria-disabled')).toBe(String(nextDisabled))
      expectNamedTextbox(view())
      state.modelValue += '\nSELECT 2;'
      await nextTick()
      expect(view().state.doc.toString()).toBe(state.modelValue)
      expectNamedTextbox(view())
    }
  })

  it('重新挂载后新建的真实编辑器仍有名称', async () => {
    const { state, view } = mountEditor()
    const original = view()
    state.generation++
    state.disabled = true
    state.modelValue = 'SHOW TABLES;'
    await nextTick()
    expect(view()).not.toBe(original)
    expect(original.dom.isConnected).toBe(false)
    expect(view().state.doc.toString()).toBe('SHOW TABLES;')
    expectNamedTextbox(view())
  })

  it.each(['ctrlKey', 'metaKey'] as const)('%s+Enter 执行一次且不插入换行', async (modifier) => {
    const { state, view, update, execute } = mountEditor()
    const event = new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', [modifier]: true, bubbles: true, cancelable: true })
    view().contentDOM.dispatchEvent(event)
    await nextTick()
    expect({ executions: execute.mock.calls.length, document: view().state.doc.toString() })
      .toEqual({ executions: 1, document: 'SELECT 1;' })
    expect(event.defaultPrevented).toBe(true)
    expect(view().state.doc.toString()).toBe('SELECT 1;')
    expect(state.modelValue).toBe('SELECT 1;')
    expect(update).not.toHaveBeenCalled()
  })

  it.each(['ctrlKey', 'metaKey'] as const)('禁用时 %s+Enter 不执行且不修改文档，解除后恢复', async (modifier) => {
    const { state, view, update, execute } = mountEditor(true)
    for (const disabled of [true, false, true]) {
      state.disabled = disabled
      await nextTick()
      execute.mockClear()
      const event = new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', [modifier]: true, bubbles: true, cancelable: true })
      view().contentDOM.dispatchEvent(event)
      await nextTick()
      expect(execute).toHaveBeenCalledTimes(disabled ? 0 : 1)
      expect(event.defaultPrevented).toBe(true)
      expect(view().state.doc.toString()).toBe('SELECT 1;')
      expect(state.modelValue).toBe('SELECT 1;')
      expect(update).not.toHaveBeenCalled()
      expectNamedTextbox(view())
    }
  })

  it('普通 Enter 保留换行编辑行为而不执行 SQL', async () => {
    const { state, view, execute } = mountEditor()
    view().dispatch({ selection: { anchor: view().state.doc.length } })
    const event = new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', bubbles: true, cancelable: true })
    view().contentDOM.dispatchEvent(event)
    await nextTick()
    expect(view().state.doc.toString()).toBe('SELECT 1;\n')
    expect(state.modelValue).toBe('SELECT 1;\n')
    expect(execute).not.toHaveBeenCalled()
    expectNamedTextbox(view())
  })

  describe.each([false, true])('初始 disabled=%s 的禁用编辑保护', (initiallyDisabled) => {
    it.each(['Enter', 'Backspace', 'paste', 'undo'] as const)('禁用时 %s 不修改文档或双向绑定', async (action) => {
      const { state, view, update, execute } = mountEditor(initiallyDisabled)
      // 保留一条可撤销的程序化更新，验证禁用只阻止用户编辑。
      view().dispatch({ changes: { from: view().state.doc.length, insert: '\n' } })
      await nextTick()
      view().dispatch({ selection: { anchor: view().state.doc.length } })
      state.disabled = true
      await nextTick()
      expectEditingState(view(), true)
      update.mockClear()

      dispatchEditorInput(view(), action)
      await nextTick()
      expect(view().state.doc.toString()).toBe('SELECT 1;\n')
      expect(state.modelValue).toBe('SELECT 1;\n')
      expect(update).not.toHaveBeenCalled()
      expect(execute).not.toHaveBeenCalled()
      expectEditingState(view(), true)
    })
  })

  it.each([false, true])('初始 disabled=%s 后反复切换会同步真实编辑状态并恢复输入', async (disabled) => {
    const { state, view, update } = mountEditor(disabled)
    const original = view()
    expectEditingState(view(), disabled)
    for (const nextDisabled of [true, false, true, false]) {
      state.disabled = nextDisabled
      await nextTick()
      expectEditingState(view(), nextDisabled)

      state.modelValue = `SELECT ${nextDisabled ? 2 : 3};`
      await nextTick()
      expect(view().state.doc.toString()).toBe(state.modelValue)
      expectEditingState(view(), nextDisabled)
      view().dispatch({ selection: { anchor: view().state.doc.length } })
      update.mockClear()
      dispatchEditorInput(view(), 'Enter')
      await nextTick()
      expect(view().state.doc.toString()).toBe(nextDisabled ? 'SELECT 2;' : 'SELECT 3;\n')
      expect(update).toHaveBeenCalledTimes(nextDisabled ? 0 : 1)
      expect(state.modelValue).toBe(view().state.doc.toString())
      expect(view()).toBe(original)
      expectEditingState(view(), nextDisabled)
    }
  })

  it('解除初始禁用后恢复退格和粘贴编辑', async () => {
    const { state, view } = mountEditor(true)
    state.disabled = false
    await nextTick()
    expectEditingState(view(), false)
    view().dispatch({ selection: { anchor: view().state.doc.length } })
    dispatchEditorInput(view(), 'Backspace')
    await nextTick()
    expect(state.modelValue).toBe('SELECT 1')
    dispatchEditorInput(view(), 'paste')
    await nextTick()
    expect(state.modelValue).toBe('SELECT 1INSERTED')
  })

  it('禁用切换保留撤销历史，禁用时不撤销而解除后恢复', async () => {
    const { state, view } = mountEditor()
    view().dispatch({ selection: { anchor: view().state.doc.length } })
    dispatchEditorInput(view(), 'Enter')
    await nextTick()
    expect(state.modelValue).toBe('SELECT 1;\n')
    state.disabled = true
    await nextTick()
    dispatchEditorInput(view(), 'undo')
    await nextTick()
    expect(state.modelValue).toBe('SELECT 1;\n')
    state.disabled = false
    await nextTick()
    dispatchEditorInput(view(), 'undo')
    await nextTick()
    expect(state.modelValue).toBe('SELECT 1;')
  })
})
