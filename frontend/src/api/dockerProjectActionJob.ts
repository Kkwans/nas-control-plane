import { NcpApiError, requestOptions } from './systemTransport'

export type DockerProjectAction = 'start' | 'stop' | 'restart'
export interface DockerProjectActionJobItem {
  containerId: string
  outcome: 'succeeded' | 'state-mismatch' | 'unknown'
  state: 'running' | 'stopped' | 'unknown'
  errorCode?: 'DOCKER_CONTAINER_STATE_MISMATCH' | 'JOB_EXECUTION_INTERRUPTED'
}
export interface DockerProjectActionJobResult {
  projectId: 'standalone'
  kind: 'standalone'
  action: DockerProjectAction
  items: DockerProjectActionJobItem[]
}
export interface DockerProjectActionJobSnapshot {
  id: string
  type: `docker-project-${DockerProjectAction}`
  reference: 'standalone'
  cancellable: false
  status: 'queued' | 'running' | 'completed' | 'failed' | 'interrupted'
  artifactState: 'unknown'
  message: string
  progress: number
  createdAt: string
  updatedAt: string
  completedAt?: string
  downloadedBytes: 0
  totalBytes: 0
  speedBytes: 0
  layers: Record<string, never>
  progressLimited?: false
  persistenceStatus?: 'pending'
  error?: string
  errorCode?: string
  projectActionResult?: DockerProjectActionJobResult
}

