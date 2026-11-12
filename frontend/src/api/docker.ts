import { decodeComposeActionReceipt, decodeContainerActionReceipt, decodeStandaloneActionReceipt, requestDockerActionReceipt } from './dockerActionOutcome'
import { captureSession } from '@/session/sessionLifecycle'
import { DOCKER_INVENTORY_RESPONSE_INVALID, parseDockerInventoryResponse } from './dockerInventoryResponse'
import {
  NcpApiError,
  isApiErrorResponse,
  jsonRequest,
  requestJson,
  requestOptions,
  responseError,
} from './systemTransport'
import type {
  ContainerAction,
  ContainerActionResult,
  ContainerDetails,
  ContainerLogsResult,
  ComposeDeployInput,
  ComposeLifecycleResult,
  ComposeProjectConfig,
  ComposeValidationResult,
  DockerContainerCreateInput,
  DockerContainerCreateResult,
  DockerResources,
  DockerHubRepository,
  DockerHubSearchResult,
  DockerHubTag,
  DockerHubTagsResult,
  DockerInventory,
  DockerImageInventory,
  DockerImageSummary,
  DockerImageRemoveBatchResult,
  DockerImageRemoveResult,
  DockerProject,
  DockerProjectActionResult,
  DockerProjectDeleteResult,
  JobListResult,
  JobSnapshot,
  ServiceListResponse,
} from './system'

interface DockerImageWireSummary extends Omit<DockerImageSummary, 'repoTags' | 'repoDigests'> {
  repoTags: string[] | null
  repoDigests: string[] | null
}

interface DockerImageWireInventory {
  collectedAt: string
  images: DockerImageWireSummary[]
}

interface DockerHubTagWire extends Omit<DockerHubTag, 'publishedAt'> {
  publishedAt?: string
}

interface DockerHubTagsWireResult extends Omit<DockerHubTagsResult, 'results'> {
  results: DockerHubTagWire[]
}

export async function requestDockerImages(fetcher: typeof fetch = fetch): Promise<DockerImageInventory> {
  const payload = await requestJson(
    '/api/v1/docker/images',
    {},
    isDockerImageWireInventory,
    fetcher,
    'DOCKER_IMAGE_LIST_RESPONSE_INVALID',
  )
  return {
    collectedAt: payload.collectedAt,
    images: payload.images.map((image) => ({
      ...image,
      repoTags: image.repoTags ?? [],
      repoDigests: image.repoDigests ?? [],
    })),
  }
}

export async function pullDockerImage(
  reference: string,
  expectedBytes = 0,
  fetcher: typeof fetch = fetch,
): Promise<JobSnapshot> {
  return requestJobSnapshot(
    '/api/v1/docker/images/pull',
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ reference, expectedBytes }),
    },
    fetcher,
    'DOCKER_IMAGE_PULL_RESPONSE_INVALID',
  )
}

export interface JobSubscription {
  close: () => void
  done: Promise<JobSnapshot>
}

export function subscribeJob(jobId: string, onProgress: (job: JobSnapshot) => void): JobSubscription {
  const session = captureSession()
  let settled = false
  let resolveDone!: (job: JobSnapshot) => void
  let rejectDone!: (error: unknown) => void
  const done = new Promise<JobSnapshot>((resolve, reject) => {
    resolveDone = resolve
    rejectDone = reject
  })
  const source = new EventSource(`/api/v1/jobs/${encodeURIComponent(jobId)}/events`)
  const close = () => {
    if (settled) return
    settled = true
    session.signal.removeEventListener('abort', close)
    source.close()
    rejectDone(new DOMException('Job subscription closed', 'AbortError'))
  }
  const finish = (job: JobSnapshot) => {
    if (settled) return
    settled = true
    session.signal.removeEventListener('abort', close)
    source.close()
    resolveDone(job)
  }
  session.signal.addEventListener('abort', close, { once: true })
  if (session.signal.aborted) close()
  source.addEventListener('progress', (event) => {
    if (settled) return
    try {
      const payload: unknown = JSON.parse((event as MessageEvent<string>).data)
      const job = parseJobSnapshot(payload)
      if (!job) throw new Error('任务进度格式无效')
      onProgress(job)
      if (job.persistenceStatus !== 'pending' && (job.status === 'completed' || job.status === 'failed' || job.status === 'interrupted' || job.status === 'cancelled')) finish(job)
    } catch (error) {
      if (settled) return
      settled = true
      session.signal.removeEventListener('abort', close)
      source.close()
      rejectDone(error)
    }
  })
  source.onerror = () => {
    if (settled) return
    settled = true
    session.signal.removeEventListener('abort', close)
    source.close()
    rejectDone(new NcpApiError('JOB_STREAM_FAILED', '任务进度连接已中断。'))
  }
  return { close, done }
}

