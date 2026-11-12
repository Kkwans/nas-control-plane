import { ref } from 'vue'
import { followLogs, requestLogs, type LogEntry, type LogResponse } from '../api/control'
import { createScopedReadRequest } from '../domain/scopedReadRequest'
import { createRequestSequenceGate } from '../domain/requestSequence'
import { mergeLogEntries } from '../domain/logEntries'
import { captureSession, isCurrentSession, isAbortError } from '../session/sessionLifecycle'

export type FollowState = 'paused' | 'connecting' | 'connected' | 'reconnecting' | 'failed'
type LogQuery = Parameters<typeof requestLogs>[0]

export function useLogQuerySession(api = { requestLogs, followLogs }) {
  const entries = ref<LogEntry[]>([])
  const nextCursor = ref('')
  const loading = ref(false)
  const loadingMore = ref(false)
  const error = ref('')
  const lastReceivedAt = ref<string | null>(null)
  const followState = ref<FollowState>('paused')
  const reads = createScopedReadRequest()
  const streams = createRequestSequenceGate()
  let activeQuery: LogQuery | null = null
  let queryKey = ''
  let pendingLive: LogEntry[] = []
  let historyPending = false
  let stream: EventSource | null = null
  let releaseStreamSession: (() => void) | undefined

  function stopFollow() {
    streams.invalidate()
    stream?.close()
    stream = null
    releaseStreamSession?.()
    releaseStreamSession = undefined
    followState.value = 'paused'
  }

  async function load(input: LogQuery) {
    const request = reads.begin()
    stopFollow()
    activeQuery = { ...input, query: input.query?.trim(), cursor: undefined }
    const key = JSON.stringify(activeQuery)
    if (queryKey !== key) {
      entries.value = []
      lastReceivedAt.value = null
    }
    queryKey = key
    nextCursor.value = ''
    loadingMore.value = false
    error.value = ''
    pendingLive = []
    historyPending = true
    loading.value = true
    if (input.source === 'container' && !input.containerId) {
      entries.value = []
      loading.value = false
      historyPending = false
      request.finish()
      return
    }
    try {
      const result = await api.requestLogs(activeQuery, request.signal)
      if (!request.isCurrent()) return
      // SSE 可先于历史响应到达；仅合并此轮请求期间到达的实时记录。
      entries.value = mergeLogEntries(result.entries, pendingLive, pendingLive.length ? 500 : undefined)
      nextCursor.value = result.nextCursor
      if (!pendingLive.length) lastReceivedAt.value = result.collectedAt
    } catch (caught) {
      if (request.isCurrent() && !isAbortError(caught)) error.value = '日志读取失败，请确认目标服务正在运行。'
    } finally {
      request.finish()
      if (request.isCurrent()) {
        loading.value = false
        historyPending = false
        pendingLive = []
      }
    }
  }

  async function loadMore() {
    if (!activeQuery || !nextCursor.value || loading.value || loadingMore.value) return
    const request = reads.begin()
    const input = { ...activeQuery, cursor: nextCursor.value }
    loadingMore.value = true
    error.value = ''
    try {
      const result = await api.requestLogs(input, request.signal)
      if (!request.isCurrent()) return
      // 保留已到达的实时版本，不让较旧的分页快照覆盖它。
      entries.value = mergeLogEntries(result.entries, entries.value)
      nextCursor.value = result.nextCursor
    } catch (caught) {
      if (request.isCurrent() && !isAbortError(caught)) error.value = '更多日志读取失败，请重试。'
    } finally {
      request.finish()
      if (request.isCurrent()) loadingMore.value = false
    }
  }

  function syncFollow(enabled: boolean, interval: number, limit: number) {
    stopFollow()
    if (!enabled || !activeQuery || (activeQuery.source === 'container' && !activeQuery.containerId)) return
    const sequence = streams.begin()
    const session = captureSession()
    const isCurrent = () => streams.isLatest(sequence) && isCurrentSession(session.generation) && !session.signal.aborted
    session.signal.addEventListener('abort', stopFollow, { once: true })
    releaseStreamSession = () => session.signal.removeEventListener('abort', stopFollow)
    followState.value = 'connecting'
    stream = api.followLogs({ ...activeQuery, limit }, interval, (result: LogResponse) => {
      if (!isCurrent()) return
      if (historyPending) pendingLive = mergeLogEntries(pendingLive, result.entries, 500)
      entries.value = mergeLogEntries(entries.value, result.entries, 500)
      followState.value = 'connected'
      lastReceivedAt.value = result.collectedAt
      error.value = ''
    }, (state = 'reconnecting') => {
      if (!isCurrent()) return
      followState.value = state
      error.value = state === 'failed'
        ? '实时日志连接失败，请暂停后重新开启。'
        : '实时日志正在重新连接，期间不会丢失已加载的历史记录。'
    })
  }

  function dispose() {
    reads.invalidate()
    stopFollow()
    loading.value = false
    loadingMore.value = false
  }

  return { entries, nextCursor, loading, loadingMore, error, lastReceivedAt, followState, load, loadMore, syncFollow, dispose }
}
