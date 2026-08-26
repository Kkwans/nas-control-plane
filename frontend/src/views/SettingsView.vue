<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ArrowDown, ArrowUp, Check, CircleAlert, ExternalLink, Gauge, GripVertical, LayoutPanelLeft, LoaderCircle, RotateCcw, Settings2, TextCursorInput, Waves } from '@lucide/vue'
import { ElButton, ElMessageBox, ElOption, ElSelect } from 'element-plus'

import type { UserPreferences } from '@/api/control'
import WorkspaceHeader from '@/components/WorkspaceHeader.vue'
import { DEFAULT_NAVIGATION_ORDER, navigationLabels } from '@/router/navigation'
import { DEFAULT_USER_PREFERENCES, useSystemStore } from '@/stores/system'

const systemStore = useSystemStore()
const clonePreferences = (value: UserPreferences): UserPreferences => ({
  ...value,
  navigationOrder: [...(value.navigationOrder ?? [])],
})
const draft = ref<UserPreferences>(clonePreferences(systemStore.preferences))
const persisted = ref<UserPreferences>(clonePreferences(systemStore.preferences))
const saveState = ref<'idle' | 'saving' | 'saved' | 'error'>('idle')
const changed = computed(() => JSON.stringify(draft.value) !== JSON.stringify(persisted.value))
const draggingNavigationID = ref<string | null>(null)
const dragOverNavigationID = ref<string | null>(null)
const normalizedNavigationOrder = computed(() => {
  const result: string[] = []
  const seen = new Set<string>()
  for (const id of [...(draft.value.navigationOrder ?? []), ...DEFAULT_NAVIGATION_ORDER]) {
    if (!navigationLabels[id] || seen.has(id)) continue
    seen.add(id)
    result.push(id)
  }
  return result
})
const preferenceSections = {
  realtime: ['refreshIntervalSeconds'] as const,
  display: ['interfaceDensity', 'baseFontSize'] as const,
  navigation: ['sidebarDefault', 'linkOpenMode', 'siteDefaultProtocol'] as const,
  fonts: ['chineseFont', 'latinFont'] as const,
  navigationOrder: ['navigationOrder'] as const,
}
let saveTimer: ReturnType<typeof setTimeout> | null = null
let saving = false
let pending = false
let suppressDraftWatch = false

const chineseDeviceCandidates = [
  { value: 'microsoft-yahei', label: '微软雅黑 UI', family: 'Microsoft YaHei UI' },
  { value: 'source-han-sans-sc', label: '思源黑体 SC', family: 'Source Han Sans SC' },
  { value: 'misans', label: 'MiSans', family: 'MiSans' },
  { value: 'harmonyos-sans-sc', label: 'HarmonyOS Sans SC', family: 'HarmonyOS Sans SC' },
] as const
const availableChineseDeviceFonts = ref<(typeof chineseDeviceCandidates)[number][]>([])

const intervals = [
  { value: 2, label: '2 秒（高频）' }, { value: 5, label: '5 秒（推荐）' },
  { value: 10, label: '10 秒' }, { value: 30, label: '30 秒' },
  { value: 60, label: '1 分钟' }, { value: 300, label: '5 分钟' },
]