export function followJob(jobId: string, onProgress: (job: JobSnapshot) => void): Promise<JobSnapshot> {
  return subscribeJob(jobId, onProgress).done
}

export async function requestJobs(type = '', fetcher: typeof fetch = fetch): Promise<JobListResult> {
  const parameters = new URLSearchParams()
  if (type) parameters.set('type', type)
  const payload = await requestJson(
    `/api/v1/jobs${parameters.size ? `?${parameters}` : ''}`,
    {},
    (value): value is { jobs: unknown[] } => isRecord(value) && Array.isArray(value.jobs),
    fetcher,
    'JOB_LIST_RESPONSE_INVALID',
  )
  const jobs: JobSnapshot[] = []
  let invalidCount = 0
  for (const value of payload.jobs) {
    const job = parseJobSnapshot(value)
    if (job) jobs.push(job)
    else invalidCount += 1
  }
  if (invalidCount > 0) {
    console.warn(`[NCP] 已忽略 ${invalidCount} 条无法解析的任务记录。`)
  }
  return { jobs, invalidCount }
}

export async function retryJob(jobId: string, fetcher: typeof fetch = fetch): Promise<JobSnapshot> {
  return requestJobSnapshot(
    `/api/v1/jobs/${encodeURIComponent(jobId)}/retry`,
    { method: 'POST' },
    fetcher,
    'JOB_RETRY_RESPONSE_INVALID',
  )
}

export async function cancelJob(jobId: string, fetcher: typeof fetch = fetch): Promise<JobSnapshot> {
  return requestJobSnapshot(
    `/api/v1/jobs/${encodeURIComponent(jobId)}/cancel`,
    { method: 'POST' },
    fetcher,
    'JOB_CANCEL_RESPONSE_INVALID',
  )
}

export async function deleteJob(jobId: string, fetcher: typeof fetch = fetch): Promise<void> {
  const response = await fetcher(`/api/v1/jobs/${encodeURIComponent(jobId)}`, requestOptions({ method: 'DELETE' }))
  if (!response.ok) throw await responseError(response)
}

export class DockerImageRemoveAdmissionUnknownError extends NcpApiError {
  constructor() {
    super('DOCKER_IMAGE_REMOVE_ADMISSION_UNKNOWN', '删除任务可能已接收，结果尚未确认。请查看最近任务并只读刷新镜像，勿重复提交。')
  }
}

/** 202 只证明任务接收；不把传输失败或异常回执解释为未执行。 */
export async function requestDockerImageRemoveJob(imageId: string, fetcher: typeof fetch = fetch): Promise<JobSnapshot> {
  if (!/^sha256:[a-f0-9]{64}$/.test(imageId)) throw new NcpApiError('DOCKER_IMAGE_REMOVE_INVALID', '请选择完整的镜像 ID。')
  let rejection: NcpApiError | undefined
  try {
    const response = await fetcher('/api/v1/docker/images/remove-job', requestOptions(jsonRequest({ imageId })))
    const mediaType = response.headers.get('content-type')?.split(';')[0]?.trim().toLowerCase()
    if (mediaType !== 'application/json') throw new DockerImageRemoveAdmissionUnknownError()
    const payload: unknown = await response.json()
    if (isApiErrorResponse(payload) && (
      (response.status === 400 && payload.code === 'DOCKER_IMAGE_REMOVE_INVALID') ||
      (response.status === 503 && ['AGENT_DOCKER_IMAGES_UNAVAILABLE', 'JOB_QUEUE_FULL', 'JOB_PERSIST_FAILED', 'JOB_ENGINE_UNAVAILABLE'].includes(payload.code))
    )) {
      rejection = new NcpApiError(payload.code, payload.message, payload.requestId)
      throw rejection
    }
    const job = parseJobSnapshot(payload)
    if (response.status !== 202 || !isRecord(payload) || payload.artifactState !== 'unknown' || !job || !job.id.trim() || job.type !== 'docker-image-remove' || job.reference !== imageId || job.cancellable !== false ||
      job.status !== 'queued' || job.progressLimited === true || job.cancellationStatus !== undefined || job.persistenceStatus !== undefined ||
      job.completedAt !== undefined || job.error !== undefined || job.errorCode !== undefined || Object.keys(job.layers).length !== 0 ||
      !Number.isFinite(Date.parse(job.createdAt)) || !Number.isFinite(Date.parse(job.updatedAt)) ||
      ![job.progress, job.downloadedBytes, job.totalBytes, job.speedBytes].every((value) => value === 0)) throw new DockerImageRemoveAdmissionUnknownError()
    return job
  } catch (caught) {
    if (rejection && caught === rejection) throw rejection
    throw new DockerImageRemoveAdmissionUnknownError()
  }
}

