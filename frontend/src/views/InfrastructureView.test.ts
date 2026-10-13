// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick, type App } from 'vue'
import type { DNSCapability, DNSChangePreview, MihomoInspection, SystemDetails } from '@/api/system'
import { beginSession, invalidateSession } from '@/session/sessionLifecycle'
import InfrastructureView from './InfrastructureView.vue'

const api = vi.hoisted(() => ({ requestSystemDetails: vi.fn(), requestDNSCapability: vi.fn(), inspectMihomo: vi.fn(), previewDNSChange: vi.fn(), confirmDNSChange: vi.fn(), rollbackDNSChange: vi.fn() }))
const refresh = vi.hoisted(() => ({ register: vi.fn(), unregister: vi.fn() }))
vi.mock('@/api/system', () => api)
vi.mock('@/stores/system', () => ({ useSystemStore: () => ({ services: [] }) }))
vi.mock('@/composables/manualRefresh', () => ({ useManualRefreshRegistry: () => ({ register: refresh.register }) }))
vi.mock('@/components/WorkspaceHeader.vue', () => ({ default: { render: () => null } }))
vi.mock('@/components/InfrastructureOverviewPanel.vue', () => ({ default: { render: () => null } }))
vi.mock('element-plus', () => {
  const passthrough = defineComponent({ setup(_, { slots }) { return () => h('div', [slots.default?.(), slots.footer?.()]) } })
  return { ElButton: passthrough, ElInput: passthrough, ElDialog: passthrough }
})
const dns: DNSCapability = { backend: 'ugos-network-service', detected: true, state: 'ready', readOnly: false, canRead: true, canPreview: true, canConfirm: true, canRollback: true, nameservers: ['192.0.2.1'], configuredNameservers: ['192.0.2.1'], detectionSource: 'fixture', errorCode: '' }
const details = { device: { architecture: 'fixture' }, warnings: [], network: { interfaces: [], routes: [], gateway: '', listeningPorts: [] }, storage: { mounts: [], disks: [] }, proxy: { mihomo: { detected: false, state: 'unknown' } }, dns } as unknown as SystemDetails
const inspection = { errorCode: '', publicEgress: { address: '192.0.2.10' } } as MihomoInspection
const preview: DNSChangePreview = { previewId: 'fixture-preview', backend: dns.backend, before: { interface: '', connectionId: '', nameservers: dns.nameservers, searchDomains: [] }, after: { interface: '', connectionId: '', nameservers: ['192.0.2.2'], searchDomains: [] }, requiresConfirm: true, rollbackAvailable: true, expiresAt: '', errorCode: '' }
const applied = { backend: dns.backend, applied: true, rollbackAvailable: true, changeId: 'fixture-change', errorCode: '' }
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
type State = {
  details: SystemDetails | null; dnsCapability: DNSCapability | null; loading: boolean; errorMessage: string;
  dnsDraft: string; dnsPreview: DNSChangePreview | null; dnsChangeId: string; dnsMessage: string; dnsDialogOpen: boolean; dnsPending: boolean; dnsChangeBackend: string;
  mihomoInspection: MihomoInspection | null; publicEgressLoading: boolean; publicEgressMessage: string;
  loadDetails(): Promise<void>; checkMihomo(force: boolean): Promise<void>; previewDNS(): Promise<void>; confirmDNS(): Promise<void>; rollbackDNS(): Promise<void>;
  openDNSEditor(): void; invalidateDNSPreview(): void; handleManualRefresh(): Promise<void> | undefined;
}
let app: App | undefined
let host: HTMLDivElement
let state: State
function mount() {
  host = document.createElement('div'); document.body.append(host)
  app = createApp({ render: () => h(InfrastructureView, { ref: (instance: unknown) => { if (instance) state = (instance as { $: { setupState: State } }).$.setupState } }) })
  app.mount(host)
}
function stop(action: 'unmount' | 'session') {
  if (action === 'unmount') { app!.unmount(); app = undefined } else { invalidateSession(); beginSession() }
}
async function flush() { for (let i = 0; i < 8; i++) await nextTick() }
function expectCleared() {
  expect(state.details).toBeNull(); expect(state.dnsCapability).toBeNull(); expect(state.mihomoInspection).toBeNull()
  expect(state.dnsDraft).toBe(''); expect(state.dnsPreview).toBeNull(); expect(state.dnsChangeId).toBe(''); expect(state.dnsMessage).toBe(''); expect(state.dnsDialogOpen).toBe(false)
  expect(state.loading).toBe(false); expect(state.dnsPending).toBe(false); expect(state.publicEgressLoading).toBe(false); expect(state.errorMessage).toBe(''); expect(state.publicEgressMessage).toBe('')
}
beforeEach(() => {
  vi.resetAllMocks(); beginSession(); refresh.register.mockReturnValue(refresh.unregister)
  api.requestSystemDetails.mockResolvedValue(details); api.requestDNSCapability.mockResolvedValue(dns); api.inspectMihomo.mockResolvedValue(inspection)
  api.previewDNSChange.mockResolvedValue(preview); api.confirmDNSChange.mockResolvedValue(applied); api.rollbackDNSChange.mockResolvedValue({ ...applied, applied: false, rollbackAvailable: false })
})
afterEach(() => { app?.unmount(); app = undefined; host?.remove() })

