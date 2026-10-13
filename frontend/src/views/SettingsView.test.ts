// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick, type App } from 'vue'
import { createPinia, setActivePinia, type Pinia } from 'pinia'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import { PreferencesWriteUnknownError } from '@/api/preferenceResponse'
import { DEFAULT_USER_PREFERENCES, useSystemStore } from '@/stores/system'
import type { UserPreferences } from '@/api/control'
import SettingsView from './SettingsView.vue'

const api = vi.hoisted(() => ({ requestPreferences: vi.fn(), updatePreferences: vi.fn(), requestListPreference: vi.fn(), updateListPreference: vi.fn() }))
vi.mock('@/api/control', () => api)
vi.mock('@/components/WorkspaceHeader.vue', () => ({ default: defineComponent({ setup(_, { slots }) { return () => h('header', slots.actions?.()) } }) }))
vi.mock('element-plus', () => ({
  ElMessageBox: { confirm: vi.fn().mockResolvedValue(undefined) },
  ElButton: defineComponent({ props: ['disabled'], emits: ['click'], setup(props, { slots, emit }) { return () => h('button', { disabled: props.disabled, onClick: () => emit('click') }, slots.default?.()) } }),
  ElSelect: defineComponent({ props: ['modelValue', 'disabled'], emits: ['update:modelValue'], setup(props, { slots, emit }) { return () => h('select', { value: props.modelValue, disabled: props.disabled, onChange: (e: Event) => emit('update:modelValue', (e.target as HTMLSelectElement).value) }, slots.default?.()) } }),
  ElOption: defineComponent({ props: ['value', 'label'], setup(props) { return () => h('option', { value: props.value }, props.label) } }),
}))
const baseline: UserPreferences = { ...DEFAULT_USER_PREFERENCES, baseFontSize: 17, chineseFont: 'misans', latinFont: 'system', navigationOrder: ['settings', 'sites', 'overview'] }
const copy = (value = baseline) => ({ ...value, navigationOrder: [...value.navigationOrder] })
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
type State = { draft: UserPreferences; canEdit: boolean; retrySave(): void; retryLoad(): void; restoreSection(fields: (keyof UserPreferences)[]): void }
let pinia: Pinia
let store: ReturnType<typeof useSystemStore>
const mounted: { app: App; host: HTMLDivElement }[] = []
function mount() {
  const host = document.createElement('div'); document.body.append(host)
  let state!: State
  const app = createApp({ render: () => h(SettingsView, { ref: (instance: unknown) => { if (instance) state = (instance as { $: { setupState: State } }).$.setupState } }) })
  app.use(pinia); app.mount(host)
  const entry = { app, host }; mounted.push(entry)
  return { host, state, unmount() { app.unmount(); host.remove(); mounted.splice(mounted.indexOf(entry), 1) } }
}
async function flush() { for (let i = 0; i < 8; i++) await nextTick() }
async function tick(ms = 400) { await vi.advanceTimersByTimeAsync(ms); await flush() }
beforeEach(() => {
  vi.useFakeTimers(); vi.resetAllMocks(); beginSession()
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null)
  pinia = createPinia(); setActivePinia(pinia); store = useSystemStore()
  api.requestPreferences.mockResolvedValue(copy())
  api.updatePreferences.mockImplementation(async (value: UserPreferences) => copy(value))
})
afterEach(() => {
  invalidateSession()
  for (const { app, host } of mounted.splice(0)) { app.unmount(); host.remove() }
  store.$dispose(); vi.restoreAllMocks(); vi.useRealTimers()
})

