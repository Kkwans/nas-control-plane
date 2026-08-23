import {
  NcpApiError,
  jsonRequest,
  requestJson,
  requestOptions,
  responseError,
} from './systemTransport'
import type {
  DockerHubRepository,
  DockerHubSearchResult,
  DockerHubTag,
  DockerHubTagsResult,
  DockerImageInventory,
  DockerImageSummary,
  DockerImageRemoveBatchResult,
  DockerImageRemoveResult,
  JobListResult,
  JobSnapshot,
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

export function followJob(jobId: string, onProgress: (job: JobSnapshot) => void): Promise<JobSnapshot> {
  return new Promise((resolve, reject) => {
    const source = new EventSource(`/api/v1/jobs/${encodeURIComponent(jobId)}/events`)
    source.addEventListener('progress', (event) => {
      try {
        const payload: unknown = JSON.parse((event as MessageEvent<string>).data)
        const job = parseJobSnapshot(payload)
        if (!job) throw new Error('任务进度格式无效')
        onProgress(job)
        if (job.status === 'completed' || job.status === 'failed' || job.status === 'interrupted' || job.status === 'cancelled') {
          source.close()
          resolve(job)
        }
      } catch (error) {
        source.close()
        reject(error)
      }
    })
    source.onerror = () => {
      source.close()
      reject(new NcpApiError('JOB_STREAM_FAILED', '任务进度连接已中断。'))
    }
  })
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
  return requestJson(
    '/api/v1/docker/images/remove-batch',
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ imageIds }),
    },
    isDockerImageRemoveBatchResult,
    fetcher,
    'DOCKER_IMAGE_REMOVE_BATCH_RESPONSE_INVALID',
  )
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
    isRecord(value.layers))) {
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

function isDockerImageRemoveBatchResult(value: unknown): value is DockerImageRemoveBatchResult {
  return (
    isRecord(value) &&
    typeof value.removedCount === 'number' &&
    typeof value.failedCount === 'number' &&
    typeof value.completed === 'boolean' &&
    Array.isArray(value.items) &&
    value.items.every(
      (item) =>
        isRecord(item) &&
        typeof item.imageId === 'string' &&
        typeof item.removed === 'boolean' &&
        (item.errorCode === undefined || typeof item.errorCode === 'string'),
    )
  )
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
