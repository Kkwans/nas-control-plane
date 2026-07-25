<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { CheckCircle2, FileCode2, LoaderCircle, RefreshCw, Save } from '@lucide/vue'
import { ElDrawer, ElMessage } from 'element-plus'

import { NcpApiError, readComposeConfig, validateComposeConfig, type ComposeFileSnapshot, type DockerProject } from '@/api/system'
import { requestComposeDraft, saveComposeDraft } from '@/api/control'
import PlainCodeEditor from '@/components/PlainCodeEditor.vue'

const props = defineProps<{ modelValue: boolean; project: DockerProject | null }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
const loading = ref(false)
const validating = ref(false)
const saving = ref(false)
const error = ref<string | null>(null)
const files = ref<ComposeFileSnapshot[]>([])
const activePath = ref('')
const validation = ref<{ services: string[] } | null>(null)
const savedContent = ref<Record<string, string>>({})
const draftUpdatedAt = ref<Record<string, string>>({})
const activeFile = computed(() => files.value.find((file) => file.path === activePath.value) ?? null)
const activeContent = computed({
  get: () => activeFile.value?.content ?? '',
  set: (content: string) => {
    const file = activeFile.value
    if (file) file.content = content
    validation.value = null
  },
})
const dirty = computed(() => Boolean(activeFile.value && savedContent.value[activeFile.value.path] !== activeFile.value.content))

async function load() {
  if (!props.project || props.project.kind !== 'compose' || !props.project.configFiles.length) return
  loading.value = true
  error.value = null
  try {
    const result = await readComposeConfig(props.project)
    files.value = result.files.map((file) => ({ ...file }))
    savedContent.value = Object.fromEntries(result.files.map((file) => [file.path, file.content]))
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
  if (!file || validating.value) return
  validating.value = true
  error.value = null
  try {
    const result = await validateComposeConfig(file.path, file.content)
    validation.value = { services: result.services }
    ElMessage.success(`配置有效，识别到 ${result.services.length} 个服务`)
  } catch (caught) {
    error.value = caught instanceof NcpApiError ? caught.message : 'Compose 配置校验失败。'
  } finally {
    validating.value = false
  }
}

function draftTimeLabel(path: string) {
  const value = draftUpdatedAt.value[path]
  return value ? new Date(value).toLocaleString('zh-CN') : ''
}

watch(() => props.modelValue, (open) => { if (open) void load() })
</script>

<template>
  <ElDrawer :model-value="modelValue" size="min(980px, 100%)" class="compose-editor-drawer" @update:model-value="emit('update:modelValue', $event)">
    <template #header>
      <div class="compose-title"><span><FileCode2 :size="21" /></span><div><strong>Compose 配置</strong><small>{{ project?.name }} · 真实项目文件只读工作台</small></div></div>
    </template>
    <div class="compose-workspace">
      <header class="compose-toolbar">
        <nav>
          <button v-for="file in files" :key="file.path" type="button" :class="{ active: activePath === file.path }" @click="activePath = file.path">{{ file.name }}</button>
        </nav>
        <div>
          <button class="secondary-button" type="button" :disabled="loading" @click="load"><RefreshCw :class="{ spin: loading }" :size="16" />重新读取</button>
          <button class="secondary-button" type="button" :disabled="!activeFile || saving || !dirty" @click="saveDraft"><LoaderCircle v-if="saving" class="spin" :size="16" /><Save v-else :size="16" />保存草稿</button>
          <button class="primary-button" type="button" :disabled="!activeFile || validating" @click="validate"><LoaderCircle v-if="validating" class="spin" :size="16" /><CheckCircle2 v-else :size="16" />校验配置</button>
        </div>
      </header>
      <div v-if="error" class="compose-error">{{ error }}</div>
      <div v-if="validation" class="validation-result"><CheckCircle2 :size="17" /><span>配置有效</span><small>{{ validation.services.join('、') || '未声明服务' }}</small></div>
      <div v-if="loading" class="editor-skeleton"><i v-for="line in 14" :key="line" class="ncp-skeleton" :style="{ width: `${35 + (line * 17) % 58}%` }"></i></div>
      <PlainCodeEditor v-else-if="activeFile" v-model="activeContent" />
      <div v-else class="compose-empty">当前项目未定位到可读取的 Compose 配置文件。</div>
      <footer><span>{{ dirty ? '当前修改尚未保存' : draftUpdatedAt[activePath] ? `草稿已保存 · ${draftTimeLabel(activePath)}` : '当前显示项目原始配置' }}</span><code>{{ activeFile?.path }}</code></footer>
    </div>
  </ElDrawer>
</template>

<style scoped>
.compose-title{display:flex;align-items:center;gap:10px}.compose-title>span{display:grid;width:40px;height:40px;place-items:center;border-radius:11px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.compose-title>div{display:grid}.compose-title strong{font-size:.96rem}.compose-title small{color:var(--ncp-text-subtle);font-size:.74rem}.compose-workspace{display:grid;grid-template-rows:auto auto minmax(440px,1fr) auto;height:calc(100vh - 110px);min-height:0}.compose-toolbar{display:flex;align-items:center;justify-content:space-between;gap:12px;padding-bottom:12px;border-bottom:1px solid var(--ncp-line)}.compose-toolbar nav,.compose-toolbar>div{display:flex;align-items:center;gap:7px}.compose-toolbar nav{overflow-x:auto}.compose-toolbar nav button{min-height:38px;padding:0 12px;border-radius:8px;color:var(--ncp-text-muted);font-family:var(--ncp-font-mono);font-size:.73rem}.compose-toolbar nav button.active{background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.secondary-button,.primary-button{display:flex;min-height:40px;align-items:center;gap:6px;padding:0 13px;border-radius:9px;font-size:.8rem;font-weight:720}.secondary-button{border:1px solid var(--ncp-line);background:#fff;color:var(--ncp-text-muted)}.primary-button{background:var(--ncp-primary);color:#fff}.compose-error,.validation-result{display:flex;min-height:42px;align-items:center;gap:7px;margin:10px 0 0;padding:8px 11px;border-radius:9px;font-size:.78rem}.compose-error{background:var(--ncp-danger-soft);color:var(--ncp-danger-strong)}.validation-result{background:var(--ncp-success-soft);color:var(--ncp-success)}.validation-result small{margin-left:auto;color:var(--ncp-text-muted)}.editor-skeleton{display:grid;align-content:start;gap:13px;padding:22px}.editor-skeleton i{height:11px}.compose-empty{display:grid;place-items:center;color:var(--ncp-text-subtle);font-size:.82rem}.compose-workspace>footer{display:flex;align-items:center;justify-content:space-between;gap:14px;padding-top:10px;border-top:1px solid var(--ncp-line);color:var(--ncp-text-subtle);font-size:.7rem}.compose-workspace>footer code{max-width:45%;overflow:hidden;font-family:var(--ncp-font-mono);text-overflow:ellipsis;white-space:nowrap}.spin{animation:spin .8s linear infinite}@keyframes spin{to{transform:rotate(360deg)}}@media(max-width:700px){.compose-toolbar{align-items:stretch;flex-direction:column}.compose-toolbar>div{justify-content:flex-end}.compose-workspace>footer{align-items:flex-start;flex-direction:column}.compose-workspace>footer code{max-width:100%}}
</style>
