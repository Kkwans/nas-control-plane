<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { CheckCircle2, CloudUpload, FileCode2, History, LoaderCircle, RefreshCw, RotateCcw, Save, X } from '@lucide/vue'
import { ElDrawer, ElMessage, ElMessageBox } from 'element-plus'

import { requestComposeDraft, requestComposeRevisions, saveComposeDraft, type ComposeRevision } from '@/api/control'
import { deployComposeConfig, followJob, NcpApiError, readComposeConfig, validateComposeConfig, type ComposeFileSnapshot, type DockerProject } from '@/api/system'
import PlainCodeEditor from '@/components/PlainCodeEditor.vue'

const props = defineProps<{ modelValue: boolean; project: DockerProject | null }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
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
const activeFile = computed(() => files.value.find((file) => file.path === activePath.value) ?? null)
const activeRevisions = computed(() => revisions.value.filter((revision) => revision.configPath === activePath.value))
const activeContent = computed({
  get: () => activeFile.value?.content ?? '',
  set: (content: string) => {
    if (activeFile.value) activeFile.value.content = content
    validation.value = null
  },
})
const dirty = computed(() => Boolean(activeFile.value && savedContent.value[activeFile.value.path] !== activeFile.value.content))
const changedLines = computed(() => {
  const file = activeFile.value
  if (!file) return 0
  const before = (baseContent.value[file.path] ?? '').split('\n')
  const after = file.content.split('\n')
  let changes = 0
  for (let index = 0; index < Math.max(before.length, after.length); index += 1) {
    if (before[index] !== after[index]) changes += 1
  }
  return changes
})

async function load() {
  if (!props.project || props.project.kind !== 'compose' || !props.project.configFiles.length) return
  loading.value = true
  error.value = null
  try {
    const result = await readComposeConfig(props.project)
    files.value = result.files.map((file) => ({ ...file }))
    baseContent.value = Object.fromEntries(result.files.map((file) => [file.path, file.content]))
    savedContent.value = { ...baseContent.value }
    draftUpdatedAt.value = {}
    for (const file of files.value) {
      try {
        const draft = await requestComposeDraft(props.project.id, file.path)
        file.content = draft.content
        savedContent.value[file.path] = draft.content
        draftUpdatedAt.value[file.path] = draft.updatedAt
      } catch (caught) {
        if (!(caught instanceof NcpApiError) || caught.code !== 'COMPOSE_DRAFT_NOT_FOUND') throw caught
      }
    }
    activePath.value = result.files[0]?.path ?? ''
    revisions.value = await requestComposeRevisions(props.project.id)
  } catch (caught) {
    error.value = caught instanceof NcpApiError ? caught.message : 'Compose 配置读取失败。'
  } finally {
    loading.value = false
  }
}

async function saveDraft() {
  const file = activeFile.value
  if (!file || !props.project || saving.value) return
  saving.value = true
  error.value = null
  try {
    const draft = await saveComposeDraft({ projectId: props.project.id, configPath: file.path, content: file.content })
    savedContent.value[file.path] = file.content
    draftUpdatedAt.value[file.path] = draft.updatedAt
    ElMessage.success('Compose 草稿已保存到 NCP')
  } catch (caught) {
    error.value = caught instanceof NcpApiError ? caught.message : 'Compose 草稿保存失败。'
  } finally {
    saving.value = false
  }
}

async function validate() {
  const file = activeFile.value
  if (!file || validating.value) return false
  validating.value = true
  error.value = null
  try {
    const result = await validateComposeConfig(file.path, file.content)
    validation.value = { services: result.services }
    ElMessage.success(`配置有效，识别到 ${result.services.length} 个服务`)
    return true
  } catch (caught) {
    error.value = caught instanceof NcpApiError ? caught.message : 'Compose 配置校验失败。'
    return false
  } finally {
    validating.value = false
  }
}

