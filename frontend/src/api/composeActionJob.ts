import { NcpApiError, requestOptions } from './systemTransport'

export type ComposeAction = 'start' | 'stop' | 'restart'
export interface ComposeActionJobInput {
  projectId: string
  workingDirectory: string
  configFiles: readonly string[]
}
export interface ComposeActionJobResult {
  projectId: string
  action: ComposeAction
  state: 'running' | 'stopped'
  serviceCount: number
}
export interface ComposeActionJobSnapshot {
  id: string
  type: `compose-${ComposeAction}`
  reference: string
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
  composeActionResult?: ComposeActionJobResult
}

export class ComposeActionAdmissionUnknownError extends NcpApiError {
  constructor() {
    super('COMPOSE_ACTION_ADMISSION_UNKNOWN', '项目操作任务可能已接收，结果尚未确认。请查看最近任务并只读刷新，勿重复提交。')
    this.name = 'ComposeActionAdmissionUnknownError'
  }
}
class ComposeActionAdmissionRejectedError extends NcpApiError {}
export function isComposeActionAdmissionRejected(error: unknown): error is NcpApiError {
  return error instanceof ComposeActionAdmissionRejectedError
}
const invalidRead = () => new NcpApiError('COMPOSE_ACTION_JOB_RESPONSE_INVALID', '项目操作任务响应无法核对，请稍后只读刷新，勿重复提交。')
const invalidInput = () => new ComposeActionAdmissionRejectedError('COMPOSE_ACTION_INVALID', '项目操作参数无效或超过大小限制，请检查后再试。')
const encoder = new TextEncoder()
function record(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}
function text(value: unknown, cap: number, identity = false): value is string {
  return typeof value === 'string' && encoder.encode(value).length <= cap &&
    !/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(value) &&
    (!identity || (normalizeComposeProjectId(value).length > 0 && !Array.from(value).some(char => { const code = char.codePointAt(0)!; return code < 32 || (code >= 127 && code <= 159) })))
}
function timestamp(value: unknown): value is string {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value)) return false
  const date = new Date(value)
  return Number.isFinite(date.valueOf()) && date.toISOString().slice(0, 19) === value.slice(0, 19)
}
// 保留 Go RFC3339Nano 的纳秒排序，避免 Date.parse 毫秒截断掩盖倒序回执。
function timeKey(value: string) { return value.slice(0, 19) + (value.slice(19, -1).replace('.', '')).padEnd(9, '0') }
const actions: readonly ComposeAction[] = ['start', 'stop', 'restart']
// 与 Go strings.TrimSpace 一致；不把 BOM 当作可移除空白。
export function normalizeComposeProjectId(value: string): string {
  // eslint-disable-next-line no-control-regex -- 显式匹配 Go Unicode 空白表。
  return value.replace(/^[\u0009-\u000d\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+|[\u0009-\u000d\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+$/gu, '')
}
function validProject(value: unknown): value is string { return text(value, 256, true) && normalizeComposeProjectId(value).length > 0 }
function validAction(action: unknown): action is ComposeAction { return actions.includes(action as ComposeAction) }
function decodeResult(value: unknown, projectId: string, action: ComposeAction): ComposeActionJobResult {
  if (!record(value) || Object.keys(value).length !== 4 || Object.keys(value).some(key => !['projectId', 'action', 'state', 'serviceCount'].includes(key)) ||
    value.projectId !== projectId || value.action !== action || value.state !== (action === 'stop' ? 'stopped' : 'running') ||
    !Number.isInteger(value.serviceCount) || (value.serviceCount as number) < 1 || (value.serviceCount as number) > 1024) throw invalidRead()
  return { projectId, action, state: value.state as 'running' | 'stopped', serviceCount: value.serviceCount as number }
}
const fields = ['id', 'type', 'reference', 'cancellable', 'status', 'artifactState', 'message', 'progress', 'createdAt', 'updatedAt', 'downloadedBytes', 'totalBytes', 'speedBytes', 'layers', 'progressLimited', 'persistenceStatus', 'cancellationStatus', 'completedAt', 'error', 'errorCode', 'composeActionResult']
function decode(value: unknown, action: ComposeAction, initial: boolean, jobId?: string, projectId?: string): ComposeActionJobSnapshot {
  if (!record(value) || Object.keys(value).some(key => !fields.includes(key)) ||
    !text(value.id, 256, true) || (jobId !== undefined && value.id !== jobId) ||
    value.type !== `compose-${action}` || (!validProject(value.reference) || normalizeComposeProjectId(value.reference) !== value.reference || (projectId !== undefined && value.reference !== projectId)) || value.cancellable !== false || value.artifactState !== 'unknown' ||
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
  let result: ComposeActionJobResult | undefined
  if ('composeActionResult' in value) {
    if (value.status !== 'completed' || 'persistenceStatus' in value) throw invalidRead()
    result = decodeResult(value.composeActionResult, value.reference as string, action)
  }
  if (value.status === 'completed' && (!result || 'error' in value || 'errorCode' in value)) throw invalidRead()
  if (value.persistenceStatus === 'pending' && terminal) throw invalidRead()
  if (!terminal && ('error' in value || 'errorCode' in value)) throw invalidRead()
  if (initial && (value.status !== 'queued' || value.createdAt !== value.updatedAt || 'persistenceStatus' in value)) throw invalidRead()
  return { ...value, layers: {}, ...(result ? { composeActionResult: result } : {}) } as unknown as ComposeActionJobSnapshot
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
  return (status === 400 && code === 'COMPOSE_LIFECYCLE_INVALID') ||
    (status === 401 && code === 'AUTH_UNAUTHORIZED') ||
    (status === 403 && code === 'HTTP_CROSS_ORIGIN_REJECTED') ||
    (status === 413 && code === 'HTTP_REQUEST_BODY_TOO_LARGE') ||
    (status === 415 && code === 'HTTP_UNSUPPORTED_MEDIA_TYPE') ||
    (status === 503 && ['AGENT_DOCKER_CONTROL_UNAVAILABLE', 'JOB_QUEUE_FULL', 'JOB_PERSIST_FAILED', 'JOB_ENGINE_UNAVAILABLE'].includes(code))
}

/** 复制并冻结有序配置，只提交一次；未知接纳绝不重试或降级同步入口。 */
export async function requestComposeActionJob(input: ComposeActionJobInput, action: ComposeAction, fetcher: typeof fetch = fetch): Promise<ComposeActionJobSnapshot> {
  let body: string, projectId: string
  try {
    if (!record(input) || !validProject(input.projectId) || !validAction(action) || !text(input.workingDirectory, 4096, true) ||
      !Array.isArray(input.configFiles) || input.configFiles.length < 1 || input.configFiles.length > 32 ||
      !Array.from(input.configFiles).every(path => text(path, 4096, true))) throw invalidInput()
    projectId = normalizeComposeProjectId(input.projectId)
    body = JSON.stringify({ workingDirectory: input.workingDirectory, configFiles: [...input.configFiles] })
    if (encoder.encode(body).length > 65536) throw invalidInput()
  } catch { throw invalidInput() }
  let rejection: ComposeActionAdmissionRejectedError | undefined
  try {
    const response = await fetcher(`/api/v1/docker/compose/projects/${encodeURIComponent(projectId)}/action-jobs/${action}`, requestOptions({ method: 'POST', headers: { 'Content-Type': 'application/json' }, body, redirect: 'error' }))
    const payload = await json(response)
    if (record(payload) && Object.keys(payload).length === 3 && text(payload.code, 128, true) && text(payload.message, 8192) && text(payload.requestId, 256) && rejected(response.status, payload.code)) {
      rejection = new ComposeActionAdmissionRejectedError(payload.code, 'Compose 操作任务未接收，请检查后再试。')
      throw rejection
    }
    if (response.status !== 202) throw invalidRead()
    return decode(payload, action, true, undefined, projectId)
  } catch (error) {
    if (rejection && error === rejection) throw rejection
    throw new ComposeActionAdmissionUnknownError()
  }
}

/** 只读核对冻结的任务、项目与动作；不保留部分坏回执。 */
export async function requestComposeActionJobStatus(jobId: string, projectId: string, action: ComposeAction, signal?: AbortSignal, fetcher: typeof fetch = fetch): Promise<ComposeActionJobSnapshot> {
  if (!text(jobId, 256, true) || !validProject(projectId) || !validAction(action)) throw invalidRead()
  const normalized = normalizeComposeProjectId(projectId)
  try {
    const response = await fetcher(`/api/v1/jobs/${encodeURIComponent(jobId)}`, requestOptions({ method: 'GET', cache: 'no-store', redirect: 'error', signal }))
    const payload = await json(response)
    if (response.status !== 200) throw invalidRead()
    return decode(payload, action, false, jobId, normalized)
  } catch { throw invalidRead() }
}

export type ComposeActionJobObservation = Pick<ComposeActionJobSnapshot, 'id' | 'type' | 'reference' | 'status' | 'persistenceStatus'>
export interface ComposeActionJobList {
  jobs: ComposeActionJobObservation[]
  invalidCount: number
}

/** 三份列表独立限流；单页 8 MiB，覆盖正常活动列表；异常或过大的历史明确失败并保留保护。
 * 仅输出最小活动观察，终态也必须完整验证。任何失败不能提供“无活动”证据；
 * 此接口永远不能认领未知接纳任务，也不能证明请求未执行。
 */
export async function requestComposeActionJobs(signal?: AbortSignal, fetcher: typeof fetch = fetch): Promise<ComposeActionJobList> {
  try {
    const pages = await Promise.all(actions.map(async action => {
      const response = await fetcher(`/api/v1/jobs?type=compose-${action}`, requestOptions({ method: 'GET', cache: 'no-store', redirect: 'error', signal }))
      // 不复用单条快照预算；大历史只报核对失败，不据此解除保护。
      const payload = await json(response, 8 * 1024 * 1024)
      if (response.status !== 200 || !record(payload) || Object.keys(payload).length !== 1 || !Array.isArray(payload.jobs) || payload.jobs.length > 1024) throw invalidRead()
      return { action, values: payload.jobs }
    }))
    const jobs: ComposeActionJobObservation[] = [], ids = new Set<string>()
    let invalidCount = 0
    for (const { action, values } of pages) {
      for (const value of values) {
        try {
          const snapshot = decode(value, action, false)
          if (ids.has(snapshot.id)) { invalidCount++; continue }
          ids.add(snapshot.id)
          if (snapshot.status === 'queued' || snapshot.status === 'running') jobs.push({ id: snapshot.id, type: snapshot.type, reference: snapshot.reference, status: snapshot.status, ...(snapshot.persistenceStatus ? { persistenceStatus: snapshot.persistenceStatus } : {}) })
        } catch { invalidCount++ }
      }
    }
    return { jobs, invalidCount }
  } catch { throw invalidRead() }
}
