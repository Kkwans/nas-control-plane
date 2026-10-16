import type { DockerContainerCreateInput } from './system'
import { NcpApiError, requestOptions } from './systemTransport'

export interface DockerContainerCreateJobResult {
  containerId: string
  created: true
  started: boolean
}
export interface DockerContainerCreateJobSnapshot {
  id: string
  type: 'docker-container-create'
  reference?: ''
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
  createResult?: DockerContainerCreateJobResult
}

export class DockerContainerCreateAdmissionUnknownError extends NcpApiError {
  constructor() {
    super('DOCKER_CONTAINER_CREATE_ADMISSION_UNKNOWN', '容器创建任务可能已接收，结果尚未确认。请查看最近任务并只读刷新，勿重复提交。')
    this.name = 'DockerContainerCreateAdmissionUnknownError'
  }
}
class DockerContainerCreateAdmissionRejectedError extends NcpApiError {}
export function isDockerContainerCreateAdmissionRejected(error: unknown): error is NcpApiError {
  return error instanceof DockerContainerCreateAdmissionRejectedError
}
const invalidRead = () => new NcpApiError('DOCKER_CONTAINER_CREATE_JOB_RESPONSE_INVALID', '创建任务响应无法核对，请稍后只读刷新，勿重复提交。')
const invalidInput = () => new DockerContainerCreateAdmissionRejectedError('DOCKER_CONTAINER_CREATE_INVALID', '容器创建参数无效或超过大小限制，请检查后再试。')
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
function empty(value: Record<string, unknown>, key: string) { return !(key in value) || value[key] === '' }
const fields = ['id', 'type', 'reference', 'cancellable', 'status', 'artifactState', 'message', 'progress', 'createdAt', 'updatedAt', 'downloadedBytes', 'totalBytes', 'speedBytes', 'layers', 'progressLimited', 'persistenceStatus', 'cancellationStatus', 'completedAt', 'error', 'errorCode', 'createResult']
function decode(value: unknown, initial: boolean, jobId?: string, runContainer?: boolean): DockerContainerCreateJobSnapshot {
  if (!record(value) || Object.keys(value).some(key => !fields.includes(key)) ||
    !text(value.id, 256, true) || (jobId !== undefined && value.id !== jobId) ||
    value.type !== 'docker-container-create' || !empty(value, 'reference') || value.cancellable !== false || value.artifactState !== 'unknown' ||
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
  if (value.status === 'completed') {
    const result = value.createResult
    if ('persistenceStatus' in value || 'error' in value || 'errorCode' in value ||
      !record(result) || Object.keys(result).length !== 3 || Object.keys(result).some(key => !['containerId', 'created', 'started'].includes(key)) ||
      typeof result.containerId !== 'string' || !/^[a-f0-9]{64}$/.test(result.containerId) || result.created !== true ||
      typeof result.started !== 'boolean' || result.started !== runContainer) throw invalidRead()
  } else if ('createResult' in value) throw invalidRead()
  if (value.persistenceStatus === 'pending' && terminal) throw invalidRead()
  if (!terminal && ('error' in value || 'errorCode' in value)) throw invalidRead()
  if (initial && (value.status !== 'queued' || value.createdAt !== value.updatedAt || 'persistenceStatus' in value)) throw invalidRead()
  // 只有 reference 允许空串或省略；其余字段遵守原始快照合同。
  const result: Record<string, unknown> = { ...value, layers: {} }
  if (empty(result, 'reference')) delete result.reference
  return result as unknown as DockerContainerCreateJobSnapshot
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
async function json(response: Response): Promise<unknown> {
  if (response.redirected !== false || response.headers.get('content-type')?.split(';')[0]?.trim().toLowerCase() !== 'application/json' || !response.body) throw invalidRead()
  const reader = response.body.getReader(), decoder = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true })
  let bytes = 0, source = ''
  try {
    for (;;) {
      const chunk = await reader.read()
      if (chunk.done) break
      bytes += chunk.value.byteLength
      if (bytes > 65536) throw invalidRead()
      source += decoder.decode(chunk.value, { stream: true })
    }
    return strictJSON(source + decoder.decode())
  } catch (error) {
    await reader.cancel().catch(() => {})
    throw error
  } finally { reader.releaseLock() }
}
function rejected(status: number, code: string) {
  return (status === 400 && ['CONTROL_INPUT_INVALID', 'DOCKER_CONTAINER_CREATE_INVALID'].includes(code)) ||
    (status === 401 && code === 'AUTH_UNAUTHORIZED') ||
    (status === 403 && code === 'HTTP_CROSS_ORIGIN_REJECTED') ||
    (status === 413 && code === 'HTTP_REQUEST_BODY_TOO_LARGE') ||
    (status === 415 && code === 'HTTP_UNSUPPORTED_MEDIA_TYPE') ||
    (status === 503 && ['AGENT_DOCKER_CONTROL_UNAVAILABLE', 'JOB_QUEUE_FULL', 'JOB_PERSIST_FAILED', 'JOB_ENGINE_UNAVAILABLE'].includes(code))
}

