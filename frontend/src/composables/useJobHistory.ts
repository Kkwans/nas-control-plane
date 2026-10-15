import { onScopeDispose, ref, shallowRef, watch } from 'vue'
import { requestJobHistory, type JobHistoryPage, type JobHistoryQuery } from '@/api/jobHistory'
import { NcpApiError } from '@/api/systemTransport'
import { captureSession, isCurrentSession } from '@/session/sessionLifecycle'

export function useJobHistory(onUnauthorized: () => void = () => {}, active: () => boolean = () => true) {
  const page = shallowRef<JobHistoryPage | null>(null)
  const query = shallowRef<Readonly<JobHistoryQuery>>(Object.freeze({ type: '', limit: 50 }))
  const loading = ref(false)
  const error = shallowRef<NcpApiError | null>(null)
  const session = captureSession()
  let generation = 0, disposed = false
  let controller: AbortController | undefined
  let failed: Readonly<JobHistoryQuery> | undefined
  const alive = (owner: number) => !disposed && active() && !session.signal.aborted && isCurrentSession(session.generation) && owner === generation
  function invalidate() { generation++; controller?.abort(); controller = undefined; loading.value = false }
  async function read(target: Readonly<JobHistoryQuery>) {
    if (disposed || !active() || session.signal.aborted || loading.value) return
    const owner = generation
    controller = new AbortController()
    loading.value = true
    error.value = null
    try {
      const result = await requestJobHistory(target, controller.signal)
      if (!alive(owner)) return
      page.value = result
      query.value = target
      failed = undefined
    } catch (caught) {
      if (!alive(owner)) return
      failed = target
      error.value = caught instanceof NcpApiError ? caught : new NcpApiError('JOB_HISTORY_UNAVAILABLE', '已保存历史暂时无法读取，请重试。')
      if (error.value.code === 'AUTH_UNAUTHORIZED') onUnauthorized()
    } finally {
      // 旧读取的 finally 不得释放新代次的单飞门闩。
      if (alive(owner)) { loading.value = false; controller = undefined }
    }
  }
  function latest(type = query.value.type, limit = query.value.limit) {
    invalidate()
    page.value = null
    failed = undefined
    query.value = Object.freeze({ type, limit })
    void read(query.value)
  }
  function next() {
    if (loading.value || failed || !page.value?.nextCursor) return
    void read(Object.freeze({ ...query.value, cursor: page.value.nextCursor }))
  }
  function retry() { if (failed && error.value?.code !== 'JOB_HISTORY_UNSUPPORTED' && error.value?.code !== 'AUTH_UNAUTHORIZED') void read(failed) }
  function dispose() { disposed = true; invalidate(); page.value = null; error.value = null; failed = undefined; session.signal.removeEventListener('abort', dispose) }
  session.signal.addEventListener('abort', dispose, { once: true })
  watch(active, value => { if (!value) dispose() }, { flush: 'sync' })
  onScopeDispose(dispose)
  latest()
  return { page, query, loading, error, latest, next, retry }
}