async function deploy() {
  const file = activeFile.value
  if (!file || !props.project || deploying.value || !(await validate())) return
  try {
    await ElMessageBox.confirm(
      `将发布 ${file.name}，预计变更 ${changedLines.value} 行。NCP 会先备份原文件；部署失败时自动恢复并重新应用旧配置。`,
      '确认部署 Compose 项目',
      { confirmButtonText: '备份并部署', cancelButtonText: '取消', type: 'warning' },
    )
  } catch { return }
  deploying.value = true
  deployProgress.value = 0
  try {
    const job = await deployComposeConfig({
      projectId: props.project.id, workingDirectory: props.project.workingDirectory,
      configFiles: props.project.configFiles, targetPath: file.path, content: file.content,
    })
    const completed = await followJob(job.id, (snapshot) => { deployProgress.value = snapshot.progress })
    if (completed.status === 'failed') throw new NcpApiError('COMPOSE_DEPLOY_FAILED', completed.error || completed.message)
    ElMessage.success(completed.message)
    await load()
  } catch (caught) {
    error.value = caught instanceof NcpApiError ? caught.message : 'Compose 部署失败。'
  } finally {
    deploying.value = false
    deployProgress.value = 0
  }
}

async function rollback(revision: ComposeRevision) {
  if (!props.project || deploying.value) return
  try {
    await ElMessageBox.confirm(
      `将把 ${activeFile.value?.name ?? '当前配置'} 回滚到 ${new Date(revision.createdAt).toLocaleString('zh-CN')} 的版本。NCP 仍会先备份现有文件，并在应用失败时自动恢复。`,
      '确认回滚 Compose 配置',
      { confirmButtonText: '备份并回滚', cancelButtonText: '取消', type: 'warning' },
    )
  } catch { return }
  deploying.value = true
  deployProgress.value = 0
  error.value = null
  try {
    const job = await deployComposeConfig({
      projectId: props.project.id,
      workingDirectory: props.project.workingDirectory,
      configFiles: props.project.configFiles,
      targetPath: revision.configPath,
      content: revision.content,
    })
    const completed = await followJob(job.id, (snapshot) => { deployProgress.value = snapshot.progress })
    if (completed.status === 'failed') throw new NcpApiError('COMPOSE_ROLLBACK_FAILED', completed.error || completed.message)
    historyOpen.value = false
    ElMessage.success('Compose 配置已回滚并重新部署')
    await load()
  } catch (caught) {
    error.value = caught instanceof NcpApiError ? caught.message : 'Compose 回滚失败。'
  } finally {
    deploying.value = false
    deployProgress.value = 0
  }
}

function loadRevision(revision: ComposeRevision) {
  const file = files.value.find((candidate) => candidate.path === revision.configPath)
  if (!file) return
  activePath.value = revision.configPath
  file.content = revision.content
  historyOpen.value = false
  validation.value = null
  ElMessage.info('历史版本已载入编辑器，尚未部署')
}

function draftTimeLabel(path: string) {
  const value = draftUpdatedAt.value[path]
  return value ? new Date(value).toLocaleString('zh-CN') : ''
}

watch(() => props.modelValue, (open) => { if (open) void load() })
</script>

