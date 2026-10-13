<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { Plus, SquareTerminal, X } from '@lucide/vue'
import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import ActionButton from '@/components/ActionButton.vue'
import TerminalPanel from '@/components/TerminalPanel.vue'
import { createTerminalPageOwner } from '@/domain/terminalPageOwner'
import { useSystemStore } from '@/stores/system'

type Summary = { title: string; state: string; stateLabel: string; target: string }
type Tab = { id: string; summary: Summary }
const systemStore = useSystemStore()
const containers = computed(() => (systemStore.inventory?.containers ?? []).filter(item => item.state === 'running'))
const page = createTerminalPageOwner()
let sequence = 0
let selectionIntent = 0
const newTab = (): Tab => ({ id: `terminal-${++sequence}`, summary: { title: 'root@主机', state: 'idle', stateLabel: '未连接', target: 'host' } })
const tabs = ref<Tab[]>([newTab()])
const selectedId = ref(tabs.value[0]!.id)
const panelRefs = new Map<string, InstanceType<typeof TerminalPanel>>()
const tablist = ref<HTMLElement | null>(null)
const pendingClose = ref<string | null>(null)
const closeCancel = ref<HTMLButtonElement | null>(null)
const closeTarget = computed(() => tabs.value.find(tab => tab.id === pendingClose.value))
const selected = computed(() => tabs.value.find(tab => tab.id === selectedId.value)!)
const stats = computed<WorkspaceStat[]>(() => [
  { label: '会话状态', value: selected.value.summary.stateLabel, tone: selected.value.summary.state === 'connected' ? 'success' : undefined },
  { label: '终端目标', value: selected.value.summary.target === 'host' ? 'NAS 主机' : 'Docker 容器' },
  { label: '本页连接', value: `${page.occupiedCount} / ${page.limit}` },
])
function focusIntent() {
  const origin = document.activeElement
  return () => document.activeElement === origin || (origin instanceof HTMLElement && !origin.isConnected && document.activeElement === document.body)
}
function focusTab(id: string) { tablist.value?.querySelector<HTMLButtonElement>(`#tab-${id}`)?.focus() }
function selectTab(id: string, focus = false) {
  if (!page.current() || pendingClose.value) return
  selectionIntent++
  selectedId.value = id
  page.select(id)
  if (focus) focusTab(id)
}
async function addTab() {
  if (!page.current() || tabs.value.length >= 4 || pendingClose.value) return
  const intent = ++selectionIntent
  const focus = focusIntent()
  page.deselect()
  const tab = newTab()
  tabs.value.push(tab)
  selectedId.value = tab.id
  await nextTick()
  if (!page.current() || intent !== selectionIntent || !tabs.value.some(item => item.id === tab.id) || selectedId.value !== tab.id) return
  page.select(tab.id)
  if (focus()) focusTab(tab.id)
}
function tabKey(event: KeyboardEvent, id: string) {
  const index = tabs.value.findIndex(tab => tab.id === id)
  let next: number
  if (event.key === 'ArrowRight') next = (index + 1) % tabs.value.length
  else if (event.key === 'ArrowLeft') next = (index + tabs.value.length - 1) % tabs.value.length
  else if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = tabs.value.length - 1
  else return
  event.preventDefault()
  selectTab(tabs.value[next]!.id, true)
}
async function removeTab(id: string) {
  const index = tabs.value.findIndex(tab => tab.id === id)
  if (!page.current() || index < 0) return
  const intent = ++selectionIntent
  const focus = focusIntent()
  // 同步撤销 owner 后再删 UI；准确 socket 的 onclose 仍由页面 lease 记录负责。
  panelRefs.get(id)?.detach()
  tabs.value.splice(index, 1)
  if (!tabs.value.length) tabs.value.push(newTab())
  if (selectedId.value === id) selectedId.value = tabs.value[Math.min(index, tabs.value.length - 1)]!.id
  pendingClose.value = null
  await nextTick()
  if (!page.current() || intent !== selectionIntent) return
  page.select(selectedId.value)
  if (focus()) focusTab(selectedId.value)
}
async function requestClose(id: string) {
  if (!page.current() || pendingClose.value) return
  const state = panelRefs.get(id)?.state
  if (state === 'connecting' || state === 'connected') {
    const focus = focusIntent()
    pendingClose.value = id
    await nextTick()
    if (page.current() && pendingClose.value === id && focus()) closeCancel.value?.focus()
  } else await removeTab(id)
}
function confirmClose() {
  const id = pendingClose.value
  if (page.current() && id && tabs.value.some(tab => tab.id === id)) void removeTab(id)
}
function cancelClose() {
  const id = pendingClose.value
  pendingClose.value = null
  if (id) focusTab(id)
}
// 先登记页面撤销，确保面板清理中的同步回调也不能访问兄弟连接。
const abortPage = () => { selectionIntent++; pendingClose.value = null; page.invalidate('认证已失效，已停止终端交互；服务端资源回收可能仍在进行。', true) }
const hidePage = () => { selectionIntent++; pendingClose.value = null; page.invalidate('已离开页面并请求断开；服务端资源回收可能仍在进行。') }
const showPage = () => page.show()
page.session.signal.addEventListener('abort', abortPage, { once: true })
onMounted(() => {
  void systemStore.refresh({ inventory: true })
  window.addEventListener('pagehide', hidePage)
  window.addEventListener('pageshow', showPage)
})
onBeforeUnmount(() => {
  page.invalidate('已离开终端页面。', true)
  page.session.signal.removeEventListener('abort', abortPage)
  window.removeEventListener('pagehide', hidePage)
  window.removeEventListener('pageshow', showPage)
})
</script>

