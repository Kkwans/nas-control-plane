<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { CheckCircle2, CloudUpload, FileCode2, History, LoaderCircle, RefreshCw, Save } from '@lucide/vue'
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
const activeFile = computed(() => files.value.find((file) => file.path === activePath.value) ?? null)
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
          <button class="primary-button" type="button" :disabled="!activeFile || validating" @click="validate"><LoaderCircle v-if="validating" class="spin" :size="16" /><CheckCircle2 v-else :size="16" />校验</button>
          <button class="deploy-button" type="button" :disabled="!activeFile || deploying || changedLines === 0" @click="deploy"><LoaderCircle v-if="deploying" class="spin" :size="16" /><CloudUpload v-else :size="16" />{{ deploying ? `部署 ${deployProgress}%` : '预览并部署' }}</button>
        </div>
      </header>
      <div v-if="error" class="compose-error">{{ error }}</div>
      <div v-if="validation" class="validation-result"><CheckCircle2 :size="17" /><span>配置有效</span><small>{{ validation.services.join('、') || '未声明服务' }}</small></div>
      <div v-if="loading" class="editor-skeleton"><i v-for="line in 14" :key="line" class="ncp-skeleton" :style="{ width: `${35 + (line * 17) % 58}%` }"></i></div>
      <PlainCodeEditor v-else-if="activeFile" v-model="activeContent" />
      <div v-else class="compose-empty">当前项目未定位到可读取的 Compose 配置文件。</div>
      <footer>
        <span>{{ dirty ? '当前修改尚未保存' : draftUpdatedAt[activePath] ? `草稿已保存 · ${draftTimeLabel(activePath)}` : '当前显示项目原始配置' }} · 变更 {{ changedLines }} 行</span>
        <span class="revision-count"><History :size="14" />{{ revisions.length }} 个部署版本</span>
        <code>{{ activeFile?.path }}</code>
      </footer>
    </div>
  </ElDrawer>
</template>

<style scoped>
.compose-title{display:flex;align-items:center;gap:10px}.compose-title>span{display:grid;width:40px;height:40px;place-items:center;border-radius:11px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.compose-title>div{display:grid}.compose-title strong{font-size:.96rem}.compose-title small{color:var(--ncp-text-subtle);font-size:.74rem}.compose-workspace{display:grid;grid-template-rows:auto auto minmax(440px,1fr) auto;height:calc(100vh - 110px);min-height:0}.compose-toolbar{display:flex;align-items:center;justify-content:space-between;gap:12px;padding-bottom:12px;border-bottom:1px solid var(--ncp-line)}.compose-toolbar nav,.compose-toolbar>div{display:flex;align-items:center;gap:7px}.compose-toolbar nav{overflow-x:auto}.compose-toolbar nav button{min-height:38px;padding:0 12px;border-radius:8px;color:var(--ncp-text-muted);font-family:var(--ncp-font-mono);font-size:.73rem}.compose-toolbar nav button.active{background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.secondary-button,.primary-button,.deploy-button{display:flex;min-height:40px;align-items:center;gap:6px;padding:0 13px;border-radius:9px;font-size:.8rem;font-weight:720}.secondary-button{border:1px solid var(--ncp-line);background:#fff;color:var(--ncp-text-muted)}.primary-button{background:var(--ncp-primary);color:#fff}.deploy-button{background:#15365f;color:#fff}.secondary-button:disabled,.primary-button:disabled,.deploy-button:disabled{opacity:.45}.compose-error,.validation-result{display:flex;min-height:42px;align-items:center;gap:7px;margin:10px 0 0;padding:8px 11px;border-radius:9px;font-size:.78rem}.compose-error{background:var(--ncp-danger-soft);color:var(--ncp-danger-strong)}.validation-result{background:var(--ncp-success-soft);color:var(--ncp-success)}.validation-result small{margin-left:auto;color:var(--ncp-text-muted)}.editor-skeleton{display:grid;align-content:start;gap:13px;padding:22px}.editor-skeleton i{height:11px}.compose-empty{display:grid;place-items:center;color:var(--ncp-text-subtle);font-size:.82rem}.compose-workspace>footer{display:flex;align-items:center;justify-content:space-between;gap:14px;padding-top:10px;border-top:1px solid var(--ncp-line);color:var(--ncp-text-subtle);font-size:.7rem}.revision-count{display:flex;align-items:center;gap:4px;white-space:nowrap}.compose-workspace>footer code{max-width:36%;overflow:hidden;font-family:var(--ncp-font-mono);text-overflow:ellipsis;white-space:nowrap}.spin{animation:spin .8s linear infinite}@keyframes spin{to{transform:rotate(360deg)}}@media(max-width:900px){.compose-toolbar{align-items:stretch;flex-direction:column}.compose-toolbar>div{justify-content:flex-end;flex-wrap:wrap}}@media(max-width:700px){.compose-workspace>footer{align-items:flex-start;flex-direction:column}.compose-workspace>footer code{max-width:100%}}
</style>