<template>
  <ElDrawer :model-value="modelValue" size="min(1040px, 100%)" class="compose-editor-drawer" @update:model-value="emit('update:modelValue', $event)">
    <template #header><div class="compose-title"><span><FileCode2 :size="21" /></span><div><strong>Compose 配置</strong><small>{{ project?.name }} · 草稿、校验、备份与部署</small></div></div></template>
    <div class="compose-workspace">
      <header class="compose-toolbar">
        <nav><button v-for="file in files" :key="file.path" type="button" :class="{ active: activePath === file.path }" @click="activePath = file.path">{{ file.name }}</button></nav>
        <div>
          <button class="secondary-button" type="button" :disabled="loading" @click="load"><RefreshCw :class="{ spin: loading }" :size="16" />重新读取</button>
          <button class="secondary-button" type="button" :disabled="!activeFile || saving || !dirty" @click="saveDraft"><LoaderCircle v-if="saving" class="spin" :size="16" /><Save v-else :size="16" />保存草稿</button>
          <button class="secondary-button" type="button" :disabled="!activeRevisions.length" @click="historyOpen = true"><History :size="16" />版本</button>
          <button class="primary-button" type="button" :disabled="!activeFile || validating" @click="validate"><LoaderCircle v-if="validating" class="spin" :size="16" /><CheckCircle2 v-else :size="16" />校验</button>
          <button class="deploy-button" type="button" :disabled="!activeFile || deploying || changedLines === 0" @click="deploy"><LoaderCircle v-if="deploying" class="spin" :size="16" /><CloudUpload v-else :size="16" />{{ deploying ? `部署 ${deployProgress}%` : '预览并部署' }}</button>
        </div>
      </header>
      <div class="compose-status" aria-live="polite">
        <div v-if="error" class="compose-error">{{ error }}</div>
        <div v-else-if="validation" class="validation-result"><CheckCircle2 :size="17" /><span>配置有效</span><small>{{ validation.services.join('、') || '未声明服务' }}</small></div>
        <span v-else>编辑后可校验配置；校验结果会显示在这里，不会改变编辑区高度。</span>
      </div>
      <div v-if="loading" class="editor-skeleton"><i v-for="line in 14" :key="line" class="ncp-skeleton" :style="{ width: `${35 + (line * 17) % 58}%` }"></i></div>
      <PlainCodeEditor v-else-if="activeFile" v-model="activeContent" />
      <div v-else class="compose-empty">当前项目未定位到可读取的 Compose 配置文件。</div>
      <aside v-if="historyOpen" class="revision-panel">
        <header>
          <div><History :size="18" /><span><strong>部署版本</strong><small>{{ activeFile?.name }}</small></span></div>
          <button type="button" title="关闭版本记录" @click="historyOpen = false"><X :size="18" /></button>
        </header>
        <div class="revision-list">
          <article v-for="(revision, index) in activeRevisions" :key="revision.id">
            <div class="revision-meta">
              <strong>{{ index === 0 ? '当前部署版本' : `历史版本 ${activeRevisions.length - index}` }}</strong>
              <time>{{ new Date(revision.createdAt).toLocaleString('zh-CN') }}</time>
            </div>
            <code>{{ revision.contentHash.slice(0, 12) }}</code>
            <pre>{{ revision.content.split('\n').slice(0, 6).join('\n') }}</pre>
            <footer>
              <button type="button" @click="loadRevision(revision)">载入编辑器</button>
              <button class="rollback-button" type="button" :disabled="deploying || index === 0" @click="rollback(revision)">
                <RotateCcw :size="14" />{{ index === 0 ? '当前版本' : '回滚到此版本' }}
              </button>
            </footer>
          </article>
        </div>
      </aside>
      <footer>
        <span>{{ dirty ? '当前修改尚未保存' : draftUpdatedAt[activePath] ? `草稿已保存 · ${draftTimeLabel(activePath)}` : '当前显示项目原始配置' }} · 变更 {{ changedLines }} 行</span>
        <span class="revision-count"><History :size="14" />{{ revisions.length }} 个部署版本</span>
        <code>{{ activeFile?.path }}</code>
      </footer>
    </div>
  </ElDrawer>
</template>

