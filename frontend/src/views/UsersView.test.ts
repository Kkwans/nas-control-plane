// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick, type App } from 'vue'
import type { ManagedUser, PasswordPolicy } from '@/api/control'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import UsersView from './UsersView.vue'

const api = vi.hoisted(() => ({ requestUsers: vi.fn(), requestPasswordPolicy: vi.fn(), updatePasswordPolicy: vi.fn(), createUser: vi.fn(), updateUserPassword: vi.fn(), updateUserStatus: vi.fn(), deleteUser: vi.fn() }))
const messages = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn() }))
vi.mock('@/api/control', () => api)
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { id: 1 } }) }))
vi.mock('@/components/WorkspaceHeader.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/ConfirmDangerDialog.vue', () => ({ default: { render: () => null } }))
vi.mock('element-plus', () => {
  const passthrough = defineComponent({ setup(_, { slots }) { return () => h('div', [slots.default?.(), slots.footer?.()]) } })
  return {
    ElMessage: messages,
    ElButton: defineComponent({ props: ['disabled', 'loading'], emits: ['click'], setup(props, { slots, emit }) { return () => h('button', { disabled: props.disabled, onClick: () => emit('click') }, slots.default?.()) } }),
    ElDialog: passthrough, ElDropdown: passthrough, ElDropdownItem: passthrough, ElDropdownMenu: passthrough,
    ElInput: passthrough, ElInputNumber: passthrough, ElSwitch: passthrough,
  }
})
const policy: PasswordPolicy = { minLength: 12, requireUppercase: false, requireLowercase: false, requireDigit: true, requireSpecial: false }
const user: ManagedUser = { id: 2, username: 'fixture-operator', role: 'root', disabled: false, createdAt: '2026-10-13T00:00:00Z', updatedAt: '2026-10-13T00:00:00Z' }
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
type State = {
  users: ManagedUser[]; usersLoading: boolean; usersError: string; policyLoading: boolean; policyError: string;
  policyDraft: PasswordPolicy | null; confirmedPolicy: PasswordPolicy | null; policyDirty: boolean; policySaving: boolean; policyReady: boolean;
  createForm: { username: string; password: string }; passwordForm: { currentPassword: string; newPassword: string; confirmation: string };
  loadUsers(): Promise<void>; loadPasswordPolicy(): Promise<void>; savePasswordPolicy(): Promise<void>;
  requestStatusChange(user: ManagedUser): void; requestDelete(user: ManagedUser): void; confirmDanger(): Promise<void>;
  submitCreate(): Promise<void>; submitPassword(): Promise<void>; openPassword(user: ManagedUser): void; passwordRuleText(): string;
}
let app: App | undefined
let host: HTMLDivElement
let state: State
function mount() {
  host = document.createElement('div'); document.body.append(host)
  app = createApp({ render: () => h(UsersView, { ref: (instance: unknown) => { if (instance) state = (instance as { $: { setupState: State } }).$.setupState } }) })
  app.mount(host)
}
async function flush() { for (let i = 0; i < 5; i++) await nextTick() }
function button(text: string) { return [...host.querySelectorAll('button')].find((item) => item.textContent === text)! }
beforeEach(() => {
  vi.resetAllMocks(); beginSession()
  api.requestUsers.mockResolvedValue([user]); api.requestPasswordPolicy.mockResolvedValue({ ...policy })
})
afterEach(() => { app?.unmount(); app = undefined; host?.remove() })

