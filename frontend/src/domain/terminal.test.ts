import { describe, expect, it } from 'vitest'

import {
  BRACKETED_PASTE_END,
  BRACKETED_PASTE_START,
  createBracketedPaste,
  createTerminalPastePayload,
  describeTerminalCapability,
  formatTerminalEnhancement,
  formatTerminalShell,
  getTerminalCapabilityState,
  isMultilineTerminalPaste,
  isTerminalLiteralNextShortcut,
  isTerminalPasteShortcut,
  normalizeTerminalCapabilities,
  normalizeTerminalPaste,
  supportsSafeMultilinePaste,
  terminalShortcuts,
} from './terminal'

describe('terminal paste helpers', () => {
  it('normalizes Windows and classic Mac line endings', () => {
    expect(normalizeTerminalPaste('one\r\ntwo\rthree\nfour')).toBe('one\ntwo\nthree\nfour')
  })

  it('wraps multiline clipboard content with bracketed paste markers', () => {
    expect(createBracketedPaste('echo one\r\necho two\n')).toBe(
      `${BRACKETED_PASTE_START}echo one\recho two\r${BRACKETED_PASTE_END}`,
    )
  })

  it('does not leak bracketed-paste markers when the shell has not enabled the mode', () => {
    expect(createTerminalPastePayload('echo one\n echo two', false)).toBe('echo one\r echo two')
    expect(createTerminalPastePayload('echo one\n echo two', true)).toContain(BRACKETED_PASTE_START)
  })

  it('recognizes Windows/Linux and macOS paste shortcuts', () => {
    expect(isTerminalPasteShortcut({ key: 'v', ctrlKey: true, metaKey: false, altKey: false })).toBe(true)
    expect(isTerminalPasteShortcut({ key: 'V', ctrlKey: false, metaKey: true, altKey: false })).toBe(true)
    expect(isTerminalPasteShortcut({ key: 'v', ctrlKey: true, metaKey: false, altKey: true })).toBe(false)
    expect(isTerminalPasteShortcut({ key: 'c', ctrlKey: true, metaKey: false, altKey: false })).toBe(false)
  })

  it('moves the terminal literal-next behavior from Ctrl+V to Ctrl+Q', () => {
    expect(isTerminalLiteralNextShortcut({ key: 'q', ctrlKey: true, metaKey: false, altKey: false })).toBe(true)
    expect(isTerminalLiteralNextShortcut({ key: 'Q', ctrlKey: true, metaKey: false, altKey: false, shiftKey: true })).toBe(false)
    expect(isTerminalLiteralNextShortcut({ key: 'q', ctrlKey: false, metaKey: true, altKey: false })).toBe(false)
  })

  it('keeps multiline paste and capability gaps explicit', () => {
    expect(isMultilineTerminalPaste('one\r\ntwo')).toBe(true)
    expect(isMultilineTerminalPaste('one line')).toBe(false)

    const capabilities = normalizeTerminalCapabilities({
      readline: true,
      bracketedPaste: true,
      multilinePaste: true,
      ansiColors: false,
      ignored: true,
    })
    expect(capabilities).toEqual({ readline: true, bracketedPaste: true, multilinePaste: true, ansiColors: false })
    expect(supportsSafeMultilinePaste(capabilities)).toBe(true)
    expect(getTerminalCapabilityState(capabilities, 'ansiColors')).toBe('unsupported')
    expect(getTerminalCapabilityState(capabilities, 'resize')).toBe('unknown')
    expect(describeTerminalCapability(undefined)).toBe('未报告')
    expect(describeTerminalCapability(false)).toBe('不支持')
    expect(normalizeTerminalCapabilities(undefined)).toBeNull()
    expect(supportsSafeMultilinePaste({ bracketedPaste: true })).toBe(false)
  })

  it('labels the active shell and enhancement without inventing host ble.sh', () => {
    expect(formatTerminalShell('bash')).toBe('bash')
    expect(formatTerminalShell('')).toBe('未报告')
    expect(formatTerminalEnhancement('blesh')).toBe('ble.sh')
    expect(formatTerminalEnhancement('readline')).toBe('readline')
    expect(formatTerminalEnhancement('native')).toBe('原生 Shell')
    expect(formatTerminalEnhancement(undefined)).toBe('未报告')
  })

  it('keeps the documented shortcut list complete and user-facing', () => {
    expect(terminalShortcuts.map((shortcut) => shortcut.keys)).toEqual([
      'Ctrl+V / ⌘V',
      'Ctrl+Shift+V / ⌘Shift+V',
      'Ctrl+Q',
      'Enter',
      'Ctrl+M',
      'Ctrl+J',
      'Ctrl+C',
      'Ctrl+L',
      'Tab',
      '↑ / ↓',
      'Ctrl+R',
      'Ctrl+A / Home',
      'Ctrl+E / End',
      'Ctrl+U',
      'Ctrl+W',
      'Ctrl+D',
    ])
  })
})
