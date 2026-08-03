export const BRACKETED_PASTE_START = '\u001b[200~'
export const BRACKETED_PASTE_END = '\u001b[201~'

export interface TerminalCapabilities {
  resize?: boolean
  readline?: boolean
  bracketedPaste?: boolean
  multilinePaste?: boolean
  ansiColors?: boolean
}

export type TerminalCapabilityKey = keyof TerminalCapabilities
export type TerminalCapabilityState = 'supported' | 'unsupported' | 'unknown'

export type TerminalShortcutEvent = Pick<KeyboardEvent, 'key' | 'ctrlKey' | 'metaKey' | 'altKey'> &
  Partial<Pick<KeyboardEvent, 'shiftKey'>>

const terminalCapabilityKeys: readonly TerminalCapabilityKey[] = [
  'resize',
  'readline',
  'bracketedPaste',
  'multilinePaste',
  'ansiColors',
]

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

/**
 * Started capabilities are optional for compatibility with older agents. Keep
 * omitted values as `undefined` so the UI can distinguish "not reported" from
 * an explicit capability denial.
 */
export function normalizeTerminalCapabilities(value: unknown): TerminalCapabilities | null {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return null

  const input = value as Record<string, unknown>
  const normalized: TerminalCapabilities = {}
  for (const key of terminalCapabilityKeys) {
    const capability = input[key]
    if (typeof capability === 'boolean') normalized[key] = capability
  }
  return normalized
}

export function getTerminalCapabilityState(
  capabilities: TerminalCapabilities | null | undefined,
  key: TerminalCapabilityKey,
): TerminalCapabilityState {
  const value = capabilities?.[key]
  if (value === true) return 'supported'
  if (value === false) return 'unsupported'
  return 'unknown'
}

export function describeTerminalCapability(value: boolean | undefined): string {
  if (value === true) return '支持'
  if (value === false) return '不支持'
  return '未报告'
}

export function formatTerminalShell(shell: string | undefined): string {
  return shell?.trim() || '未报告'
}

export function formatTerminalEnhancement(enhancement: string | undefined): string {
  const normalized = enhancement?.trim().toLowerCase() ?? ''
  if (normalized === 'blesh') return 'ble.sh'
  if (normalized === 'readline') return 'readline'
  if (normalized === 'native') return '原生 Shell'
  if (normalized === 'unsupported') return '不支持'
  return enhancement?.trim() || '未报告'
}

export function supportsSafeMultilinePaste(capabilities: TerminalCapabilities | null | undefined): boolean {
  return capabilities?.bracketedPaste === true && capabilities.multilinePaste === true
}

export function isMultilineTerminalPaste(text: string): boolean {
  return normalizeTerminalPaste(text).includes('\n')
}

export function isTerminalPasteShortcut(event: TerminalShortcutEvent): boolean {
  return (event.ctrlKey || event.metaKey) && !event.altKey && event.key.toLowerCase() === 'v'
}

/**
 * Ctrl+V is reserved for browser paste. Send its terminal literal-next
 * control byte from Ctrl+Q instead, so the old readline behavior remains
 * available without competing with the browser clipboard shortcut.
 */
export function isTerminalLiteralNextShortcut(event: TerminalShortcutEvent): boolean {
  return event.ctrlKey && !event.metaKey && !event.altKey && !event.shiftKey && event.key.toLowerCase() === 'q'
}

export const terminalShortcuts = [
  { keys: 'Ctrl+V / ⌘V', action: '由浏览器 paste 事件读取剪贴板并写入 PTY' },
  { keys: 'Ctrl+Shift+V / ⌘Shift+V', action: '由浏览器 paste 事件读取剪贴板（兼容快捷键）' },
  { keys: 'Ctrl+Q', action: '发送原 Ctrl+V 的 literal-next 控制字符' },
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