/** 只发起一次创建；202 只证明接纳。未知结果绝不重试或降级到同步入口。 */
export async function requestDockerContainerCreateJob(input: DockerContainerCreateInput, fetcher: typeof fetch = fetch): Promise<DockerContainerCreateJobSnapshot> {
  let body: string
  try {
    if (!record(input) || typeof input.runContainer !== 'boolean') throw invalidInput()
    body = JSON.stringify(input)
    const serialized: unknown = JSON.parse(body)
    if (!record(serialized) || serialized.runContainer !== input.runContainer || encoder.encode(body).length > 16384) throw invalidInput()
  } catch { throw invalidInput() }
  let rejection: DockerContainerCreateAdmissionRejectedError | undefined
  try {
    const response = await fetcher('/api/v1/docker/containers/create-jobs', requestOptions({ method: 'POST', headers: { 'Content-Type': 'application/json' }, body, redirect: 'error' }))
    const payload = await json(response)
    if (record(payload) && Object.keys(payload).length === 3 && text(payload.code, 128, true) && text(payload.message, 8192) && text(payload.requestId, 256) && rejected(response.status, payload.code)) {
      // 后端错误文本、请求 ID 和异常 cause 均不透传，以免意外回显输入。
      rejection = new DockerContainerCreateAdmissionRejectedError(payload.code, '容器创建任务未接收，请检查后再试。')
      throw rejection
    }
    if (response.status !== 202) throw invalidRead()
    return decode(payload, true)
  } catch (error) {
    if (rejection && error === rejection) throw rejection
    throw new DockerContainerCreateAdmissionUnknownError()
  }
}

/** 只读精确任务；调用方只需保存 jobId 和启动布尔意图，不能保存请求正文。 */
export async function requestDockerContainerCreateJobStatus(jobId: string, runContainer: boolean, signal?: AbortSignal, fetcher: typeof fetch = fetch): Promise<DockerContainerCreateJobSnapshot> {
  if (!text(jobId, 256, true) || typeof runContainer !== 'boolean') throw invalidRead()
  try {
    const response = await fetcher(`/api/v1/jobs/${encodeURIComponent(jobId)}`, requestOptions({ method: 'GET', cache: 'no-store', redirect: 'error', signal }))
    const payload = await json(response)
    if (response.status !== 200) throw invalidRead()
    return decode(payload, false, jobId, runContainer)
  } catch { throw invalidRead() }
}

export type DockerContainerCreateJobObservation = Pick<DockerContainerCreateJobSnapshot, 'id' | 'type' | 'status' | 'persistenceStatus'>
export interface DockerContainerCreateJobList {
  jobs: DockerContainerCreateJobObservation[]
  invalidCount: number
}

/** 最近列表只用于观察防重复保护，不能认领任务或证明请求未执行。 */
export async function requestDockerContainerCreateJobs(signal?: AbortSignal, fetcher: typeof fetch = fetch): Promise<DockerContainerCreateJobList> {
  try {
    const response = await fetcher('/api/v1/jobs?type=docker-container-create', requestOptions({ method: 'GET', cache: 'no-store', redirect: 'error', signal }))
    const payload = await json(response)
    if (response.status !== 200 || !record(payload) || Object.keys(payload).length !== 1 || !Array.isArray(payload.jobs)) throw invalidRead()
    const jobs: DockerContainerCreateJobObservation[] = [], ids = new Set<string>()
    let invalidCount = 0
    for (const value of payload.jobs) {
      try {
        // 列表没有提交意图，仅验证结果结构；从不对外发布结果或表单线索。
        const started = record(value) && record(value.createResult) && typeof value.createResult.started === 'boolean' ? value.createResult.started : undefined
        const snapshot = decode(value, false, undefined, started)
        if (ids.has(snapshot.id)) { invalidCount++; continue }
        ids.add(snapshot.id)
        jobs.push({ id: snapshot.id, type: snapshot.type, status: snapshot.status, ...(snapshot.persistenceStatus ? { persistenceStatus: snapshot.persistenceStatus } : {}) })
      } catch { invalidCount++ }
    }
    return { jobs, invalidCount }
  } catch { throw invalidRead() }
}