<style scoped>
.compose-title{display:flex;align-items:center;gap:10px}.compose-title>span{display:grid;width:40px;height:40px;place-items:center;border-radius:11px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.compose-title>div{display:grid}.compose-title strong{font-size:.96rem}.compose-title small{color:var(--ncp-text-subtle);font-size:.74rem}.compose-workspace{position:relative;display:grid;grid-template-rows:auto 52px minmax(0,1fr) 38px;height:100%;min-height:0;overflow:hidden}.compose-toolbar{display:flex;align-items:center;justify-content:space-between;gap:12px;padding-bottom:12px;border-bottom:1px solid var(--ncp-line)}.compose-toolbar nav,.compose-toolbar>div{display:flex;align-items:center;gap:7px}.compose-toolbar nav{overflow-x:auto}.compose-toolbar nav button{min-height:38px;padding:0 12px;border-radius:8px;color:var(--ncp-text-muted);font-family:var(--ncp-font-mono);font-size:.73rem}.compose-toolbar nav button.active{background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.secondary-button,.primary-button,.deploy-button{display:flex;min-height:40px;align-items:center;gap:6px;padding:0 13px;border-radius:9px;font-size:.8rem;font-weight:720}.secondary-button{border:1px solid var(--ncp-line);background:#fff;color:var(--ncp-text-muted)}.primary-button{background:var(--ncp-primary);color:#fff}.deploy-button{background:#15365f;color:#fff}.secondary-button:disabled,.primary-button:disabled,.deploy-button:disabled{opacity:.45}.compose-status{display:flex;min-height:0;align-items:center;color:var(--ncp-text-subtle);font-size:.76rem}.compose-error,.validation-result{display:flex;width:100%;min-height:40px;align-items:center;gap:7px;padding:7px 11px;border-radius:9px;font-size:.78rem}.compose-error{background:var(--ncp-danger-soft);color:var(--ncp-danger-strong)}.validation-result{background:var(--ncp-success-soft);color:var(--ncp-success)}.validation-result small{margin-left:auto;color:var(--ncp-text-muted)}.editor-skeleton{display:grid;min-height:0;align-content:start;gap:13px;overflow:hidden;padding:22px}.editor-skeleton i{height:11px}.compose-empty{display:grid;min-height:0;place-items:center;color:var(--ncp-text-subtle);font-size:.82rem}.revision-panel{position:absolute;z-index:5;top:53px;right:0;bottom:37px;width:min(420px,90%);border-left:1px solid var(--ncp-line);background:#f8fafc;box-shadow:-18px 0 48px rgb(26 52 85 / 12%)}.revision-panel>header{display:flex;height:58px;align-items:center;justify-content:space-between;padding:0 16px;border-bottom:1px solid var(--ncp-line);background:#fff}.revision-panel>header>div{display:flex;align-items:center;gap:9px;color:var(--ncp-primary-strong)}.revision-panel>header span{display:grid}.revision-panel>header strong{color:var(--ncp-text);font-size:.86rem}.revision-panel>header small{color:var(--ncp-text-subtle);font-size:.68rem}.revision-panel>header button{display:grid;width:34px;height:34px;place-items:center;border-radius:8px;color:var(--ncp-text-muted)}.revision-list{display:grid;max-height:calc(100% - 58px);gap:10px;overflow-y:auto;padding:12px}.revision-list article{display:grid;gap:9px;padding:13px;border:1px solid var(--ncp-line);border-radius:11px;background:#fff}.revision-meta{display:flex;align-items:center;justify-content:space-between;gap:10px}.revision-meta strong{font-size:.78rem}.revision-meta time,.revision-list article>code{color:var(--ncp-text-subtle);font-family:var(--ncp-font-mono);font-size:.65rem}.revision-list pre{max-height:112px;overflow:hidden;margin:0;padding:10px;border:1px solid #e7ebf1;border-radius:8px;background:#fbfcfe;color:#435269;font-family:var(--ncp-font-mono);font-size:.66rem;line-height:1.55}.revision-list footer{display:flex;justify-content:flex-end;gap:7px}.revision-list footer button{display:flex;min-height:34px;align-items:center;gap:5px;padding:0 10px;border:1px solid var(--ncp-line);border-radius:8px;color:var(--ncp-text-muted);font-size:.72rem;font-weight:700}.revision-list footer .rollback-button{border-color:#c8d8f4;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.revision-list footer button:disabled{opacity:.45}.compose-workspace>footer{display:flex;min-height:0;align-items:center;justify-content:space-between;gap:14px;border-top:1px solid var(--ncp-line);color:var(--ncp-text-subtle);font-size:.7rem}.revision-count{display:flex;align-items:center;gap:4px;white-space:nowrap}.compose-workspace>footer code{max-width:36%;overflow:hidden;font-family:var(--ncp-font-mono);text-overflow:ellipsis;white-space:nowrap}.spin{animation:spin .8s linear infinite}:global(.compose-editor-drawer .el-drawer__body){height:calc(100% - 74px);overflow:hidden;padding:0 20px 14px}:global(.compose-editor-drawer .el-drawer__header){height:74px;flex:0 0 74px;margin:0;padding:12px 20px}@keyframes spin{to{transform:rotate(360deg)}}@media(max-width:900px){.compose-toolbar{align-items:stretch;flex-direction:column}.compose-toolbar>div{justify-content:flex-end;flex-wrap:wrap}.revision-panel{top:102px}}@media(max-width:700px){.compose-workspace>footer{align-items:flex-start;flex-direction:column}.compose-workspace>footer code{max-width:100%}.revision-panel{top:148px;width:100%}}
</style>
