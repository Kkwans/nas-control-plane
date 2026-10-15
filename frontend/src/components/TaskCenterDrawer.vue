<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, useId } from 'vue'
import { ListChecks, X } from '@lucide/vue'
import { ElDrawer } from 'element-plus'
import RecentTasks from './RecentTasks.vue'
import SavedJobHistory from './SavedJobHistory.vue'
import { captureSession } from '@/session/sessionLifecycle'

const emit = defineEmits<{ close: [] }>()
const open = ref(true)
const mode = ref<'recent' | 'history'>('recent')
const recentActive = () => open.value && mode.value === 'recent'
const historyActive = () => open.value && mode.value === 'history'
const titleId = useId()
const closeButton = ref<HTMLButtonElement | null>(null)
const session = captureSession()
function close() { open.value = false }
function closeSession() { close(); emit('close') }
session.signal.addEventListener('abort', closeSession, { once: true })
onBeforeUnmount(() => session.signal.removeEventListener('abort', closeSession))
function focusCloseButton() { closeButton.value?.focus({ preventScroll: true }) }
async function focusClose() {
  // 等待 ElDrawer 内建起始焦点完成后，再聚焦明确的关闭按钮。
  await nextTick()
  await nextTick()
  focusCloseButton()
}
</script>

<template>
  <ElDrawer :model-value="open" :title="mode === 'recent' ? '最近任务' : '已保存历史'" size="min(760px, 100%)" append-to-body :show-close="false" :with-header="false" :aria-labelledby="titleId" body-class="task-center-drawer__body" class="task-center-drawer"
    @update:model-value="!$event && close()" @close="close" @closed="emit('close')" @open-auto-focus="focusClose">
    <div class="task-heading">
        <span class="task-heading__icon"><ListChecks :size="22" aria-hidden="true" /></span>
        <div><h2 :id="titleId">{{ mode === 'recent' ? '最近任务' : '已保存历史' }}</h2><p>跨页面查看任务进展与结果</p></div>
        <button ref="closeButton" class="task-icon-button" type="button" :aria-label="mode === 'recent' ? '关闭最近任务' : '关闭已保存历史'" @click="close"><X :size="20" aria-hidden="true" /></button>
    </div>
    <nav class="task-modes" aria-label="任务记录来源">
      <button type="button" :aria-pressed="mode === 'recent'" @click="mode = 'recent'">最近任务</button>
      <button type="button" :aria-pressed="mode === 'history'" @click="mode = 'history'">已保存历史</button>
    </nav>
    <RecentTasks v-if="mode === 'recent'" :active="recentActive" @focus-close="focusCloseButton" />
    <SavedJobHistory v-else-if="open" :active="historyActive" />
  </ElDrawer>
</template>

<style scoped>
/* Teleport 的第三方根节点不保证继承 scope ID；仅通过此抽屉专属 body-class 定位布局。 */
:global(.task-center-drawer > .task-center-drawer__body){display:flex;flex-direction:column;min-height:0;padding:0;overflow:hidden}
.task-heading{position:sticky;top:0;z-index:2;background:var(--ncp-surface);display:flex;width:100%;min-width:0;flex-shrink:0;align-items:center;gap:12px;padding:18px 20px;border-bottom:1px solid var(--ncp-line)}.task-heading__icon{display:grid;flex:0 0 44px;height:44px;place-items:center;border-radius:var(--ncp-radius-md);background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.task-heading>div{min-width:0;flex:1}.task-heading h2{margin:0;color:var(--ncp-text);font-size:1.1rem}.task-heading p{margin:4px 0 0;color:var(--ncp-text-subtle);font-size:.78rem}.task-icon-button{display:grid;min-width:44px;min-height:44px;place-items:center;border-radius:var(--ncp-radius-control);background:var(--ncp-surface-quiet);color:var(--ncp-text-muted)}

.task-modes{display:flex;flex-shrink:0;gap:8px;padding:8px 20px;border-bottom:1px solid var(--ncp-line);background:var(--ncp-surface)}.task-modes button{min-width:44px;min-height:44px;padding:8px 12px;border-radius:var(--ncp-radius-control);background:var(--ncp-surface-quiet);color:var(--ncp-text-muted);font:inherit}.task-modes button[aria-pressed=true]{background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}button:focus-visible{outline:2px solid var(--ncp-primary);outline-offset:2px}
@media(max-height:640px){:global(.task-center-drawer > .task-center-drawer__body){display:block;overflow:auto;overscroll-behavior:contain}}
@media(prefers-reduced-motion:reduce){.task-center-drawer{transition:none}}
</style>
