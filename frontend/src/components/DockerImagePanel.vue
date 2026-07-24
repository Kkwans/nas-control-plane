<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Download, Image as ImageIcon, RefreshCw, Trash2 } from '@lucide/vue'
import { ElDialog, ElInput, ElMessage, ElMessageBox, ElTooltip } from 'element-plus'

import {
  NcpApiError,
  pullDockerImage,
  removeDockerImage,
  requestDockerImages,
  type DockerImageSummary,
  type DockerInventory,
} from '@/api/system'
import { formatBytes } from '@/domain/overview'

const props = defineProps<{
  query: string
  containers: DockerInventory['containers']
}>()

const images = ref<DockerImageSummary[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const pullDialogOpen = ref(false)
const pullReference = ref('')
const pullPending = ref(false)
const removePending = ref<string | null>(null)

const filteredImages = computed(() => {
  const term = props.query.trim().toLowerCase()
  if (!term) return images.value
  return images.value.filter((image) =>
    [image.id, ...image.repoTags, ...image.repoDigests].some((value) => value.toLowerCase().includes(term)),
  )
})

async function refresh() {
  loading.value = true
  error.value = null
  try {
    images.value = (await requestDockerImages()).images
  } catch (caught) {
    error.value = caught instanceof NcpApiError ? caught.message : '本地镜像读取失败。'
  } finally {
    loading.value = false
  }
}

async function submitPull() {
  if (!pullReference.value.trim() || pullPending.value) return
  pullPending.value = true
  try {
    await pullDockerImage(pullReference.value.trim())
    ElMessage.success(`镜像 ${pullReference.value.trim()} 拉取完成`)
    pullDialogOpen.value = false
    pullReference.value = ''
    await refresh()
  } catch (caught) {
    ElMessage.error(caught instanceof NcpApiError ? caught.message : '镜像拉取失败。')
  } finally {
    pullPending.value = false
  }
}

async function confirmRemove(image: DockerImageSummary) {
  const name = displayName(image)
  try {
    await ElMessageBox.confirm(
      `将从 NAS 删除本地镜像“${name}”。正在被容器使用的镜像会由 Docker 拒绝删除。`,
      '删除本地镜像',
      {
        confirmButtonText: '确认删除',
        cancelButtonText: '取消',
        type: 'warning',
      },
    )
  } catch {
    return
  }
  removePending.value = image.id
  try {
    await removeDockerImage(image.id)
    ElMessage.success(`已删除镜像 ${name}`)
    await refresh()
  } catch (caught) {
    ElMessage.error(caught instanceof NcpApiError ? caught.message : '镜像删除失败。')
  } finally {
    removePending.value = null
  }
}

function displayName(image: DockerImageSummary) {
  return image.repoTags[0] ?? '未标记镜像'
}

function shortId(imageId: string) {
  return imageId.replace(/^sha256:/, '').slice(0, 12)
}

function createdAt(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.valueOf())
    ? '—'
    : new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' }).format(date)
}

function usageCount(image: DockerImageSummary) {
  return props.containers.filter((container) => image.repoTags.includes(container.image) || container.image === image.id).length
}

onMounted(refresh)
</script>

<template>
  <section class="image-toolbar">
    <div>
      <strong>本地镜像</strong>
      <span>{{ images.length }} 个镜像 · {{ loading ? '正在同步' : '来自 Docker Engine' }}</span>
    </div>
    <div>
      <ElTooltip content="重新读取 Docker 本地镜像" placement="top">
        <button class="icon-button" type="button" aria-label="刷新本地镜像" :disabled="loading" @click="refresh"><RefreshCw :class="{ spin: loading }" :size="17" /></button>
      </ElTooltip>
      <button class="primary-button" type="button" @click="pullDialogOpen = true"><Download :size="17" />拉取镜像</button>
    </div>
  </section>

  <div v-if="error" class="image-error" role="alert">{{ error }}</div>

  <section class="image-panel panel" aria-label="Docker 本地镜像列表">
    <div class="image-table__head">
      <span>镜像</span><span>镜像 ID</span><span>大小</span><span>创建日期</span><span>容器引用</span><span>操作</span>
    </div>
    <div v-for="image in filteredImages" :key="image.id" class="image-row">
      <div class="image-name">
        <span><ImageIcon :size="18" /></span>
        <div>
          <strong>{{ displayName(image) }}</strong>
          <small v-if="image.repoTags.length > 1">另有 {{ image.repoTags.length - 1 }} 个标签</small>
          <small v-else>{{ image.repoDigests[0] ?? '本地构建镜像' }}</small>
        </div>
      </div>
      <span class="mono">{{ shortId(image.id) }}</span>
      <span>{{ formatBytes(image.sizeBytes) }}</span>
      <span>{{ createdAt(image.createdAt) }}</span>
      <span>{{ usageCount(image) ? `${usageCount(image)} 个容器` : '未检测到引用' }}</span>
      <ElTooltip content="删除本地镜像；正在使用时 Docker 会拒绝" placement="top">
        <button class="remove-button" type="button" :disabled="removePending === image.id" @click="confirmRemove(image)">
          <Trash2 :size="16" />删除
        </button>
      </ElTooltip>
    </div>
    <div v-if="!loading && !filteredImages.length" class="table-empty">没有匹配的本地镜像。</div>
  </section>

  <section class="image-mobile-list" aria-label="Docker 本地镜像列表">
    <article v-for="image in filteredImages" :key="image.id" class="image-card panel">
      <header>
        <span><ImageIcon :size="18" /></span>
        <div><strong>{{ displayName(image) }}</strong><small>{{ shortId(image.id) }}</small></div>
      </header>
      <dl>
        <div><dt>大小</dt><dd>{{ formatBytes(image.sizeBytes) }}</dd></div>
        <div><dt>创建日期</dt><dd>{{ createdAt(image.createdAt) }}</dd></div>
        <div><dt>容器引用</dt><dd>{{ usageCount(image) ? `${usageCount(image)} 个` : '未检测到' }}</dd></div>
      </dl>
      <button class="remove-button" type="button" :disabled="removePending === image.id" @click="confirmRemove(image)"><Trash2 :size="16" />删除镜像</button>
    </article>
    <p v-if="!loading && !filteredImages.length" class="table-empty panel">没有匹配的本地镜像。</p>
  </section>

  <ElDialog v-model="pullDialogOpen" title="从镜像仓库拉取" width="min(520px, calc(100vw - 28px))" append-to-body>
    <div class="pull-form">
      <label for="docker-image-reference">镜像地址</label>
      <ElInput id="docker-image-reference" v-model="pullReference" size="large" placeholder="例如 nginx:alpine 或 ghcr.io/example/app:v1" @keyup.enter="submitPull" />
      <p>必须包含明确的标签或 SHA256 摘要。支持 Docker Hub、GHCR 以及其他兼容 Docker Registry 的公开镜像。</p>
    </div>
    <template #footer>
      <button class="dialog-button" type="button" @click="pullDialogOpen = false">取消</button>
      <button class="dialog-button dialog-button--primary" type="button" :disabled="!pullReference.trim() || pullPending" @click="submitPull">{{ pullPending ? '正在拉取…' : '开始拉取' }}</button>
    </template>
  </ElDialog>