export class DockerProjectActionAdmissionUnknownError extends NcpApiError {
  constructor() {
    super('DOCKER_PROJECT_ACTION_ADMISSION_UNKNOWN', '项目操作任务可能已接收，结果尚未确认。请查看最近任务并只读刷新，勿重复提交。')
    this.name = 'DockerProjectActionAdmissionUnknownError'
  }
}
class DockerProjectActionAdmissionRejectedError extends NcpApiError {}
export function isDockerProjectActionAdmissionRejected(error: unknown): error is NcpApiError {
  return error instanceof DockerProjectActionAdmissionRejectedError
}
const invalidRead = () => new NcpApiError('DOCKER_PROJECT_ACTION_JOB_RESPONSE_INVALID', '项目操作任务响应无法核对，请稍后只读刷新，勿重复提交。')
const invalidInput = () => new DockerProjectActionAdmissionRejectedError('DOCKER_PROJECT_ACTION_INVALID', '项目操作参数无效或超过大小限制，请检查后再试。')
const encoder = new TextEncoder()
function record(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}
function text(value: unknown, cap: number, identity = false): value is string {
  return typeof value === 'string' && encoder.encode(value).length <= cap &&
    !/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(value) &&
    (!identity || (value.trim().length > 0 && !Array.from(value).some(char => { const code = char.codePointAt(0)!; return code < 32 || (code >= 127 && code <= 159) })))
}
function timestamp(value: unknown): value is string {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value)) return false
  const date = new Date(value)
  return Number.isFinite(date.valueOf()) && date.toISOString().slice(0, 19) === value.slice(0, 19)
}
// 保留 Go RFC3339Nano 的纳秒排序，避免 Date.parse 毫秒截断掩盖倒序回执。
function timeKey(value: string) { return value.slice(0, 19) + (value.slice(19, -1).replace('.', '')).padEnd(9, '0') }
const actions: readonly DockerProjectAction[] = ['start', 'stop', 'restart']
function validIDs(ids: unknown): ids is readonly string[] {
  return Array.isArray(ids) && ids.length > 0 && ids.length <= 128 &&
    Array.from(ids).every(id => typeof id === 'string' && /^[a-f0-9]{64}$/.test(id)) && new Set(ids).size === ids.length
}
function validAction(action: unknown): action is DockerProjectAction { return actions.includes(action as DockerProjectAction) }
function decodeResult(value: unknown, action: DockerProjectAction, status: unknown, ids?: readonly string[]): DockerProjectActionJobResult {
  if (!record(value) || Object.keys(value).length !== 4 || Object.keys(value).some(key => !['projectId', 'kind', 'action', 'items'].includes(key)) ||
    value.projectId !== 'standalone' || value.kind !== 'standalone' || value.action !== action || !Array.isArray(value.items) ||
    !validIDs(value.items.map(item => record(item) ? item.containerId : undefined))) throw invalidRead()
  const actualIDs = value.items.map(item => item.containerId as string)
  if (ids && (actualIDs.length !== ids.length || actualIDs.some(id => !ids.includes(id)))) throw invalidRead()
  const desired = action === 'stop' ? 'stopped' : 'running', opposite = action === 'stop' ? 'running' : 'stopped'
  let mismatches = 0, unknown = 0
  for (const item of value.items) {
    if (Object.keys(item).some(key => !['containerId', 'outcome', 'state', 'errorCode'].includes(key))) throw invalidRead()
    if (item.outcome === 'succeeded') {
      if (item.state !== desired || 'errorCode' in item) throw invalidRead()
    } else if (item.outcome === 'state-mismatch') {
      if (item.state !== opposite || item.errorCode !== 'DOCKER_CONTAINER_STATE_MISMATCH') throw invalidRead()
      mismatches++
    } else if (item.outcome === 'unknown') {
      if (item.state !== 'unknown' || item.errorCode !== 'JOB_EXECUTION_INTERRUPTED') throw invalidRead()
      unknown++
    } else throw invalidRead()
  }
  if ((status === 'completed' && (mismatches !== 0 || unknown !== 0)) ||
    (status === 'failed' && (mismatches === 0 || unknown !== 0)) ||
    (status === 'interrupted' && unknown === 0)) throw invalidRead()
  return { projectId: 'standalone', kind: 'standalone', action, items: value.items.map(item => ({ ...item })) }
}
const fields = ['id', 'type', 'reference', 'cancellable', 'status', 'artifactState', 'message', 'progress', 'createdAt', 'updatedAt', 'downloadedBytes', 'totalBytes', 'speedBytes', 'layers', 'progressLimited', 'persistenceStatus', 'cancellationStatus', 'completedAt', 'error', 'errorCode', 'projectActionResult']
function decode(value: unknown, action: DockerProjectAction, initial: boolean, jobId?: string, ids?: readonly string[]): DockerProjectActionJobSnapshot {
  if (!record(value) || Object.keys(value).some(key => !fields.includes(key)) ||
    !text(value.id, 256, true) || (jobId !== undefined && value.id !== jobId) ||
    value.type !== `docker-project-${action}` || value.reference !== 'standalone' || value.cancellable !== false || value.artifactState !== 'unknown' ||
    !text(value.message, 8192) || !timestamp(value.createdAt) || !timestamp(value.updatedAt) || timeKey(value.updatedAt) < timeKey(value.createdAt) ||
    ![value.downloadedBytes, value.totalBytes, value.speedBytes].every(number => number === 0) ||
    !record(value.layers) || Object.keys(value.layers).length !== 0 ||
    ('progressLimited' in value && value.progressLimited !== false) || 'cancellationStatus' in value ||
    ('persistenceStatus' in value && value.persistenceStatus !== 'pending') ||
    ('error' in value && !text(value.error, 8192, true)) || ('errorCode' in value && !text(value.errorCode, 128, true))) throw invalidRead()
  if (!['queued', 'running', 'completed', 'failed', 'interrupted'].includes(value.status as string)) throw invalidRead()
  const terminal = ['completed', 'failed', 'interrupted'].includes(value.status as string)
  if (terminal) {
    if (!timestamp(value.completedAt) || timeKey(value.completedAt) < timeKey(value.createdAt) || timeKey(value.completedAt) > timeKey(value.updatedAt)) throw invalidRead()
  } else if ('completedAt' in value) throw invalidRead()
  if (value.progress !== (value.status === 'completed' ? 100 : 0)) throw invalidRead()
  let result: DockerProjectActionJobResult | undefined
  if ('projectActionResult' in value) {
    if (!terminal || 'persistenceStatus' in value) throw invalidRead()
    result = decodeResult(value.projectActionResult, action, value.status, ids)
    if (value.status === 'failed' && value.errorCode !== 'DOCKER_PROJECT_STATE_MISMATCH') throw invalidRead()
    if (value.status === 'interrupted' && value.errorCode !== 'JOB_EXECUTION_INTERRUPTED') throw invalidRead()
  }
  if (value.status === 'completed' && (!result || 'error' in value || 'errorCode' in value)) throw invalidRead()
  if (value.persistenceStatus === 'pending' && terminal) throw invalidRead()
  if (!terminal && ('error' in value || 'errorCode' in value)) throw invalidRead()
  if (initial && (value.status !== 'queued' || value.createdAt !== value.updatedAt || 'persistenceStatus' in value)) throw invalidRead()
  return { ...value, layers: {}, ...(result ? { projectActionResult: result } : {}) } as unknown as DockerProjectActionJobSnapshot
}