describe('InfrastructureView 页面与认证生命周期', () => {
  it('正常加载、检查、预览、确认与回滚路径保留，写操作不重放', async () => {
    mount(); await flush(); expect(state.details).toEqual(details); expect(state.mihomoInspection).toEqual(inspection)
    expect(api.inspectMihomo.mock.calls[0]?.[0]).toBe(false)
    await state.checkMihomo(true); expect(api.inspectMihomo.mock.calls[1]?.[0]).toBe(true)
    state.openDNSEditor(); state.dnsDraft = '192.0.2.2'; await state.previewDNS()
    expect(api.previewDNSChange).toHaveBeenCalledWith({ nameservers: ['192.0.2.2'] }); expect(state.dnsPreview).toEqual(preview)
    await state.confirmDNS(); await flush(); expect(state.dnsChangeId).toBe('fixture-change'); expect(state.dnsMessage).toBe('DNS 已应用。')
    await state.rollbackDNS(); expect(state.dnsChangeId).toBe(''); expect(state.dnsMessage).toBe('DNS 已回滚。')
    expect(api.confirmDNSChange).toHaveBeenCalledExactlyOnceWith({ previewId: preview.previewId, confirmed: true })
    expect(api.rollbackDNSChange).toHaveBeenCalledExactlyOnceWith('fixture-change')
    expect(api.requestSystemDetails).toHaveBeenCalledTimes(3)
  })

  it.each(['unmount', 'session'] as const)('%s 同步清理、取消两项读取；迟到结果不再发起检查', async (action) => {
    const read = deferred<SystemDetails>(); const dnsRead = deferred<DNSCapability>()
    api.requestSystemDetails.mockReturnValue(read.promise); api.requestDNSCapability.mockReturnValue(dnsRead.promise)
    mount(); state.dnsDraft = 'fixture'; state.dnsDialogOpen = true; state.dnsPreview = preview; state.dnsChangeId = 'fixture'
    const signals = [api.requestSystemDetails.mock.calls[0]![1], api.requestDNSCapability.mock.calls[0]![1]] as AbortSignal[]
    stop(action); expect(signals.every((signal) => signal.aborted)).toBe(true); expectCleared(); expect(refresh.unregister).toHaveBeenCalledTimes(1)
    read.resolve(details); dnsRead.resolve(dns); await flush(); expectCleared(); expect(api.inspectMihomo).not.toHaveBeenCalled()
    await state.loadDetails(); await state.checkMihomo(true); await state.previewDNS(); await state.confirmDNS(); await state.rollbackDNS(); await state.handleManualRefresh(); state.openDNSEditor(); state.invalidateDNSPreview()
    expect(api.requestSystemDetails).toHaveBeenCalledTimes(1); expect(api.previewDNSChange).not.toHaveBeenCalled(); expect(api.confirmDNSChange).not.toHaveBeenCalled(); expect(api.rollbackDNSChange).not.toHaveBeenCalled(); expectCleared()
  })

  it.each(['success', 'failure'] as const)('较旧读取 %s 不发布、不触发检查或清理新请求 busy', async (outcome) => {
    const old = deferred<SystemDetails>(); const latest = deferred<SystemDetails>()
    api.requestSystemDetails.mockReturnValueOnce(old.promise).mockReturnValueOnce(latest.promise)
    mount(); const oldSignal = api.requestSystemDetails.mock.calls[0]![1] as AbortSignal
    const retry = state.loadDetails(); expect(oldSignal.aborted).toBe(true)
    if (outcome === 'success') old.resolve(details); else old.reject(new Error('late fixture'))
    await flush(); expect(state.loading).toBe(true); expect(state.errorMessage).toBe(''); expect(api.inspectMihomo).not.toHaveBeenCalled()
    latest.resolve(details); await retry; await flush(); expect(state.loading).toBe(false); expect(api.inspectMihomo).toHaveBeenCalledTimes(1)
  })

  it.each(['unmount', 'session'] as const)('%s 后迟到检查成功或错误均静默', async (action) => {
    const read = deferred<MihomoInspection>(); api.inspectMihomo.mockReturnValue(read.promise)
    mount(); await flush(); const signal = api.inspectMihomo.mock.calls[0]![2] as AbortSignal
    stop(action); expect(signal.aborted).toBe(true)
    if (action === 'unmount') read.resolve(inspection); else read.reject(new Error('late fixture'))
    await flush(); expectCleared()
  })

  it('刷新取消旧检查，其迟到 finally 不清理新检查 busy', async () => {
    const old = deferred<MihomoInspection>(); const latest = deferred<MihomoInspection>()
    api.inspectMihomo.mockReturnValueOnce(old.promise).mockReturnValueOnce(latest.promise)
    mount(); await flush(); const oldSignal = api.inspectMihomo.mock.calls[0]![2] as AbortSignal
    await state.loadDetails(); expect(oldSignal.aborted).toBe(true); expect(state.publicEgressLoading).toBe(true)
    old.reject(new Error('old fixture')); await flush(); expect(state.publicEgressLoading).toBe(true); expect(state.publicEgressMessage).toBe('')
    latest.resolve(inspection); await flush(); expect(state.mihomoInspection).toEqual(inspection); expect(state.publicEgressLoading).toBe(false)
  })

  for (const operation of ['preview', 'confirm', 'rollback'] as const) {
    for (const action of ['unmount', 'session'] as const) {
      it.each(['success', 'failure'] as const)(`${operation} pending 时 ${action}，迟到 %s 无发布、重读或重放`, async (outcome) => {
        mount(); await flush(); state.openDNSEditor();
        if (operation === 'confirm') await state.previewDNS()
        state.dnsChangeId = 'fixture-change'; state.dnsChangeBackend = dns.backend
        api.previewDNSChange.mockClear()
        const response = deferred<unknown>()
        const mock = operation === 'preview' ? api.previewDNSChange : operation === 'confirm' ? api.confirmDNSChange : api.rollbackDNSChange
        mock.mockReturnValue(response.promise)
        const pending = operation === 'preview' ? state.previewDNS() : operation === 'confirm' ? state.confirmDNS() : state.rollbackDNS()
        expect(state.dnsPending).toBe(true); stop(action); expectCleared()
        if (outcome === 'success') response.resolve(operation === 'preview' ? preview : applied); else response.reject(new Error('late fixture'))
        await pending; await flush(); expectCleared(); expect(mock).toHaveBeenCalledTimes(1)
        expect(api.requestSystemDetails).toHaveBeenCalledTimes(1); expect(api.requestDNSCapability).toHaveBeenCalledTimes(1); expect(api.inspectMihomo).toHaveBeenCalledTimes(1)
      })
    }
  }

  it.each(['confirm', 'rollback'] as const)('%s 已完成但复核读取 pending 时认证更替，不发布最终提示', async (operation) => {
    mount(); await flush(); state.openDNSEditor(); await state.previewDNS(); state.dnsChangeId = 'fixture-change'; state.dnsChangeBackend = dns.backend
    const read = deferred<SystemDetails>(); api.requestSystemDetails.mockReturnValueOnce(read.promise)
    const pending = operation === 'confirm' ? state.confirmDNS() : state.rollbackDNS(); await flush()
    expect(api.requestSystemDetails).toHaveBeenCalledTimes(2); stop('session'); read.resolve(details); await pending; await flush()
    expectCleared(); expect(api.inspectMihomo).toHaveBeenCalledTimes(1)
  })
})

