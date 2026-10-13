// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type App } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { NcpApiError } from '@/api/system'
import { useAuthStore } from '@/stores/auth'
import LoginView from './LoginView.vue'

const api = vi.hoisted(() => ({ bootstrapRoot: vi.fn(), loginRoot: vi.fn(), logoutRoot: vi.fn(), requestAuthStatus: vi.fn() }))
vi.mock('@/api/system', () => ({
  NcpApiError: class extends Error { constructor(readonly code: string, message: string) { super(message) } },
  ...api,
}))
vi.mock('@/components/NcpLogo.vue', () => ({ default: { render: () => null } }))
let app: App
let host: HTMLDivElement
let store: ReturnType<typeof useAuthStore>
const authenticated = vi.fn()
const user = { id: 1, username: 'root', role: 'root' }
async function settle() { for (let i = 0; i < 8; i++) await Promise.resolve(); await nextTick() }
function input(index: number, value: string) {
  const element = host.querySelectorAll('input')[index]!
  element.value = value
  element.dispatchEvent(new Event('input', { bubbles: true }))
}
function submit() { host.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) }
function fill() { input(0, 'root'); input(1, 'secret-one'); input(2, 'secret-one') }
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
beforeEach(() => {
  vi.resetAllMocks()
  const pinia = createPinia()
  setActivePinia(pinia)
  store = useAuthStore()
  store.state = 'setup'
  host = document.createElement('div')
  document.body.append(host)
  app = createApp({ render: () => h(LoginView, { onAuthenticated: authenticated }) })
  app.use(pinia)
  app.mount(host)
})
afterEach(() => { app.unmount(); host.remove() })

