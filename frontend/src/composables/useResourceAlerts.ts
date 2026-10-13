import { onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import { requestResourceAlerts } from '@/api/control'
import { NcpApiError } from '@/api/system'
import type { ResourceAlerts } from '@/domain/monitoring/alerts'
import { captureSession, isAbortError, isCurrentSession } from '@/session/sessionLifecycle'

/** 每次请求结束后才排下一次；历史筛选与此生命周期无关。 */
export function useResourceAlerts() {
  const snapshot = shallowRef<ResourceAlerts | null>(null)
  const loading = ref(true)
  const errorCode = ref('')
  const now = ref(Date.now())
  let lastMonotonic = performance.now()
  const session = captureSession()
  let disposed = false
  let timer: ReturnType<typeof setTimeout> | undefined
  let freshnessTimer: ReturnType<typeof setInterval> | undefined
  let controller: AbortController | undefined
  let active: Promise<void> | undefined
  let refreshWhenSettled = false
  function allowed() { return !disposed && !session.signal.aborted && isCurrentSession(session.generation) && document.visibilityState !== 'hidden' }
  function clearPoll() { clearTimeout(timer); timer = undefined }
  function stopFreshness() { clearInterval(freshnessTimer); freshnessTimer = undefined }
  function updateNow() {
    const monotonic = performance.now()
    const elapsed = Math.max(0, monotonic - lastMonotonic)
    lastMonotonic = monotonic
    // 本地墙钟后退不能让旧数据重新变绿，也不能暂停数据年龄的增长。
    now.value = Math.max(now.value + elapsed, Date.now())
  }
  function startFreshness() {
    updateNow()
    if (!freshnessTimer) freshnessTimer = setInterval(updateNow, 1000)
  }
  function refresh(): Promise<void> {
    if (!allowed()) return Promise.resolve()
    if (active) return active
    clearPoll()
    const owner = new AbortController()
    controller = owner
    loading.value = true
    active = (async () => {
      try {
        const result = await requestResourceAlerts(owner.signal)
        if (!allowed() || owner.signal.aborted || controller !== owner) return
        snapshot.value = result
        updateNow()
        errorCode.value = ''
      } catch (error) {
        if (!allowed() || owner.signal.aborted || controller !== owner || isAbortError(error)) return
        errorCode.value = error instanceof NcpApiError ? error.code : 'MONITORING_ALERTS_READ_FAILED'
      } finally {
        if (controller === owner) {
          controller = undefined
          active = undefined
          loading.value = false
          if (allowed()) {
            if (refreshWhenSettled) { refreshWhenSettled = false; void refresh() }
            else timer = setTimeout(() => { void refresh() }, 5000)
          }
        }
      }
    })()
    return active
  }
  function visibilityChanged() {
    if (!allowed()) { clearPoll(); stopFreshness(); controller?.abort(); return }
    startFreshness()
    if (active) refreshWhenSettled = true
    else void refresh()
  }
  function sessionEnded() {
    clearPoll(); stopFreshness(); controller?.abort()
    snapshot.value = null
    errorCode.value = ''
    loading.value = false
  }
  onMounted(() => {
    session.signal.addEventListener('abort', sessionEnded, { once: true })
    document.addEventListener('visibilitychange', visibilityChanged)
    if (allowed()) { startFreshness(); void refresh() }
    else loading.value = false
  })
  onBeforeUnmount(() => {
    disposed = true
    clearPoll(); stopFreshness(); controller?.abort()
    session.signal.removeEventListener('abort', sessionEnded)
    document.removeEventListener('visibilitychange', visibilityChanged)
  })
  return { snapshot, loading, errorCode, now, refresh }
}