describe('基础设施读取取消信号传递', () => {
  it.each(['dns', 'inspection'] as const)('%s 传递调用者 signal 并保留请求语义，取消不重试', async (operation) => {
    const actual = await import('@/api/systemInfo')
    const controller = new AbortController()
    const response = deferred<Response>()
    const fetcher = vi.fn<typeof fetch>(() => response.promise)
    const request = operation === 'dns' ? actual.requestDNSCapability(fetcher, controller.signal) : actual.inspectMihomo(true, fetcher, controller.signal)
    const init = fetcher.mock.calls[0]![1]!
    expect(init.signal).toBe(controller.signal); expect(init.credentials).toBe('same-origin')
    expect(fetcher.mock.calls[0]![0]).toBe(operation === 'dns' ? '/api/v1/system/dns/capability' : '/api/v1/proxy/mihomo/inspect')
    if (operation === 'inspection') { expect(init.method).toBe('POST'); expect(init.body).toBe(JSON.stringify({ force: true })) }
    controller.abort(); expect(init.signal!.aborted).toBe(true)
    response.reject(new DOMException('fixture abort', 'AbortError'))
    await expect(request).rejects.toMatchObject({ name: 'AbortError' }); expect(fetcher).toHaveBeenCalledTimes(1)
  })
})

