// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick, type App } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { invalidateSession } from '@/session/sessionLifecycle'
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
vi.mock('@/api/control', () => ({ requestSites: vi.fn(), requestIgnoredSites: vi.fn(), createSite: vi.fn(), updateSite: vi.fn(), deleteSite: vi.fn(), restoreSite: vi.fn(), uploadSiteIcon: vi.fn(), deleteSiteIcon: vi.fn(), recordSiteVisit: vi.fn() }))

const profile: SiteProfileInput = { name: 'Demo', description: '', iconUrl: '', category: 'web', primaryPort: 8080, launchUrl: '', favorite: false, sortOrder: 0, hidden: false }
const site = { ...profile, id: 'manual:demo', projectId: 'manual:demo', ports: [8080], state: 'running', source: 'manual' } as Site
interface ViewState {
  draft: SiteProfileInput
  editOpen: boolean
  saving: boolean
  syncPending: boolean
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
afterEach(() => { if (!unmounted) app.unmount(); host.remove(); vi.restoreAllMocks() })

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
    expect(messages.warning).toHaveBeenCalled()
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
  expect(host.querySelector<HTMLInputElement>('input[type=file]')?.disabled).toBe(true)
  created.resolve({ ...profile, projectId: site.id }); await operation
  expect(view.editOpen).toBe(false)
})