export async function removeDockerImage(
  imageId: string,
  fetcher: typeof fetch = fetch,
): Promise<DockerImageRemoveResult> {
  return requestJson(
    '/api/v1/docker/images/remove',
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ imageId }),
    },
    isDockerImageRemoveResult,
    fetcher,
    'DOCKER_IMAGE_REMOVE_RESPONSE_INVALID',
  )
}

export async function removeDockerImages(
  imageIds: string[],
  fetcher: typeof fetch = fetch,
): Promise<DockerImageRemoveBatchResult> {
  // 在首次异步等待前保存请求身份，防止调用方修改数组污染回执校验。
  const requestedIds = Object.freeze([...imageIds])
  let rejection: NcpApiError | undefined
  try {
    const response = await fetcher(
      '/api/v1/docker/images/remove-batch',
      requestOptions({ ...jsonRequest({ imageIds: requestedIds }), redirect: 'error' }),
    )
    // 禁止重定向重放删除请求，也不信任已跟随重定向取得的回执。
    if (response.redirected !== false) throw new Error('回执重定向状态无效')
    const mediaType = response.headers.get('content-type')?.split(';')[0]?.trim().toLowerCase()
    if (mediaType !== 'application/json') throw new Error('回执媒体类型无效')
    const payload: unknown = await response.json()
    // 仅保留服务端在调用 Agent 前明确拒绝请求体的状态与错误码组合。
    if (isApiErrorResponse(payload) && (
      (response.status === 413 && payload.code === 'HTTP_REQUEST_BODY_TOO_LARGE') ||
      (response.status === 415 && payload.code === 'HTTP_UNSUPPORTED_MEDIA_TYPE')
    )) {
      rejection = new NcpApiError(payload.code, payload.message, payload.requestId)
      throw rejection
    }
    if (response.status !== 200) throw new Error('回执状态无效')
    if (!isDockerImageRemoveBatchResult(payload, requestedIds)) throw new Error('回执与请求不一致')
    return payload
  } catch (caught) {
    if (rejection && caught === rejection) throw rejection
    // 请求可能已执行；任何不完整或矛盾的回执都不能作为再次删除的依据。
    throw new NcpApiError(
      'DOCKER_IMAGE_REMOVE_BATCH_OUTCOME_UNKNOWN',
      '批量删除结果尚未确认，部分镜像可能已删除。请只读刷新核对，勿重复提交。',
    )
  }
}

export async function searchDockerHub(
  query: string,
  page = 1,
  pageSize = 20,
  sort = 'relevance',
  fetcher: typeof fetch = fetch,
): Promise<DockerHubSearchResult> {
  const parameters = new URLSearchParams({ query, page: String(page), pageSize: String(pageSize), sort })
  return requestJson(
    `/api/v1/docker/hub/search?${parameters}`,
    {},
    isDockerHubSearchResult,
    fetcher,
    'DOCKER_HUB_SEARCH_RESPONSE_INVALID',
  )
}

export async function requestDockerHubTags(
  namespace: string,
  repository: string,
  page = 1,
  pageSize = 25,
  fetcher: typeof fetch = fetch,
): Promise<DockerHubTagsResult> {
  const parameters = new URLSearchParams({ namespace, repository, page: String(page), pageSize: String(pageSize) })
  const payload = await requestJson(
    `/api/v1/docker/hub/tags?${parameters}`,
    {},
    isDockerHubTagsWireResult,
    fetcher,
    'DOCKER_HUB_TAGS_RESPONSE_INVALID',
  )
  return {
    ...payload,
    results: payload.results.map((tag) => ({
      ...tag,
      publishedAt: tag.publishedAt?.trim() || tag.lastUpdated,
    })),
  }
}

export async function requestDockerInventory(fetcher: typeof fetch = fetch, signal?: AbortSignal): Promise<DockerInventory> {
  const payload = await requestJson('/api/v1/docker/inventory', signal ? { signal } : {}, isRecord, fetcher, DOCKER_INVENTORY_RESPONSE_INVALID)
  return parseDockerInventoryResponse(payload)
}

export async function requestDockerResources(fetcher: typeof fetch = fetch, signal?: AbortSignal): Promise<DockerResources> {
  return requestJson('/api/v1/docker/resources', signal ? { signal } : {}, isDockerResources, fetcher, 'DOCKER_RESOURCES_RESPONSE_INVALID')
}

export async function requestServices(fetcher: typeof fetch = fetch): Promise<ServiceListResponse> {
  return requestJson('/api/v1/services', {}, isServiceListResponse, fetcher, 'SERVICES_RESPONSE_INVALID')
}

