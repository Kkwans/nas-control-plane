<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { Check, CircleAlert, ExternalLink, Gauge, LayoutPanelLeft, LoaderCircle, RotateCcw, Settings2, TextCursorInput, Waves } from '@lucide/vue'
import { ElButton, ElOption, ElSelect } from 'element-plus'

import type { UserPreferences } from '@/api/control'
import WorkspaceHeader from '@/components/WorkspaceHeader.vue'
import { useSystemStore } from '@/stores/system'

const systemStore = useSystemStore()
const draft = ref<UserPreferences>({ ...systemStore.preferences })
const persisted = ref<UserPreferences>({ ...systemStore.preferences })
const saveState = ref<'idle' | 'saving' | 'saved' | 'error'>('idle')
const changed = computed(() => JSON.stringify(draft.value) !== JSON.stringify(persisted.value))
let saveTimer: ReturnType<typeof setTimeout> | null = null
let saving = false
let pending = false
let suppressDraftWatch = false

const intervals = [
  { value: 2, label: '2 秒（高频）' }, { value: 5, label: '5 秒（推荐）' },
  { value: 10, label: '10 秒' }, { value: 30, label: '30 秒' },
  { value: 60, label: '1 分钟' }, { value: 300, label: '5 分钟' },
]

watch(draft, (value) => {
  if (suppressDraftWatch) return
  systemStore.previewPreferences(value)
  pending = true
  saveState.value = 'idle'
  if (saveTimer) clearTimeout(saveTimer)
  saveTimer = setTimeout(() => void flushSave(), 400)
}, { deep: true })

async function flushSave() {
  if (saving || !pending || !changed.value) return
  saving = true
  pending = false
  saveState.value = 'saving'
  const snapshot = { ...draft.value }
  try {
    const saved = await systemStore.setPreferences(snapshot)
    persisted.value = { ...saved }
    saveState.value = pending ? 'idle' : 'saved'
  } catch {
    suppressDraftWatch = true
    draft.value = { ...persisted.value }
    systemStore.previewPreferences(persisted.value)
    suppressDraftWatch = false
    pending = false
    saveState.value = 'error'
  } finally {
    saving = false
    if (pending) {
      if (saveTimer) clearTimeout(saveTimer)
      saveTimer = setTimeout(() => void flushSave(), 120)
    }
  }
}

function retrySave() {
  pending = true
  void flushSave()
}

onBeforeUnmount(() => {
  if (saveTimer) clearTimeout(saveTimer)
  if (pending && changed.value) void flushSave()
})
</script>

