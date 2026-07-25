<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ExternalLink, Gauge, LayoutPanelLeft, Save, Settings2, TextCursorInput, Waves } from '@lucide/vue'
import { ElButton, ElMessage, ElOption, ElSelect } from 'element-plus'

import type { UserPreferences } from '@/api/control'
import WorkspaceHeader from '@/components/WorkspaceHeader.vue'
import { useSystemStore } from '@/stores/system'

const systemStore = useSystemStore()
const saving = ref(false)
const draft = ref<UserPreferences>({ ...systemStore.preferences })
const changed = computed(() => JSON.stringify(draft.value) !== JSON.stringify(systemStore.preferences))

watch(() => systemStore.preferences, (value) => { draft.value = { ...value } }, { deep: true })

const intervals = [
  { value: 2, label: '2 秒（高频）' }, { value: 5, label: '5 秒（推荐）' },
  { value: 10, label: '10 秒' }, { value: 30, label: '30 秒' },
  { value: 60, label: '1 分钟' }, { value: 300, label: '5 分钟' },
]

async function save() {
  saving.value = true
  try {
    await systemStore.setPreferences({ ...draft.value })
    ElMessage.success('控制台偏好已保存并立即生效')
  } catch {
    ElMessage.error('设置保存失败，请检查选项后重试')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="page workspace-page settings-page">
    <WorkspaceHeader title="系统设置" description="统一调整数据更新、界面密度、字体与站点打开方式" :icon="Settings2" :stats="[]">
      <template #actions>
        <ElButton type="primary" :loading="saving" :disabled="!changed" @click="save"><Save :size="16" />保存更改</ElButton>
      </template>
    </WorkspaceHeader>

    <section class="settings-grid">
      <article class="settings-section panel">
        <header><span><Waves :size="20" /></span><div><h2>实时数据</h2><p>决定概览、Docker 与站点状态的推送和断线轮询频率。</p></div></header>
        <label class="setting-row"><span><strong>刷新间隔</strong><small>SSE 正常时按该间隔推送更新信号</small></span>
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

    <aside class="settings-note"><ExternalLink :size="16" />所有设置保存在 NCP SQLite 中，跨浏览器登录后仍然生效。</aside>
  </div>
</template>

<style scoped>
.settings-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px}.settings-section{overflow:hidden}.settings-section>header{display:flex;align-items:flex-start;gap:11px;padding:18px;border-bottom:1px solid var(--ncp-line);background:linear-gradient(135deg,#fff 60%,var(--ncp-surface-quiet))}.settings-section>header>span{display:grid;width:40px;height:40px;flex:0 0 auto;place-items:center;border-radius:11px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.settings-section h2{margin:0;font-size:.95rem}.settings-section p{margin:3px 0 0;color:var(--ncp-text-subtle);font-size:.78rem}.setting-row{display:flex;min-height:72px;align-items:center;justify-content:space-between;gap:20px;padding:12px 18px;border-bottom:1px solid var(--ncp-line)}.setting-row:last-child{border-bottom:0}.setting-row>span{display:grid;gap:2px}.setting-row strong{font-size:.82rem}.setting-row small{color:var(--ncp-text-subtle);font-size:.72rem}.setting-row :deep(.el-select){width:190px}.setting-row :deep(.el-select__wrapper){min-height:40px;border-radius:9px}.settings-note{display:flex;align-items:center;gap:7px;margin-top:14px;padding:12px 15px;border:1px solid var(--ncp-line);border-radius:11px;background:#fff;color:var(--ncp-text-muted);font-size:.75rem}@media(max-width:980px){.settings-grid{grid-template-columns:1fr}}@media(max-width:560px){.setting-row{align-items:stretch;flex-direction:column;gap:9px}.setting-row :deep(.el-select){width:100%}}
</style>