export async function requestContainerAction(
  containerId: string,
  action: ContainerAction,
  fetcher: typeof fetch = fetch,
): Promise<ContainerActionResult> {
  const intent = { containerId, action }
  return requestDockerActionReceipt(
    `/api/v1/docker/containers/${encodeURIComponent(intent.containerId)}/actions/${intent.action}`,
    { method: 'POST' },
    'container',
    (payload) => decodeContainerActionReceipt(payload, intent),
    fetcher,
  )
}

export async function requestContainerDetails(
  containerId: string,
  fetcher: typeof fetch = fetch,
): Promise<ContainerDetails> {
  return requestJson(
    `/api/v1/docker/containers/${encodeURIComponent(containerId)}`,
    {},
    isContainerDetails,
    fetcher,
    'DOCKER_CONTAINER_DETAILS_RESPONSE_INVALID',
  )
}

export async function requestDockerProjectAction(
  project: Pick<DockerProject, 'id' | 'kind' | 'workingDirectory' | 'configFiles'> & { containerIds?: string[] },
  action: ContainerAction,
  fetcher: typeof fetch = fetch,
): Promise<DockerProjectActionResult | ComposeLifecycleResult> {
  if (project.kind === 'compose') {
    const intent = { projectId: project.id, action, workingDirectory: project.workingDirectory, configFiles: [...project.configFiles] }
    return requestDockerActionReceipt(
      `/api/v1/docker/compose/projects/${encodeURIComponent(intent.projectId)}/actions/${intent.action}`,
      jsonRequest(intent),
      'compose',
      (payload) => decodeComposeActionReceipt(payload, intent),
      fetcher,
    )
  }
  // 提交前复制标识和目标集合，等待响应时调用者的变化不能改变回执归属。
  const intent = { projectId: project.id, action, containerIds: [...(project.containerIds ?? [])] }
  return requestDockerActionReceipt(
    `/api/v1/docker/projects/${encodeURIComponent(intent.projectId)}/actions/${intent.action}`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ projectId: intent.projectId, kind: 'standalone', containerIds: intent.containerIds, action: intent.action }),
    },
    'standalone',
    (payload) => decodeStandaloneActionReceipt(payload, intent),
    fetcher,
  )
}

export async function requestContainerLogs(
  containerId: string,
  tail = 120,
  fetcher: typeof fetch = fetch,
): Promise<ContainerLogsResult> {
  return requestJson(
    `/api/v1/docker/containers/${encodeURIComponent(containerId)}/logs?tail=${tail}`,
    {},
    isContainerLogsResult,
    fetcher,
    'DOCKER_LOGS_RESPONSE_INVALID',
  )
}

export class CreateContainerOutcomeUnknownError extends NcpApiError {
  constructor() {
    super('DOCKER_CONTAINER_CREATE_OUTCOME_UNKNOWN', '无法核实容器创建结果，请先检查容器列表，勿重复提交。')
    this.name = 'CreateContainerOutcomeUnknownError'
  }
}

// 仅已核实在创建副作用之前返回的状态/稳定码配对允许作为普通拒绝处理。
// Agent 不可用及创建/启动/检查/补偿失败均可能发生在创建之后，不能加入此表。
const createContainerRejections = new Map<string, number>([
  ['DOCKER_CONTAINER_CREATE_INVALID', 400],
  ['CONTROL_INPUT_INVALID', 400],
  ['AUTH_UNAUTHORIZED', 401],
  ['HTTP_CROSS_ORIGIN_REJECTED', 403],
  ['ROUTE_NOT_FOUND', 404],
  ['METHOD_NOT_ALLOWED', 405],
  ['HTTP_REQUEST_BODY_TOO_LARGE', 413],
  ['HTTP_UNSUPPORTED_MEDIA_TYPE', 415],
  ['AUTH_UNAVAILABLE', 503],
])

export async function createDockerContainer(
  input: DockerContainerCreateInput,
  fetcher: typeof fetch = fetch,
): Promise<DockerContainerCreateResult> {
  // 固定本次提交意图，避免等待响应时表单变化影响核验。
  const runContainer = input.runContainer
  // 本地序列化与请求构造失败尚未调用 fetch，不能误报为远端结果未知。
  // 禁止 fetch 通过 307/308 自动向跳转目标重发创建 POST。
  const options = requestOptions({ ...jsonRequest(input), redirect: 'error' })
  let response: Response
  try {
    response = await fetcher('/api/v1/docker/containers', options)
  } catch {
    throw new CreateContainerOutcomeUnknownError()
  }
  // 跳转后的响应只描述后续请求，不能确认原始创建请求的结果。
  if (response.redirected) throw new CreateContainerOutcomeUnknownError()

  let payload: unknown
  try {
    payload = await response.json()
  } catch {
    throw new CreateContainerOutcomeUnknownError()
  }
  if (!response.ok) {
    if (isApiErrorResponse(payload) && createContainerRejections.get(payload.code) === response.status) {
      throw new NcpApiError(payload.code, payload.message, payload.requestId)
    }
    throw new CreateContainerOutcomeUnknownError()
  }
  if (!isDockerContainerCreateResult(payload, runContainer)) {
    throw new CreateContainerOutcomeUnknownError()
  }
  return payload
}

