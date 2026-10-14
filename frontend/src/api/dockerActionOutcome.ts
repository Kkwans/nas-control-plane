import type { ContainerAction, ContainerActionResult, DockerProjectActionResult, DockerProjectContainerActionResult } from './system'
import { isApiErrorResponse, NcpApiError, requestOptions } from './systemTransport'

type ActionScope = 'container' | 'standalone'
type ContainerIntent = Readonly<{ containerId: string; action: ContainerAction }>
type ProjectIntent = Readonly<{ projectId: string; action: ContainerAction; containerIds: readonly string[] }>

export class DockerActionOutcomeUnknownError extends NcpApiError {
  constructor(scope: ActionScope) {
    super(scope === 'container' ? 'DOCKER_CONTAINER_ACTION_OUTCOME_UNKNOWN' : 'DOCKER_PROJECT_ACTION_OUTCOME_UNKNOWN',
      'Docker 操作可能已经执行，结果尚未确认。请先只读核对，勿重复提交。')
    this.name = 'DockerActionOutcomeUnknownError'
  }
}

export function dockerActionStateMatches(action: ContainerAction, state: string): boolean {
  return state === (action === 'stop' ? 'stopped' : 'running')
}

function invalid(): never { throw new Error('Invalid Docker lifecycle receipt') }
function record(value: unknown, keys: readonly string[]): Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return invalid()
  if (!keys.every((key) => Object.hasOwn(value, key))) return invalid()
  return value as Record<string, unknown>
}
function string(value: unknown): string { return typeof value === 'string' ? value : invalid() }
function action(value: unknown): ContainerAction {
  return value === 'start' || value === 'stop' || value === 'restart' ? value : invalid()
}
function boolean(value: unknown): boolean { return typeof value === 'boolean' ? value : invalid() }

function containerReceipt(value: unknown, intent: ContainerIntent): ContainerActionResult {
  const input = record(value, ['containerId', 'name', 'action', 'state'])
  const result = { containerId: string(input.containerId), name: string(input.name), action: action(input.action), state: string(input.state) }
  if (!result.containerId || !result.state || result.containerId !== intent.containerId || result.action !== intent.action) return invalid()
  return result
}

export function decodeContainerActionReceipt(value: unknown, intent: ContainerIntent): ContainerActionResult {
  try { return containerReceipt(value, intent) } catch { throw new DockerActionOutcomeUnknownError('container') }
}

export function decodeStandaloneActionReceipt(value: unknown, intent: ProjectIntent): DockerProjectActionResult {
  try {
    const input = record(value, ['projectId', 'kind', 'action', 'state', 'completed', 'containers'])
    if (input.projectId !== intent.projectId || !intent.projectId || input.kind !== 'standalone' || action(input.action) !== intent.action) return invalid()
    const state = string(input.state)
    const completed = boolean(input.completed)
    if (!state || !Array.isArray(input.containers) || !intent.containerIds.length || input.containers.length !== intent.containerIds.length) return invalid()
    const remaining = new Set(intent.containerIds)
    if (remaining.size !== intent.containerIds.length || remaining.has('')) return invalid()
    const containers: DockerProjectContainerActionResult[] = []
    // for-of 不会跳过稀疏项；只复制公开字段，不保留原始响应或可变引用。
    for (const raw of input.containers) {
      const item = record(raw, ['containerId', 'name', 'action', 'state', 'success'])
      const containerId = string(item.containerId)
      if (!remaining.delete(containerId)) return invalid()
      const decoded = containerReceipt(item, { containerId, action: intent.action })
      const success = boolean(item.success)
      const errorCode = item.errorCode === undefined ? undefined : string(item.errorCode)
      if (success && errorCode) return invalid()
      containers.push({ ...decoded, success, ...(errorCode !== undefined ? { errorCode } : {}) })
    }
    if (remaining.size || (completed && containers.some((item) => !item.success))) return invalid()
    // 旧 Agent 的 completed 仅表示命令/读回均无错误，不能据此推导目标状态。
    return { projectId: intent.projectId, kind: 'standalone', action: intent.action, state, completed, containers }
  } catch { throw new DockerActionOutcomeUnknownError('standalone') }
}

const commonRejections: Record<string, readonly [number, string]> = {
  AUTH_UNAUTHORIZED: [401, '请先登录。'],
  AUTH_UNAVAILABLE: [503, '登录服务暂不可用。'],
  HTTP_CROSS_ORIGIN_REJECTED: [403, '请求来源校验失败。'],
  ROUTE_NOT_FOUND: [404, '请求的接口不存在。'],
  METHOD_NOT_ALLOWED: [405, '请求方法不受支持。'],
  AGENT_PROTOCOL_MISMATCH: [409, 'Root Agent 与 NCP Server 版本不一致，请同步更新后再操作。'],
}
const rejections: Record<ActionScope, Record<string, readonly [number, string]>> = {
  container: { ...commonRejections, DOCKER_CONTAINER_ACTION_INVALID: [400, '容器操作参数无效。'] },
  standalone: {
    ...commonRejections,
    DOCKER_PROJECT_ACTION_INVALID: [400, 'Docker 独立容器项目参数无效。'],
    HTTP_REQUEST_BODY_TOO_LARGE: [413, '请求体超过大小限制。'],
    HTTP_UNSUPPORTED_MEDIA_TYPE: [415, '请求必须使用 application/json 内容类型。'],
  },
}

export async function requestDockerActionReceipt<T>(
  path: string,
  options: RequestInit,
  scope: ActionScope,
  decode: (value: unknown) => T,
  fetcher: typeof fetch,
): Promise<T> {
  // 构造失败在派发前保留本地错误；禁止 307/308 自动向新地址重发写操作。
  const init = requestOptions({ ...options, redirect: 'error' })
  let response: Response
  try { response = await fetcher(path, init) } catch { throw new DockerActionOutcomeUnknownError(scope) }
  let status: number
  let payload: unknown
  try {
    if (response.redirected !== false) throw new DockerActionOutcomeUnknownError(scope)
    status = response.status
    payload = await response.json()
  } catch { throw new DockerActionOutcomeUnknownError(scope) }
  if (status !== 200) {
    let rejection: NcpApiError | undefined
    try {
      if (!Array.isArray(payload) && isApiErrorResponse(payload) && ['code', 'message', 'requestId'].every((key) => Object.hasOwn(payload, key))) {
        const code = payload.code
        const rule = Object.hasOwn(rejections[scope], code) ? rejections[scope][code] : undefined
        if (rule && rule[0] === status) {
          const requestId = /^[A-Za-z0-9._:-]{1,128}$/.test(payload.requestId) ? payload.requestId : undefined
          rejection = new NcpApiError(code, rule[1], requestId)
        }
      }
    } catch { throw new DockerActionOutcomeUnknownError(scope) }
    if (rejection) throw rejection
    throw new DockerActionOutcomeUnknownError(scope)
  }
  try { return decode(payload) } catch { throw new DockerActionOutcomeUnknownError(scope) }
}
