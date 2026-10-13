import { computed, onScopeDispose, ref, watch, type Ref } from 'vue'

import { requestComposeDraft, requestComposeRevisions, saveComposeDraft, type ComposeRevision } from '@/api/control'
import { deployComposeConfig, NcpApiError, readComposeConfig, validateComposeConfig, type ComposeDeployInput, type ComposeFileSnapshot, type DockerProject } from '@/api/system'
import { subscribeJob } from '@/api/docker'
import { createRequestSequenceGate } from '@/domain/requestSequence'
import { captureSession, isCurrentSession } from '@/session/sessionLifecycle'

interface Feedback {
  confirm(message: string, title: string, rollback: boolean): Promise<unknown>
  success(message: string): void
  info(message: string): void
}

/** 草稿、校验和确认各自捕获提交版本；关闭仅结束观察，不取消后端任务。 */
export function useComposeWorkspace(open: Ref<boolean>, project: Ref<DockerProject | null>, feedback: Feedback) {
  const loading = ref(false)
  const validating = ref(false)
  const saving = ref(false)
  const deploying = ref(false)
  const deployProgress = ref(0)
  const error = ref<string | null>(null)
  const files = ref<ComposeFileSnapshot[]>([])
  const activePath = ref('')
  const validation = ref<{ services: string[] } | null>(null)
  const savedContent = ref<Record<string, string>>({})
  const baseContent = ref<Record<string, string>>({})
  const draftUpdatedAt = ref<Record<string, string>>({})
  const revisions = ref<ComposeRevision[]>([])
  const historyOpen = ref(false)
  const workspaceGate = createRequestSequenceGate()
  const validationGate = createRequestSequenceGate()
  let closeObservation: (() => void) | undefined
  let removeSessionListener: (() => void) | undefined
  let editRevision = 0
  const projectKey = computed(() => JSON.stringify(project.value && [project.value.id, project.value.kind, project.value.workingDirectory, project.value.configFiles]))
  const activeFile = computed(() => files.value.find((file) => file.path === activePath.value) ?? null)
  const activeRevisions = computed(() => revisions.value.filter((revision) => revision.configPath === activePath.value))
  function invalidateValidation() {
    editRevision += 1
    validationGate.invalidate()
    validating.value = false
    validation.value = null
  }
  const activeContent = computed({
    get: () => activeFile.value?.content ?? '',
    set: (content: string) => {
      if (activeFile.value) activeFile.value.content = content
      invalidateValidation()
    },
  })
  watch(activePath, invalidateValidation, { flush: 'sync' })
  const dirty = computed(() => Boolean(activeFile.value && savedContent.value[activeFile.value.path] !== activeFile.value.content))
  const dirtyCount = computed(() => files.value.filter((file) => savedContent.value[file.path] !== file.content).length)
  function countChanges(path: string, content: string) {
    const before = (baseContent.value[path] ?? '').split('\n')
    const after = content.split('\n')
    return Array.from({ length: Math.max(before.length, after.length) }, (_, i) => before[i] !== after[i]).filter(Boolean).length
  }
  const changedLines = computed(() => activeFile.value ? countChanges(activeFile.value.path, activeFile.value.content) : 0)

  function reset() {
    workspaceGate.invalidate()
    closeObservation?.()
    closeObservation = undefined
    removeSessionListener?.()
    removeSessionListener = undefined
    invalidateValidation()
    loading.value = saving.value = deploying.value = false
    deployProgress.value = 0
    error.value = null
    files.value = []
    activePath.value = ''
    savedContent.value = {}
    baseContent.value = {}
    draftUpdatedAt.value = {}
    revisions.value = []
    historyOpen.value = false
  }
  function capture() {
    const sequence = workspaceGate.begin()
    const key = projectKey.value
    const session = captureSession()
    return () => open.value && key === projectKey.value && workspaceGate.isLatest(sequence) && isCurrentSession(session.generation)
  }
  let current = () => false
  async function load() {
    reset()
    const target = project.value
    if (!open.value || !target || target.kind !== 'compose' || !target.configFiles.length) return
    const frozenProject = { ...target, configFiles: [...target.configFiles] }
    const owns = capture()
    current = owns
    const session = captureSession()
    session.signal.addEventListener('abort', reset, { once: true })
    removeSessionListener = () => session.signal.removeEventListener('abort', reset)
    loading.value = true
    try {
      const result = await readComposeConfig(frozenProject)
      if (!owns()) return
      const loaded = result.files.map((file) => ({ ...file }))
      const baseline = Object.fromEntries(loaded.map((file) => [file.path, file.content]))
      const timestamps: Record<string, string> = {}
      for (const file of loaded) {
        try {
          const draft = await requestComposeDraft(frozenProject.id, file.path)
          if (!owns()) return
          file.content = draft.content
          timestamps[file.path] = draft.updatedAt
        } catch (caught) {
          if (!owns()) return
          if (!(caught instanceof NcpApiError) || caught.code !== 'COMPOSE_DRAFT_NOT_FOUND') throw caught
        }
      }
      const history = await requestComposeRevisions(frozenProject.id)
      if (!owns()) return
      files.value = loaded
      baseContent.value = baseline
      savedContent.value = Object.fromEntries(loaded.map((file) => [file.path, file.content]))
      draftUpdatedAt.value = timestamps
      revisions.value = history
      activePath.value = loaded[0]?.path ?? ''
    } catch (caught) {
      if (owns()) error.value = caught instanceof NcpApiError ? caught.message : 'Compose 配置读取失败。'
    } finally {
      if (owns()) loading.value = false
    }
  }

  async function saveDraft() {
    const file = activeFile.value
    const target = project.value
    if (!current() || !file || !target || saving.value) return
    const owns = current
    const payload = { projectId: target.id, configPath: file.path, content: file.content }
    saving.value = true
    error.value = null
    try {
      const draft = await saveComposeDraft(payload)
      if (!owns()) return
      savedContent.value[payload.configPath] = payload.content
      draftUpdatedAt.value[payload.configPath] = draft.updatedAt
      feedback.success('Compose 草稿已保存到 NCP')
    } catch (caught) {
      if (owns()) error.value = caught instanceof NcpApiError ? caught.message : 'Compose 草稿保存失败。'
    } finally {
      if (owns()) saving.value = false
    }
  }

  async function validate() {
    const file = activeFile.value
    if (!current() || !file || validating.value) return false
    const owns = current
    const sequence = validationGate.begin()
    const path = file.path
    const content = file.content
    const revision = editRevision
    const valid = () => owns() && validationGate.isLatest(sequence) && activePath.value === path && editRevision === revision
    validating.value = true
    error.value = null
    try {
      const result = await validateComposeConfig(path, content)
      if (!valid()) return false
      if (!result.valid) throw new Error('invalid')
      validation.value = { services: result.services }
      feedback.success(`配置有效，识别到 ${result.services.length} 个服务`)
      return true
    } catch (caught) {
      if (valid()) error.value = caught instanceof NcpApiError ? caught.message : 'Compose 配置校验失败。'
      return false
    } finally {
      if (valid()) validating.value = false
    }
  }

  async function publish(revision?: ComposeRevision) {
    const target = project.value
    const file = activeFile.value
    if (!current() || !target || !file || deploying.value || loading.value) return
    if (revision && (!revisions.value.some((item) => item.id === revision.id) || revision.configPath !== file.path)) return
    const owns = current
    const payload: ComposeDeployInput = {
      projectId: target.id, workingDirectory: target.workingDirectory, configFiles: [...target.configFiles],
      targetPath: revision?.configPath ?? file.path, content: revision?.content ?? file.content,
    }
    const name = file.name
    const changes = countChanges(payload.targetPath, payload.content)
    const capturedRevision = editRevision
    deploying.value = true // 校验和确认阶段也持锁，避免重复确认/提交。
    deployProgress.value = 0
    error.value = null
    try {
      if (!revision && (!(await validate()) || !owns() || editRevision !== capturedRevision)) return
      try {
        await feedback.confirm(revision
          ? `将把 ${name} 回滚到 ${new Date(revision.createdAt).toLocaleString('zh-CN')} 的版本。NCP 仍会先备份现有文件，并在应用失败时自动恢复。`
          : `将发布 ${name}，预计变更 ${changes} 行。NCP 会先备份原文件；部署失败时自动恢复并重新应用旧配置。`,
        revision ? '确认回滚 Compose 配置' : '确认部署 Compose 项目', Boolean(revision))
      } catch { return }
      if (!owns()) return
      // 确认后的编辑不改变本次已预览的请求，也不被任务完成时覆盖。
      const job = await deployComposeConfig(payload)
      if (!owns()) return
      const observation = subscribeJob(job.id, (snapshot) => { if (owns()) deployProgress.value = snapshot.progress })
      closeObservation = observation.close
      const completed = await observation.done
      if (!owns()) return
      closeObservation = undefined
      if (completed.status !== 'completed') {
        throw new NcpApiError('COMPOSE_DEPLOY_FAILED', completed.error || completed.message || 'Compose 任务未完成。')
      }
      baseContent.value[payload.targetPath] = payload.content
      historyOpen.value = false
      feedback.success(completed.message || 'Compose 配置已部署')
      try {
        const history = await requestComposeRevisions(payload.projectId)
        if (owns()) revisions.value = history
      } catch {
        if (owns()) error.value = 'Compose 已部署，但版本记录刷新失败；可重新读取。'
      }
    } catch (caught) {
      if (owns()) error.value = caught instanceof NcpApiError ? caught.message : 'Compose 部署状态读取失败，请检查任务状态。'
    } finally {
      if (owns()) {
        deploying.value = false
        deployProgress.value = 0
      }
    }
  }
  function loadRevision(revision: ComposeRevision) {
    if (!current() || !revisions.value.some((item) => item.id === revision.id)) return
    const file = files.value.find((candidate) => candidate.path === revision.configPath)
    if (!file) return
    activePath.value = revision.configPath
    activeContent.value = revision.content
    historyOpen.value = false
    feedback.info('历史版本已载入编辑器，尚未部署')
  }
  function draftTimeLabel(path: string) {
    const value = draftUpdatedAt.value[path]
    return value ? new Date(value).toLocaleString('zh-CN') : ''
  }
  watch([open, projectKey], () => { void load() }, { immediate: true, flush: 'sync' })
  onScopeDispose(reset)
  return { loading, validating, saving, deploying, deployProgress, error, files, activePath, validation, draftUpdatedAt, revisions, historyOpen,
    activeFile, activeRevisions, activeContent, dirty, dirtyCount, changedLines, load, saveDraft, validate,
    deploy: () => publish(), rollback: (revision: ComposeRevision) => publish(revision), loadRevision, draftTimeLabel }
}