// 这些状态/稳定码配对仅在删除副作用之前产生；注册表失败、回滚失败及
// Agent 通道异常可能发生在部分容器已经删除之后，必须保留结果未知语义。
const projectDeleteRejections = new Map<string, readonly [number, string]>([
  ['DOCKER_PROJECT_DELETE_INVALID', [400, 'Docker 项目删除参数无效。']],
  ['DOCKER_PROJECT_DELETE_RUNNING', [409, '项目仍有运行中的容器，停止后才能删除。']],
  ['DOCKER_PROJECT_DELETE_PROTECTED', [409, '该项目受保护，不能删除。']],
  ['DOCKER_PROJECT_DELETE_REGISTRY_NOT_FOUND', [409, '未找到精确匹配的项目注册记录，已拒绝删除。']],
  ['DOCKER_PROJECT_DELETE_NOT_FOUND', [404, '当前 Docker 清单中找不到该项目，请先刷新状态。']],
  ['CONTROL_INPUT_INVALID', [400, '请求参数无效。']],
  ['AUTH_UNAUTHORIZED', [401, '请先登录。']],
  ['AUTH_UNAVAILABLE', [503, '登录服务暂不可用。']],
  ['HTTP_CROSS_ORIGIN_REJECTED', [403, '请求来源校验失败。']],
  ['ROUTE_NOT_FOUND', [404, '请求的接口不存在。']],
  ['METHOD_NOT_ALLOWED', [405, '请求方法不受支持。']],
  ['HTTP_REQUEST_BODY_TOO_LARGE', [413, '请求体超过大小限制。']],
  ['HTTP_UNSUPPORTED_MEDIA_TYPE', [415, '请求必须使用 application/json 内容类型。']],
])

class DockerProjectDeleteRejectedError extends NcpApiError {}
export function isDockerProjectDeleteRejected(error: unknown): error is NcpApiError {
  return error instanceof DockerProjectDeleteRejectedError
}

function projectDeleteOutcomeUnknown(): NcpApiError {
  return new NcpApiError('DOCKER_PROJECT_DELETE_OUTCOME_UNKNOWN',
    '项目可能已部分或全部删除，结果尚未确认。请先只读核对容器及项目注册状态，勿重复提交。')
}

// DeleteProject RPC 沿用 gRPC 默认 4MiB 接收上限；HTTP JSON 字符串
// 转义最多放大六倍。32MiB 留出结构余量，不对合法项目另设容器数量上限。
const projectDeleteResponseMaxBytes = 32 * 1024 * 1024

function parseProjectDeleteJSON(source: string): unknown {
  const payload: unknown = JSON.parse(source)
  // JSON.parse 的后键覆盖会掩盖矛盾结果，按对象分别核对解码后的键。
  const stack: (Set<string> | null)[] = []
  const tokens = /"(?:\\.|[^"\\])*"|[{}[\]:,]/g
  for (const match of source.matchAll(tokens)) {
    const token = match[0]
    if (token === '{' || token === '[') {
      stack.push(token === '{' ? new Set() : null)
      if (stack.length > 32) throw projectDeleteOutcomeUnknown()
    } else if (token === '}' || token === ']') stack.pop()
    else if (token.startsWith('"') && /^\s*:/.test(source.slice(match.index! + token.length))) {
      const keys = stack.at(-1), key = JSON.parse(token) as string
      if (!keys || keys.has(key)) throw projectDeleteOutcomeUnknown()
      keys.add(key)
    }
  }
  return payload
}

async function readProjectDeleteJSON(response: Response): Promise<unknown> {
  if (response.headers.get('content-type')?.split(';')[0]?.trim().toLowerCase() !== 'application/json' || !response.body) throw projectDeleteOutcomeUnknown()
  const reader = response.body.getReader()
  const decoder = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true })
  let bytes = 0, source = ''
  try {
    for (;;) {
      const chunk = await reader.read()
      if (chunk.done) break
      bytes += chunk.value.byteLength
      if (bytes > projectDeleteResponseMaxBytes) throw projectDeleteOutcomeUnknown()
      source += decoder.decode(chunk.value, { stream: true })
    }
    return parseProjectDeleteJSON(source + decoder.decode())
  } catch (error) {
    await reader.cancel().catch(() => {})
    throw error
  } finally { reader.releaseLock() }
}