// JSON.parse 会吞掉重复键。先验证语法，再按词法扫描所有对象键（包括转义键）。
function strictJSON(source: string): unknown {
  const value: unknown = JSON.parse(source)
  const stack: (Set<string> | null)[] = []
  const tokens = /"(?:\\.|[^"\\])*"|[{}[\]:,]/g
  for (const match of source.matchAll(tokens)) {
    const token = match[0]
    if (token === '{' || token === '[') {
      stack.push(token === '{' ? new Set() : null)
      if (stack.length > 32) throw invalidRead()
    } else if (token === '}' || token === ']') stack.pop()
    else if (token.startsWith('"') && /^\s*:/.test(source.slice(match.index! + token.length))) {
      const keys = stack.at(-1), key = JSON.parse(token) as string
      if (!keys || keys.has(key)) throw invalidRead()
      keys.add(key)
    }
  }
  return value
}
async function json(response: Response, budget = 262144): Promise<unknown> {
  if (response.redirected !== false || response.headers.get('content-type')?.split(';')[0]?.trim().toLowerCase() !== 'application/json' || !response.body) throw invalidRead()
  const reader = response.body.getReader(), decoder = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true })
  let bytes = 0, source = ''
  try {
    for (;;) {
      const chunk = await reader.read()
      if (chunk.done) break
      bytes += chunk.value.byteLength
      if (bytes > budget) throw invalidRead()
      source += decoder.decode(chunk.value, { stream: true })
    }
    return strictJSON(source + decoder.decode())
  } catch (error) {
    await reader.cancel().catch(() => {})
    throw error
  } finally { reader.releaseLock() }
}
function rejected(status: number, code: string) {
  return (status === 400 && code === 'DOCKER_PROJECT_ACTION_INVALID') ||
    (status === 401 && code === 'AUTH_UNAUTHORIZED') ||
    (status === 403 && code === 'HTTP_CROSS_ORIGIN_REJECTED') ||
    (status === 413 && code === 'HTTP_REQUEST_BODY_TOO_LARGE') ||
    (status === 415 && code === 'HTTP_UNSUPPORTED_MEDIA_TYPE') ||
    (status === 503 && ['AGENT_DOCKER_CONTROL_UNAVAILABLE', 'JOB_QUEUE_FULL', 'JOB_PERSIST_FAILED', 'JOB_ENGINE_UNAVAILABLE'].includes(code))
}

