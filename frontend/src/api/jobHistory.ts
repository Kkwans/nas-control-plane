import { NcpApiError, requestOptions } from './systemTransport'

/** 与可操作的 JobSnapshot 隔离；所有字段只代表最后一次成功保存。 */
export interface SavedJob {
  id: string; type: string; status: string; message: string
  reference?: string; error?: string; errorCode?: string; cancellationStatus?: string
  createdAt: string; updatedAt: string; completedAt?: string
}
export interface JobHistoryPage { source: 'persisted'; items: SavedJob[]; nextCursor?: string }
export interface JobHistoryQuery { type: string; limit: number; cursor?: string }
export const HISTORY_RESPONSE_LIMIT = 1 << 20
const encoder = new TextEncoder()
const invalid = () => new NcpApiError('JOB_HISTORY_RESPONSE_INVALID', '已保存历史响应不符合协议，请重试。')
function record(value: unknown): value is Record<string, unknown> { return typeof value === 'object' && value !== null && !Array.isArray(value) }
function text(value: unknown, cap: number, required = false, controls = false): value is string {
  return typeof value === 'string' && (!required || value.length > 0) && !/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(value) && encoder.encode(value).length <= cap && (!controls || !Array.from(value).some(char => { const code = char.codePointAt(0)!; return code < 32 || (code >= 127 && code <= 159) }))
}
export function validHistoryType(value: string) { return text(value, 128, false, true) }
function timestamp(value: unknown): value is string {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/.test(value)) return false
  const parsed = new Date(value)
  return Number.isFinite(parsed.valueOf()) && parsed.toISOString() === value.replace('Z', '.000Z')
}
function cursor(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0 && value.length <= 2048 && /^[A-Za-z0-9_-]+$/.test(value) && value.length % 4 !== 1
}
export function parseJobHistory(value: unknown, query: JobHistoryQuery): JobHistoryPage {
  if (!record(value) || value.source !== 'persisted' || !Array.isArray(value.items) || value.items.length > query.limit) throw invalid()
  if (Object.keys(value).some(key => !['source', 'items', 'nextCursor'].includes(key))) throw invalid()
  if ('nextCursor' in value && (!cursor(value.nextCursor) || value.items.length !== query.limit || value.nextCursor === query.cursor)) throw invalid()
  const ids = new Set<string>()
  const items: SavedJob[] = []
  let previous: SavedJob | undefined
  const fields = ['id', 'type', 'status', 'message', 'reference', 'error', 'errorCode', 'cancellationStatus', 'createdAt', 'updatedAt', 'completedAt']
  for (const item of value.items) {
    if (!record(item) || Object.keys(item).some(key => !fields.includes(key)) ||
      !text(item.id, 256, true, true) || !text(item.type, 128, true, true) || !text(item.status, 64, true) || !text(item.message, 8192) ||
      !timestamp(item.createdAt) || !timestamp(item.updatedAt) || ('completedAt' in item && !timestamp(item.completedAt)) ||
      (query.type !== '' && item.type !== query.type) || ids.has(item.id)) throw invalid()
    for (const [key, cap] of [['reference', 4096], ['error', 8192], ['errorCode', 128], ['cancellationStatus', 64]] as const) {
      if (key in item && !text(item[key], cap)) throw invalid()
    }
    const entry = Object.fromEntries(fields.filter(key => key in item).map(key => [key, item[key]])) as unknown as SavedJob
    if (previous) {
      const currentTime = Date.parse(entry.createdAt), previousTime = Date.parse(previous.createdAt)
      if (currentTime > previousTime || (currentTime === previousTime && compareUTF8(entry.id, previous.id) >= 0)) throw invalid()
    }
    ids.add(item.id)
    items.push(entry)
    previous = entry
  }
  return { source: 'persisted', items, ...('nextCursor' in value ? { nextCursor: value.nextCursor as string } : {}) }
}

// SQLite BINARY 比较 UTF-8 字节，不能使用 JavaScript UTF-16 或区域排序。
function compareUTF8(a: string, b: string) {
  const left = encoder.encode(a), right = encoder.encode(b)
  for (let i = 0; i < Math.min(left.length, right.length); i++) if (left[i] !== right[i]) return left[i]! - right[i]!
  return left.length - right.length
}

// 流式限制累计响应字节；fatal 解码拒绝非法 UTF-8，绝不悄悄替换损坏字符。
async function boundedJson(response: Response): Promise<unknown> {
  if (!response.body) throw invalid()
  const reader = response.body.getReader()
  const decoder = new TextDecoder('utf-8', { fatal: true })
  let bytes = 0, raw = ''
  try {
    for (;;) {
      const chunk = await reader.read()
      if (chunk.done) break
      bytes += chunk.value.byteLength
      if (bytes > HISTORY_RESPONSE_LIMIT) throw invalid()
      raw += decoder.decode(chunk.value, { stream: true })
    }
    raw += decoder.decode()
    return JSON.parse(raw)
  } catch {
    await reader.cancel().catch(() => {})
    throw invalid()
  } finally { reader.releaseLock() }
}
export async function requestJobHistory(query: JobHistoryQuery, signal?: AbortSignal, fetcher: typeof fetch = fetch): Promise<JobHistoryPage> {
  query = Object.freeze({ type: query.type, limit: query.limit, ...(query.cursor !== undefined ? { cursor: query.cursor } : {}) })
  if (!validHistoryType(query.type) || !Number.isInteger(query.limit) || query.limit < 1 || query.limit > 100 || (query.cursor !== undefined && !cursor(query.cursor))) {
    throw new NcpApiError('JOB_HISTORY_INVALID_QUERY', '类型须为最多 128 UTF-8 字节且不含控制字符的原始文本。')
  }
  const params = new URLSearchParams({ limit: String(query.limit) })
  if (query.type !== '') params.set('type', query.type)
  if (query.cursor !== undefined) params.set('cursor', query.cursor)
  const response = await fetcher(`/api/v1/job-history?${params}`, requestOptions({ method: 'GET', signal, cache: 'no-store', redirect: 'error' }))
  if (response.redirected !== false) throw invalid()
  if (response.status === 404 || response.status === 501) throw new NcpApiError('JOB_HISTORY_UNSUPPORTED', '此服务暂不能提供已保存历史，可返回最近任务。')
  if (response.status === 401) throw new NcpApiError('AUTH_UNAUTHORIZED', '会话已失效，请重新登录。')
  if (response.status !== 200) {
    let payload: unknown
    try { payload = await boundedJson(response) } catch { /* 不展示代理或底层错误文本。 */ }
    if (response.status === 503 && record(payload) && payload.code === 'JOB_HISTORY_TOO_LARGE') throw new NcpApiError('JOB_HISTORY_TOO_LARGE', '本页任务历史过大，请减少每页条数。')
    if (response.status === 503 && record(payload) && payload.code === 'JOB_HISTORY_CORRUPT') throw new NcpApiError('JOB_HISTORY_CORRUPT', '已保存历史含无法读取的数据；减少每页条数不能修复该记录。')
    throw new NcpApiError('JOB_HISTORY_UNAVAILABLE', '已保存历史暂时无法读取，请重试。')
  }
  if (!/^application\/json(?:\s*;|$)/i.test(response.headers.get('Content-Type') ?? '')) throw invalid()
  return parseJobHistory(await boundedJson(response), query)
}