<template>
  <div class="page workspace-page settings-page">
    <WorkspaceHeader title="系统设置" description="统一调整数据更新、界面密度、字体与站点打开方式" :icon="Settings2" :stats="[]">
      <template #actions>
        <div :class="['save-state', `save-state--${saveState}`]" role="status" aria-live="polite">
          <LoaderCircle v-if="saveState === 'saving'" class="save-state__spinner" :size="16" />
          <CircleAlert v-else-if="saveState === 'error'" :size="16" />
          <Check v-else :size="16" />
          <span>{{ saveState === 'saving' ? '正在保存' : saveState === 'error' ? '保存失败' : changed ? '等待保存' : '已自动保存' }}</span>
          <button v-if="saveState === 'error'" type="button" @click="retrySave"><RotateCcw :size="14" />重试</button>
        </div>
      </template>
    </WorkspaceHeader>

    <section class="settings-grid">
      <article class="settings-section panel">
        <header><span><Waves :size="20" /></span><div><h2>实时数据</h2><p>决定总览、系统信息与监控页面的实时快照推送频率。</p></div></header>
        <label class="setting-row"><span><strong>刷新间隔</strong><small>仅应用于需要实时数据的页面</small></span>
          <ElSelect v-model="draft.refreshIntervalSeconds"><ElOption v-for="item in intervals" :key="item.value" :label="item.label" :value="item.value" /></ElSelect>
        </label>
      </article>

      <article class="settings-section panel">
        <header><span><Gauge :size="20" /></span><div><h2>显示密度</h2><p>控制表格信息量、基础字号和列表分页规模。</p></div></header>
        <label class="setting-row"><span><strong>界面密度</strong><small>紧凑模式适合大屏资源管理</small></span>
          <ElSelect v-model="draft.interfaceDensity"><ElOption label="舒适" value="comfortable" /><ElOption label="紧凑" value="compact" /></ElSelect>
        </label>
        <label class="setting-row"><span><strong>基础字号</strong><small>全站正文与控件字体基准</small></span>
          <ElSelect v-model="draft.baseFontSize"><ElOption v-for="size in [13,14,15,16,17,18]" :key="size" :label="`${size} px`" :value="size" /></ElSelect>
        </label>
        <label class="setting-row"><span><strong>每页数量</strong><small>资源表格的默认分页数量</small></span>
          <ElSelect v-model="draft.pageSize"><ElOption v-for="size in [20,25,50,100]" :key="size" :label="`${size} 条`" :value="size" /></ElSelect>
        </label>
      </article>

      <article class="settings-section panel">
        <header><span><LayoutPanelLeft :size="20" /></span><div><h2>导航与链接</h2><p>设置登录后的侧栏形态，以及站点入口的默认行为。</p></div></header>
        <label class="setting-row"><span><strong>侧栏默认状态</strong><small>移动端始终使用汉堡菜单</small></span>
          <ElSelect v-model="draft.sidebarDefault"><ElOption label="默认折叠" value="collapsed" /><ElOption label="默认展开" value="expanded" /></ElSelect>
        </label>
        <label class="setting-row"><span><strong>链接打开方式</strong><small>应用于站点中心的入口链接</small></span>
          <ElSelect v-model="draft.linkOpenMode"><ElOption label="新标签页" value="new-tab" /><ElOption label="当前页面" value="same-tab" /></ElSelect>
        </label>
        <label class="setting-row"><span><strong>默认协议</strong><small>站点没有自定义 URL 时使用</small></span>
          <ElSelect v-model="draft.siteDefaultProtocol"><ElOption label="HTTP" value="http" /><ElOption label="HTTPS" value="https" /></ElSelect>
        </label>
      </article>

      <article class="settings-section panel">
        <header><span><TextCursorInput :size="20" /></span><div><h2>字体</h2><p>中文与西文分别选择；未启用的 Web 字体不会进入首次加载。</p></div></header>
        <label class="setting-row"><span><strong>中文字体</strong><small>系统 UI 字体通常拥有最佳本地渲染</small></span>
          <ElSelect v-model="draft.chineseFont"><ElOption label="系统中文 UI" value="system" /><ElOption label="Noto Sans SC（本机优先）" value="noto-sans-sc" /></ElSelect>
        </label>
        <label class="setting-row"><span><strong>西文字体</strong><small>影响数字、英文标题与拉丁字符</small></span>
          <ElSelect v-model="draft.latinFont"><ElOption label="系统 UI" value="system" /><ElOption label="Manrope" value="manrope" /></ElSelect>
        </label>
      </article>
    </section>

    <aside class="settings-note"><ExternalLink :size="16" />修改会自动保存到 NCP SQLite，并在当前控制台立即生效。</aside>
  </div>
</template>

<style scoped>
.settings-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:16px}.settings-section{overflow:hidden}.settings-section>header{display:flex;align-items:flex-start;gap:12px;padding:20px;border-bottom:1px solid var(--ncp-line);background:linear-gradient(135deg,#fff 58%,var(--ncp-surface-quiet))}.settings-section>header>span{display:grid;width:42px;height:42px;flex:0 0 auto;place-items:center;border-radius:12px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.settings-section h2{margin:0;font-size:1rem}.settings-section p{margin:4px 0 0;color:var(--ncp-text-subtle);font-size:.86rem}.setting-row{display:flex;min-height:76px;align-items:center;justify-content:space-between;gap:22px;padding:14px 20px;border-bottom:1px solid var(--ncp-line)}.setting-row:last-child{border-bottom:0}.setting-row>span{display:grid;gap:3px}.setting-row strong{font-size:.9rem}.setting-row small{color:var(--ncp-text-subtle);font-size:.8rem}.setting-row :deep(.el-select){width:200px}.setting-row :deep(.el-select__wrapper){min-height:42px;border-radius:10px}.save-state{display:flex;min-height:38px;align-items:center;gap:7px;padding:0 12px;border:1px solid var(--ncp-line);border-radius:10px;background:var(--ncp-surface-quiet);color:var(--ncp-text-muted);font-size:.82rem;font-weight:650}.save-state--saved{border-color:rgba(35,134,111,.18);background:var(--ncp-success-soft);color:var(--ncp-success)}.save-state--error{border-color:rgba(201,83,97,.2);background:var(--ncp-danger-soft);color:var(--ncp-danger-strong)}.save-state button{display:flex;align-items:center;gap:4px;margin-left:4px;padding:4px 6px;border-radius:6px;background:rgba(255,255,255,.72);color:inherit;font-size:.76rem}.save-state__spinner{animation:spin .8s linear infinite}.settings-note{display:flex;align-items:center;gap:8px;margin-top:2px;padding:13px 16px;border:1px solid var(--ncp-line);border-radius:12px;background:#fff;color:var(--ncp-text-muted);font-size:.82rem}@keyframes spin{to{transform:rotate(360deg)}}@media(max-width:980px){.settings-grid{grid-template-columns:1fr}}@media(max-width:560px){.setting-row{align-items:stretch;flex-direction:column;gap:10px}.setting-row :deep(.el-select){width:100%}}
</style>