function browserHasFont(family: string) {
  const canvas = document.createElement('canvas')
  const context = canvas.getContext('2d')
  if (!context) return false
  const sample = 'mmmmmmmmmmWWWWWWWWWW汉字字体0123456789'
  const measure = (font: string) => {
    context.font = `72px ${font}`
    return context.measureText(sample).width
  }
  const mono = measure('monospace')
  const sans = measure('sans-serif')
  return measure(`"${family}", monospace`) !== mono || measure(`"${family}", sans-serif`) !== sans
}

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
  const snapshot = clonePreferences(draft.value)
  try {
    const saved = await systemStore.setPreferences(snapshot)
    persisted.value = clonePreferences(saved)
    saveState.value = pending ? 'idle' : 'saved'
  } catch {
    suppressDraftWatch = true
    draft.value = clonePreferences(persisted.value)
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

function updateNavigationOrder(order: string[]) {
  draft.value.navigationOrder = [...order]
}

function moveNavigation(id: string, offset: number) {
  const order = [...normalizedNavigationOrder.value]
  const index = order.indexOf(id)
  const target = index + offset
  if (index < 0 || target < 0 || target >= order.length) return
  const current = order[index]
  const destination = order[target]
  if (!current || !destination) return
  order[index] = destination
  order[target] = current
  updateNavigationOrder(order)
}

function startNavigationDrag(id: string, event: DragEvent) {
  draggingNavigationID.value = id
  dragOverNavigationID.value = null
  if (event.dataTransfer) {
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', id)
  }
}

function dropNavigation(targetID: string) {
  const sourceID = draggingNavigationID.value
  draggingNavigationID.value = null
  dragOverNavigationID.value = null
  if (!sourceID || sourceID === targetID) return
  const order = [...normalizedNavigationOrder.value]
  const sourceIndex = order.indexOf(sourceID)
  const targetIndex = order.indexOf(targetID)
  if (sourceIndex < 0 || targetIndex < 0) return
  order.splice(sourceIndex, 1)
  order.splice(targetIndex, 0, sourceID)
  updateNavigationOrder(order)
}

function endNavigationDrag() {
  draggingNavigationID.value = null
  dragOverNavigationID.value = null
}

function restoreNavigationOrder() {
  updateNavigationOrder([...DEFAULT_NAVIGATION_ORDER])
}

function sectionChanged(fields: readonly (keyof UserPreferences)[]) {
  return fields.some((field) => {
    const draftValue = draft.value[field]
    const defaultValue = DEFAULT_USER_PREFERENCES[field]
    return Array.isArray(draftValue) || Array.isArray(defaultValue)
      ? JSON.stringify(draftValue) !== JSON.stringify(defaultValue)
      : draftValue !== defaultValue
  })
}

function restoreSection(fields: readonly (keyof UserPreferences)[]) {
  const next = clonePreferences(draft.value)
  for (const field of fields) {
    const value = DEFAULT_USER_PREFERENCES[field]
    next[field] = (Array.isArray(value) ? [...value] : value) as never
  }
  draft.value = next
}

async function restoreAllDefaults() {
  try {
    await ElMessageBox.confirm('将恢复所有设置为 NCP 默认值，并自动保存。已保存的自定义设置不会保留。', '恢复全部默认设置', {
      confirmButtonText: '恢复全部默认',
      cancelButtonText: '取消',
      type: 'warning',
    })
  } catch {
    return
  }
  draft.value = clonePreferences(DEFAULT_USER_PREFERENCES)
}

onMounted(() => {
  availableChineseDeviceFonts.value = chineseDeviceCandidates.filter((item) => browserHasFont(item.family))
  const selectedDeviceFont = chineseDeviceCandidates.find((item) => item.value === draft.value.chineseFont)
  if (selectedDeviceFont && !availableChineseDeviceFonts.value.some((item) => item.value === selectedDeviceFont.value)) {
    draft.value.chineseFont = 'system'
  }
})

onBeforeUnmount(() => {
  if (saveTimer) clearTimeout(saveTimer)
  if (pending && changed.value) void flushSave()
})
</script>

<template>
  <div class="page workspace-page settings-page">
    <WorkspaceHeader title="设置" description="统一调整数据更新、界面密度、字体与站点打开方式" :icon="Settings2" :stats="[]">
      <template #actions>
        <div class="settings-header-actions">
          <ElButton text :disabled="!changed" @click="restoreAllDefaults">恢复全部默认</ElButton>
          <div :class="['save-state', `save-state--${saveState}`]" role="status" aria-live="polite">
          <LoaderCircle v-if="saveState === 'saving'" class="save-state__spinner" :size="16" />
          <CircleAlert v-else-if="saveState === 'error'" :size="16" />
          <Check v-else :size="16" />
          <span>{{ saveState === 'saving' ? '正在保存' : saveState === 'error' ? '保存失败' : changed ? '等待保存' : '已自动保存' }}</span>
          <button v-if="saveState === 'error'" type="button" @click="retrySave"><RotateCcw :size="14" />重试</button>
          </div>
        </div>
      </template>
    </WorkspaceHeader>

    <section class="settings-grid">
      <article class="settings-section panel">
        <header><span><Waves :size="20" /></span><div><h2>实时数据</h2><p>决定总览、系统信息与监控页面的实时快照推送频率。</p></div><ElButton v-if="sectionChanged(preferenceSections.realtime)" class="settings-section__reset" text @click="restoreSection(preferenceSections.realtime)">恢复本区默认</ElButton></header>
        <label class="setting-row"><span><strong>刷新间隔</strong><small>仅应用于需要实时数据的页面</small></span>
          <ElSelect v-model="draft.refreshIntervalSeconds"><ElOption v-for="item in intervals" :key="item.value" :label="item.label" :value="item.value" /></ElSelect>
        </label>
      </article>

      <article class="settings-section panel">
        <header><span><Gauge :size="20" /></span><div><h2>显示密度</h2><p>控制表格信息量、基础字号和列表分页规模。</p></div><ElButton v-if="sectionChanged(preferenceSections.display)" class="settings-section__reset" text @click="restoreSection(preferenceSections.display)">恢复本区默认</ElButton></header>
        <label class="setting-row"><span><strong>界面密度</strong><small>紧凑模式适合大屏资源管理</small></span>
          <ElSelect v-model="draft.interfaceDensity"><ElOption label="舒适" value="comfortable" /><ElOption label="紧凑" value="compact" /></ElSelect>
        </label>
        <label class="setting-row"><span><strong>基础字号</strong><small>全站正文与控件字体基准</small></span>
          <ElSelect v-model="draft.baseFontSize"><ElOption v-for="size in [13,14,15,16,17,18]" :key="size" :label="`${size} px`" :value="size" /></ElSelect>
        </label>
      </article>

      <article class="settings-section panel">
        <header><span><LayoutPanelLeft :size="20" /></span><div><h2>导航与链接</h2><p>设置登录后的侧栏形态，以及站点入口的默认行为。</p></div><ElButton v-if="sectionChanged(preferenceSections.navigation)" class="settings-section__reset" text @click="restoreSection(preferenceSections.navigation)">恢复本区默认</ElButton></header>
        <label class="setting-row"><span><strong>侧栏默认状态</strong><small>移动端始终使用汉堡菜单</small></span>
          <ElSelect v-model="draft.sidebarDefault"><ElOption label="默认折叠" value="collapsed" /><ElOption label="默认展开" value="expanded" /></ElSelect>
        </label>
        <label class="setting-row"><span><strong>链接打开方式</strong><small>应用于站点管理的入口链接</small></span>
          <ElSelect v-model="draft.linkOpenMode"><ElOption label="新标签页" value="new-tab" /><ElOption label="当前页面" value="same-tab" /></ElSelect>
        </label>
        <label class="setting-row"><span><strong>默认协议</strong><small>站点没有自定义 URL 时使用</small></span>
          <ElSelect v-model="draft.siteDefaultProtocol"><ElOption label="HTTP" value="http" /><ElOption label="HTTPS" value="https" /></ElSelect>
        </label>
      </article>

      <article class="settings-section panel">
        <header><span><TextCursorInput :size="20" /></span><div><h2>字体</h2><p>中文与西文分别选择；未启用的 Web 字体不会进入首次加载。</p></div><ElButton v-if="sectionChanged(preferenceSections.fonts)" class="settings-section__reset" text @click="restoreSection(preferenceSections.fonts)">恢复本区默认</ElButton></header>
        <label class="setting-row"><span><strong>中文字体</strong><small>系统 UI 字体通常拥有最佳本地渲染</small></span>
          <ElSelect v-model="draft.chineseFont">
            <ElOption label="当前浏览器设备字体（推荐）" value="system" />
            <ElOption label="Noto Sans SC（随 NCP 提供）" value="noto-sans-sc" />
            <ElOption v-for="item in availableChineseDeviceFonts" :key="item.value" :label="`${item.label}（当前设备可用）`" :value="item.value" />
          </ElSelect>
        </label>
        <label class="setting-row"><span><strong>西文字体</strong><small>影响数字、英文标题与拉丁字符</small></span>
          <ElSelect v-model="draft.latinFont">
            <ElOption label="系统 UI" value="system" />
            <ElOption label="Manrope（内置）" value="manrope" />
            <ElOption label="Inter（内置）" value="inter" />
            <ElOption label="IBM Plex Sans（内置）" value="ibm-plex-sans" />
          </ElSelect>
        </label>
      </article>

      <article class="settings-section settings-section--wide panel">
        <header>
          <span><LayoutPanelLeft :size="20" /></span>
          <div><h2>侧栏菜单顺序</h2><p>拖拽菜单项或使用升降按钮调整；新增模块会自动合并，已删除模块会自动清理。</p></div>
          <div class="settings-section__actions"><ElButton v-if="sectionChanged(preferenceSections.navigationOrder)" class="settings-section__reset" text @click="restoreSection(preferenceSections.navigationOrder)">恢复本区默认</ElButton><ElButton class="settings-section__action" plain :icon="RotateCcw" @click="restoreNavigationOrder">恢复默认顺序</ElButton></div>
        </header>
        <ol class="navigation-order">
          <li
            v-for="(id, index) in normalizedNavigationOrder"
            :key="id"
            :class="{ 'navigation-order__item--dragging': draggingNavigationID === id, 'navigation-order__item--drop-target': dragOverNavigationID === id && draggingNavigationID !== id }"
            draggable="true"
            @dragstart="startNavigationDrag(id, $event)"
            @dragend="endNavigationDrag"
            @dragenter.prevent="dragOverNavigationID = id"
            @dragover.prevent="dragOverNavigationID = id"
            @drop.prevent="dropNavigation(id)"
          >
            <button class="navigation-order__handle" type="button" draggable="false" :aria-label="`拖动${navigationLabels[id]}调整顺序`" title="拖动调整顺序"><GripVertical :size="18" aria-hidden="true" /></button>
            <span><strong>{{ index + 1 }}. {{ navigationLabels[id] }}</strong><small>{{ id }}</small></span>
            <div class="navigation-order__actions">
              <ElButton class="navigation-order__move" plain :disabled="index === 0" aria-label="上移" title="上移" @click="moveNavigation(id, -1)"><ArrowUp :size="15" /></ElButton>
              <ElButton class="navigation-order__move" plain :disabled="index === normalizedNavigationOrder.length - 1" aria-label="下移" title="下移" @click="moveNavigation(id, 1)"><ArrowDown :size="15" /></ElButton>
            </div>
          </li>
        </ol>
      </article>
    </section>

    <aside class="settings-note"><ExternalLink :size="16" />修改会自动保存到 NCP SQLite，并在当前控制台立即生效。</aside>
  </div>
</template>

<style scoped>
.settings-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:16px}.settings-section{overflow:hidden}.settings-section--wide{grid-column:1/-1}.settings-section>header{display:flex;align-items:flex-start;gap:12px;padding:20px;border-bottom:1px solid var(--ncp-line);background:linear-gradient(135deg,#fff 58%,var(--ncp-surface-quiet))}.settings-section>header>span{display:grid;width:42px;height:42px;flex:0 0 auto;place-items:center;border-radius:12px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.settings-section h2{margin:0;font-size:1rem}.settings-section p{margin:4px 0 0;color:var(--ncp-text-subtle);font-size:.86rem}.settings-section__action{margin-left:auto}.setting-row{display:flex;min-height:76px;align-items:center;justify-content:space-between;gap:22px;padding:14px 20px;border-bottom:1px solid var(--ncp-line)}.setting-row:last-child{border-bottom:0}.setting-row>span{display:grid;gap:3px}.setting-row strong{font-size:.9rem}.setting-row small{color:var(--ncp-text-subtle);font-size:.8rem}.setting-row :deep(.el-select){width:200px}.setting-row :deep(.el-select__wrapper){min-height:42px;border-radius:10px}.navigation-order{display:grid;grid-template-columns:1fr;gap:8px;margin:0;padding:16px 20px 20px;list-style:none;background:var(--ncp-surface-quiet)}.navigation-order li{display:grid;grid-template-columns:46px minmax(0,1fr) auto;align-items:center;gap:11px;min-height:68px;padding:10px 12px 10px 9px;border:1px solid var(--ncp-line-strong);border-radius:11px;background:var(--ncp-surface);color:var(--ncp-text-muted);cursor:grab;transition:border-color var(--ncp-duration-fast),background-color var(--ncp-duration-fast),box-shadow var(--ncp-duration-fast),opacity var(--ncp-duration-fast)}.navigation-order li:hover{border-color:var(--ncp-primary-border);background:var(--ncp-primary-soft);box-shadow:0 5px 15px rgba(45,79,124,.08)}.navigation-order li:focus-within{border-color:var(--ncp-primary);box-shadow:0 0 0 3px var(--ncp-focus-ring)}.navigation-order li:active{cursor:grabbing}.navigation-order li.navigation-order__item--dragging{opacity:.45;transform:scale(.995)}.navigation-order li.navigation-order__item--drop-target{border-color:var(--ncp-primary);box-shadow:inset 0 3px 0 var(--ncp-primary),0 5px 15px rgba(45,79,124,.08)}.navigation-order__handle{display:grid;width:42px;height:42px;place-items:center;border:1px dashed var(--ncp-line-strong);border-radius:10px;background:var(--ncp-surface-quiet);color:var(--ncp-text-subtle);cursor:grab}.navigation-order__handle:hover{border-color:var(--ncp-primary);background:var(--ncp-primary-hover);color:var(--ncp-primary-strong)}.navigation-order__handle:active{cursor:grabbing}.navigation-order li>span{display:grid;gap:3px}.navigation-order li strong{font-size:.9rem}.navigation-order li small{color:var(--ncp-text-subtle);font-family:var(--ncp-font-mono);font-size:.72rem}.navigation-order__actions{display:flex;align-items:center;gap:6px}.navigation-order__move{display:grid!important;width:38px!important;height:38px!important;min-height:38px!important;margin:0!important;padding:0!important;place-items:center;border:1px solid var(--ncp-line)!important;border-radius:9px!important;background:var(--ncp-surface)!important;color:var(--ncp-text-muted)!important}.navigation-order__move:hover:not(:disabled){border-color:var(--ncp-primary)!important;background:var(--ncp-primary-soft)!important;color:var(--ncp-primary-strong)!important}.navigation-order__move:disabled{background:var(--ncp-surface-quiet)!important;color:var(--ncp-line-strong)!important}.save-state{display:flex;min-height:38px;align-items:center;gap:7px;padding:0 12px;border:1px solid var(--ncp-line);border-radius:10px;background:var(--ncp-surface-quiet);color:var(--ncp-text-muted);font-size:.82rem;font-weight:650}.save-state--saved{border-color:rgba(35,134,111,.18);background:var(--ncp-success-soft);color:var(--ncp-success)}.save-state--error{border-color:rgba(201,83,97,.2);background:var(--ncp-danger-soft);color:var(--ncp-danger-strong)}.save-state button{display:flex;align-items:center;gap:4px;margin-left:4px;padding:4px 6px;border-radius:6px;background:rgba(255,255,255,.72);color:inherit;font-size:.76rem}.save-state__spinner{animation:spin .8s linear infinite}.settings-note{display:flex;align-items:center;gap:8px;margin-top:2px;padding:13px 16px;border:1px solid var(--ncp-line);border-radius:12px;background:#fff;color:var(--ncp-text-muted);font-size:.82rem}@keyframes spin{to{transform:rotate(360deg)}}@media(max-width:980px){.settings-grid{grid-template-columns:1fr}}@media(max-width:560px){.setting-row{align-items:stretch;flex-direction:column;gap:10px}.setting-row :deep(.el-select){width:100%}.settings-section>header{flex-wrap:wrap}.settings-section__action{margin-left:54px}.navigation-order{padding:13px 14px 16px}.navigation-order li{grid-template-columns:44px minmax(0,1fr) auto;min-height:72px;padding-right:8px}.navigation-order__handle{width:40px;height:46px}.navigation-order__actions{gap:4px}.navigation-order__move{width:36px!important;height:36px!important;min-height:36px!important}}
.settings-header-actions{display:flex;align-items:center;gap:8px;flex-wrap:wrap;justify-content:flex-end}.settings-section>header>div{min-width:0}.settings-section__actions{display:flex;align-items:center;gap:6px;margin-left:auto;flex-wrap:wrap;justify-content:flex-end}.settings-section__reset{flex:0 0 auto}.settings-section__action{margin-left:0}
@media(max-width:700px){.settings-header-actions{width:100%;justify-content:flex-start}.settings-header-actions .save-state{flex:1}.settings-section__actions{width:100%;margin-left:54px;justify-content:flex-start}.settings-section__reset{margin-left:0}}
</style>