describe('SettingsView 可信基线、会话归属与串行保存', () => {
  it('初始 GET 延迟/失败时禁用编辑，重试完整 hydration 不保存或清理设备字体', async () => {
    const read = deferred<UserPreferences>(); api.requestPreferences.mockReturnValueOnce(read.promise)
    const view = mount()
    expect(view.state.canEdit).toBe(false)
    expect([...view.host.querySelectorAll('select')].every((item) => item.disabled)).toBe(true)
    view.state.restoreSection(['baseFontSize']); await tick()
    expect(api.updatePreferences).not.toHaveBeenCalled()
    read.reject(new Error('fixture read failure')); await flush()
    expect(view.host.textContent).toContain('设置读取失败')
    view.state.retryLoad(); await flush(); await tick()
    expect(view.state.draft).toEqual(baseline)
    expect(api.updatePreferences).not.toHaveBeenCalled()
    view.state.draft.baseFontSize = 18; await tick()
    expect(api.updatePreferences.mock.calls[0]![0]).toEqual({ ...baseline, baseFontSize: 18 })
  })

  it.each(['success', 'failure'] as const)('旧初始 GET %s 不影响替换会话的新基线；同会话 GET 去重', async (outcome) => {
    const oldRead = deferred<UserPreferences>(); api.requestPreferences.mockReturnValueOnce(oldRead.promise)
    const old = mount()
    const duplicate = store.loadPreferences()
    expect(api.requestPreferences).toHaveBeenCalledTimes(1)
    invalidateSession(); beginSession(); const fresh = mount(); await flush()
    if (outcome === 'success') oldRead.resolve({ ...baseline, baseFontSize: 13 }); else oldRead.reject(new Error('late read'))
    await duplicate; await flush(); old.unmount()
    expect(fresh.state.draft).toEqual(baseline)
    expect(store.preferencesLoadState).toBe('loaded')
    expect(document.documentElement.style.fontSize).toBe('17px')
    expect(api.updatePreferences).not.toHaveBeenCalled()
  })

  it('A 成功不覆盖 B 预览；B 失败保留草稿、持久化 A 和重试状态', async () => {
    const a = deferred<UserPreferences>(); api.updatePreferences.mockReturnValueOnce(a.promise).mockRejectedValueOnce(new Error('fixture failure'))
    const view = mount(); await flush()
    view.state.draft.baseFontSize = 16; await tick()
    view.state.draft.baseFontSize = 18
    a.resolve({ ...baseline, baseFontSize: 16 }); await flush()
    expect(document.documentElement.style.fontSize).toBe('18px')
    expect(store.preferences.baseFontSize).toBe(16)
    expect(view.state.draft.baseFontSize).toBe(18)
    expect(store.preferencesDirty).toBe(true)
    expect(store.preferencesSaveState).toBe('error')
    expect(view.host.textContent).toContain('保存失败')
  })

  it.each(['before', 'during'] as const)('在 %s 写入开始时创建的 GET 不能覆盖新 PUT', async (phase) => {
    const view = mount(); await flush()
    const read = deferred<UserPreferences>(); api.requestPreferences.mockReturnValueOnce(read.promise)
    const write = deferred<UserPreferences>(); api.updatePreferences.mockReturnValueOnce(write.promise)
    let reading: Promise<void>
    if (phase === 'before') reading = store.loadPreferences()
    view.state.draft.baseFontSize = 18; await tick()
    if (phase === 'during') reading = store.loadPreferences()
    write.resolve({ ...baseline, baseFontSize: 18, refreshIntervalSeconds: 30 }); await flush()
    read.resolve(copy()); await reading!; await flush()
    expect(store.preferences.baseFontSize).toBe(18)
    expect(store.refreshIntervalSeconds).toBe(30)
    expect(document.documentElement.style.fontSize).toBe('18px')
  })

  it.each(['before', 'during'] as const)('写入 %s 的旧 GET 迟到失败不显示过期错误或卡在 loading', async (phase) => {
    const view = mount(); await flush()
    const read = deferred<UserPreferences>(); api.requestPreferences.mockReturnValueOnce(read.promise)
    const write = deferred<UserPreferences>(); api.updatePreferences.mockReturnValueOnce(write.promise)
    let reading: Promise<void>
    if (phase === 'before') reading = store.loadPreferences()
    view.state.draft.baseFontSize = 18; await tick()
    if (phase === 'during') reading = store.loadPreferences()
    write.resolve({ ...baseline, baseFontSize: 18 }); await flush()
    read.reject(new Error('obsolete read')); await reading!; await flush()
    expect(store.preferencesLoadState).toBe('loaded')
    expect(store.preferences.baseFontSize).toBe(18)
    expect(document.documentElement.style.fontSize).toBe('18px')
    expect(view.host.textContent).not.toContain('设置读取失败')
    expect(view.host.textContent).not.toContain('正在读取已保存设置')
  })

  it('旧编辑器的读取重试不能绑定替换会话，新编辑器仍可重试', async () => {
    const old = mount(); await flush()
    invalidateSession(); beginSession()
    old.state.retryLoad(); await flush()
    expect(api.requestPreferences).toHaveBeenCalledTimes(1)
    const fresh = mount(); await flush()
    expect(api.requestPreferences).toHaveBeenCalledTimes(2)
    old.state.retryLoad(); await flush()
    expect(api.requestPreferences).toHaveBeenCalledTimes(2)
    fresh.state.retryLoad(); await flush()
    expect(api.requestPreferences).toHaveBeenCalledTimes(3)
    expect(fresh.state.draft).toEqual(baseline)
  })

  it('后台读取失败保留可信基线与未保存草稿', async () => {
    const view = mount(); await flush()
    view.state.draft.baseFontSize = 18
    api.requestPreferences.mockRejectedValueOnce(new Error('fixture read failure'))
    await expect(store.loadPreferences()).rejects.toThrow()
    expect(store.preferencesLoaded).toBe(true)
    expect(store.preferences).toEqual(baseline)
    expect(view.state.draft.baseFontSize).toBe(18)
  })

  it('debounce 前失效并立即登录，旧编辑器/卸载不能写入新会话', async () => {
    const view = mount(); await flush()
    view.state.draft.baseFontSize = 18
    invalidateSession(); beginSession()
    const fresh = mount(); await flush()
    view.state.draft.baseFontSize = 13; view.unmount(); await tick(1000)
    expect(api.updatePreferences).not.toHaveBeenCalled()
    expect(fresh.state.draft).toEqual(baseline)
    expect(document.documentElement.style.fontSize).toBe('17px')
    fresh.state.draft.baseFontSize = 16; await tick()
    expect(api.updatePreferences).toHaveBeenCalledTimes(1)
  })

  it.each(['success', 'failure'] as const)('A %s 迟到且 B 待保存，退出/重新登录后不发送 B 或覆盖新 DOM', async (outcome) => {
    const a = deferred<UserPreferences>(); api.updatePreferences.mockReturnValueOnce(a.promise)
    const old = mount(); await flush()
    old.state.draft.baseFontSize = 16; await tick()
    old.state.draft.baseFontSize = 18
    const oldSignal = api.updatePreferences.mock.calls[0]![1] as AbortSignal
    invalidateSession(); beginSession(); const fresh = mount(); await flush()
    expect(oldSignal.aborted).toBe(true)
    if (outcome === 'success') a.resolve({ ...baseline, baseFontSize: 16 }); else a.reject(new Error('late'))
    old.unmount(); await tick(2000)
    expect(api.updatePreferences).toHaveBeenCalledTimes(1)
    expect(fresh.state.draft).toEqual(baseline)
    expect(document.documentElement.style.fontSize).toBe('17px')
  })

  it('同会话离开在 debounce 前 flush，返回沿用该队列且新 C 预览不被旧 A/B 覆盖', async () => {
    const a = deferred<UserPreferences>(); const b = deferred<UserPreferences>(); const c = deferred<UserPreferences>()
    api.updatePreferences.mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise).mockReturnValueOnce(c.promise)
    const first = mount(); await flush()
    first.state.draft.baseFontSize = 16; first.unmount()
    expect(api.updatePreferences).toHaveBeenCalledTimes(1)
    const second = mount(); await flush()
    expect(second.state.draft.baseFontSize).toBe(16)
    second.state.draft.baseFontSize = 18; second.unmount()
    a.resolve({ ...baseline, baseFontSize: 16 }); await flush()
    expect(api.updatePreferences).toHaveBeenCalledTimes(2)
    const third = mount(); third.state.draft.baseFontSize = 13
    b.resolve({ ...baseline, baseFontSize: 18 }); await flush()
    expect(api.updatePreferences).toHaveBeenCalledTimes(3)
    expect(document.documentElement.style.fontSize).toBe('13px')
    expect(api.updatePreferences.mock.calls.map(([value]) => value.baseFontSize)).toEqual([16, 18, 13])
    c.resolve({ ...baseline, baseFontSize: 13 }); await flush()
    expect(store.preferencesSaveState).toBe('saved')
    expect(store.preferencesDirty).toBe(false)
  })

  it('普通导航 flush 后退出，不发送正在保存 A 之后的 B', async () => {
    const a = deferred<UserPreferences>(); api.updatePreferences.mockReturnValueOnce(a.promise)
    const view = mount(); await flush()
    view.state.draft.baseFontSize = 16; await tick()
    view.state.draft.baseFontSize = 18; view.unmount(); invalidateSession()
    a.resolve({ ...baseline, baseFontSize: 16 }); await tick(1000)
    expect(api.updatePreferences).toHaveBeenCalledTimes(1)
    expect(document.documentElement.style.fontSize).toBe('15px')
  })

  it('未知 A 后核实时保留明确回改至旧基线的 B，不能用旧 dirty=false 丢弃意图', async () => {
    const a = deferred<UserPreferences>(); api.updatePreferences.mockReturnValueOnce(a.promise)
    const view = mount(); await flush()
    view.state.draft.baseFontSize = 16; await tick()
    view.state.draft.baseFontSize = 17
    expect(store.preferencesDirty).toBe(false)
    a.reject(new PreferencesWriteUnknownError()); await flush()
    api.requestPreferences.mockResolvedValueOnce({ ...baseline, baseFontSize: 16 })
    view.state.retryLoad(); await flush(); await tick(1000)
    expect(store.preferences.baseFontSize).toBe(16)
    expect(view.state.draft.baseFontSize).toBe(17)
    expect(document.documentElement.style.fontSize).toBe('17px')
    expect(store.preferencesDirty).toBe(true)
    expect(store.preferencesSaveState).toBe('review')
    expect(api.updatePreferences).toHaveBeenCalledTimes(1)
    view.state.retrySave(); await flush()
    expect(api.updatePreferences.mock.calls.map(([value]) => value.baseFontSize)).toEqual([16, 17])
    expect(store.preferencesSaveState).toBe('saved')
  })

  it('未知写结果暂停 B；只读核实保留 B，用户确认后才重新保存', async () => {
    const a = deferred<UserPreferences>(); api.updatePreferences.mockReturnValueOnce(a.promise)
    const view = mount(); await flush()
    view.state.draft.baseFontSize = 16; await tick()
    view.state.draft.baseFontSize = 18
    a.reject(new PreferencesWriteUnknownError()); await tick(1000)
    expect(store.preferencesSaveState).toBe('unknown')
    expect(view.host.textContent).toContain('保存结果未知')
    view.state.retrySave(); await tick(); expect(api.updatePreferences).toHaveBeenCalledTimes(1)
    api.requestPreferences.mockRejectedValueOnce(new Error('reconcile unavailable'))
    view.state.retryLoad(); await flush()
    expect(store.preferencesSaveState).toBe('unknown')
    view.state.retrySave(); await tick(); expect(api.updatePreferences).toHaveBeenCalledTimes(1)
    api.requestPreferences.mockResolvedValueOnce({ ...baseline, baseFontSize: 16 })
    view.state.retryLoad(); await flush(); await tick()
    expect(store.preferences.baseFontSize).toBe(16)
    expect(view.state.draft.baseFontSize).toBe(18)
    expect(document.documentElement.style.fontSize).toBe('18px')
    expect(store.preferencesSaveState).toBe('review')
    expect(api.updatePreferences).toHaveBeenCalledTimes(1)
    view.state.retrySave(); await flush()
    expect(api.updatePreferences).toHaveBeenCalledTimes(2)
    expect(store.preferencesSaveState).toBe('saved')
  })
})
