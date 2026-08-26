import { describe, expect, it } from 'vitest'

import { getTerminalTheme, resolveTerminalTheme } from './terminalTheme'

describe('terminal theme adapter', () => {
  it('resolves terminal colors from shared CSS tokens', () => {
    const theme = resolveTerminalTheme({
      getPropertyValue: (name) => name === '--ncp-terminal-background' ? 'rgb(1, 2, 3)' : '',
    })

    expect(theme.background).toBe('rgb(1, 2, 3)')
    expect(theme.foreground).toBe('#25354b')
    expect(theme.brightWhite).toBe('#ffffff')
  })

  it('has a browser-safe fallback when computed styles are unavailable', () => {
    expect(getTerminalTheme().background).toBeTruthy()
  })
})
