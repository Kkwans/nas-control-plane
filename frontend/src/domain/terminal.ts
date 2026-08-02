export const BRACKETED_PASTE_START = '\u001b[200~'
export const BRACKETED_PASTE_END = '\u001b[201~'

export type TerminalShortcutEvent = Pick<KeyboardEvent, 'key' | 'ctrlKey' | 'metaKey' | 'altKey'>

/**
 * xterm and readline use carriage return for a pasted line ending. Keeping
 * this conversion in one place makes browser clipboard, context-menu paste
 * and keyboard paste behave identically.
 */
export function normalizeTerminalPaste(text: string): string {
  return text.replace(/\r\n?/g, '\n')
}

export function createBracketedPaste(text: string): string {
  const normalized = normalizeTerminalPaste(text).replace(/\n/g, '\r')
  return `${BRACKETED_PASTE_START}${normalized}${BRACKETED_PASTE_END}`
}

export function createTerminalPastePayload(text: string, bracketedPasteMode: boolean): string {
  if (bracketedPasteMode) return createBracketedPaste(text)
  return normalizeTerminalPaste(text).replace(/\n/g, '\r')
}

export function isTerminalPasteShortcut(event: TerminalShortcutEvent): boolean {
  return (event.ctrlKey || event.metaKey) && !event.altKey && event.key.toLowerCase() === 'v'
}

export const terminalShortcuts = [
  { keys: 'Ctrl+V / ⌘V', action: '粘贴系统剪贴板内容' },
  { keys: 'Ctrl+Shift+V / ⌘Shift+V', action: '粘贴系统剪贴板内容（兼容快捷键）' },
  { keys: 'Ctrl+Q', action: '按字面插入下一个按键（替代原 Ctrl+V）' },
  { keys: 'Enter', action: '执行单行命令；多行粘贴模式下插入换行' },
  { keys: 'Ctrl+M', action: '多行粘贴模式下插入换行' },
  { keys: 'Ctrl+J', action: '执行多行粘贴内容' },
  { keys: 'Ctrl+C', action: '中断正在运行的命令；编辑命令时取消当前输入' },
  { keys: 'Ctrl+L', action: '请求 Shell 清空当前屏幕' },
  { keys: 'Tab', action: '命令、路径和参数补全' },
  { keys: '↑ / ↓', action: '浏览历史命令' },
  { keys: 'Ctrl+R', action: '反向搜索历史命令' },
  { keys: 'Ctrl+A / Home', action: '移动到当前命令开头' },
  { keys: 'Ctrl+E / End', action: '移动到当前命令末尾' },
  { keys: 'Ctrl+U', action: '删除光标前的整段输入' },
  { keys: 'Ctrl+W', action: '删除光标前的一个单词' },
  { keys: 'Ctrl+D', action: '删除光标下字符；空输入时结束 Shell' },
] as const
