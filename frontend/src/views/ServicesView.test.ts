// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick, type App } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { invalidateSession } from '@/session/sessionLifecycle'
import { SiteCreatedIconError, SiteIconUploadError } from '@/api/siteCreatedIconOutcome'
import { SiteWriteUnknownError } from '@/api/siteWriteOutcome'
import { SiteSyncError, useSitesStore } from '@/stores/sites'
import type { Site, SiteProfileInput } from '@/api/control'
import ServicesView from './ServicesView.vue'

const messages = vi.hoisted(() => ({ success: vi.fn(), warning: vi.fn(), error: vi.fn() }))
vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  const stub = defineComponent({ setup: (_, { slots }) => () => h('div', [slots.default?.(), slots.footer?.()]) })
  return { ElMessage: messages, ElMessageBox: { confirm: vi.fn().mockResolvedValue(undefined) },
    ...Object.fromEntries(['ElButton', 'ElDrawer', 'ElFormItem', 'ElInput', 'ElInputNumber', 'ElOption', 'ElSelect', 'ElSwitch', 'ElTooltip'].map((name) => [name, stub])),
    ElForm: defineComponent({ setup(_, { slots, expose }) { expose({ validate: () => Promise.resolve(true) }); return () => h('form', slots.default?.()) } }),
  }
})
vi.mock('@/stores/system', () => ({ useSystemStore: () => ({ preferences: { siteDefaultProtocol: 'http', linkOpenMode: 'new-tab' } }) }))
vi.mock('@/api/control', () => ({ requestSites: vi.fn(), requestIgnoredSites: vi.fn(), createSite: vi.fn(), updateSite: vi.fn(), deleteSite: vi.fn(), restoreSite: vi.fn(), uploadSiteIcon: vi.fn(), uploadCreatedSiteIcon: vi.fn(), deleteSiteIcon: vi.fn(), recordSiteVisit: vi.fn() }))

