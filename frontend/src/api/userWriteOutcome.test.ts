import { afterEach, describe, expect, it, vi } from 'vitest'
import { createUser, deleteUser, updateUserPassword, updateUserStatus } from './control'
import { NcpApiError } from './systemTransport'
import { UserWriteOutcomeUnknownError, type UserWriteOperation } from './userWriteOutcome'

const user = { id: 2, username: 'fixture-root', role: 'root', disabled: false, createdAt: '2026-10-13T00:00:00Z', updatedAt: '2026-10-13T00:00:00Z' }
const secret = 'fixture-secret-never-display'
const operations: Record<UserWriteOperation, () => Promise<unknown>> = {
  create: () => createUser({ username: user.username, password: secret }),
  status: () => updateUserStatus(2, true),
  password: () => updateUserPassword(2, { currentPassword: secret, newPassword: secret }),
  delete: () => deleteUser(2),
}
const names = Object.keys(operations) as UserWriteOperation[]
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status })
function mock(response: Response) { const fetcher = vi.fn().mockResolvedValue(response); vi.stubGlobal('fetch', fetcher); return fetcher }
function errorBody(code: string) { return { code, message: secret, requestId: secret } }
async function unknown(operation: UserWriteOperation, response: Response) {
  const fetcher = mock(response)
  const error = await operations[operation]().catch((error: unknown) => error)
  expect(error).toBeInstanceOf(UserWriteOutcomeUnknownError)
  expect(error).toMatchObject({ code: 'USER_WRITE_OUTCOME_UNKNOWN', operation })
  expect(String(error)).not.toContain(secret)
  expect(JSON.stringify(error)).not.toContain(secret)
  expect(fetcher).toHaveBeenCalledTimes(1)
}
afterEach(() => vi.unstubAllGlobals())

describe('账号写入精确成功合同', () => {
  it('创建仅接受 201 完整匹配账号，并按 Go Unicode 空白归一化', async () => {
    const fetcher = mock(json({ ...user, extra: 'compatible' }, 201))
    await expect(createUser({ username: '\u0085 fixture-root\u0085', password: secret })).resolves.toMatchObject(user)
    expect(fetcher.mock.calls[0]![1]).toMatchObject({ method: 'POST', credentials: 'same-origin', redirect: 'error' })
  })
  it('状态接受 200 完整匹配 ID 和 disabled', async () => {
    mock(json({ ...user, disabled: true }))
    await expect(operations.status()).resolves.toEqual({ ...user, disabled: true })
  })
  it.each(['password', 'delete'] as const)('%s 仅接受空 204', async (operation) => {
    mock(new Response(null, { status: 204 }))
    await expect(operations[operation]()).resolves.toBeUndefined()
    await unknown(operation, json({}, 200))
    await unknown(operation, json({}, 201))
    await unknown(operation, json({}, 202))
  })
  it.each(['create', 'status'] as const)('%s 拒绝空 204 和未完整响应', async (operation) => {
    await unknown(operation, new Response(null, { status: 204 }))
    for (const value of [null, [], {}, false, { ...user, id: 0 }, { ...user, role: 'admin' }, { ...user, createdAt: '2026-02-30T00:00:00Z' }]) {
      await unknown(operation, json(value, operation === 'create' ? 201 : 200))
    }
  })
  it('创建拒绝错用户名、禁用账号和错误成功状态', async () => {
    await unknown('create', json({ ...user, username: 'other' }, 201))
    await unknown('create', json({ ...user, disabled: true }, 201))
    await unknown('create', json(user, 200))
  })
  it('创建不能把空用户名视为已创建账号', async () => {
    mock(json({ ...user, username: '' }, 201))
    await expect(createUser({ username: ' ', password: secret })).rejects.toBeInstanceOf(UserWriteOutcomeUnknownError)
  })
  it('状态拒绝错目标、错意图和错误成功状态', async () => {
    await unknown('status', json({ ...user, id: 3, disabled: true }))
    await unknown('status', json(user))
    await unknown('status', json({ ...user, disabled: true }, 201))
  })
  it('提交后表单变化不改变核验意图或请求体', async () => {
    let resolve!: (response: Response) => void
    const fetcher = vi.fn<typeof fetch>(() => new Promise<Response>((yes) => { resolve = yes }))
    vi.stubGlobal('fetch', fetcher)
    const input = { username: user.username, password: secret }
    const write = createUser(input)
    input.username = 'changed'; input.password = 'changed'
    resolve(json(user, 201))
    await expect(write).resolves.toMatchObject(user)
    expect(JSON.parse(fetcher.mock.calls[0]![1]!.body as string)).toEqual({ username: user.username, password: secret })
  })
})

