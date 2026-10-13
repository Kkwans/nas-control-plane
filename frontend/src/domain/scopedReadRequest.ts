import { createRequestSequenceGate } from './requestSequence'
import { captureSession, isCurrentSession } from '../session/sessionLifecycle'

/** 只读请求的对象/会话边界；取消不代表服务端操作已取消。 */
export function createScopedReadRequest() {
  const gate = createRequestSequenceGate()
  let cancel: (() => void) | undefined
  function invalidate() {
    gate.invalidate()
    cancel?.()
    cancel = undefined
  }
  function begin() {
    invalidate()
    const sequence = gate.begin()
    const session = captureSession()
    const controller = new AbortController()
    const abort = () => controller.abort()
    session.signal.addEventListener('abort', abort, { once: true })
    if (session.signal.aborted) abort()
    const release = () => session.signal.removeEventListener('abort', abort)
    cancel = () => { abort(); release() }
    return {
      signal: controller.signal,
      isCurrent: () => gate.isLatest(sequence) && isCurrentSession(session.generation) && !controller.signal.aborted,
      finish: release,
    }
  }
  return { begin, invalidate }
}
