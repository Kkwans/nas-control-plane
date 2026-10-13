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
const applied = { applied: true, rollbackAvailable: true, changeId: 'fixture-change', errorCode: '' }
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
type State = {
  details: SystemDetails | null; dnsCapability: DNSCapability | null; loading: boolean; errorMessage: string;
  dnsDraft: string; dnsPreview: DNSChangePreview | null; dnsChangeId: string; dnsMessage: string; dnsDialogOpen: boolean; dnsPending: boolean;
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
    await state.confirmDNS(); expect(state.dnsChangeId).toBe('fixture-change'); expect(state.dnsMessage).toBe('DNS 已应用。')
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
        mount(); await flush(); state.openDNSEditor(); state.dnsPreview = preview; state.dnsChangeId = 'fixture-change'
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
    mount(); await flush(); state.dnsPreview = preview; state.dnsChangeId = 'fixture-change'
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
