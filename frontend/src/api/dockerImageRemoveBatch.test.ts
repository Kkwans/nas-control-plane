import { describe, expect, it, vi } from 'vitest'
import { removeDockerImages } from './docker'
import { NcpApiError } from './systemTransport'

const first = `sha256:${'a'.repeat(64)}`
const second = `sha256:${'b'.repeat(64)}`
const third = `sha256:${'c'.repeat(64)}`
const ids = [first, second]
const removed = (imageId: string) => ({ imageId, removed: true })
const failed = (imageId: string, errorCode = 'DOCKER_IMAGE_IN_USE') => ({ imageId, removed: false, errorCode })
const success = () => ({ items: ids.map(removed), removedCount: 2, failedCount: 0, completed: true })
const mixed = () => ({ items: [failed(second), removed(first)], removedCount: 1, failedCount: 1, completed: false })
const response = (body: unknown, status = 200, contentType = 'application/json') => new Response(JSON.stringify(body), {
  status, headers: { 'Content-Type': contentType },
})
const unknownOutcome = { name: 'NcpApiError', code: 'DOCKER_IMAGE_REMOVE_BATCH_OUTCOME_UNKNOWN' }

describe('batch image deletion receipts', () => {
  it.each([success(), { ...success(), items: [removed(first), { ...removed(second), errorCode: '' }] }, mixed(), {
    items: [failed(second, 'DOCKER_IMAGE_REMOVE_FAILED'), failed(first)], removedCount: 0, failedCount: 2, completed: false,
  }])('accepts coherent exact receipts, including reordered mixed failures', async (payload) => {
    const fetcher = vi.fn().mockResolvedValue(response(payload))
    await expect(removeDockerImages(ids, fetcher)).resolves.toEqual(payload)
    expect(fetcher).toHaveBeenCalledOnce()
    expect(fetcher.mock.calls[0]![0]).toBe('/api/v1/docker/images/remove-batch')
    expect(fetcher.mock.calls[0]![1]).toMatchObject({ method: 'POST', redirect: 'error', credentials: 'same-origin' })
    expect(JSON.parse(fetcher.mock.calls[0]![1].body)).toEqual({ imageIds: ids })
  })

  it.each([true, undefined, null])('rejects non-false redirected state %s before reading a receipt', async (redirected) => {
    const receipt = response(success())
    Object.defineProperty(receipt, 'redirected', { value: redirected })
    const read = vi.spyOn(receipt, 'json')
    const fetcher = vi.fn().mockResolvedValue(receipt)
    await expect(removeDockerImages(ids, fetcher)).rejects.toMatchObject(unknownOutcome)
    expect(read).not.toHaveBeenCalled()
    expect(fetcher).toHaveBeenCalledOnce()
  })

  it('does not trust redirected preflight rejection receipts', async () => {
    const receipt = response({ code: 'HTTP_UNSUPPORTED_MEDIA_TYPE', message: '拒绝', requestId: 'fixture' }, 415)
    Object.defineProperty(receipt, 'redirected', { value: true })
    await expect(removeDockerImages(ids, vi.fn().mockResolvedValue(receipt))).rejects.toMatchObject(unknownOutcome)
  })

  it('does not retry a browser rejection caused by redirect:error', async () => {
    const fetcher = vi.fn().mockRejectedValue(new TypeError('Failed to fetch: redirect disallowed'))
    await expect(removeDockerImages(ids, fetcher)).rejects.toMatchObject(unknownOutcome)
    expect(fetcher).toHaveBeenCalledOnce()
    expect(fetcher.mock.calls[0]![1].redirect).toBe('error')
  })

  it.each([
    ['foreign ID', { ...success(), items: [removed(first), removed(third)] }],
    ['missing ID', { items: [removed(first)], removedCount: 1, failedCount: 0, completed: true }],
    ['extra ID', { items: [...ids.map(removed), removed(third)], removedCount: 3, failedCount: 0, completed: true }],
    ['duplicate ID', { ...success(), items: [removed(first), removed(first)] }],
    ['nonboolean removed', { ...success(), items: [removed(first), { imageId: second, removed: 'true' }] }],
    ['missing removed', { ...success(), items: [removed(first), { imageId: second }] }],
    ['success with error', { ...success(), items: [removed(first), { ...removed(second), errorCode: 'DOCKER_IMAGE_IN_USE' }] }],
    ['success with null error', { ...success(), items: [removed(first), { ...removed(second), errorCode: null }] }],
    ['success with nonstring error', { ...success(), items: [removed(first), { ...removed(second), errorCode: false }] }],
    ['failure without error', { ...mixed(), items: [removed(first), { imageId: second, removed: false }] }],
    ['failure with empty error', { ...mixed(), items: [removed(first), failed(second, '')] }],
    ['failure with whitespace error', { ...mixed(), items: [removed(first), failed(second, ' ')] }],
    ['failure with nonstring error', { ...mixed(), items: [removed(first), { ...failed(second), errorCode: 42 }] }],
    ['negative removed count', { ...success(), removedCount: -1 }],
    ['negative failed count', { ...success(), failedCount: -1 }],
    ['fractional removed count', { ...mixed(), removedCount: 1.5 }],
    ['fractional failed count', { ...mixed(), failedCount: 0.5 }],
    ['wrong counts with correct total', { ...mixed(), removedCount: 0, failedCount: 2 }],
    ['wrong total', { ...mixed(), removedCount: 2 }],
    ['string count', { ...success(), removedCount: '2' }],
    ['missing count', { ...success(), removedCount: undefined }],
    ['null count', { ...success(), failedCount: null }],
    ['mixed marked completed', { ...mixed(), completed: true }],
    ['success marked incomplete', { ...success(), completed: false }],
    ['nonboolean completed', { ...success(), completed: 'true' }],
    ['missing items', { ...success(), items: undefined }],
    ['null receipt', null],
  ])('fails closed on %s', async (_label, payload) => {
    const fetcher = vi.fn().mockResolvedValue(response(payload))
    await expect(removeDockerImages(ids, fetcher)).rejects.toMatchObject(unknownOutcome)
    expect(fetcher).toHaveBeenCalledOnce()
  })

  it.each([201, 202, 204, 301, 302, 303, 307, 308, 400, 401, 403, 409, 413, 415, 500, 503])('requires exact HTTP 200, not %s', async (status) => {
    const fetcher = vi.fn().mockResolvedValue(status === 204 ? new Response(null, { status }) : response(success(), status))
    await expect(removeDockerImages(ids, fetcher)).rejects.toMatchObject(unknownOutcome)
    expect(fetcher).toHaveBeenCalledOnce()
  })

  it.each(['text/html', 'text/plain', 'application/problem+json', ''])('rejects implausible media type %s', async (mediaType) => {
    await expect(removeDockerImages(ids, vi.fn().mockResolvedValue(response(success(), 200, mediaType)))).rejects.toMatchObject(unknownOutcome)
  })

  it.each([
    [413, 'HTTP_REQUEST_BODY_TOO_LARGE'],
    [415, 'HTTP_UNSUPPORTED_MEDIA_TYPE'],
  ] as const)('preserves explicit preflight rejection %s only for its matching code', async (status, code) => {
    const error = { code, message: '请求体在执行前被拒绝。', requestId: 'fixture' }
    const fetcher = vi.fn().mockResolvedValue(response(error, status))
    await expect(removeDockerImages(ids, fetcher)).rejects.toMatchObject(error)
    expect(fetcher).toHaveBeenCalledOnce()
    for (const wrongStatus of [200, 409, status === 413 ? 415 : 413]) {
      await expect(removeDockerImages(ids, vi.fn().mockResolvedValue(response(error, wrongStatus)))).rejects.toMatchObject(unknownOutcome)
    }
    await expect(removeDockerImages(ids, vi.fn().mockResolvedValue(response({ ...error, requestId: undefined }, status)))).rejects.toMatchObject(unknownOutcome)
  })

  it('accepts JSON media type with charset and case variation', async () => {
    await expect(removeDockerImages(ids, vi.fn().mockResolvedValue(response(success(), 200, 'Application/JSON; charset=utf-8')))).resolves.toEqual(success())
  })

  it('never retries network failures, unreadable or malformed bodies, or API errors', async () => {
    for (const fetcher of [
      vi.fn().mockRejectedValue(new Error('offline')),
      vi.fn().mockRejectedValue(new DOMException('aborted', 'AbortError')),
      vi.fn().mockResolvedValue(new Response('{', { headers: { 'Content-Type': 'application/json' } })),
      vi.fn().mockResolvedValue({ status: 200, redirected: false, headers: new Headers({ 'Content-Type': 'application/json' }), json: vi.fn().mockRejectedValue(new Error('body interrupted')) }),
      vi.fn().mockResolvedValue(response({ code: 'DOCKER_IMAGE_IN_USE', message: 'safe to retry', requestId: 'r' }, 409)),
    ]) {
      const error = await removeDockerImages(ids, fetcher).catch((caught: unknown) => caught)
      expect(error).toBeInstanceOf(NcpApiError)
      expect(error).toMatchObject(unknownOutcome)
      expect((error as Error).message).not.toMatch(/safe to retry/i)
      expect(fetcher).toHaveBeenCalledOnce()
    }
  })

  it('validates the frozen ID copy when caller mutates its array during transport', async () => {
    const callerIds = [...ids]
    let finish!: (value: Response) => void
    const fetcher = vi.fn().mockImplementation(() => new Promise<Response>((resolve) => { finish = resolve }))
    const pending = removeDockerImages(callerIds, fetcher)
    callerIds.splice(0, callerIds.length, third)
    finish(response(mixed()))
    await expect(pending).resolves.toEqual(mixed())
    expect(JSON.parse(fetcher.mock.calls[0]![1].body)).toEqual({ imageIds: ids })
  })

  it('rejects receipts matching mutated caller IDs rather than dispatched IDs', async () => {
    const callerIds = [...ids]
    const fetcher = vi.fn().mockImplementation(async () => {
      callerIds.splice(0, callerIds.length, third)
      return response({ items: [removed(third)], removedCount: 1, failedCount: 0, completed: true })
    })
    await expect(removeDockerImages(callerIds, fetcher)).rejects.toMatchObject(unknownOutcome)
  })

  it('does not accept a deduplicated receipt for duplicate dispatched IDs', async () => {
    await expect(removeDockerImages([first, first], vi.fn().mockResolvedValue(response({
      items: [removed(first)], removedCount: 1, failedCount: 0, completed: true,
    })))).rejects.toMatchObject(unknownOutcome)
  })
})
