import { computed, ref } from 'vue'
import { requestPreferences, updatePreferences, type UserPreferences } from '@/api/control'
import { PreferencesWriteUnknownError } from '@/api/preferenceResponse'
import { captureSession, isCurrentSession } from '@/session/sessionLifecycle'

export const clonePreferences = (value: UserPreferences): UserPreferences => ({ ...value, navigationOrder: [...value.navigationOrder] })

// 每个 store 只有一个串行队列；页面仅持有编辑权，普通导航不丢失已编辑的快照。
export function createPreferencesState(defaults: UserPreferences, apply: (value: UserPreferences) => void, accept: (value: UserPreferences) => void) {
  const preferences = ref(clonePreferences(defaults))
  const preferenceDraft = ref(clonePreferences(defaults))
  const preferencesLoaded = ref(false)
  const preferencesLoadState = ref<'idle' | 'loading' | 'loaded' | 'error'>('idle')
  const preferencesSaveState = ref<'idle' | 'saving' | 'saved' | 'error' | 'unknown' | 'review'>('idle')
  const preferencesDirty = computed(() => JSON.stringify(preferenceDraft.value) !== JSON.stringify(preferences.value))
  let owner = captureSession()
  let epoch = 0
  let editor: symbol | null = null
  let timer: ReturnType<typeof setTimeout> | null = null
  let loading: Promise<void> | null = null
  let saving = false
  let pending = false
  let revision = 0
  let writeGeneration = 0
  let paused = false
  const current = () => !owner.signal.aborted && isCurrentSession(owner.generation)
  const cancelTimer = () => { if (timer !== null) clearTimeout(timer); timer = null }

  function reset() {
    epoch += 1
    cancelTimer()
    editor = null
    loading = null
    saving = pending = paused = false
    revision = writeGeneration = 0
    preferencesLoaded.value = false
    preferencesLoadState.value = 'idle'
    preferencesSaveState.value = 'idle'
    preferences.value = clonePreferences(defaults)
    preferenceDraft.value = clonePreferences(defaults)
    apply(preferences.value)
  }
  function bindSession() {
    if (current()) return
    owner.signal.removeEventListener('abort', reset)
    reset()
    owner = captureSession()
    owner.signal.addEventListener('abort', reset, { once: true })
  }
  owner.signal.addEventListener('abort', reset, { once: true })

  function loadPreferences(): Promise<void> {
    bindSession()
    if (loading) return loading
    const session = owner
    const generation = epoch
    const writes = writeGeneration
    const readingDuringWrite = saving
    const valid = () => generation === epoch && current() && session === owner
    const ownsRead = () => writes === writeGeneration && !saving && !readingDuringWrite
    preferencesLoadState.value = 'loading'
    const request = (async () => {
      try {
        const loaded = await requestPreferences(session.signal)
        if (!valid()) return
        // 写入开始前发出的 GET 没有更新持久化基线的权利。
        if (!ownsRead()) {
          preferencesLoadState.value = preferencesLoaded.value ? 'loaded' : 'idle'
          return
        }
        // 未确认写入后，旧基线的相等关系不能证明没有编辑：用户可能明确回改到旧值。
        const keepDraft = preferencesLoaded.value && (paused || pending || preferencesDirty.value)
        preferences.value = clonePreferences(loaded)
        preferencesLoaded.value = true
        preferencesLoadState.value = 'loaded'
        if (!keepDraft) preferenceDraft.value = clonePreferences(loaded)
        accept(loaded)
        apply(editor ? preferenceDraft.value : loaded)
        if (paused) {
          preferencesSaveState.value = preferencesDirty.value ? 'review' : 'saved'
          if (!preferencesDirty.value) { paused = false; pending = false }
        }
      } catch (error) {
        if (!valid()) return
        if (!ownsRead()) {
          preferencesLoadState.value = preferencesLoaded.value ? 'loaded' : 'idle'
          return
        }
        preferencesLoadState.value = 'error'
        throw error
      }
    })().finally(() => { if (valid() && loading === request) loading = null })
    loading = request
    return request
  }

  function retryPreferencesLoad(token: symbol): Promise<void> {
    // 页面读取同样固定编辑归属；只有全局初始化可以绑定替换后的会话。
    if (!current() || editor !== token) return Promise.resolve()
    return loadPreferences()
  }

  function openPreferencesEditor() {
    bindSession()
    const token = Symbol('preferences-editor')
    editor = token
    if (preferencesLoaded.value) apply(preferenceDraft.value)
    return token
  }
  function editPreferences(token: symbol, value: UserPreferences) {
    if (!current() || editor !== token || !preferencesLoaded.value) return
    preferenceDraft.value = clonePreferences(value)
    revision += 1
    pending = true
    apply(preferenceDraft.value)
    if (paused) return
    preferencesSaveState.value = saving ? 'saving' : 'idle'
    cancelTimer()
    timer = setTimeout(() => { timer = null; void flushPreferences() }, 400)
  }
  async function flushPreferences() {
    cancelTimer()
    if (!current() || !preferencesLoaded.value || saving || !pending || paused) return
    if (!preferencesDirty.value) { pending = false; preferencesSaveState.value = 'saved'; return }
    const session = owner
    const generation = epoch
    const savedRevision = revision
    const snapshot = clonePreferences(preferenceDraft.value)
    const valid = () => generation === epoch && current() && session === owner
    pending = false
    saving = true
    writeGeneration += 1
    preferencesSaveState.value = 'saving'
    try {
      const saved = await updatePreferences(snapshot, session.signal)
      if (!valid()) return
      preferences.value = clonePreferences(saved)
      accept(saved)
      if (revision === savedRevision) preferenceDraft.value = clonePreferences(saved)
      apply(editor ? preferenceDraft.value : saved)
      preferencesSaveState.value = preferencesDirty.value ? 'idle' : 'saved'
    } catch (error) {
      if (!valid()) return
      if (error instanceof PreferencesWriteUnknownError) {
        paused = true
        preferencesSaveState.value = 'unknown'
      } else {
        preferencesSaveState.value = 'error'
      }
    } finally {
      if (valid()) {
        saving = false
        // 仅新意图可继续；失败快照不能因 finally 或路由卸载自动重放。
        if (pending && revision > savedRevision && !paused) void flushPreferences()
      }
    }
  }
  function retryPreferences(token: symbol) {
    if (!current() || editor !== token || preferencesSaveState.value === 'unknown' || preferencesLoadState.value === 'loading') return
    paused = false
    pending = true
    void flushPreferences()
  }
  function closePreferencesEditor(token: symbol) {
    if (!current() || editor !== token) return
    editor = null
    apply(preferences.value)
    void flushPreferences()
  }
  function dispose() {
    owner.signal.removeEventListener('abort', reset)
    reset()
  }
  return { preferences, preferenceDraft, preferencesLoaded, preferencesLoadState, preferencesSaveState, preferencesDirty,
    loadPreferences, retryPreferencesLoad, openPreferencesEditor, editPreferences, closePreferencesEditor, retryPreferences, reset, dispose }
}