export async function deleteDockerProject(
  project: Pick<DockerProject, 'id' | 'name' | 'kind'>,
  fetcher: typeof fetch = fetch,
): Promise<DockerProjectDeleteResult> {
  // 固定提交身份；等待期间调用方的对象变化不能改写回执核验目标。
  const projectId = project.id
  const path = `/api/v1/docker/compose/projects/${encodeURIComponent(projectId)}`
  const options = requestOptions({
    method: 'DELETE',
    redirect: 'error',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ projectId, kind: project.kind, registryName: project.name }),
  })
  let response: Response
  let payload: unknown
  try {
    response = await fetcher(path, options)
    if (response.redirected !== false) throw projectDeleteOutcomeUnknown()
    payload = await readProjectDeleteJSON(response)
  } catch {
    throw projectDeleteOutcomeUnknown()
  }
  if (response.status !== 200) {
    if (isApiErrorResponse(payload)) {
      const rejection = projectDeleteRejections.get(payload.code)
      if (rejection?.[0] === response.status) {
        const requestId = /^[A-Za-z0-9._:-]{1,128}$/.test(payload.requestId) ? payload.requestId : undefined
        throw new DockerProjectDeleteRejectedError(payload.code, rejection[1], requestId)
      }
    }
    throw projectDeleteOutcomeUnknown()
  }
  if (!isDockerProjectDeleteResult(payload, projectId)) throw projectDeleteOutcomeUnknown()
  return payload
}

export async function readComposeConfig(
  project: Pick<DockerProject, 'id' | 'workingDirectory' | 'configFiles'>,
  fetcher: typeof fetch = fetch,
): Promise<ComposeProjectConfig> {
  return requestJson(
    '/api/v1/docker/compose/config/read',
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        projectId: project.id,
        workingDirectory: project.workingDirectory,
        configFiles: project.configFiles,
      }),
    },
    isComposeProjectConfig,
    fetcher,
    'COMPOSE_CONFIG_RESPONSE_INVALID',
  ).catch((caught: unknown) => {
    if (caught instanceof NcpApiError && caught.code === 'COMPOSE_CONFIG_RESPONSE_INVALID') {
      throw new NcpApiError(caught.code, '无法核实 Compose 配置及磁盘基线，请更新 Agent/Server 后重新读取。', caught.requestId)
    }
    throw caught
  })
}

export async function validateComposeConfig(
  path: string,
  content: string,
  fetcher: typeof fetch = fetch,
): Promise<ComposeValidationResult> {
  return requestJson(
    '/api/v1/docker/compose/config/validate',
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path, content }),
    },
    isComposeValidationResult,
    fetcher,
    'COMPOSE_VALIDATION_RESPONSE_INVALID',
  )
}

export async function deployComposeConfig(
  input: ComposeDeployInput,
  fetcher: typeof fetch = fetch,
): Promise<JobSnapshot> {
  if (!isComposeContentHash(input.baseContentHash)) {
    throw new NcpApiError('COMPOSE_BASELINE_INVALID', '缺少有效的 Compose 编辑基线，请重新读取配置后再发布。')
  }
  return requestJobSnapshot(
    '/api/v1/docker/compose/deploy',
    { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) },
    fetcher,
    'COMPOSE_DEPLOY_RESPONSE_INVALID',
  )
}

