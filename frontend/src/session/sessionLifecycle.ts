export interface SessionContext {
  generation: number
  signal: AbortSignal
}

let sessionGeneration = 0
let sessionController = new AbortController()

function currentContext(): SessionContext {
  return { generation: sessionGeneration, signal: sessionController.signal }
}

/** Start a new authenticated session and invalidate all work from the prior one. */
export function beginSession(): SessionContext {
  sessionController.abort()
  sessionController = new AbortController()
  sessionGeneration += 1
  return currentContext()
}

/** Invalidate session-scoped requests before local state is cleared. */
export function invalidateSession(): number {
  sessionController.abort()
  sessionController = new AbortController()
  sessionGeneration += 1
  return sessionGeneration
}

export function captureSession(): SessionContext {
  return currentContext()
}

export function isCurrentSession(generation: number): boolean {
  return generation === sessionGeneration
}

export function isAbortError(error: unknown): boolean {
  return (typeof DOMException !== 'undefined' && error instanceof DOMException && error.name === 'AbortError') ||
    (error instanceof Error && error.name === 'AbortError')
}