<template>
  <div class="page workspace-page terminal-page">
    <WorkspaceHeader title="终端" description="连接 NAS Root PTY 或运行中的 Docker 容器" :icon="SquareTerminal" :stats="stats" />
    <section class="terminal-tabs-panel panel" aria-label="终端标签与本页连接限制">
      <div class="terminal-tabs-row">
        <div ref="tablist" class="terminal-tablist" role="tablist" aria-label="终端标签">
          <button v-for="(tab, index) in tabs" :id="`tab-${tab.id}`" :key="tab.id" class="terminal-tab-item" :class="{ 'is-selected': tab.id === selectedId }" type="button" role="tab" :aria-selected="tab.id === selectedId" :aria-controls="`panel-${tab.id}`" :tabindex="tab.id === selectedId ? 0 : -1" :aria-label="`终端 ${index + 1}：${tab.summary.title}，${tab.summary.stateLabel}`" @click="selectTab(tab.id)" @keydown="tabKey($event, tab.id)">
            <span class="terminal-tab-title">{{ tab.summary.title }}</span><small>{{ tab.summary.stateLabel }}</small>
          </button>
        </div>
        <div class="terminal-tab-actions">
          <ActionButton class="terminal-tab-close" :icon="X" :disabled="!!pendingClose" :aria-label="`关闭当前标签：终端 ${tabs.findIndex(tab => tab.id === selectedId) + 1}，${selected.summary.title}`" @click="requestClose(selectedId)">关闭当前标签</ActionButton>
          <ActionButton class="terminal-add-tab" :icon="Plus" :disabled="tabs.length >= 4 || !!pendingClose" @click="addTab">新建标签</ActionButton>
        </div>
      </div>
      <p class="terminal-capacity-note">最多 4 个本地标签；本页连接 {{ page.occupiedCount }}/{{ page.limit }}（含连接中与关闭中）。服务端默认全局上限为 1，标签数量不代表可用名额。</p>
      <p v-if="page.retiredCount" class="terminal-retired-status" role="status">{{ page.retiredCount }} 个已关闭标签的本地连接仍在关闭中，收到连接关闭事件前继续计入本页限制；服务端资源回收可能更晚完成。</p>
    </section>
    <section v-if="closeTarget" class="terminal-close-confirm panel" role="alertdialog" aria-labelledby="terminal-close-title" aria-describedby="terminal-close-description" @keydown.esc.stop.prevent="cancelClose">
      <div><strong id="terminal-close-title">关闭 {{ closeTarget.summary.title }} 标签？</strong><p id="terminal-close-description">将请求断开此连接。不能保证远端任务已结束，已执行的命令无法撤回；本地连接关闭前仍计入本页限制，服务端可能仍在收尾。</p></div>
      <div class="terminal-close-actions"><button ref="closeCancel" type="button" @click="cancelClose">保留标签</button><ActionButton variant="danger" @click="confirmClose">断开并关闭标签</ActionButton></div>
    </section>
    <div v-for="tab in tabs" :id="`panel-${tab.id}`" :key="tab.id" role="tabpanel" :aria-labelledby="`tab-${tab.id}`" :hidden="tab.id !== selectedId" :inert="tab.id !== selectedId || !!pendingClose" :aria-hidden="tab.id !== selectedId ? true : undefined">
      <TerminalPanel :ref="instance => { if (instance) panelRefs.set(tab.id, instance as InstanceType<typeof TerminalPanel>); else panelRefs.delete(tab.id) }" :tab-id="tab.id" :selected="tab.id === selectedId" :page="page" :containers="containers" @summary="tab.summary = $event" />
    </div>
  </div>