export async function requestJobSnapshot(
  path: string,
  init: RequestInit,
  fetcher: typeof fetch,
  invalidCode: string,
): Promise<JobSnapshot> {
  const payload = await requestJson(path, init, isRecord, fetcher, invalidCode)
  const job = parseJobSnapshot(payload)
  if (!job) {
    throw new NcpApiError(invalidCode, 'NCP 服务返回了无法识别的数据。')
  }
  return job
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function isStringArrayOrNull(value: unknown): value is string[] | null {
  return value === null || (Array.isArray(value) && value.every((item) => typeof item === 'string'))
}

function isDockerImageWireInventory(value: unknown): value is DockerImageWireInventory {
  return (
    isRecord(value) &&
    typeof value.collectedAt === 'string' &&
    Array.isArray(value.images) &&
    value.images.every(
      (image) =>
        isRecord(image) &&
        typeof image.id === 'string' &&
        isStringArrayOrNull(image.repoTags) &&
        isStringArrayOrNull(image.repoDigests) &&
        typeof image.sizeBytes === 'number' &&
        typeof image.createdAt === 'string' &&
        typeof image.containers === 'number',
    )
  )
}

function parseJobSnapshot(value: unknown): JobSnapshot | null {
  if (!(isRecord(value) &&
    typeof value.id === 'string' &&
    typeof value.type === 'string' &&
    (value.status === 'queued' || value.status === 'running' || value.status === 'completed' || value.status === 'failed' || value.status === 'interrupted' || value.status === 'cancelled') &&
    typeof value.message === 'string' &&
    typeof value.progress === 'number' &&
    typeof value.createdAt === 'string' &&
    typeof value.updatedAt === 'string' &&
    typeof value.downloadedBytes === 'number' &&
    typeof value.totalBytes === 'number' &&
    typeof value.speedBytes === 'number' &&
    isRecord(value.layers) &&
    (value.cancellationStatus === undefined || value.cancellationStatus === 'requested' || value.cancellationStatus === 'confirmed' || value.cancellationStatus === 'unknown') &&
    (value.persistenceStatus === undefined || value.persistenceStatus === 'pending') &&
    (value.cancellable === undefined || typeof value.cancellable === 'boolean') &&
    (value.progressLimited === undefined || typeof value.progressLimited === 'boolean') &&
    (value.errorCode === undefined || typeof value.errorCode === 'string'))) {
    return null
  }
  const artifactState =
    value.artifactState === 'present' || value.artifactState === 'deleted' || value.artifactState === 'unknown'
      ? value.artifactState
      : 'unknown'
  return { ...value, artifactState } as unknown as JobSnapshot
}

function isDockerImageRemoveResult(value: unknown): value is DockerImageRemoveResult {
  return isRecord(value) && typeof value.imageId === 'string' && value.removed === true
}

function isDockerImageRemoveBatchResult(value: unknown, requestedIds: readonly string[]): value is DockerImageRemoveBatchResult {
  if (!isRecord(value) || !Array.isArray(value.items) || value.items.length !== requestedIds.length ||
    typeof value.removedCount !== 'number' || !Number.isSafeInteger(value.removedCount) || value.removedCount < 0 ||
    typeof value.failedCount !== 'number' || !Number.isSafeInteger(value.failedCount) || value.failedCount < 0 ||
    typeof value.completed !== 'boolean') return false

  const remainingIds = new Set(requestedIds)
  if (remainingIds.size !== requestedIds.length) return false
  let removedCount = 0
  for (const item of value.items) {
    if (!isRecord(item) || typeof item.imageId !== 'string' || !remainingIds.delete(item.imageId) ||
      typeof item.removed !== 'boolean') return false
    if (item.removed) {
      if (item.errorCode !== undefined && item.errorCode !== '') return false
      removedCount += 1
    } else if (typeof item.errorCode !== 'string' || !item.errorCode.trim()) {
      return false
    }
  }
  const failedCount = requestedIds.length - removedCount
  return remainingIds.size === 0 && value.removedCount === removedCount &&
    value.failedCount === failedCount && value.completed === (failedCount === 0)
}

function isDockerHubRepository(value: unknown): value is DockerHubRepository {
  return isRecord(value) &&
    typeof value.name === 'string' &&
    typeof value.namespace === 'string' &&
    typeof value.description === 'string' &&
    typeof value.starCount === 'number' &&
    typeof value.pullCount === 'number' &&
    typeof value.official === 'boolean'
}

function isDockerHubSearchResult(value: unknown): value is DockerHubSearchResult {
  return isRecord(value) &&
    typeof value.count === 'number' &&
    typeof value.page === 'number' &&
    typeof value.pageSize === 'number' &&
    Array.isArray(value.results) &&
    value.results.every(isDockerHubRepository)
}

function isDockerHubTagWire(value: unknown): value is DockerHubTagWire {
  return isRecord(value) &&
    typeof value.name === 'string' &&
    (value.publishedAt === undefined || typeof value.publishedAt === 'string') &&
    typeof value.lastUpdated === 'string' &&
    typeof value.fullSize === 'number' &&
    Array.isArray(value.architectures) &&
    value.architectures.every((item) => typeof item === 'string')
}

function isDockerHubTagsWireResult(value: unknown): value is DockerHubTagsWireResult {
  return isRecord(value) &&
    typeof value.count === 'number' &&
    typeof value.page === 'number' &&
    typeof value.pageSize === 'number' &&
    Array.isArray(value.results) &&
    value.results.every(isDockerHubTagWire)
}

function isDockerResources(value: unknown): value is DockerResources {
  return isRecord(value) &&
    typeof value.collectedAt === 'string' &&
    Array.isArray(value.networks) &&
    value.networks.every((network) => isRecord(network) &&
      typeof network.id === 'string' &&
      typeof network.name === 'string' &&
      typeof network.driver === 'string' &&
      typeof network.scope === 'string' &&
      typeof network.internal === 'boolean' &&
      Array.isArray(network.subnets) && network.subnets.every((item) => typeof item === 'string') &&
      Array.isArray(network.gateways) && network.gateways.every((item) => typeof item === 'string')) &&
    Array.isArray(value.volumes) &&
    value.volumes.every((volume) => isRecord(volume) &&
      typeof volume.name === 'string' &&
      typeof volume.driver === 'string' &&
      typeof volume.scope === 'string' &&
      typeof volume.mountpoint === 'string')
}

function isServiceListResponse(value: unknown): value is ServiceListResponse {
  return isRecord(value) && typeof value.collectedAt === 'string' && Array.isArray(value.services)
}

function isContainerDetails(value: unknown): value is ContainerDetails {
  return (
    isRecord(value) &&
    typeof value.id === 'string' &&
    typeof value.name === 'string' &&
    typeof value.image === 'string' &&
    typeof value.state === 'string' &&
    typeof value.healthFailingStreak === 'number' &&
    typeof value.exitCode === 'number' &&
    typeof value.restartCount === 'number' &&
    typeof value.oomKilled === 'boolean' &&
    typeof value.restartMaximumRetries === 'number' &&
    typeof value.autoRemove === 'boolean' &&
    typeof value.privileged === 'boolean' &&
    typeof value.readonlyRootfs === 'boolean' &&
    typeof value.nanoCpus === 'number' &&
    typeof value.memoryBytes === 'number' &&
    Array.isArray(value.ports) &&
    Array.isArray(value.mounts) &&
    Array.isArray(value.networks)
  )
}

function isContainerLogsResult(value: unknown): value is ContainerLogsResult {
  return (
    isRecord(value) &&
    typeof value.containerId === 'string' &&
    typeof value.tail === 'number' &&
    typeof value.collectedAt === 'string' &&
    Array.isArray(value.entries) &&
    value.entries.every(
      (entry) =>
        isRecord(entry) &&
        typeof entry.timestamp === 'string' &&
        (entry.level === 'error' || entry.level === 'warning' || entry.level === 'info' || entry.level === 'debug') &&
        (entry.stream === 'stdout' || entry.stream === 'stderr') &&
        typeof entry.message === 'string',
    )
  )
}

function isDockerContainerCreateResult(value: unknown, runContainer: boolean): value is DockerContainerCreateResult {
  return (
    isRecord(value) &&
    typeof value.containerId === 'string' && value.containerId.trim().length > 0 &&
    typeof value.name === 'string' &&
    typeof value.image === 'string' && value.image.trim().length > 0 &&
    (value.state === 'running' || value.state === 'stopped') &&
    value.created === true &&
    typeof value.started === 'boolean' && value.started === runContainer &&
    typeof value.runContainer === 'boolean' && value.runContainer === runContainer
  )
}

function isDockerProjectDeleteResult(value: unknown, projectId: string): value is DockerProjectDeleteResult {
  const containerIds = new Set<string>()
  return (
    isRecord(value) &&
    typeof value.projectId === 'string' && value.projectId === projectId && projectId.trim().length > 0 &&
    value.kind === 'compose' &&
    value.completed === true &&
    value.partial === false &&
    value.registryDeleted === true &&
    value.registryRolledBack === false &&
    Array.isArray(value.containers) &&
    value.containers.every((container) => {
      if (!isRecord(container) ||
        typeof container.containerId !== 'string' || !container.containerId.trim() ||
        containerIds.has(container.containerId) ||
        typeof container.name !== 'string' ||
        typeof container.state !== 'string' ||
        container.deleted !== true || container.success !== true ||
        (container.errorCode !== undefined && container.errorCode !== '')) return false
      containerIds.add(container.containerId)
      return true
    })
  )
}

export function isComposeContentHash(value: unknown): value is string {
  return typeof value === 'string' && value.length === 64 && /^[0-9a-f]{64}$/.test(value)
}

function isComposeProjectConfig(value: unknown): value is ComposeProjectConfig {
  return isRecord(value) &&
    typeof value.projectId === 'string' &&
    typeof value.workingDirectory === 'string' &&
    typeof value.collectedAt === 'string' &&
    Array.isArray(value.files) &&
    value.files.every((file) => isRecord(file) && typeof file.path === 'string' && typeof file.name === 'string' && typeof file.content === 'string' && isComposeContentHash(file.contentHash) && typeof file.size === 'number')
}

function isComposeValidationResult(value: unknown): value is ComposeValidationResult {
  return isRecord(value) &&
    value.valid === true &&
    Array.isArray(value.services) &&
    value.services.every((service) => typeof service === 'string') &&
    typeof value.normalized === 'string'
}