describe('LoginView 初始化结果恢复', () => {
  it('已有账号切换登录并清理密码，不发认证事件或自动登录', async () => {
    api.bootstrapRoot.mockRejectedValue(new NcpApiError('AUTH_UNAVAILABLE', 'failure'))
    api.requestAuthStatus.mockResolvedValue({ initialized: true, authenticated: false })
    fill(); submit(); await settle()
    expect(host.textContent).toContain('控制台已初始化，请登录。')
    expect(host.querySelector('[role=status]')?.classList.contains('access-form__notice')).toBe(true)
    expect(host.querySelector('.access-form__error')).toBeNull()
    expect(host.querySelectorAll('input')).toHaveLength(2)
    expect(host.querySelectorAll('input')[0]!.value).toBe('root')
    expect(host.querySelectorAll('input')[1]!.value).toBe('')
    expect(authenticated).not.toHaveBeenCalled()
    expect(api.loginRoot).not.toHaveBeenCalled()
    store.state = 'setup'; await nextTick()
    expect(host.querySelectorAll('input')[2]!.value).toBe('')
  })

  it('明确未初始化仍清除本次密码，允许用户再次填写', async () => {
    api.bootstrapRoot.mockRejectedValue(new TypeError('offline'))
    api.requestAuthStatus.mockResolvedValue({ initialized: false, authenticated: false })
    fill(); submit(); await settle()
    expect(host.textContent).toContain('尚未初始化')
    expect(host.querySelector('[role=status]')?.classList.contains('access-form__notice')).toBe(true)
    expect(host.querySelector('.access-form__error')).toBeNull()
    expect([...host.querySelectorAll('input[type=password]')].map((element) => (element as HTMLInputElement).value)).toEqual(['', ''])
    expect(authenticated).not.toHaveBeenCalled()
  })

  it('复核失败显示不确定结果；重新检查只有只读请求', async () => {
    api.bootstrapRoot.mockRejectedValue(new TypeError('offline'))
    api.requestAuthStatus.mockRejectedValueOnce(new TypeError('offline'))
    fill(); submit(); await settle()
    expect(host.textContent).toContain('初始化结果尚未确认，请重新检查。')
    expect(host.querySelector('form')).toBeNull()
    api.requestAuthStatus.mockResolvedValueOnce({ initialized: true, authenticated: false })
    host.querySelector('button')!.click(); await settle()
    expect(store.state).toBe('anonymous')
    expect(host.querySelectorAll('input')[1]!.value).toBe('')
    expect(api.bootstrapRoot).toHaveBeenCalledTimes(1)
    expect(api.requestAuthStatus).toHaveBeenCalledTimes(2)
    expect(api.loginRoot).not.toHaveBeenCalled()
    expect(authenticated).not.toHaveBeenCalled()
  })

  it.each(['success', 'status'])('只有权威认证成功才发事件：%s', async (source) => {
    if (source === 'success') api.bootstrapRoot.mockResolvedValue({ user })
    else {
      api.bootstrapRoot.mockRejectedValue(new TypeError('offline'))
      api.requestAuthStatus.mockResolvedValue({ initialized: true, authenticated: true, user })
    }
    fill(); submit(); await settle()
    expect(authenticated).toHaveBeenCalledTimes(1)
  })

  it('明确校验失败保留可修正字段', async () => {
    api.bootstrapRoot.mockRejectedValue(new NcpApiError('AUTH_INPUT_INVALID', '请检查账号'))
    fill(); submit(); await settle()
    expect(host.textContent).toContain('请检查账号')
    expect(host.querySelector('[role=alert]')?.classList.contains('access-form__error')).toBe(true)
    expect(host.querySelector('.access-form__notice')).toBeNull()
    expect(host.querySelectorAll('input')[1]!.value).toBe('secret-one')
    expect(host.querySelectorAll('input')[2]!.value).toBe('secret-one')
    expect(api.requestAuthStatus).not.toHaveBeenCalled()
  })

  it('普通登录成功清理密码并发出认证事件', async () => {
    store.state = 'anonymous'; await nextTick()
    api.loginRoot.mockResolvedValue({ user })
    input(0, 'root'); input(1, 'login-test-secret'); submit(); await settle()
    expect(api.loginRoot).toHaveBeenCalledTimes(1)
    expect(api.loginRoot.mock.calls[0]?.[0]).toEqual({ username: 'root', password: 'login-test-secret' })
    expect(api.bootstrapRoot).not.toHaveBeenCalled()
    expect(api.requestAuthStatus).not.toHaveBeenCalled()
    expect(host.querySelectorAll('input')[1]!.value).toBe('')
    expect(authenticated).toHaveBeenCalledTimes(1)
    expect(store.isAuthenticated).toBe(true)
  })

  it('普通登录失败保留可修正密码，不调和或发认证事件', async () => {
    store.state = 'anonymous'; await nextTick()
    api.loginRoot.mockRejectedValue(new NcpApiError('AUTH_INVALID_CREDENTIALS', '账号或密码不正确。'))
    input(0, 'root'); input(1, 'incorrect-test-secret'); submit(); await settle()
    expect(host.textContent).toContain('账号或密码不正确。')
    expect(host.querySelector('[role=alert]')?.classList.contains('access-form__error')).toBe(true)
    expect(host.querySelector('.access-form__notice')).toBeNull()
    expect(host.querySelectorAll('input')[1]!.value).toBe('incorrect-test-secret')
    expect(host.querySelector('button')!.disabled).toBe(false)
    expect(api.bootstrapRoot).not.toHaveBeenCalled()
    expect(api.requestAuthStatus).not.toHaveBeenCalled()
    expect(authenticated).not.toHaveBeenCalled()
    expect(store.state).toBe('anonymous')
  })

  it('服务端退出未确认仍显示错误样式，保留状态播报语义', async () => {
    store.state = 'anonymous'
    store.errorCode = 'LOGOUT_UNCONFIRMED'
    store.errorMessage = '本地已退出，但服务端会话尚未确认。'
    await nextTick()
    const message = host.querySelector('[role=status]')
    expect(message?.textContent).toBe('本地已退出，但服务端会话尚未确认。')
    expect(message?.classList.contains('access-form__error')).toBe(true)
    expect(host.querySelector('.access-form__notice')).toBeNull()
  })

  it('请求及复核期间阻止重复提交', async () => {
    const pending = deferred<unknown>()
    api.bootstrapRoot.mockRejectedValue(new TypeError('offline'))
    api.requestAuthStatus.mockReturnValue(pending.promise)
    fill(); submit(); await settle(); submit()
    expect(host.querySelector('button')!.disabled).toBe(true)
    expect([...host.querySelectorAll('input')].every((element) => element.disabled)).toBe(true)
    expect(api.bootstrapRoot).toHaveBeenCalledTimes(1)
    pending.resolve({ initialized: true, authenticated: false }); await settle()
    expect(host.querySelector('button')!.disabled).toBe(false)
  })

  it('旧复核不覆盖新模式或清除新输入，不发认证事件', async () => {
    const pending = deferred<unknown>()
    api.bootstrapRoot.mockRejectedValue(new TypeError('offline'))
    api.requestAuthStatus.mockReturnValueOnce(pending.promise)
    fill(); submit(); await settle()
    api.requestAuthStatus.mockResolvedValueOnce({ initialized: true, authenticated: false })
    await store.refresh(); await nextTick()
    input(1, 'new-input')
    pending.resolve({ initialized: true, authenticated: true, user }); await settle()
    expect(host.querySelectorAll('input')[1]!.value).toBe('new-input')
    expect(authenticated).not.toHaveBeenCalled()
    expect(store.state).toBe('anonymous')
  })

  it.each(['resolved', 'rejected'])('已完成旧操作的 %s 通知不影响较新操作', async (outcome) => {
    const newer = deferred<unknown>()
    api.loginRoot.mockReturnValue(newer.promise)
    if (outcome === 'resolved') {
      api.bootstrapRoot.mockRejectedValue(new TypeError('offline'))
      api.requestAuthStatus.mockResolvedValue({ initialized: false, authenticated: false })
    } else api.bootstrapRoot.mockRejectedValue(new NcpApiError('VALIDATION_ERROR', '旧错误'))
    const stop = store.$onAction(({ name, after, onError }) => {
      if (name !== 'bootstrap') return
      const supersede = () => {
        void store.login({ username: 'root', password: 'other-test-secret' })
        input(1, 'newer-input')
      }
      after(supersede)
      onError(supersede)
    })
    fill(); submit(); await settle()
    expect(host.querySelectorAll('input')[1]!.value).toBe('newer-input')
    expect(host.textContent).not.toContain('旧错误')
    expect(authenticated).not.toHaveBeenCalled()
    stop()
    newer.resolve({ user }); await settle()
  })

  it('卸载后的迟到成功不发事件', async () => {
    const pending = deferred<unknown>()
    api.bootstrapRoot.mockReturnValue(pending.promise)
    fill(); submit(); app.unmount()
    pending.resolve({ user }); await settle()
    expect(authenticated).not.toHaveBeenCalled()
  })
})
