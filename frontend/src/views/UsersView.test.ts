// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick, type App } from 'vue'
import type { ManagedUser, PasswordPolicy } from '@/api/control'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import UsersView from './UsersView.vue'
import { UserWriteOutcomeUnknownError } from '@/api/userWriteOutcome'

const api = vi.hoisted(() => ({ requestUsers: vi.fn(), requestPasswordPolicy: vi.fn(), updatePasswordPolicy: vi.fn(), createUser: vi.fn(), updateUserPassword: vi.fn(), updateUserStatus: vi.fn(), deleteUser: vi.fn() }))
const messages = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn() }))
vi.mock('@/api/control', () => api)
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { id: 1 } }) }))
vi.mock('@/components/WorkspaceHeader.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/ConfirmDangerDialog.vue', () => ({ default: defineComponent({
  props: ['modelValue', 'busy', 'confirmationTarget'], emits: ['update:modelValue', 'confirm'],
  setup(props, { emit }) { return () => h('section', { 'data-danger': '', 'data-target': props.confirmationTarget }, [h('button', { 'data-danger-close': '', onClick: () => emit('update:modelValue', false) }, 'fixture close')]) },
}) }))
vi.mock('element-plus', () => {
  const passthrough = defineComponent({ setup(_, { slots }) { return () => h('div', [slots.default?.(), slots.footer?.()]) } })
  return {
    ElMessage: messages,
    ElButton: defineComponent({ props: ['disabled', 'loading'], emits: ['click'], setup(props, { slots, emit }) { return () => h('button', { disabled: props.disabled, onClick: () => emit('click') }, slots.default?.()) } }),
    ElDialog: defineComponent({ props: ['modelValue', 'title'], emits: ['update:modelValue'], setup(props, { slots, emit }) { return () => h('section', { 'data-dialog': props.title, onKeydown: (event: KeyboardEvent) => { if (event.key === 'Escape') emit('update:modelValue', false) } }, [h('button', { 'data-model-close': '', onClick: () => emit('update:modelValue', false) }, 'fixture close'), slots.default?.(), slots.footer?.()]) } }), ElDropdown: passthrough, ElDropdownItem: passthrough, ElDropdownMenu: passthrough,
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
  unknownWrites: { action: string; target: string | number; username: string }[]; createUnknown: boolean; passwordUnknown: boolean;
  users: ManagedUser[]; usersLoading: boolean; usersError: string; policyLoading: boolean; policyError: string;
  policyDraft: PasswordPolicy | null; confirmedPolicy: PasswordPolicy | null; policyDirty: boolean; policySaving: boolean; policyReady: boolean;
  createOpen: boolean; passwordOpen: boolean; dangerOpen: boolean; createBusy: boolean; passwordBusy: boolean; dangerBusy: boolean;
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
afterEach(() => { app?.unmount(); app = undefined; host?.remove(); vi.useRealTimers(); vi.unstubAllGlobals() })

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
    state.createOpen = true; state.createForm = { username: user.username, password: 'fixture-password-only' }; await state.submitCreate()
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


describe('UsersView 写入弹窗与目标归属', () => {
  const second = { ...user, id: 3, username: 'fixture-second' }
  const password = { currentPassword: 'fixture-current-only', newPassword: 'fixture-password-only', confirmation: 'fixture-password-only' }
  function enterCreate(name = 'fixture-new') { state.createOpen = true; state.createForm = { username: name, password: password.newPassword } }
  function enterPassword(target = user) { state.openPassword(target); state.passwordForm = { ...password } }

  it.each(['success', 'failure'] as const)('创建关闭重开后旧 %s 不清新草稿、不发 toast；锁跨弹窗保留', async (outcome) => {
    const write = deferred<ManagedUser>(); api.createUser.mockReturnValue(write.promise)
    mount(); await flush(); enterCreate(); const pending = state.submitCreate()
    state.createForm.username = 'fixture-edited'; state.createForm.password = 'fixture-edited-secret'
    expect(api.createUser).toHaveBeenCalledWith({ username: 'fixture-new', password: password.newPassword })
    state.createOpen = false; expect(state.createForm.password).toBe('')
    enterCreate('fixture-next'); await state.submitCreate(); expect(api.createUser).toHaveBeenCalledTimes(1)
    expect(state.createBusy).toBe(true)
    if (outcome === 'success') { api.requestUsers.mockRejectedValue(new Error('fixture read')); write.resolve(second) } else write.reject(new Error('fixture old error'))
    await pending; await flush()
    expect(state.createOpen).toBe(true); expect(state.createForm.username).toBe('fixture-next'); expect(state.createForm.password).toBe(password.newPassword)
    expect(state.createBusy).toBe(false); expect(messages.success).not.toHaveBeenCalled(); expect(messages.error).not.toHaveBeenCalled()
    if (outcome === 'success') expect(state.users).toContainEqual(second)
  })

  it.each(['success', 'failure'] as const)('改密 A 关闭后 B 可独立提交，A 的 %s/finally 不影响 B', async (outcome) => {
    const old = deferred<void>(); const latest = deferred<void>()
    api.updateUserPassword.mockReturnValueOnce(old.promise).mockReturnValueOnce(latest.promise)
    mount(); await flush(); enterPassword(); const first = state.submitPassword()
    state.passwordForm.newPassword = 'fixture-edited-password'
    state.passwordOpen = false; expect(state.passwordForm.newPassword).toBe('')
    enterPassword(); await state.submitPassword(); expect(api.updateUserPassword).toHaveBeenCalledTimes(1)
    enterPassword(second); const next = state.submitPassword(); await state.submitPassword()
    expect(api.updateUserPassword.mock.calls).toEqual([[user.id, { currentPassword: '', newPassword: password.newPassword }], [second.id, { currentPassword: '', newPassword: password.newPassword }]])
    if (outcome === 'success') old.resolve(); else old.reject(new Error('old fixture error'))
    await first; await flush()
    expect(state.passwordOpen).toBe(true); expect(state.passwordBusy).toBe(true); expect(state.passwordForm).toEqual(password)
    expect(messages.success).not.toHaveBeenCalled(); expect(messages.error).not.toHaveBeenCalled()
    latest.resolve(); await next
    expect(state.passwordOpen).toBe(false); expect(state.passwordBusy).toBe(false); expect(state.passwordForm.newPassword).toBe('')
    expect(messages.success).toHaveBeenCalledTimes(1)
  })

  it.each(['success', 'failure'] as const)('危险操作 A/status 到 B/delete，旧 %s 保留新弹窗和 busy', async (outcome) => {
    const old = deferred<ManagedUser>(); const latest = deferred<void>()
    api.updateUserStatus.mockReturnValue(old.promise); api.deleteUser.mockReturnValue(latest.promise)
    mount(); await flush(); state.users = [user, second]; state.requestStatusChange(user); const first = state.confirmDanger()
    state.dangerOpen = false; state.requestDelete(second); const next = state.confirmDanger(); await state.confirmDanger()
    expect(api.updateUserStatus).toHaveBeenCalledWith(user.id, true); expect(api.deleteUser).toHaveBeenCalledTimes(1); expect(api.deleteUser).toHaveBeenCalledWith(second.id)
    api.requestUsers.mockRejectedValue(new Error('fixture read'))
    if (outcome === 'success') old.resolve({ ...user, disabled: true }); else old.reject('fixture non-Error')
    await first; await flush()
    expect(state.dangerOpen).toBe(true); expect(state.dangerBusy).toBe(true)
    expect(messages.success).not.toHaveBeenCalled(); expect(messages.error).not.toHaveBeenCalled()
    if (outcome === 'success') expect(state.users.find((item) => item.id === user.id)?.disabled).toBe(true)
    latest.resolve(); await next; await flush()
    expect(state.dangerOpen).toBe(false); expect(state.users.some((item) => item.id === second.id)).toBe(false)
    expect(messages.success).toHaveBeenCalledWith('账号已删除。')
  })

  it('目标写入锁跨密码与危险操作，创建独立；关闭不释放锁', async () => {
    const write = deferred<void>(); const create = deferred<ManagedUser>()
    api.updateUserPassword.mockReturnValue(write.promise); api.createUser.mockReturnValue(create.promise)
    mount(); await flush(); enterPassword(); const pending = state.submitPassword()
    state.passwordOpen = false; state.requestDelete(user); await state.confirmDanger()
    expect(state.dangerBusy).toBe(true); expect(api.deleteUser).not.toHaveBeenCalled()
    state.requestStatusChange(user); await state.confirmDanger(); expect(api.updateUserStatus).not.toHaveBeenCalled()
    enterCreate(); const creating = state.submitCreate(); expect(api.createUser).toHaveBeenCalledTimes(1)
    write.reject(new Error('fixture')); await pending; expect(state.createBusy).toBe(true); expect(state.dangerBusy).toBe(false)
    create.reject(new Error('fixture')); await creating
  })

  it.each(['Escape', 'model', 'cancel'] as const)('%s 关闭路径清密码且旧失败不恢复敏感输入', async (close) => {
    const write = deferred<void>(); api.updateUserPassword.mockReturnValue(write.promise)
    mount(); await flush(); enterPassword(); const pending = state.submitPassword(); await flush()
    const dialog = host.querySelector('[data-dialog^="重置"]')!
    if (close === 'Escape') dialog.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    else if (close === 'model') (dialog.querySelector('[data-model-close]') as HTMLButtonElement).click()
    else ([...dialog.querySelectorAll('button')].find((item) => item.textContent === '取消')!).click()
    expect(state.passwordOpen).toBe(false); expect(Object.values(state.passwordForm)).toEqual(['', '', ''])
    write.reject(new Error('fixture')); await pending; expect(messages.error).not.toHaveBeenCalled()
  })

  it('危险弹窗 model 关闭使旧失败失去观察权', async () => {
    const write = deferred<void>(); api.deleteUser.mockReturnValue(write.promise)
    mount(); await flush(); state.requestDelete(user); const pending = state.confirmDanger(); await flush()
    ;(host.querySelector('[data-danger-close]') as HTMLButtonElement).click()
    expect(state.dangerOpen).toBe(false); write.reject('fixture'); await pending
    expect(messages.error).not.toHaveBeenCalled()
  })

  it.each(['Escape', 'model', 'cancel'] as const)('创建经 %s 关闭清空密码，旧请求仍保持单飞', async (close) => {
    const write = deferred<ManagedUser>(); api.createUser.mockReturnValue(write.promise)
    mount(); await flush(); enterCreate(); const pending = state.submitCreate(); await flush()
    const dialog = host.querySelector('[data-dialog="新增 Root 账号"]')!
    if (close === 'Escape') dialog.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    else if (close === 'model') (dialog.querySelector('[data-model-close]') as HTMLButtonElement).click()
    else ([...dialog.querySelectorAll('button')].find((item) => item.textContent === '取消')!).click()
    expect(state.createOpen).toBe(false); expect(state.createForm.password).toBe(''); expect(state.createBusy).toBe(true)
    write.reject(new Error('fixture')); await pending; expect(messages.error).not.toHaveBeenCalled()
  })

  it('自身改密关闭后成功仍提醒原会话重新登录，但不清后来弹窗；只触发一次 reload', async () => {
    vi.useFakeTimers(); const write = deferred<void>(); api.updateUserPassword.mockReturnValue(write.promise)
    mount(); await flush(); enterPassword({ ...user, id: 1 }); const pending = state.submitPassword()
    state.passwordOpen = false; enterPassword(second); write.resolve(); await pending
    expect(state.passwordOpen).toBe(true); expect(state.passwordForm).toEqual(password)
    expect(messages.success).toHaveBeenCalledWith('密码已修改，请重新登录。')
    expect(api.updateUserPassword).toHaveBeenCalledWith(1, { currentPassword: password.currentPassword, newPassword: password.newPassword })
    const reload = vi.fn(); vi.stubGlobal('window', { location: { reload } })
    vi.advanceTimersByTime(600); expect(reload).toHaveBeenCalledTimes(1); vi.advanceTimersByTime(600); expect(reload).toHaveBeenCalledTimes(1)
    vi.unstubAllGlobals(); expect(api.requestUsers).toHaveBeenCalledTimes(1)
  })

  it.each(['unmount', 'session', 'new-session'] as const)('自身改密成功后 %s 回收原会话 reload 定时器', async (cleanup) => {
    vi.useFakeTimers(); api.updateUserPassword.mockResolvedValue(undefined)
    mount(); await flush(); enterPassword({ ...user, id: 1 }); await state.submitPassword()
    expect(messages.success).toHaveBeenCalledWith('密码已修改，请重新登录。'); expect(vi.getTimerCount()).toBe(1)
    if (cleanup === 'unmount') { app!.unmount(); app = undefined } else if (cleanup === 'session') invalidateSession(); else beginSession()
    expect(vi.getTimerCount()).toBe(0); vi.advanceTimersByTime(600)
    expect(api.updateUserPassword).toHaveBeenCalledTimes(1); expect(api.requestUsers).toHaveBeenCalledTimes(1)
  })

  it.each(['success', 'failure'] as const)('离开会话后 pending 写入 %s 不 toast/对账/reload', async (outcome) => {
    vi.useFakeTimers(); const write = deferred<void>(); api.updateUserPassword.mockReturnValue(write.promise)
    mount(); await flush(); enterPassword({ ...user, id: 1 }); const pending = state.submitPassword(); beginSession()
    if (outcome === 'success') write.resolve(); else write.reject(new Error('fixture'))
    await pending; expect(messages.success).not.toHaveBeenCalled(); expect(messages.error).not.toHaveBeenCalled(); expect(vi.getTimerCount()).toBe(0)
    expect(api.requestUsers).toHaveBeenCalledTimes(1)
  })
})


describe('UsersView 账号写入结果未知', () => {
  const password = { currentPassword: 'fixture-current-only', newPassword: 'fixture-password-only', confirmation: 'fixture-password-only' }
  const second = { ...user, id: 3, username: 'fixture-second' }

  it.each([' ', '\u0085', '\u2003'])('创建未知清除提交密码，关闭重开和只读列表不能解锁，同名 %j 空白也不能重放，其他用户名可用', async (space) => {
    api.createUser.mockRejectedValueOnce(new UserWriteOutcomeUnknownError('create')).mockResolvedValue(second)
    mount(); await flush(); state.createOpen = true
    state.createForm = { username: `${space}${user.username}${space}`, password: password.newPassword }
    await state.submitCreate(); await flush()
    expect(state.createForm.password).toBe(''); expect(state.createBusy).toBe(false)
    expect(state.unknownWrites).toEqual([{ action: 'create', target: user.username, username: user.username }])
    expect(button('创建账号').disabled).toBe(true)
    state.createOpen = false; state.createOpen = true
    state.createForm = { username: user.username, password: password.newPassword }
    await state.loadUsers(); await state.submitCreate(); await flush()
    expect(api.createUser).toHaveBeenCalledTimes(1); expect(state.createUnknown).toBe(true)
    expect(host.textContent).toContain('不能证明此前请求成功'); expect(messages.success).not.toHaveBeenCalled()
    state.createForm = { username: second.username, password: password.newPassword }
    await state.submitCreate(); expect(api.createUser).toHaveBeenCalledTimes(2)
  })

  it('创建未知键与 SQLite NOCASE 一致，ASCII 大小写不能绕过，非 ASCII 不误合并', async () => {
    api.createUser.mockRejectedValue(new UserWriteOutcomeUnknownError('create'))
    mount(); await flush(); state.createOpen = true
    state.createForm = { username: 'Fixture-Ä', password: password.newPassword }; await state.submitCreate()
    state.createForm = { username: '\u0085FIXTURE-Ä\u0085', password: password.newPassword }; await state.submitCreate()
    expect(api.createUser).toHaveBeenCalledTimes(1); expect(state.createUnknown).toBe(true)
    expect(state.unknownWrites).toEqual([{ action: 'create', target: 'fixture-Ä', username: 'Fixture-Ä' }])
    state.createForm = { username: 'fixture-ä', password: password.newPassword }; await state.submitCreate()
    expect(api.createUser).toHaveBeenCalledTimes(2)
  })

  it('旧创建弹窗迟到未知仍在页面可见，不清后来不同用户名的密码草稿', async () => {
    const write = deferred<ManagedUser>(); api.createUser.mockReturnValue(write.promise)
    mount(); await flush(); state.createOpen = true
    state.createForm = { username: user.username, password: password.newPassword }
    const pending = state.submitCreate(); state.createOpen = false; state.createOpen = true
    state.createForm = { username: second.username, password: 'fixture-later-secret' }
    write.reject(new UserWriteOutcomeUnknownError('create')); await pending; await flush()
    expect(host.textContent).toContain(`${user.username}：创建账号结果未确认`)
    expect(state.createForm).toEqual({ username: second.username, password: 'fixture-later-secret' })
    expect(state.createUnknown).toBe(false); expect(messages.error).not.toHaveBeenCalled()
    expect(JSON.stringify(state.unknownWrites)).not.toContain('secret')
  })

  it.each([1, 2])('账号 %s 改密未知不成功/重载，清密码；旧列表失效且只读核对不能确认密码', async (id) => {
    vi.useFakeTimers(); const stale = deferred<ManagedUser[]>()
    api.requestUsers.mockReturnValueOnce(stale.promise); api.updateUserPassword.mockRejectedValue(new UserWriteOutcomeUnknownError('password'))
    mount(); await flush(); state.openPassword({ ...user, id }); state.passwordForm = { ...password }
    await state.submitPassword(); await flush()
    expect(Object.values(state.passwordForm)).toEqual(['', '', '']); expect(state.passwordBusy).toBe(false)
    expect(button('保存新密码').disabled).toBe(true)
    expect((api.requestUsers.mock.calls[0]![0] as AbortSignal).aborted).toBe(true)
    expect(api.requestUsers).toHaveBeenCalledTimes(1); expect(vi.getTimerCount()).toBe(0)
    stale.resolve([{ ...user, username: 'stale' }]); await flush(); expect(state.users).toEqual([])
    state.passwordOpen = false; state.openPassword({ ...user, id }); state.passwordForm = { ...password }
    await state.loadUsers(); await state.submitPassword()
    expect(api.updateUserPassword).toHaveBeenCalledTimes(1); expect(state.passwordUnknown).toBe(true)
    expect(messages.success).not.toHaveBeenCalled(); expect(messages.error).not.toHaveBeenCalled()
  })

  it('改密 A 迟到未知不影响 B 草稿/在途锁，A 的其他操作仍能提交', async () => {
    const first = deferred<void>(); const next = deferred<void>()
    api.updateUserPassword.mockReturnValueOnce(first.promise).mockReturnValueOnce(next.promise)
    api.updateUserStatus.mockResolvedValue({ ...user, disabled: true })
    mount(); await flush(); state.openPassword(user); state.passwordForm = { ...password }
    const pending = state.submitPassword(); state.passwordOpen = false; state.openPassword(second); state.passwordForm = { ...password }
    state.passwordForm = { currentPassword: '', newPassword: 'fixture-second-password', confirmation: 'fixture-second-password' }
    const secondDraft = { ...state.passwordForm }
    const secondPending = state.submitPassword()
    first.reject(new UserWriteOutcomeUnknownError('password')); await pending; await flush()
    expect(host.textContent).toContain(`${user.username}：修改密码结果未确认`)
    expect(state.passwordForm).toEqual(secondDraft); expect(state.passwordBusy).toBe(true); expect(state.passwordUnknown).toBe(false)
    state.requestStatusChange(user); await state.confirmDanger(); expect(api.updateUserStatus).toHaveBeenCalledTimes(1)
    next.resolve(); await secondPending
  })

  it.each(['status', 'delete'] as const)('%s 未知即使弹窗关闭仍显示；使旧 GET 失效，后续 GET 不清保护且其他账号可操作', async (action) => {
    const write = deferred<ManagedUser | void>(); const stale = deferred<ManagedUser[]>()
    const writeApi = action === 'status' ? api.updateUserStatus : api.deleteUser
    writeApi.mockReturnValueOnce(write.promise)
    api.requestUsers.mockReturnValueOnce(stale.promise)
    mount(); await flush(); state.users = [user, second]
    const open = action === 'status' ? state.requestStatusChange : state.requestDelete
    open(user); const pending = state.confirmDanger(); state.dangerOpen = false
    write.reject(new UserWriteOutcomeUnknownError(action)); await pending; await flush()
    expect(host.textContent).toContain('账号操作结果未确认')
    expect((api.requestUsers.mock.calls[0]![0] as AbortSignal).aborted).toBe(true)
    stale.resolve([]); await flush(); expect(state.users).toEqual([user, second])
    await state.loadUsers(); open(user); state.dangerOpen = true; await state.confirmDanger()
    expect(writeApi).toHaveBeenCalledTimes(1)
    writeApi.mockResolvedValue(action === 'status' ? { ...second, disabled: true } : undefined)
    open(second); await state.confirmDanger(); expect(writeApi).toHaveBeenCalledTimes(2)
    expect(state.unknownWrites).toHaveLength(1)
  })

  it('未知禁用后只读列表观察到已禁用，反向启用仍不能绕过同目标状态保护', async () => {
    api.updateUserStatus.mockRejectedValue(new UserWriteOutcomeUnknownError('status'))
    mount(); await flush(); state.requestStatusChange(user); await state.confirmDanger()
    api.requestUsers.mockResolvedValue([{ ...user, disabled: true }]); await state.loadUsers()
    expect(state.users[0]!.disabled).toBe(true)
    state.requestStatusChange(state.users[0]!); state.dangerOpen = true; await state.confirmDanger()
    expect(api.updateUserStatus).toHaveBeenCalledTimes(1)
    expect(api.updateUserStatus).toHaveBeenCalledWith(user.id, true)
    expect(state.unknownWrites).toEqual([{ action: 'status', target: user.id, username: user.username }])
    expect(messages.success).not.toHaveBeenCalled()
  })

  it('危险操作旧弹窗未知到达时，关闭同目标重开弹窗，不留下看似可提交的确认按钮', async () => {
    const write = deferred<ManagedUser>(); api.updateUserStatus.mockReturnValue(write.promise)
    mount(); await flush(); state.requestStatusChange(user); const pending = state.confirmDanger()
    state.dangerOpen = false; state.requestStatusChange(user)
    write.reject(new UserWriteOutcomeUnknownError('status')); await pending; await flush()
    expect(state.dangerOpen).toBe(false); expect(host.querySelector('[data-danger]')).toBeNull()
    state.requestStatusChange(user); await state.confirmDanger()
    expect(state.dangerOpen).toBe(false); expect(api.updateUserStatus).toHaveBeenCalledTimes(1)
    state.requestDelete(user); await flush(); expect(host.querySelector('[data-danger]')).not.toBeNull()
  })

  it('会话失效清除已有未知记录及敏感输入，不把保护带到下一会话', async () => {
    api.createUser.mockRejectedValue(new UserWriteOutcomeUnknownError('create'))
    mount(); await flush(); state.createOpen = true
    state.createForm = { username: user.username, password: password.newPassword }; await state.submitCreate()
    expect(state.unknownWrites).toHaveLength(1)
    beginSession(); await flush()
    expect(state.unknownWrites).toEqual([]); expect(state.createForm.password).toBe('')
    expect(host.textContent).not.toContain('账号操作结果未确认')
  })

  it.each(['unmount', 'session'] as const)('%s 清除未知记录，迟到未知不恢复旧页面状态', async (cleanup) => {
    const write = deferred<void>(); api.updateUserPassword.mockReturnValue(write.promise)
    mount(); await flush(); state.openPassword(user); state.passwordForm = { ...password }
    const pending = state.submitPassword()
    if (cleanup === 'unmount') { app!.unmount(); app = undefined } else invalidateSession()
    write.reject(new UserWriteOutcomeUnknownError('password')); await pending
    expect(state.unknownWrites).toEqual([]); expect(messages.success).not.toHaveBeenCalled(); expect(messages.error).not.toHaveBeenCalled()
    expect(state.passwordForm.newPassword).toBe('')
  })
})
