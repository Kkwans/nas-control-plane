import { vi } from 'vitest'

export function mockReducedMotion(initial = false) {
  const listeners = new Set<(event: MediaQueryListEvent) => void>()
  const media = {
    matches: initial,
    addEventListener: vi.fn((_type: string, listener: (event: MediaQueryListEvent) => void) => listeners.add(listener)),
    removeEventListener: vi.fn((_type: string, listener: (event: MediaQueryListEvent) => void) => listeners.delete(listener)),
  }
  vi.stubGlobal('matchMedia', vi.fn(() => media))
  return {
    media,
    change(matches: boolean) {
      media.matches = matches
      for (const listener of listeners) listener({ matches } as MediaQueryListEvent)
    },
  }
}
