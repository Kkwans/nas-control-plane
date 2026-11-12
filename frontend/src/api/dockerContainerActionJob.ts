import type { ContainerAction, JobSnapshot } from './system'
import { isApiErrorResponse, NcpApiError, requestOptions } from './systemTransport'

export class DockerContainerActionAdmissionUnknownError extends NcpApiError {
  constructor() {
    super('DOCKER_CONTAINER_ACTION_ADMISSION_UNKNOWN', '容器操作任务可能已接收，结果尚未确认。请查看最近任务并只读刷新容器，勿重复提交。')
    this.name = 'DockerContainerActionAdmissionUnknownError'
  }
}
class DockerContainerActionAdmissionRejectedError extends NcpApiError {}
export function isDockerContainerActionAdmissionRejected(error: unknown): error is NcpApiError {
  return error instanceof DockerContainerActionAdmissionRejectedError
}
const invalidRead = () => new NcpApiError('DOCKER_CONTAINER_ACTION_JOB_RESPONSE_INVALID', '任务响应无法核对，请稍后只读刷新，勿重复提交。')
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
function validIntent(containerId: string, action: ContainerAction) {
  return typeof containerId === 'string' && /^[a-f0-9]{64}$/.test(containerId) && ['start', 'stop', 'restart'].includes(action)
}
const fields = ['id', 'type', 'reference', 'cancellable', 'status', 'artifactState', 'message', 'progress', 'createdAt', 'updatedAt', 'downloadedBytes', 'totalBytes', 'speedBytes', 'layers', 'progressLimited', 'persistenceStatus', 'completedAt', 'error', 'errorCode']
function decode(value: unknown, containerId: string, action: ContainerAction, initial: boolean, jobId?: string): JobSnapshot {
  if (!record(value) || Object.keys(value).some(key => !fields.includes(key)) ||
    !text(value.id, 256, true) || (jobId !== undefined && value.id !== jobId) ||
    value.type !== `docker-container-${action}` || value.reference !== containerId || value.cancellable !== false || value.artifactState !== 'unknown' ||
    !text(value.message, 8192) || !timestamp(value.createdAt) || !timestamp(value.updatedAt) || Date.parse(value.updatedAt) < Date.parse(value.createdAt) ||
    !Number.isInteger(value.progress) || (value.progress as number) < 0 || (value.progress as number) > 100 ||
    ![value.downloadedBytes, value.totalBytes, value.speedBytes].every(number => number === 0) ||
    !record(value.layers) || Object.keys(value.layers).length !== 0 ||
    ('progressLimited' in value && typeof value.progressLimited !== 'boolean') ||
    ('persistenceStatus' in value && value.persistenceStatus !== 'pending') ||
    ('error' in value && !text(value.error, 8192)) || ('errorCode' in value && !text(value.errorCode, 128, true))) throw invalidRead()
  if (!['queued', 'running', 'completed', 'failed', 'interrupted'].includes(value.status as string)) throw invalidRead()
  const terminal = ['completed', 'failed', 'interrupted'].includes(value.status as string)
  if (terminal) {
    if (!timestamp(value.completedAt) || Date.parse(value.completedAt) < Date.parse(value.createdAt) || Date.parse(value.completedAt) > Date.parse(value.updatedAt)) throw invalidRead()
  } else if ('completedAt' in value) throw invalidRead()
  if (value.status === 'completed' && (value.progress !== 100 || 'error' in value || 'errorCode' in value)) throw invalidRead()
  if (initial && (value.status !== 'queued' || value.progress !== 0 || value.progressLimited === true ||
    value.createdAt !== value.updatedAt || 'completedAt' in value || 'persistenceStatus' in value || 'error' in value || 'errorCode' in value)) throw invalidRead()
  return { ...value, layers: {} } as unknown as JobSnapshot
}

// 限制回执大小并拒绝无效 UTF-8，避免代理响应被误认成接收证明。
async function json(response: Response): Promise<unknown> {
  if (response.redirected !== false || response.headers.get('content-type')?.split(';')[0]?.trim().toLowerCase() !== 'application/json' || !response.body) throw invalidRead()
  const reader = response.body.getReader(), decoder = new TextDecoder('utf-8', { fatal: true })
  let bytes = 0, source = ''
  try {
    for (;;) {
      const chunk = await reader.read()
      if (chunk.done) break
      bytes += chunk.value.byteLength
      if (bytes > 65536) throw invalidRead()
      source += decoder.decode(chunk.value, { stream: true })
    }
    return JSON.parse(source + decoder.decode())
  } catch (error) {
    await reader.cancel().catch(() => {})
    throw error
  } finally { reader.releaseLock() }
}
function rejected(status: number, code: string) {
  return (status === 400 && code === 'DOCKER_CONTAINER_ACTION_INVALID') ||
    (status === 401 && code === 'AUTH_UNAUTHORIZED') ||
    (status === 403 && code === 'HTTP_CROSS_ORIGIN_REJECTED') ||
    (status === 503 && ['JOB_QUEUE_FULL', 'JOB_PERSIST_FAILED', 'JOB_ENGINE_UNAVAILABLE'].includes(code))
}

/** 仅证明接收；任何不明确的传输或回执都不能授权重发。 */
export async function requestDockerContainerActionJob(containerId: string, action: ContainerAction, fetcher: typeof fetch = fetch): Promise<JobSnapshot> {
  if (!validIntent(containerId, action)) throw new DockerContainerActionAdmissionRejectedError('DOCKER_CONTAINER_ACTION_INVALID', '请选择完整容器 ID 及 start、stop 或 restart。')
  let rejection: DockerContainerActionAdmissionRejectedError | undefined
  try {
    const response = await fetcher(`/api/v1/docker/containers/${containerId}/action-jobs/${action}`, requestOptions({ method: 'POST', redirect: 'error' }))
    const payload = await json(response)
    if (isApiErrorResponse(payload) && rejected(response.status, payload.code)) {
      rejection = new DockerContainerActionAdmissionRejectedError(payload.code, '容器操作任务未接收，请检查后再试。', payload.requestId)
      throw rejection
    }
    if (response.status !== 202) throw invalidRead()
    return decode(payload, containerId, action, true)
  } catch (error) {
    if (rejection && error === rejection) throw rejection
    throw new DockerContainerActionAdmissionUnknownError()
  }
}

/** 只读核对已知任务；绝不自动选择另一任务或重试写请求。 */
export async function requestDockerContainerActionJobStatus(jobId: string, containerId: string, action: ContainerAction, signal?: AbortSignal, fetcher: typeof fetch = fetch): Promise<JobSnapshot> {
  if (!validIntent(containerId, action) || !text(jobId, 256, true)) throw invalidRead()
  try {
    const response = await fetcher(`/api/v1/jobs/${encodeURIComponent(jobId)}`, requestOptions({ method: 'GET', cache: 'no-store', redirect: 'error', signal }))
    const payload = await json(response)
    if (response.status !== 200) throw invalidRead()
    return decode(payload, containerId, action, false, jobId)
  } catch { throw invalidRead() }
}