</template>

<style scoped>
.terminal-tabs-panel { min-width: 0; padding: 12px 14px; }
.terminal-tabs-row { display: flex; align-items: center; gap: 12px; min-width: 0; }
.terminal-tablist { display: flex; flex: 1; gap: 6px; overflow-x: auto; min-width: 0; padding: 3px; }
.terminal-tab-item { display: grid; flex: 0 0 auto; min-width: 0; max-width: 270px; gap: 3px; padding: 8px 10px; text-align: left; color: var(--ncp-text); border: 1px solid var(--ncp-line); border-radius: 10px; background: var(--ncp-surface-quiet); }
.terminal-tab-item.is-selected { border-color: var(--ncp-primary); background: var(--ncp-primary-soft); }
.terminal-tab-title { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: .8rem; font-weight: 650; }
.terminal-tab-item small { color: var(--ncp-text-muted); font-size: .7rem; }
.terminal-tab-actions { display: flex; flex-shrink: 0; align-items: center; gap: 8px; }
.terminal-capacity-note,.terminal-retired-status { margin: 9px 3px 0; color: var(--ncp-text-muted); font-size: .73rem; line-height: 1.5; overflow-wrap: anywhere; }
.terminal-retired-status { color: var(--ncp-warning-strong); }
.terminal-close-confirm { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 16px; border-color: var(--ncp-warning); }
.terminal-close-confirm strong { overflow-wrap: anywhere; }
.terminal-close-confirm p { margin: 6px 0 0; color: var(--ncp-text-muted); font-size: .8rem; line-height: 1.5; }
.terminal-close-actions { display: flex; flex-shrink: 0; gap: 12px; align-items: center; }
.terminal-close-actions > button:first-child { min-height: 40px; padding: 0 10px; border-radius: 8px; }
button:focus-visible { outline: 2px solid var(--ncp-primary); outline-offset: 2px; }
[role=tabpanel][hidden] { display: none; }
@media(max-width:760px) { .terminal-tabs-panel { padding: 10px; } .terminal-tabs-row { flex-wrap: wrap; gap: 8px; } .terminal-tablist { flex-basis: 100%; } .terminal-tab-actions { width: 100%; justify-content: flex-end; flex-wrap: wrap; } .terminal-close-confirm { align-items: stretch; flex-direction: column; } .terminal-close-actions { justify-content: flex-end; flex-wrap: wrap; } }
</style>