</template>

<style scoped>
.image-toolbar { display:flex; min-height:56px; align-items:center; justify-content:space-between; gap:14px; }
.image-toolbar>div:first-child { display:grid; gap:2px; }.image-toolbar strong{font-size:.86rem}.image-toolbar span{color:var(--ncp-text-subtle);font-size:.67rem}
.image-toolbar>div:last-child { display:flex; gap:8px; }
.icon-button,.primary-button,.dialog-button { display:flex; min-height:40px; align-items:center; justify-content:center; gap:6px; border-radius:9px; font-size:.72rem; font-weight:720; }
.icon-button { width:40px; border:1px solid var(--ncp-line); background:#fff; color:var(--ncp-text-muted); }.primary-button{padding:0 15px;background:var(--ncp-primary);color:#fff;box-shadow:0 6px 16px rgba(36,104,216,.18)}
.image-error{padding:10px 13px;border:1px solid rgba(212,81,93,.2);border-radius:10px;background:var(--ncp-danger-soft);color:var(--ncp-danger-strong);font-size:.7rem}
.image-panel{overflow:hidden}.image-table__head,.image-row{display:grid;grid-template-columns:minmax(240px,1.4fr) 130px 100px 118px 128px 92px;align-items:center;gap:12px}.image-table__head{min-height:42px;padding:0 16px;background:var(--ncp-surface-quiet);color:var(--ncp-text-subtle);font-size:.75rem;font-weight:730}.image-row{min-height:72px;padding:0 16px;border-top:1px solid var(--ncp-line);color:var(--ncp-text-muted);font-size:.75rem}.image-row:hover{background:var(--ncp-surface-hover)}
.image-name{display:flex;min-width:0;align-items:center;gap:9px}.image-name>span,.image-card header>span{display:grid;width:36px;height:36px;flex:0 0 auto;place-items:center;border-radius:10px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.image-name>div,.image-card header>div{display:grid;min-width:0;gap:2px}.image-name strong,.image-name small{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.image-name strong,.image-card strong{color:var(--ncp-text);font-size:.81rem}.image-name small,.image-card small{color:var(--ncp-text-subtle);font-size:.61rem}
.mono{font-family:'JetBrains Mono Variable',monospace;font-size:.65rem}.remove-button{display:flex;min-height:36px;align-items:center;justify-content:center;gap:5px;padding:0 10px;border-radius:8px;background:var(--ncp-danger-soft);color:var(--ncp-danger-strong);font-size:.68rem;font-weight:710}.remove-button:disabled{opacity:.45}.table-empty{padding:36px;color:var(--ncp-text-subtle);font-size:.72rem;text-align:center}.image-mobile-list{display:none}
.pull-form{display:grid;gap:9px}.pull-form label{font-size:.76rem;font-weight:720}.pull-form p{margin:0;color:var(--ncp-text-subtle);font-size:.66rem;line-height:1.6}.dialog-button{padding:0 14px;background:var(--ncp-surface-quiet);color:var(--ncp-text-muted)}.dialog-button--primary{background:var(--ncp-primary);color:#fff}.dialog-button:disabled{opacity:.45}.spin{animation:spin .8s linear infinite}@keyframes spin{to{transform:rotate(360deg)}}
@media(max-width:980px){.image-table__head,.image-row{grid-template-columns:minmax(210px,1.4fr) 108px 84px 106px 112px 84px;gap:8px}}
@media(max-width:767px){.image-toolbar{align-items:flex-end}.image-toolbar>div:first-child span{max-width:180px}.image-panel{display:none}.image-mobile-list{display:grid;gap:10px}.image-card{display:grid;gap:13px;padding:15px}.image-card header{display:flex;align-items:center;gap:9px}.image-card dl{display:grid;gap:8px;margin:0;padding:11px;border-radius:10px;background:var(--ncp-surface-quiet)}.image-card dl>div{display:flex;justify-content:space-between;gap:10px}.image-card dt{color:var(--ncp-text-subtle);font-size:.65rem}.image-card dd{margin:0;color:var(--ncp-text-muted);font-size:.68rem}}
</style>