describe('UsersView 独立读取与已确认密码规则', () => {
  it('列表先成功可见；规则 pending/失败时 UI 和 handler 均禁止密码写入', async () => {
    const read = deferred<PasswordPolicy>(); api.requestPasswordPolicy.mockReturnValue(read.promise)
    mount(); await flush()
    expect(host.textContent).toContain(user.username)
    expect(state.usersLoading).toBe(false)
    expect(state.policyLoading).toBe(true)
    for (const label of ['保存规则', '新增账号', '创建账号', '保存新密码']) expect(button(label).disabled).toBe(true)
    state.createForm = { username: 'fixture-created', password: 'fixture-password-only' }
    state.openPassword(user)
    state.passwordForm = { currentPassword: '', newPassword: 'fixture-password-only', confirmation: 'fixture-password-only' }
    await state.savePasswordPolicy(); await state.submitCreate(); await state.submitPassword()
    expect(api.updatePasswordPolicy).not.toHaveBeenCalled(); expect(api.createUser).not.toHaveBeenCalled(); expect(api.updateUserPassword).not.toHaveBeenCalled()
    read.reject(new Error('fixture unavailable')); await flush()
    expect(state.policyError).toContain('密码规则加载失败'); expect(host.textContent).toContain(user.username)
    expect(state.policyDraft).toBeNull(); expect(state.confirmedPolicy).toBeNull()
  })

  it('列表失败不丢失规则，各自重试只发对应 GET', async () => {
    api.requestUsers.mockRejectedValueOnce(new Error('fixture unavailable'))
    mount(); await flush()
    expect(state.policyReady).toBe(true); expect(state.usersError).not.toBe('')
    await state.loadUsers()
    expect(api.requestUsers).toHaveBeenCalledTimes(2); expect(api.requestPasswordPolicy).toHaveBeenCalledTimes(1)
    await state.loadPasswordPolicy()
    expect(api.requestUsers).toHaveBeenCalledTimes(2); expect(api.requestPasswordPolicy).toHaveBeenCalledTimes(2)
  })

  it.each(['success', 'failure'] as const)('旧列表读取 %s 不覆盖新读取，忽略 abort 的 mock 也被隔离', async (outcome) => {
    const old = deferred<ManagedUser[]>(); const latest = deferred<ManagedUser[]>()
    api.requestUsers.mockReturnValueOnce(old.promise).mockReturnValueOnce(latest.promise)
    mount(); const signal = api.requestUsers.mock.calls[0]![0] as AbortSignal
    const retry = state.loadUsers(); expect(signal.aborted).toBe(true)
    if (outcome === 'success') old.resolve([{ ...user, username: 'stale' }]); else old.reject(new Error('stale'))
    await flush(); expect(state.usersLoading).toBe(true); expect(state.usersError).toBe('')
    latest.resolve([user]); await retry
    expect(state.users[0]?.username).toBe(user.username); expect(state.usersLoading).toBe(false)
  })

  it.each(['success', 'failure'] as const)('旧规则读取 %s 不覆盖新规则或 busy', async (outcome) => {
    const old = deferred<PasswordPolicy>(); const latest = deferred<PasswordPolicy>()
    api.requestPasswordPolicy.mockReturnValueOnce(old.promise).mockReturnValueOnce(latest.promise)
    mount(); const signal = api.requestPasswordPolicy.mock.calls[0]![0] as AbortSignal
    const retry = state.loadPasswordPolicy(); expect(signal.aborted).toBe(true)
    if (outcome === 'success') old.resolve({ ...policy, minLength: 1 }); else old.reject(new Error('stale'))
    await flush(); expect(state.policyLoading).toBe(true); expect(state.policyError).toBe('')
    latest.resolve(policy); await retry
    expect(state.confirmedPolicy).toEqual(policy); expect(state.policyDraft).toEqual(policy)
  })

  it.each(['unmount', 'session'] as const)('%s 取消读取并清理敏感输入，迟到结果无副作用', async (action) => {
    const users = deferred<ManagedUser[]>(); const rules = deferred<PasswordPolicy>()
    api.requestUsers.mockReturnValue(users.promise); api.requestPasswordPolicy.mockReturnValue(rules.promise)
    mount(); state.createForm.password = 'fixture-only'; state.passwordForm.newPassword = 'fixture-only'
    const signals = [api.requestUsers.mock.calls[0]![0], api.requestPasswordPolicy.mock.calls[0]![0]] as AbortSignal[]
    if (action === 'unmount') { app!.unmount(); app = undefined } else invalidateSession()
    expect(signals.every((signal) => signal.aborted)).toBe(true)
    users.resolve([user]); rules.reject(new Error('late')); await flush()
    expect(state.users).toEqual([]); expect(state.confirmedPolicy).toBeNull(); expect(state.createForm.password).toBe(''); expect(state.passwordForm.newPassword).toBe('')
    expect(messages.error).not.toHaveBeenCalled(); expect(messages.success).not.toHaveBeenCalled()
  })

  it('保存捕获 A，后续编辑 B 保留；单飞，提示只使用已确认规则', async () => {
    const save = deferred<PasswordPolicy>(); api.updatePasswordPolicy.mockReturnValue(save.promise)
    mount(); await flush(); state.policyDraft!.minLength = 16
    expect(state.passwordRuleText()).toContain('12')
    const first = state.savePasswordPolicy(); state.policyDraft!.minLength = 20
    await state.savePasswordPolicy(); await state.submitCreate(); await state.submitPassword()
    expect(api.updatePasswordPolicy).toHaveBeenCalledTimes(1); expect(api.updatePasswordPolicy).toHaveBeenCalledWith({ ...policy, minLength: 16 })
    expect(api.createUser).not.toHaveBeenCalled(); expect(api.updateUserPassword).not.toHaveBeenCalled()
    save.resolve({ ...policy, minLength: 16 }); await first
    expect(state.policyDraft!.minLength).toBe(20); expect(state.policyDirty).toBe(true)
    expect(state.confirmedPolicy!.minLength).toBe(16); expect(state.passwordRuleText()).toContain('16')
    await state.loadPasswordPolicy(); expect(state.policyDraft!.minLength).toBe(20)
  })

  it.each(['malformed', 'network'] as const)('保存 %s 结果未确认，保留草稿并要求只读重新确认', async (outcome) => {
    mount(); await flush(); state.policyDraft!.minLength = 18
    if (outcome === 'malformed') api.updatePasswordPolicy.mockResolvedValue({ minLength: 1 })
    else api.updatePasswordPolicy.mockRejectedValue(new Error('network'))
    await state.savePasswordPolicy(); await flush()
    expect(state.policyReady).toBe(false); expect(state.confirmedPolicy).toBeNull(); expect(state.policyDraft!.minLength).toBe(18)
    expect(state.policyError).toContain('结果未确认'); expect(messages.success).not.toHaveBeenCalled()
    await state.savePasswordPolicy(); expect(api.updatePasswordPolicy).toHaveBeenCalledTimes(1)
    await state.loadPasswordPolicy(); expect(state.policyReady).toBe(true); expect(state.policyDraft!.minLength).toBe(18)
  })

  it('已有列表在刷新或刷新失败时仍可见', async () => {
    mount(); await flush(); const retry = deferred<ManagedUser[]>(); api.requestUsers.mockReturnValue(retry.promise)
    const pending = state.loadUsers(); await flush(); expect(host.textContent).toContain(user.username)
    retry.reject(new Error('fixture')); await pending; await flush()
    expect(host.textContent).toContain(user.username); expect(host.textContent).toContain('账号列表加载失败')
  })

  it('创建成功屏蔽更早的列表响应，对账失败保留已确认账号且不重放 POST', async () => {
    const old = deferred<ManagedUser[]>(); const refresh = deferred<ManagedUser[]>()
    api.requestUsers.mockReturnValueOnce(old.promise).mockReturnValueOnce(refresh.promise)
    api.createUser.mockResolvedValue(user); mount(); await flush()
    state.createForm = { username: user.username, password: 'fixture-password-only' }; await state.submitCreate()
    old.resolve([]); await flush(); expect(state.users).toEqual([user])
    refresh.reject(new Error('fixture')); await flush()
    expect(state.users).toEqual([user]); expect(state.usersLoading).toBe(false); expect(api.createUser).toHaveBeenCalledTimes(1)
  })

  it('保存完成时已离开会话，不恢复规则或发出旧 toast', async () => {
    const save = deferred<PasswordPolicy>(); api.updatePasswordPolicy.mockReturnValue(save.promise)
    mount(); await flush(); const pending = state.savePasswordPolicy(); invalidateSession()
    save.resolve(policy); await pending
    expect(state.confirmedPolicy).toBeNull(); expect(state.policyDraft).toBeNull(); expect(messages.success).not.toHaveBeenCalled()
  })

  it.each(['success', 'failure'] as const)('新读取完成后旧读取 %s 不能覆盖两类资源', async (outcome) => {
    const oldUsers = deferred<ManagedUser[]>(); const oldPolicy = deferred<PasswordPolicy>()
    api.requestUsers.mockReturnValueOnce(oldUsers.promise)
    api.requestPasswordPolicy.mockReturnValueOnce(oldPolicy.promise)
    mount(); await state.loadUsers(); await state.loadPasswordPolicy()
    if (outcome === 'success') {
      oldUsers.resolve([{ ...user, username: 'stale' }]); oldPolicy.resolve({ ...policy, minLength: 1 })
    } else {
      oldUsers.reject(new Error('stale')); oldPolicy.reject(new Error('stale'))
    }
    await flush()
    expect(state.users).toEqual([user]); expect(state.confirmedPolicy).toEqual(policy)
    expect(state.usersLoading).toBe(false); expect(state.policyLoading).toBe(false)
    expect(state.usersError).toBe(''); expect(state.policyError).toBe('')
  })

  it('非法规则 GET 不成为已确认规则，handler 无法绕过禁用按钮', async () => {
    api.requestPasswordPolicy.mockResolvedValue({ minLength: 6 })
    mount(); await flush()
    expect(state.policyReady).toBe(false); expect(state.confirmedPolicy).toBeNull()
    await state.savePasswordPolicy(); await state.submitCreate(); await state.submitPassword()
    expect(api.updatePasswordPolicy).not.toHaveBeenCalled(); expect(api.createUser).not.toHaveBeenCalled(); expect(api.updateUserPassword).not.toHaveBeenCalled()
  })

  it('加载规则前发生草稿修改，读取成功不会覆盖后来编辑', async () => {
    const read = deferred<PasswordPolicy>(); api.requestPasswordPolicy.mockReturnValue(read.promise)
    mount(); state.policyDraft = { ...policy, minLength: 24 }
    read.resolve(policy); await flush()
    expect(state.confirmedPolicy).toEqual(policy); expect(state.policyDraft.minLength).toBe(24); expect(state.policyDirty).toBe(true)
  })

  it('规则重读失败后禁用写入且不丢草稿，重读恢复后仍保留未保存修改', async () => {
    mount(); await flush(); state.policyDraft!.minLength = 24
    api.requestPasswordPolicy.mockRejectedValueOnce(new Error('fixture unavailable'))
    await state.loadPasswordPolicy(); await flush()
    expect(state.policyReady).toBe(false); expect(state.policyDraft!.minLength).toBe(24)
    expect(button('保存规则').disabled).toBe(true)
    await state.savePasswordPolicy(); expect(api.updatePasswordPolicy).not.toHaveBeenCalled()
    await state.loadPasswordPolicy()
    expect(state.policyReady).toBe(true); expect(state.policyDraft!.minLength).toBe(24)
  })


  it.each(['status', 'delete'] as const)('规则不可用时仍允许 %s，已确认写入使旧目录读取失效', async (action) => {
    const old = deferred<ManagedUser[]>(); const refresh = deferred<ManagedUser[]>()
    api.requestUsers.mockReturnValueOnce(old.promise).mockReturnValueOnce(refresh.promise)
    api.requestPasswordPolicy.mockRejectedValue(new Error('fixture'))
    api.updateUserStatus.mockResolvedValue({ ...user, disabled: true }); api.deleteUser.mockResolvedValue(undefined)
    mount(); await flush(); state.users = [user]
    if (action === 'status') state.requestStatusChange(user); else state.requestDelete(user)
    await state.confirmDanger()
    expect(action === 'status' ? api.updateUserStatus : api.deleteUser).toHaveBeenCalledTimes(1)
    old.resolve([user]); await flush()
    expect(state.users).toEqual(action === 'status' ? [{ ...user, disabled: true }] : [])
    refresh.reject(new Error('fixture')); await flush(); expect(state.usersLoading).toBe(false)
  })

  it.each([1, 2])('账号 %s 修改密码后旧目录读取失效，自身改密不再发认证 GET', async (id) => {
    const old = deferred<ManagedUser[]>()
    api.requestUsers.mockReturnValueOnce(old.promise); api.updateUserPassword.mockResolvedValue(undefined)
    mount(); await flush(); state.openPassword({ ...user, id })
    state.passwordForm = { currentPassword: 'fixture-current-only', newPassword: 'fixture-password-only', confirmation: 'fixture-password-only' }
    await state.submitPassword()
    expect((api.requestUsers.mock.calls[0]![0] as AbortSignal).aborted).toBe(true)
    expect(api.requestUsers).toHaveBeenCalledTimes(id === 1 ? 1 : 2)
    old.resolve([{ ...user, username: 'stale' }]); await flush()
    expect(state.users.some((item) => item.username === 'stale')).toBe(false)
    invalidateSession()
  })

})
