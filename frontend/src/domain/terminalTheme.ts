export interface TerminalTheme {
  background: string
  foreground: string
  cursor: string
  cursorAccent: string
  selectionBackground: string
  black: string
  brightBlack: string
  red: string
  brightRed: string
  green: string
  brightGreen: string
  yellow: string
  brightYellow: string
  blue: string
  brightBlue: string
  magenta: string
  brightMagenta: string
  cyan: string
  brightCyan: string
  white: string
  brightWhite: string
}

const fallbackTheme: TerminalTheme = {
  background: '#fbfcfe',
  foreground: '#25354b',
  cursor: '#3474d4',
  cursorAccent: '#fbfcfe',
  selectionBackground: '#dbe9fb',
  black: '#263548',
  brightBlack: '#718096',
  red: '#c95361',
  brightRed: '#df6673',
  green: '#23866f',
  brightGreen: '#36a287',
  yellow: '#b87622',
  brightYellow: '#cf933a',
  blue: '#3474d4',
  brightBlue: '#5792e5',
  magenta: '#875eae',
  brightMagenta: '#a477c9',
  cyan: '#23828d',
  brightCyan: '#3d9da7',
  white: '#dfe6ef',
  brightWhite: '#ffffff',
}

type ThemeStyle = Pick<CSSStyleDeclaration, 'getPropertyValue'>

/** Resolve terminal colors from the shared design tokens with stable fallbacks. */
export function resolveTerminalTheme(style?: ThemeStyle): TerminalTheme {
  const token = (name: string, fallback: string) => style?.getPropertyValue(name).trim() || fallback
  return {
    background: token('--ncp-terminal-background', fallbackTheme.background),
    foreground: token('--ncp-terminal-foreground', fallbackTheme.foreground),
    cursor: token('--ncp-terminal-cursor', fallbackTheme.cursor),
    cursorAccent: token('--ncp-terminal-cursor-accent', fallbackTheme.cursorAccent),
    selectionBackground: token('--ncp-terminal-selection', fallbackTheme.selectionBackground),
    black: token('--ncp-terminal-black', fallbackTheme.black),
    brightBlack: token('--ncp-terminal-bright-black', fallbackTheme.brightBlack),
    red: token('--ncp-terminal-red', fallbackTheme.red),
    brightRed: token('--ncp-terminal-bright-red', fallbackTheme.brightRed),
    green: token('--ncp-terminal-green', fallbackTheme.green),
    brightGreen: token('--ncp-terminal-bright-green', fallbackTheme.brightGreen),
    yellow: token('--ncp-terminal-yellow', fallbackTheme.yellow),
    brightYellow: token('--ncp-terminal-bright-yellow', fallbackTheme.brightYellow),
    blue: token('--ncp-terminal-blue', fallbackTheme.blue),
    brightBlue: token('--ncp-terminal-bright-blue', fallbackTheme.brightBlue),
    magenta: token('--ncp-terminal-magenta', fallbackTheme.magenta),
    brightMagenta: token('--ncp-terminal-bright-magenta', fallbackTheme.brightMagenta),
    cyan: token('--ncp-terminal-cyan', fallbackTheme.cyan),
    brightCyan: token('--ncp-terminal-bright-cyan', fallbackTheme.brightCyan),
    white: token('--ncp-terminal-white', fallbackTheme.white),
    brightWhite: token('--ncp-terminal-bright-white', fallbackTheme.brightWhite),
  }
}

export function getTerminalTheme(): TerminalTheme {
  if (typeof document === 'undefined') return resolveTerminalTheme()
  return resolveTerminalTheme(getComputedStyle(document.documentElement))
}
