import { describe, expect, it } from 'vitest'

import {
  BRACKETED_PASTE_END,
  BRACKETED_PASTE_START,
  createBracketedPaste,
  createTerminalPastePayload,
  isTerminalPasteShortcut,
  normalizeTerminalPaste,
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