const profile: SiteProfileInput = { name: 'Demo', description: '', iconUrl: '', category: 'web', primaryPort: 8080, launchUrl: '', favorite: false, sortOrder: 0, hidden: false }
const site = { ...profile, id: 'manual:demo', projectId: 'manual:demo', ports: [8080], state: 'running', source: 'manual' } as Site
interface ViewState {
  query: string
  selectedCategory: string
  siteFilter: string
  draft: SiteProfileInput
  pendingIcon: File | null
  pendingIconPreview: string
  editOpen: boolean
  saving: boolean
  iconBusy: boolean
  unknownIcons: Map<string, SiteIconUploadError>
  syncPending: boolean
  unknownWrite: SiteWriteUnknownError | SiteCreatedIconError | SiteIconUploadError | null
  unknownObservation: string
  recheckUnknownWrite(): Promise<void>
  savedSiteId: string | null
  openCreator(): void
  openEditor(site: Site): void
  saveSite(): Promise<void>
  retryEditorSync(): Promise<void>
  toggleFavorite(site: Site): Promise<void>
  restoreIgnoredSite(id: string): Promise<void>
  uploadIcon(event: Event): Promise<void>
}
let app: App
let host: HTMLDivElement
let view: ViewState
let store: ReturnType<typeof useSitesStore>
let unmounted: boolean
async function settle() { for (let i = 0; i < 8; i++) await Promise.resolve(); await nextTick() }
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
beforeEach(async () => {
  invalidateSession()
  vi.clearAllMocks()
  vi.stubGlobal('URL', class extends URL { static createObjectURL = vi.fn(() => 'blob:fixture-icon'); static revokeObjectURL = vi.fn() })
  const pinia = createPinia()
  setActivePinia(pinia)
  store = useSitesStore()
  vi.spyOn(store, 'refresh').mockResolvedValue(true)
  host = document.createElement('div')
  document.body.append(host)
  app = createApp(ServicesView)
  app.use(pinia)
  app.mount(host)
  unmounted = false
  view = (app._instance as unknown as { setupState: ViewState }).setupState as unknown as ViewState
  await settle()
})
afterEach(() => { if (!unmounted) app.unmount(); host.remove(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe('站点部分成功反馈', () => {
  it('创建后同步失败保留草稿和创建 ID，重试只读；后续保存更新既有站点', async () => {
    const create = vi.spyOn(store, 'create').mockRejectedValue(new SiteSyncError(site.id))
    const save = vi.spyOn(store, 'save').mockResolvedValue(undefined)
    view.openCreator(); view.draft = { ...profile }; await nextTick()
    await view.saveSite()
    expect(view.editOpen).toBe(true)
    expect(view.draft).toEqual(profile)
    expect(view.savedSiteId).toBe(site.id)
    expect(view.syncPending).toBe(true)
    expect(messages.warning).not.toHaveBeenCalled()
    expect(messages.error).not.toHaveBeenCalled()
    expect(messages.success).not.toHaveBeenCalled()
    await view.saveSite()
    expect(create).toHaveBeenCalledTimes(1)
    await view.retryEditorSync()
    expect(create).toHaveBeenCalledTimes(1)
    expect(save).not.toHaveBeenCalled()
    expect(view.draft).toEqual(profile)
    view.draft.name = 'Edited'
    await view.saveSite()
    expect(save).toHaveBeenCalledWith(site.id, expect.objectContaining({ name: 'Edited' }))
    expect(create).toHaveBeenCalledTimes(1)
  })

  it('真实写入失败保留草稿并显示失败', async () => {
    vi.spyOn(store, 'save').mockRejectedValue(new Error('failure'))
    view.openEditor(site); await nextTick()
    await view.saveSite()
    expect(view.editOpen).toBe(true)
    expect(view.draft.name).toBe('Demo')
    expect(view.syncPending).toBe(false)
    expect(messages.error).toHaveBeenCalled()
    expect(messages.success).not.toHaveBeenCalled()
  })

  it('保存期间继续编辑不会丢弃新草稿，提交使用冻结快照', async () => {
    const write = deferred<void>()
    const save = vi.spyOn(store, 'save').mockReturnValue(write.promise)
    view.openEditor(site); await nextTick()
    const operation = view.saveSite(); await settle()
    view.draft.name = 'New draft'
    write.resolve(); await operation
    expect(save).toHaveBeenCalledWith(site.id, expect.objectContaining({ name: 'Demo' }))
    expect(view.draft.name).toBe('New draft')
    expect(view.editOpen).toBe(true)
    expect(view.saving).toBe(false)
  })

  it.each(['close', 'switch', 'session', 'unmount'])('迟到保存不影响已改变的编辑会话：%s', async (change) => {
    const write = deferred<void>()
    vi.spyOn(store, 'save').mockReturnValue(write.promise)
    view.openEditor(site); await nextTick()
    const operation = view.saveSite(); await settle()
    if (change === 'session') invalidateSession()
    else if (change === 'unmount') { app.unmount(); unmounted = true }
    else if (change === 'close') view.editOpen = false
    else view.openEditor({ ...site, id: 'other', name: 'Other' })
    write.reject(new SiteSyncError(site.id)); await operation
    expect(messages.warning).not.toHaveBeenCalled()
    expect(messages.error).not.toHaveBeenCalled()
    expect(messages.success).not.toHaveBeenCalled()
    expect(view.syncPending).toBe(false)
    if (change === 'switch') expect(view.draft.name).toBe('Other')
  })

  it.each(['favorite', 'restore'])('%s 的同步失败反馈为已写入而非提交失败', async (action) => {
    vi.spyOn(store, 'save').mockRejectedValue(new SiteSyncError(site.id))
    vi.spyOn(store, 'restore').mockRejectedValue(new SiteSyncError(site.id))
    if (action === 'favorite') await view.toggleFavorite(site)
    else await view.restoreIgnoredSite(site.id)
    expect(messages.warning).toHaveBeenCalled()
    expect(messages.error).not.toHaveBeenCalled()
    expect(messages.success).not.toHaveBeenCalled()
  })
})


describe('站点同步恢复的并发边界', () => {
  it('采用创建 ID 后图标直接更新同一站点，不再暂存新建图标', async () => {
    vi.spyOn(store, 'create').mockRejectedValue(new SiteSyncError(site.id))
    const upload = vi.spyOn(store, 'uploadIcon').mockRejectedValue(new SiteSyncError(site.id))
    view.openCreator(); view.draft = { ...profile }; await nextTick()
    await view.saveSite()
    const file = new File(['icon'], 'icon.png', { type: 'image/png' })
    await view.uploadIcon({ target: { files: [file] } } as unknown as Event)
    expect(upload).toHaveBeenCalledWith(site.id, file)
    expect(messages.error).not.toHaveBeenCalled()
  })

  it('每站点防重复收藏，其他站点不被阻塞', async () => {
    const write = deferred<void>()
    const save = vi.spyOn(store, 'save').mockReturnValueOnce(write.promise).mockResolvedValue(undefined)
    const first = view.toggleFavorite(site)
    await view.toggleFavorite(site)
    await view.toggleFavorite({ ...site, id: 'other' })
    expect(save).toHaveBeenCalledTimes(2)
    write.resolve(); await first
  })

  it('旧同步重试晚到时不会修改重新打开的编辑器', async () => {
    vi.spyOn(store, 'save').mockRejectedValue(new SiteSyncError(site.id))
    view.openEditor(site); await nextTick(); await view.saveSite()
    const read = deferred<boolean>()
    vi.mocked(store.refresh).mockReturnValueOnce(read.promise)
    const retry = view.retryEditorSync()
    view.editOpen = false
    view.openCreator()
    view.draft.name = 'New draft'
    messages.success.mockClear()
    read.resolve(true); await retry
    expect(view.draft.name).toBe('New draft')
    expect(view.savedSiteId).toBeNull()
    expect(view.syncPending).toBe(false)
    expect(messages.success).not.toHaveBeenCalled()
  })
})


it('创建未完成时不接收第二份图标，避免已创建后的暂存文件被忽略', async () => {
  const created = deferred<SiteProfileInput & { projectId: string }>()
  const create = vi.spyOn(store, 'create').mockReturnValue(created.promise)
  const upload = vi.spyOn(store, 'uploadIcon')
  view.openCreator(); view.draft = { ...profile }; await nextTick()
  const operation = view.saveSite(); await settle()
  const file = new File(['new'], 'new.png', { type: 'image/png' })
  await view.uploadIcon({ target: { files: [file] } } as unknown as Event)
  expect(upload).not.toHaveBeenCalled()
  expect(create).toHaveBeenCalledWith(profile, null)
  expect(document.querySelector<HTMLInputElement>('.site-editor input[type=file]')?.disabled).toBe(true)
  created.resolve({ ...profile, projectId: site.id }); await operation
  expect(view.editOpen).toBe(false)
})

describe('站点写入未知结果的只读核对', () => {
  it.each(['create', 'update', 'icon-compensation'] as const)('%s 未确认时保留草稿与身份，阻止保存和图标重放', async (operation) => {
    const error = new SiteWriteUnknownError(operation, operation === 'create' ? undefined : site.id)
    const create = vi.spyOn(store, 'create').mockRejectedValue(error)
    const save = vi.spyOn(store, 'save').mockRejectedValue(error)
    const upload = vi.spyOn(store, 'uploadIcon')
    if (operation === 'update') view.openEditor(site)
    else { view.openCreator(); view.draft = { ...profile } }
    await nextTick(); await view.saveSite(); await nextTick()
    expect(view.unknownWrite).toBe(error)
    expect(view.syncPending).toBe(false)
    expect(view.savedSiteId).toBe(operation === 'create' ? null : site.id)
    expect(messages.error).not.toHaveBeenCalled()
    expect(messages.success).not.toHaveBeenCalled()
    expect(document.querySelector('.site-editor')?.textContent).toContain('结果未确认')
    expect(document.querySelector('.site-editor')?.textContent).toContain('草稿保留在当前编辑窗口，提交和图标上传已暂停。')
    expect(document.querySelector('.site-editor')?.textContent).toContain('只读核对仅查看当前目录，不能确认最终结果，也不会解除暂停。')
    expect(document.querySelector('.site-editor')?.textContent).toContain('只读核对结果')
    expect(document.querySelector<HTMLInputElement>('.site-editor input[type=file]')?.disabled).toBe(true)
    view.draft.name = 'New draft'
    await view.saveSite()
    await view.uploadIcon({ target: { files: [new File(['icon'], 'icon.png', { type: 'image/png' })] } } as unknown as Event)
    await view.recheckUnknownWrite()
    expect(view.unknownWrite).toBe(error)
    expect(view.draft.name).toBe('New draft')
    await view.saveSite()
    expect(create).toHaveBeenCalledTimes(operation === 'update' ? 0 : 1)
    expect(save).toHaveBeenCalledTimes(operation === 'update' ? 1 : 0)
    expect(upload).not.toHaveBeenCalled()
    if (operation === 'create') expect(view.unknownObservation).toContain('没有可用于核对的站点 ID。创建结果仍未确认')
  })

  it.each(['present', 'hidden', 'ignored', 'both', 'absent'] as const)('已知手工 ID 的 %s 观察保持有限陈述，不解锁提交', async (observation) => {
    vi.spyOn(store, 'create').mockRejectedValue(new SiteWriteUnknownError('icon-compensation', site.id))
    view.openCreator(); view.draft = { ...profile }; await nextTick(); await view.saveSite()
    store.sites = ['present', 'hidden', 'both'].includes(observation) ? [{ ...site, hidden: observation === 'hidden' }] : []
    store.ignoredSites = ['ignored', 'both'].includes(observation) ? [{ ...profile, projectId: site.id }] : []
    store.discovery.status = 'unavailable'
    await view.recheckUnknownWrite()
    expect(view.unknownWrite).not.toBeNull()
    if (observation === 'absent') {
      expect(view.unknownObservation).toContain('本次未找到该记录')
      expect(view.unknownObservation).toContain('仍不能确认请求是否完成')
    } else if (observation === 'ignored') expect(view.unknownObservation).toContain('已忽略列表中发现')
    else if (observation === 'both') expect(view.unknownObservation).toContain('两份列表均发现')
    else expect(view.unknownObservation).toContain('站点目录中发现')
    await view.saveSite()
    expect(store.create).toHaveBeenCalledTimes(1)
  })

  it('读取失败保留未知，之后的迟到服务端记录只改变本次观察', async () => {
    vi.spyOn(store, 'create').mockRejectedValue(new SiteWriteUnknownError('icon-compensation', site.id))
    view.openCreator(); view.draft = { ...profile }; await nextTick(); await view.saveSite()
    vi.mocked(store.refresh).mockResolvedValueOnce(false)
    await view.recheckUnknownWrite()
    expect(view.unknownObservation).toContain('未完成')
    await view.recheckUnknownWrite()
    expect(view.unknownObservation).toContain('本次未找到该记录')
    store.sites = [{ ...site, iconUrl: '/late-icon.png' }]
    await view.recheckUnknownWrite()
    expect(view.unknownObservation).toContain('站点目录中发现')
    expect(view.unknownWrite).not.toBeNull()
    expect(store.create).toHaveBeenCalledTimes(1)
  })

  it.each(['close', 'switch', 'session', 'unmount'])('未知保存迟到不覆盖编辑所有权：%s', async (change) => {
    const write = deferred<never>()
    vi.spyOn(store, 'create').mockReturnValue(write.promise)
    view.openCreator(); view.draft = { ...profile }; await nextTick()
    const operation = view.saveSite(); await settle()
    if (change === 'session') invalidateSession()
    else if (change === 'unmount') { app.unmount(); unmounted = true }
    else if (change === 'close') view.editOpen = false
    else view.openEditor({ ...site, id: 'manual:other', name: 'Other' })
    write.reject(new SiteWriteUnknownError('icon-compensation', site.id)); await operation
    expect(view.unknownWrite).toBeNull()
    expect(messages.error).not.toHaveBeenCalled()
    if (change === 'switch') { expect(view.draft.name).toBe('Other'); expect(view.savedSiteId).toBe('manual:other') }
  })

  it.each(['close', 'switch', 'session', 'unmount'])('只读核对迟到不影响新编辑会话：%s', async (change) => {
    vi.spyOn(store, 'create').mockRejectedValue(new SiteWriteUnknownError('create'))
    view.openCreator(); view.draft = { ...profile }; await nextTick(); await view.saveSite()
    const read = deferred<boolean>()
    vi.mocked(store.refresh).mockReturnValueOnce(read.promise)
    const operation = view.recheckUnknownWrite()
    if (change === 'session') invalidateSession()
    else if (change === 'unmount') { app.unmount(); unmounted = true }
    else {
      view.editOpen = false
      if (change === 'switch') { view.openCreator(); view.draft.name = 'Fresh' }
    }
    read.resolve(true); await operation
    expect(view.unknownObservation).toBe('')
    if (change === 'switch') { expect(view.unknownWrite).toBeNull(); expect(view.draft.name).toBe('Fresh'); expect(view.saving).toBe(false) }
  })

  it('收藏的坏成功响应暂停同一资源重复写入，其他站点仍可操作', async () => {
    const save = vi.spyOn(store, 'save').mockRejectedValueOnce(new SiteWriteUnknownError('update', site.id)).mockResolvedValue(undefined)
    await view.toggleFavorite(site)
    await view.toggleFavorite(site)
    await view.toggleFavorite({ ...site, id: 'manual:other' })
    expect(save).toHaveBeenCalledTimes(2)
    expect(messages.error).not.toHaveBeenCalled()
    await nextTick()
    expect(host.textContent).toContain('收藏保存请求可能已经执行')
  })
})


describe('站点真实传输未知结果', () => {
  it.each(['disconnect', 'abort', 'gateway'] as const)('提交后 %s 保留未知创建，重复保存和图标选择不重放', async (failure) => {
    const actual = await vi.importActual<typeof import('@/api/control')>('@/api/control')
    const api = await import('@/api/control')
    vi.mocked(api.createSite).mockImplementationOnce(actual.createSite)
    const committed: string[] = []
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => {
      committed.push(`manual:${committed.length + 1}`)
      if (failure === 'disconnect') throw new TypeError('connection lost after commit')
      if (failure === 'abort') throw new DOMException('aborted after commit', 'AbortError')
      return new Response('Bad Gateway', { status: 502 })
    })
    vi.stubGlobal('fetch', fetch)
    view.openCreator(); view.draft = { ...profile }
    view.pendingIcon = new File(['icon'], 'icon.png', { type: 'image/png' })
    await nextTick(); await view.saveSite(); await view.saveSite()
    await view.uploadIcon({ target: { files: [view.pendingIcon] } } as unknown as Event)
    await view.recheckUnknownWrite(); await view.saveSite()
    expect(committed).toEqual(['manual:1'])
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(view.unknownWrite).toMatchObject({ operation: 'create', code: 'SITE_WRITE_UNKNOWN' })
    expect(view.savedSiteId).toBeNull()
    expect(view.pendingIcon).not.toBeNull()
    expect(api.uploadSiteIcon).not.toHaveBeenCalled()
    expect(api.deleteSite).not.toHaveBeenCalled()
    expect(messages.error).not.toHaveBeenCalled()
    expect(messages.success).not.toHaveBeenCalled()
  })

  it.each(['editor', 'favorite'] as const)('真实 PUT 断线暂停 %s 重提并保留目标', async (mode) => {
    const actual = await vi.importActual<typeof import('@/api/control')>('@/api/control')
    const api = await import('@/api/control')
    vi.mocked(api.updateSite).mockImplementationOnce(actual.updateSite)
    const fetch = vi.fn<typeof globalThis.fetch>().mockRejectedValue(new TypeError('connection lost after commit'))
    vi.stubGlobal('fetch', fetch)
    if (mode === 'editor') {
      view.openEditor(site); await nextTick(); await view.saveSite(); await view.saveSite()
      expect(view.savedSiteId).toBe(site.id)
      expect(view.unknownWrite).toMatchObject({ operation: 'update', siteId: site.id })
    } else { await view.toggleFavorite(site); await view.toggleFavorite(site) }
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(fetch).toHaveBeenCalledWith(`/api/v1/sites/${encodeURIComponent(site.id)}`, expect.objectContaining({ method: 'PUT', redirect: 'error' }))
    expect(messages.error).not.toHaveBeenCalled()
    expect(messages.success).not.toHaveBeenCalled()
  })
})


describe('已创建站点的图标部分结果保留', () => {
  it.each(['rejected', 'unknown'] as const)('%s 保留原始 File 和 ID，资料保存不能丢弃待传图标或重建', async (outcome) => {
    const error = new SiteCreatedIconError(site.id, outcome)
    const create = vi.spyOn(store, 'create').mockRejectedValue(error)
    const save = vi.spyOn(store, 'save')
    const upload = vi.spyOn(store, 'uploadIcon')
    view.openCreator(); view.draft = { ...profile }
    const file = new File(['icon'], 'icon.svg', { type: 'image/svg+xml' })
    view.pendingIcon = file
    await nextTick(); await view.saveSite(); await nextTick()
    expect(view.savedSiteId).toBe(site.id)
    expect(view.pendingIcon).toBe(file)
    expect(document.querySelector('.site-editor')?.textContent).toContain('站点已创建')
    expect(document.querySelector('.site-editor')?.textContent).toContain(outcome === 'rejected' ? '上传未完成' : '上传结果未确认')
    expect(document.querySelector<HTMLInputElement>('.site-editor input[type=file]')?.disabled).toBe(true)
    view.draft.name = '保留新草稿'
    await view.saveSite()
    await view.uploadIcon({ target: { files: [file] } } as unknown as Event)
    store.sites = [site]
    await view.recheckUnknownWrite()
    await view.saveSite()
    expect(view.unknownWrite).toBe(error)
    expect(view.pendingIcon).toBe(file)
    expect(view.draft.name).toBe('保留新草稿')
    expect(create).toHaveBeenCalledTimes(1)
    expect(save).not.toHaveBeenCalled()
    expect(upload).not.toHaveBeenCalled()
    expect(messages.success).not.toHaveBeenCalled()
  })

  it.each(['close', 'switch', 'session', 'unmount'])('迟到部分结果不覆盖新编辑器：%s', async (change) => {
    const write = deferred<never>()
    vi.spyOn(store, 'create').mockReturnValue(write.promise)
    view.openCreator(); view.draft = { ...profile }; await nextTick()
    const operation = view.saveSite(); await settle()
    if (change === 'session') invalidateSession()
    else if (change === 'unmount') { app.unmount(); unmounted = true }
    else if (change === 'close') view.editOpen = false
    else view.openEditor({ ...site, id: 'manual:other', name: 'Other' })
    write.reject(new SiteCreatedIconError(site.id, 'unknown')); await operation
    expect(view.unknownWrite).toBeNull()
    expect(messages.error).not.toHaveBeenCalled()
    if (change === 'switch') { expect(view.draft.name).toBe('Other'); expect(view.savedSiteId).toBe('manual:other') }
  })
})


it('图标未知结果按警告、真实预览、完整资料顺序呈现，保留上传锁和草稿', async () => {
  const error = new SiteIconUploadError(site.id, 'unknown')
  vi.spyOn(store, 'uploadIcon').mockRejectedValue(error)
  view.openEditor(site)
  const icon = new File(['icon'], 'icon.svg', { type: 'image/svg+xml' })
  await view.uploadIcon({ target: { files: [icon] } } as unknown as Event)
  await nextTick()
  const editor = document.querySelector('.site-editor')!
  const notice = editor.querySelector('.editor-sync-notice')!
  const preview = editor.querySelector<HTMLImageElement>('.icon-upload__preview img')!
  const fields = editor.querySelector('.editor-grid')!
  expect(notice.textContent).toContain('图标上传可能已经执行，结果未确认。请勿重复上传。')
  expect(notice.textContent).toContain('草稿保留在当前编辑窗口，提交和图标上传已暂停。')
  expect(notice.textContent).toContain('只读核对仅查看当前目录，不能确认最终结果，也不会解除暂停。')
  expect(notice.compareDocumentPosition(preview) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  expect(preview.compareDocumentPosition(fields) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  expect(preview.getAttribute('src')).toBe('blob:fixture-icon')
  expect(editor.querySelector<HTMLButtonElement>('.icon-upload__preview')?.disabled).toBe(true)
  expect(editor.querySelectorAll('.editor-grid')).toHaveLength(2)
  expect(editor.querySelector('.switch-grid')).not.toBeNull()
  expect(editor.querySelector('.editor-context')?.textContent).toContain('编辑手动站点资料')
  expect(editor.querySelector('.editor-context')?.textContent).not.toContain('Docker')
  expect(editor.textContent).toContain('关闭并查看目录')
  expect(editor.textContent).toContain('只读核对结果')
  expect(view.pendingIcon).toBe(icon)
  expect(view.draft).toEqual(profile)
})

describe('现有站点图标上传归属', () => {
  const file = () => new File(['icon'], 'icon.svg', { type: 'image/svg+xml' })
  const event = (icon: File) => ({ target: { files: [icon] } }) as unknown as Event
  it('同一目标单飞，未知保留File；只读核对和重开不重放，其他目标仍可用', async () => {
    const pending = deferred<{ siteId: string; iconUrl: string }>()
    const upload = vi.spyOn(store, 'uploadIcon').mockReturnValueOnce(pending.promise)
    const save = vi.spyOn(store, 'save')
    view.openEditor(site)
    const icon = file()
    const operation = view.uploadIcon(event(icon))
    expect(view.iconBusy).toBe(true)
    await view.uploadIcon(event(file()))
    await view.saveSite()
    expect(upload).toHaveBeenCalledTimes(1)
    expect(save).not.toHaveBeenCalled()
    pending.reject(new SiteIconUploadError(site.id, 'unknown'))
    await operation
    expect(view.iconBusy).toBe(false)
    expect(view.pendingIcon).toBe(icon)
    expect(view.unknownWrite).toBeInstanceOf(SiteIconUploadError)
    await view.recheckUnknownWrite()
    await view.uploadIcon(event(file()))
    await view.saveSite()
    view.editOpen = false; view.openEditor(site)
    await view.uploadIcon(event(file()))
    expect(upload).toHaveBeenCalledTimes(1)
    expect(view.unknownWrite).toBeInstanceOf(SiteIconUploadError)
    expect(messages.error).not.toHaveBeenCalled()
    const other = { ...site, id: 'manual:other', projectId: 'manual:other' }
    view.openEditor(other)
    upload.mockResolvedValueOnce({ siteId: other.id, iconUrl: `/api/v1/sites/${other.id}/icon` })
    await view.uploadIcon(event(file()))
    expect(upload).toHaveBeenCalledTimes(2)
    expect(view.unknownWrite).toBeNull()
    expect(view.unknownIcons.has(site.id)).toBe(true)
  })

  it('迟到A未知不覆盖B草稿，A重开受保护且B锁只由B完成清除', async () => {
    const a = deferred<{ siteId: string; iconUrl: string }>()
    const b = deferred<{ siteId: string; iconUrl: string }>()
    vi.spyOn(store, 'uploadIcon').mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise)
    view.openEditor(site)
    const first = view.uploadIcon(event(file()))
    const other = { ...site, id: 'manual:other', projectId: 'manual:other' }
    view.openEditor(other)
    view.draft.description = 'B draft'
    const secondFile = file()
    const second = view.uploadIcon(event(secondFile))
    a.reject(new SiteIconUploadError(site.id, 'unknown')); await first
    expect(view.iconBusy).toBe(true)
    expect(view.pendingIcon).toBe(secondFile)
    expect(view.draft.description).toBe('B draft')
    expect(view.unknownWrite).toBeNull()
    b.resolve({ siteId: other.id, iconUrl: `/api/v1/sites/${other.id}/icon` }); await second
    expect(view.iconBusy).toBe(false)
    view.openEditor(site)
    expect(view.unknownWrite).toBeInstanceOf(SiteIconUploadError)
  })

  it('明确拒绝保留正常重选能力，不宣称文件格式无效', async () => {
    vi.spyOn(store, 'uploadIcon').mockRejectedValue(new SiteIconUploadError(site.id, 'rejected'))
    view.openEditor(site)
    await view.uploadIcon(event(file()))
    expect(view.unknownWrite).toBeNull()
    expect(view.unknownIcons.size).toBe(0)
    expect(view.iconBusy).toBe(false)
    expect(messages.error).toHaveBeenCalledWith('本次图标上传未完成，请核对站点和文件后重新选择。')
  })

  it('退出后的迟到未知不新增页面记录或消息', async () => {
    const pending = deferred<{ siteId: string; iconUrl: string }>()
    vi.spyOn(store, 'uploadIcon').mockReturnValue(pending.promise)
    view.openEditor(site)
    const operation = view.uploadIcon(event(file()))
    invalidateSession()
    pending.reject(new SiteIconUploadError(site.id, 'unknown')); await operation
    expect(view.unknownIcons.size).toBe(0)
    expect(view.unknownWrite).toBeNull()
    expect(messages.error).not.toHaveBeenCalled()
  })

  it('上传在途关闭重开同站点仍单飞，迟到未知保留重开草稿', async () => {
    const pending = deferred<{ siteId: string; iconUrl: string }>()
    const upload = vi.spyOn(store, 'uploadIcon').mockReturnValueOnce(pending.promise)
    view.openEditor(site)
    const operation = view.uploadIcon(event(file()))
    view.editOpen = false
    view.openEditor(site)
    view.draft.description = '重开后的新草稿'
    expect(view.iconBusy).toBe(true)
    await view.uploadIcon(event(file()))
    expect(upload).toHaveBeenCalledTimes(1)
    pending.reject(new SiteIconUploadError(site.id, 'unknown'))
    await operation
    expect(view.iconBusy).toBe(false)
    expect(view.unknownWrite).toBeInstanceOf(SiteIconUploadError)
    expect(view.draft.description).toBe('重开后的新草稿')
    expect(messages.success).not.toHaveBeenCalled()
    expect(URL.revokeObjectURL).toHaveBeenCalledTimes(1)
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:fixture-icon')
  })
  it('抽屉关闭后到达的未知结果仍被记录，重开恢复保护', async () => {
    const pending = deferred<{ siteId: string; iconUrl: string }>()
    vi.spyOn(store, 'uploadIcon').mockReturnValueOnce(pending.promise)
    view.openEditor(site)
    const operation = view.uploadIcon(event(file()))
    view.editOpen = false
    pending.reject(new SiteIconUploadError(site.id, 'unknown'))
    await operation
    expect(view.unknownIcons.has(site.id)).toBe(true)
    expect(view.unknownWrite).toBeNull()
    view.openEditor(site)
    expect(view.unknownWrite).toBeInstanceOf(SiteIconUploadError)
    expect(URL.revokeObjectURL).toHaveBeenCalledTimes(1)
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:fixture-icon')
  })
  it.each(['确认成功', '明确拒绝'] as const)('%s 释放预览对象地址，允许用户再次选择文件', async (outcome) => {
    const upload = vi.spyOn(store, 'uploadIcon')
    if (outcome === '确认成功') upload.mockResolvedValue({ siteId: site.id, iconUrl: `/api/v1/sites/${site.id}/icon` })
    else upload.mockRejectedValue(new SiteIconUploadError(site.id, 'rejected'))
    view.openEditor(site)
    await view.uploadIcon(event(file()))
    expect(view.pendingIcon).toBeNull()
    expect(view.pendingIconPreview).toBe('')
    expect(view.iconBusy).toBe(false)
    expect(URL.revokeObjectURL).toHaveBeenCalledTimes(1)
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:fixture-icon')
    await view.uploadIcon(event(file()))
    expect(upload).toHaveBeenCalledTimes(2)
    expect(URL.revokeObjectURL).toHaveBeenCalledTimes(2)
  })
  it('卸载释放预览对象地址，迟到未知不能新增记录或消息', async () => {
    const pending = deferred<{ siteId: string; iconUrl: string }>()
    vi.spyOn(store, 'uploadIcon').mockReturnValueOnce(pending.promise)
    view.openEditor(site)
    const operation = view.uploadIcon(event(file()))
    app.unmount(); unmounted = true
    expect(URL.revokeObjectURL).toHaveBeenCalledTimes(1)
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:fixture-icon')
    pending.reject(new SiteIconUploadError(site.id, 'unknown'))
    await operation
    expect(view.unknownIcons.size).toBe(0)
    expect(messages.error).not.toHaveBeenCalled()
    expect(messages.success).not.toHaveBeenCalled()
  })
})


describe('站点状态筛选语义', () => {
  it('单选筛选保留标签计数与列表行为，切换不清除搜索和分类', async () => {
    store.sites = [site, { ...site, id: 'manual:stopped', name: 'Stopped', state: 'stopped' }, { ...site, id: 'manual:hidden', name: 'Hidden', hidden: true }]
    store.ignoredSites = [{ ...profile, projectId: 'ignored', name: 'Ignored' }]
    await nextTick()
    const group = host.querySelector('[role="radiogroup"][aria-label="站点状态筛选"]')!
    expect(group).not.toBeNull()
    expect(group.querySelector('[role="tab"], [aria-selected], [aria-controls]')).toBeNull()
    const buttons = [...group.querySelectorAll<HTMLButtonElement>('[role="radio"]')]
    expect(buttons.map((button) => button.textContent)).toEqual(['运行中1', '全部站点2', '已隐藏1', '已忽略1'])
    const names = () => [...host.querySelectorAll('.site-identity strong')].map((node) => node.textContent)
    expect(names()).toEqual(['Demo'])
    buttons[1]!.click(); await nextTick()
    expect(names()).toEqual(['Demo', 'Stopped'])
    buttons[2]!.click(); await nextTick()
    expect(names()).toEqual(['Hidden'])
    buttons[3]!.click(); await nextTick()
    expect(names()).toEqual(['Ignored'])
    expect(buttons[3]!.getAttribute('aria-checked')).toBe('true')
    view.query = 'Demo'; view.selectedCategory = 'web'
    buttons[3]!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Home', bubbles: true, cancelable: true }))
    await nextTick()
    expect(document.activeElement).toBe(buttons[0])
    expect(view.siteFilter).toBe('running')
    expect(view.query).toBe('Demo')
    expect(view.selectedCategory).toBe('web')
    expect(names()).toEqual(['Demo'])
    buttons[1]!.click(); await nextTick()
    expect(names()).toEqual(['Demo'])
    view.selectedCategory = 'other'; await nextTick()
    expect(names()).toEqual([])
    expect(host.textContent).toContain('没有匹配的站点')
  })
})
