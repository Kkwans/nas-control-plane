<script setup lang="ts">
import { ref, watch } from 'vue'
import { Clock3, RefreshCw, Save, Settings2, Waves } from '@lucide/vue'
import { ElButton, ElMessage, ElOption, ElSelect } from 'element-plus'

import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { useSystemStore } from '@/stores/system'

const systemStore = useSystemStore()
const draftInterval = ref(systemStore.refreshIntervalSeconds)
const saving = ref(false)

const stats: WorkspaceStat[] = [
  { label: '实时通道', value: 'SSE', tone: 'success' },
  { label: '最短间隔', value: '2 秒' },
  { label: '最长间隔', value: '5 分钟' },
]
const intervalOptions = [
  { value: 2, label: '2 秒（高频）' },
  { value: 5, label: '5 秒（推荐）' },
  { value: 10, label: '10 秒' },
  { value: 15, label: '15 秒' },
  { value: 30, label: '30 秒' },
  { value: 60, label: '1 分钟' },
  { value: 300, label: '5 分钟' },
]

watch(() => systemStore.refreshIntervalSeconds, (value) => {
  draftInterval.value = value
})

async function saveRefreshInterval() {
  saving.value = true
  try {
    await systemStore.setRefreshInterval(draftInterval.value)
    ElMessage.success('实时刷新间隔已保存')
  } catch {
    ElMessage.error('刷新间隔保存失败')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="page workspace-page settings-page">
    <WorkspaceHeader title="系统设置" description="调整控制台体验与实时数据策略" :icon="Settings2" :stats="stats" />

    <section class="settings-layout">
      <article class="settings-card panel">
        <header>
          <span><Waves :size="20" /></span>
          <div><h2>实时数据</h2><p>控制系统概览、Docker 状态和站点运行信息的更新频率。</p></div>
        </header>
        <div class="setting-row">
          <div>
            <strong><Clock3 :size="16" />刷新间隔</strong>
            <p>SSE 正常时按该间隔推送刷新信号；连接中断后以相同间隔自动轮询。</p>
          </div>
          <div class="setting-control">
            <ElSelect v-model="draftInterval" aria-label="实时刷新间隔">
              <ElOption v-for="option in intervalOptions" :key="option.value" :label="option.label" :value="option.value" />
            </ElSelect>
            <ElButton type="primary" :loading="saving" :disabled="draftInterval === systemStore.refreshIntervalSeconds" @click="saveRefreshInterval">
              <Save :size="16" />保存
            </ElButton>
          </div>
        </div>
      </article>

      <article class="settings-card settings-card--quiet panel">
        <header>
          <span><RefreshCw :size="20" /></span>
          <div><h2>更新策略说明</h2><p>页面首次登录会立即加载完整快照，之后进入持续更新。</p></div>
        </header>
        <ul>
          <li>标签页重新可见时立即补一次快照，避免展示过期数据。</li>
          <li>SSE 断线时自动切换轮询，恢复连接后停止重复轮询。</li>
          <li>刷新偏好保存在 NCP Server 数据库中，更换浏览器后仍然生效。</li>
        </ul>
      </article>
    </section>
  </div>
</template>

<style scoped>
.settings-layout { display:grid; grid-template-columns:minmax(0,1.45fr) minmax(320px,.75fr); gap:14px; }
.settings-card { padding:20px; }
.settings-card>header { display:flex; align-items:flex-start; gap:12px; padding-bottom:18px; border-bottom:1px solid var(--ncp-line); }
.settings-card>header>span { display:grid; width:42px; height:42px; flex:0 0 auto; place-items:center; border-radius:12px; background:var(--ncp-primary-soft); color:var(--ncp-primary-strong); }
.settings-card h2 { margin:0; font-size:.96rem; }
.settings-card p { margin:4px 0 0; color:var(--ncp-text-subtle); font-size:.8rem; line-height:1.6; }
.setting-row { display:flex; min-height:112px; align-items:center; justify-content:space-between; gap:28px; padding-top:18px; }
.setting-row>div:first-child { max-width:560px; }
.setting-row strong { display:flex; align-items:center; gap:7px; font-size:.84rem; }
.setting-control { display:flex; flex:0 0 auto; align-items:center; gap:8px; }
.setting-control :deep(.el-select) { width:190px; }
.setting-control :deep(.el-select__wrapper),.setting-control :deep(.el-button) { min-height:42px; border-radius:10px; }
.settings-card--quiet ul { display:grid; gap:12px; padding:18px 0 0 20px; margin:0; color:var(--ncp-text-muted); font-size:.8rem; line-height:1.65; }
@media(max-width:980px){.settings-layout{grid-template-columns:1fr}}
@media(max-width:620px){.settings-card{padding:16px}.setting-row{align-items:stretch;flex-direction:column;gap:16px}.setting-control{width:100%}.setting-control :deep(.el-select){min-width:0;flex:1}.setting-control :deep(.el-button){min-width:92px}}
</style>