describe('DNS 编辑器同页所有权', () => {
  async function open() { mount(); await flush(); state.openDNSEditor(); state.dnsDraft = '192.0.2.2' }
  async function ready() { await open(); await state.previewDNS() }
  function handle() { state.dnsChangeId = 'fixture-change'; state.dnsChangeBackend = dns.backend }

  it.each(['success', 'failure', 'roundtrip'] as const)('A 预览 pending 后改 B：%s 不恢复旧预览或错误，并释放自身 busy', async (outcome) => {
    await open(); const response = deferred<DNSChangePreview>(); api.previewDNSChange.mockReturnValueOnce(response.promise)
    const pending = state.previewDNS(); state.dnsDraft = '192.0.2.3'
    if (outcome === 'roundtrip') state.dnsDraft = '192.0.2.2'
    expect(api.previewDNSChange).toHaveBeenCalledExactlyOnceWith({ nameservers: ['192.0.2.2'] })
    if (outcome === 'failure') response.reject(new Error('old error')); else response.resolve(preview)
    await pending; expect(state.dnsPreview).toBeNull(); expect(state.dnsMessage).toBe(''); expect(state.dnsPending).toBe(false)
    await state.confirmDNS(); expect(api.confirmDNSChange).not.toHaveBeenCalled()
    await state.previewDNS(); expect(state.dnsPreview).toEqual(preview)
  })

  it('后台读取晚到不替换草稿；关闭再打开后的新草稿也保留', async () => {
    await ready(); const read = deferred<SystemDetails>(); api.requestSystemDetails.mockReturnValueOnce(read.promise)
    const pending = state.loadDetails(); state.dnsDialogOpen = false
    read.resolve(details); await pending; state.openDNSEditor(); state.dnsDraft = '192.0.2.9'
    const late = deferred<SystemDetails>(); api.requestSystemDetails.mockReturnValueOnce(late.promise)
    const refreshPending = state.handleManualRefresh(); expect(state.dnsPreview).toBeNull()
    late.resolve(details); await refreshPending; expect(state.dnsDraft).toBe('192.0.2.9'); expect(state.dnsPreview).toBeNull()
  })

  it('显式刷新作废在途预览，读取成功也不能复活它', async () => {
    await open(); const response = deferred<DNSChangePreview>(); api.previewDNSChange.mockReturnValueOnce(response.promise)
    const pending = state.previewDNS(); await state.handleManualRefresh(); response.resolve(preview); await pending
    expect(state.dnsPreview).toBeNull(); expect(state.dnsPending).toBe(false); expect(state.dnsDraft).toBe('192.0.2.2')
  })

  it.each(['throw', 'negative', 'wrong-backend'] as const)('确认 %s 消费原预览且不误清旧句柄或自动重试', async (outcome) => {
    await ready(); handle(); const response = deferred<unknown>(); api.confirmDNSChange.mockReturnValueOnce(response.promise)
    const pending = state.confirmDNS(); expect(state.dnsPreview).toBeNull(); await state.confirmDNS()
    if (outcome === 'throw') response.reject(new Error('connection lost'))
    else response.resolve({ ...applied, ...(outcome === 'negative' ? { errorCode: 'DENIED', applied: false } : { backend: 'other' }), changeId: 'not-known' })
    await pending; await state.confirmDNS()
    expect(api.confirmDNSChange).toHaveBeenCalledExactlyOnceWith({ previewId: preview.previewId, confirmed: true })
    expect(state.dnsChangeId).toBe('fixture-change'); expect(state.dnsPreview).toBeNull(); expect(state.dnsMessage).toContain('核实'); expect(api.requestSystemDetails).toHaveBeenCalledTimes(1)
  })

  it.each(['failure', 'pending'] as const)('确认结果先于复核读取 %s 保存；读取不抹去操作文案或恢复预览', async (outcome) => {
    await ready(); const read = deferred<SystemDetails>(); api.requestSystemDetails.mockReturnValueOnce(read.promise)
    await state.confirmDNS(); expect(state.dnsChangeId).toBe('fixture-change'); expect(state.dnsMessage).toBe('DNS 已应用。'); expect(state.dnsPending).toBe(false)
    await state.confirmDNS(); await state.rollbackDNS(); expect(api.confirmDNSChange).toHaveBeenCalledTimes(1); expect(api.rollbackDNSChange).not.toHaveBeenCalled()
    if (outcome === 'failure') read.reject(new Error('read failed')); else read.resolve(details)
    await flush(); expect(state.dnsChangeId).toBe('fixture-change'); expect(state.dnsMessage).toBe('DNS 已应用。'); expect(state.dnsPreview).toBeNull()
    if (outcome === 'failure') { expect(state.errorMessage).toBe('read failed'); expect(host.textContent).toContain('详情刷新失败') }
  })

  it.each(['confirm', 'rollback'] as const)('手动刷新不释放在途 %s 的锁且不夺取已知结果', async (kind) => {
    await ready(); handle(); const response = deferred<unknown>(); const mock = kind === 'confirm' ? api.confirmDNSChange : api.rollbackDNSChange
    mock.mockReturnValueOnce(response.promise); const pending = kind === 'confirm' ? state.confirmDNS() : state.rollbackDNS()
    await state.handleManualRefresh(); expect(state.dnsPending).toBe(true)
    await state.confirmDNS(); await state.rollbackDNS(); expect(mock).toHaveBeenCalledTimes(1)
    response.resolve(kind === 'confirm' ? applied : { ...applied, applied: false, rollbackAvailable: false }); await pending; await flush()
    expect(state.dnsChangeId).toBe(kind === 'confirm' ? 'fixture-change' : ''); expect(state.dnsMessage).toBe(kind === 'confirm' ? 'DNS 已应用。' : 'DNS 已回滚。')
  })

  it.each(['throw', 'error', 'applied', 'available', 'wrong-id', 'wrong-backend', 'success'] as const)('回滚 %s 只在明确匹配成功时清理句柄', async (outcome) => {
    await open(); handle()
    if (outcome === 'throw') api.rollbackDNSChange.mockRejectedValueOnce(new Error('unknown'))
    else api.rollbackDNSChange.mockResolvedValueOnce({ ...applied, applied: outcome === 'applied', rollbackAvailable: outcome === 'available', errorCode: outcome === 'error' ? 'DENIED' : '', changeId: outcome === 'wrong-id' ? 'other' : 'fixture-change', backend: outcome === 'wrong-backend' ? 'other' : dns.backend })
    await state.rollbackDNS(); expect(api.rollbackDNSChange).toHaveBeenCalledExactlyOnceWith('fixture-change')
    expect(state.dnsChangeId).toBe(outcome === 'success' ? '' : 'fixture-change')
  })

  it.each(['readOnly', 'canPreview', 'canConfirm', 'canRollback', 'backend'] as const)('能力/backend %s 改变作废旧预览；动作入口重新检查能力', async (key) => {
    await ready(); handle(); state.dnsCapability = { ...dns, [key]: key === 'backend' ? 'other' : key === 'readOnly' }
    expect(state.dnsPreview).toBeNull(); await state.confirmDNS(); expect(api.confirmDNSChange).not.toHaveBeenCalled()
    if (key === 'readOnly' || key === 'canRollback' || key === 'backend') { await state.rollbackDNS(); expect(api.rollbackDNSChange).not.toHaveBeenCalled() }
    if (key === 'readOnly' || key === 'canPreview') { state.dnsDialogOpen = false; state.openDNSEditor(); expect(state.dnsDialogOpen).toBe(false) }
  })

  it('能力读取失败禁止使用旧写权限；恢复读取不恢复已消费预览', async () => {
    await ready(); handle(); api.requestDNSCapability.mockRejectedValueOnce(new Error('offline'))
    await state.handleManualRefresh(); await state.previewDNS(); await state.confirmDNS(); await state.rollbackDNS()
    expect(api.previewDNSChange).toHaveBeenCalledTimes(1); expect(api.confirmDNSChange).not.toHaveBeenCalled(); expect(api.rollbackDNSChange).not.toHaveBeenCalled()
    await state.loadDetails(); expect(state.dnsPreview).toBeNull(); expect(state.dnsChangeId).toBe('fixture-change')
  })

  it('合成关闭再打开不能让旧预览侵入新编辑意图（普通 UI pending 禁止关闭）', async () => {
    await open(); const response = deferred<DNSChangePreview>(); api.previewDNSChange.mockReturnValueOnce(response.promise)
    const pending = state.previewDNS(); state.dnsDialogOpen = false; state.dnsDialogOpen = true; state.dnsDraft = '192.0.2.9'
    response.resolve(preview); await pending; expect(state.dnsPreview).toBeNull(); expect(state.dnsMessage).toBe(''); expect(state.dnsDraft).toBe('192.0.2.9'); expect(state.dnsPending).toBe(false)
  })

  it('旧回滚成功不能清理合成替换的新目标句柄', async () => {
    await open(); handle(); const response = deferred<unknown>(); api.rollbackDNSChange.mockReturnValueOnce(response.promise)
    const pending = state.rollbackDNS(); state.dnsChangeId = 'new-change'
    response.resolve({ ...applied, applied: false, rollbackAvailable: false }); await pending
    expect(api.rollbackDNSChange).toHaveBeenCalledExactlyOnceWith('fixture-change'); expect(state.dnsChangeId).toBe('new-change')
  })

  it('确认完成后的复核 supersede 旧读取，旧读取迟到不覆盖新快照或操作结果', async () => {
    await ready(); const response = deferred<unknown>(); api.confirmDNSChange.mockReturnValueOnce(response.promise)
    const pending = state.confirmDNS(); const old = deferred<SystemDetails>(); const latest = deferred<SystemDetails>()
    api.requestSystemDetails.mockReturnValueOnce(old.promise).mockReturnValueOnce(latest.promise)
    const refreshPending = state.handleManualRefresh(); response.resolve(applied); await pending
    expect(state.dnsChangeId).toBe('fixture-change'); expect(state.dnsMessage).toBe('DNS 已应用。')
    latest.resolve(details); await flush(); old.reject(new Error('superseded')); await refreshPending
    expect(state.errorMessage).toBe(''); expect(state.dnsMessage).toBe('DNS 已应用。'); expect(state.dnsChangeId).toBe('fixture-change'); expect(state.dnsDraft).toBe('192.0.2.2')
  })

  it('关闭重开保留同后端句柄，已知不可回滚的成功清理旧句柄', async () => {
    await ready(); handle(); state.dnsDialogOpen = false; state.openDNSEditor(); expect(state.dnsChangeId).toBe('fixture-change')
    await state.previewDNS(); api.confirmDNSChange.mockResolvedValueOnce({ ...applied, rollbackAvailable: false }); await state.confirmDNS(); expect(state.dnsChangeId).toBe('')
  })
})