/** 冻结子集只提交一次；未知接纳绝不重试或降级同步入口。 */
export async function requestDockerProjectActionJob(containerIds: readonly string[], action: DockerProjectAction, fetcher: typeof fetch = fetch): Promise<DockerProjectActionJobSnapshot> {
  let body: string
  try {
    if (!validIDs(containerIds) || !validAction(action)) throw invalidInput()
    body = JSON.stringify({ containerIds: [...containerIds] })
    if (encoder.encode(body).length > 16384) throw invalidInput()
  } catch { throw invalidInput() }
  let rejection: DockerProjectActionAdmissionRejectedError | undefined
  try {
    const response = await fetcher(`/api/v1/docker/projects/standalone/action-jobs/${action}`, requestOptions({ method: 'POST', headers: { 'Content-Type': 'application/json' }, body, redirect: 'error' }))
    const payload = await json(response)
    if (record(payload) && Object.keys(payload).length === 3 && text(payload.code, 128, true) && text(payload.message, 8192) && text(payload.requestId, 256) && rejected(response.status, payload.code)) {
      rejection = new DockerProjectActionAdmissionRejectedError(payload.code, '项目操作任务未接收，请检查后再试。')
      throw rejection
    }
    if (response.status !== 202) throw invalidRead()
    return decode(payload, action, true)
  } catch (error) {
    if (rejection && error === rejection) throw rejection
    throw new DockerProjectActionAdmissionUnknownError()
  }
}

/** 仅核对调用方冻结的精确任务、动作与完整集合，不保留部分坏回执。 */
export async function requestDockerProjectActionJobStatus(jobId: string, containerIds: readonly string[], action: DockerProjectAction, signal?: AbortSignal, fetcher: typeof fetch = fetch): Promise<DockerProjectActionJobSnapshot> {
  if (!text(jobId, 256, true) || !validIDs(containerIds) || !validAction(action)) throw invalidRead()
  const ids = [...containerIds]
  try {
    const response = await fetcher(`/api/v1/jobs/${encodeURIComponent(jobId)}`, requestOptions({ method: 'GET', cache: 'no-store', redirect: 'error', signal }))
    const payload = await json(response)
    if (response.status !== 200) throw invalidRead()
    return decode(payload, action, false, jobId, ids)
  } catch { throw invalidRead() }
}

export type DockerProjectActionJobObservation = Pick<DockerProjectActionJobSnapshot, 'id' | 'type' | 'status' | 'persistenceStatus'>
export interface DockerProjectActionJobList {
  jobs: DockerProjectActionJobObservation[]
  invalidCount: number
}

/** 三份列表独立限流；单页 8 MiB，覆盖正常活动列表；异常或过大的历史明确失败并保留保护。
 * 仅输出最小活动观察，终态也必须完整验证。任何失败不能提供“无活动”证据；
 * 此接口永远不能认领未知接纳任务，也不能证明请求未执行。
 */
export async function requestDockerProjectActionJobs(signal?: AbortSignal, fetcher: typeof fetch = fetch): Promise<DockerProjectActionJobList> {
  try {
    const pages = await Promise.all(actions.map(async action => {
      const response = await fetcher(`/api/v1/jobs?type=docker-project-${action}`, requestOptions({ method: 'GET', cache: 'no-store', redirect: 'error', signal }))
      // 不复用单条快照预算；大历史只报核对失败，不据此解除保护。
      const payload = await json(response, 8 * 1024 * 1024)
      if (response.status !== 200 || !record(payload) || Object.keys(payload).length !== 1 || !Array.isArray(payload.jobs) || payload.jobs.length > 1024) throw invalidRead()
      return { action, values: payload.jobs }
    }))
    const jobs: DockerProjectActionJobObservation[] = [], ids = new Set<string>()
    let invalidCount = 0
    for (const { action, values } of pages) {
      for (const value of values) {
        try {
          const snapshot = decode(value, action, false)
          if (ids.has(snapshot.id)) { invalidCount++; continue }
          ids.add(snapshot.id)
          if (snapshot.status === 'queued' || snapshot.status === 'running') jobs.push({ id: snapshot.id, type: snapshot.type, status: snapshot.status, ...(snapshot.persistenceStatus ? { persistenceStatus: snapshot.persistenceStatus } : {}) })
        } catch { invalidCount++ }
      }
    }
    return { jobs, invalidCount }
  } catch { throw invalidRead() }
}