describe('账号写入不确定传输与明确拒绝', () => {
  it.each(names)('%s 网络错误不泄露凭据并禁止跳转', async (operation) => {
    const fetcher = vi.fn().mockRejectedValue(new Error(secret)); vi.stubGlobal('fetch', fetcher)
    const error = await operations[operation]().catch((error: unknown) => error)
    expect(error).toBeInstanceOf(UserWriteOutcomeUnknownError)
    expect(String(error) + JSON.stringify(error)).not.toContain(secret)
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(fetcher.mock.calls[0]![1]).toMatchObject({ redirect: 'error', credentials: 'same-origin' })
  })
  it.each(names)('%s 已跳转、坏 JSON、读取失败均未知', async (operation) => {
    const redirected = new Response(null, { status: 204 }); Object.defineProperty(redirected, 'redirected', { value: true })
    await unknown(operation, redirected)
    await unknown(operation, new Response('<html>bad</html>', { status: 200 }))
    const broken = json(user, 201); vi.spyOn(broken, 'json').mockRejectedValue(new Error(secret))
    await unknown(operation, broken)
    await unknown(operation, json(errorBody('USER_OPERATION_FAILED'), 500))
    await unknown(operation, json(errorBody('AUTH_INPUT_INVALID'), 500))
    await unknown(operation, json({ code: 'AUTH_INPUT_INVALID' }, 400))
    await unknown(operation, json(errorBody('toString'), 400))
  })
  it.each(names)('%s 明确派发前拒绝只用稳定配对与固定文案', async (operation) => {
    for (const [code, status] of [['AUTH_INPUT_INVALID', 400], ['AUTH_UNAUTHORIZED', 401], ['HTTP_CROSS_ORIGIN_REJECTED', 403], ['ROUTE_NOT_FOUND', 404], ['METHOD_NOT_ALLOWED', 405], ['AUTH_UNAVAILABLE', 503], ['USER_MANAGEMENT_UNAVAILABLE', 503]] as const) {
      mock(json(errorBody(code), status))
      const error = await operations[operation]().catch((error: unknown) => error)
      expect(error).toBeInstanceOf(NcpApiError)
      expect(error).not.toBeInstanceOf(UserWriteOutcomeUnknownError)
      expect(error).toMatchObject({ code })
      expect(String(error) + JSON.stringify(error)).not.toContain(secret)
    }
  })
  it.each([
    ['create', 'AUTH_USERNAME_EXISTS', 409], ['password', 'AUTH_CURRENT_PASSWORD_INVALID', 401],
    ['password', 'AUTH_USER_NOT_FOUND', 404], ['delete', 'AUTH_USER_NOT_FOUND', 404],
    ['status', 'AUTH_CURRENT_USER_PROTECTED', 409], ['delete', 'AUTH_LAST_USER_PROTECTED', 409],
    ['create', 'HTTP_UNSUPPORTED_MEDIA_TYPE', 415], ['status', 'HTTP_REQUEST_BODY_TOO_LARGE', 413],
  ] as const)('%s 的 %s/%i 明确拒绝', async (operation, code, status) => {
    mock(json(errorBody(code), status))
    const error = await operations[operation]().catch((error: unknown) => error)
    expect(error).toMatchObject({ code }); expect(error).not.toBeInstanceOf(UserWriteOutcomeUnknownError)
  })
  it('状态提交后查询可以返回 404；其他操作专有码也不互相借用', async () => {
    await unknown('status', json(errorBody('AUTH_USER_NOT_FOUND'), 404))
    await unknown('create', json(errorBody('AUTH_USER_NOT_FOUND'), 404))
    await unknown('password', json(errorBody('AUTH_USERNAME_EXISTS'), 409))
    await unknown('delete', json(errorBody('HTTP_UNSUPPORTED_MEDIA_TYPE'), 415))
  })
  it('本地序列化失败未发送，不冒充未知结果', () => {
    const fetcher = vi.fn(); vi.stubGlobal('fetch', fetcher)
    const input = { username: user.username, password: secret, toJSON() { throw new Error('local serialization') } }
    expect(() => createUser(input)).toThrow('local serialization')
    expect(fetcher).not.toHaveBeenCalled()
  })
})
