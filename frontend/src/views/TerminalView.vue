<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import { Container, Plug, Server, SquareTerminal, Unplug } from '@lucide/vue'
import { ElButton, ElOption, ElSelect } from 'element-plus'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'

import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { useSystemStore } from '@/stores/system'

type Target = 'host' | 'container'
const systemStore = useSystemStore()
const target = ref<Target>('host')
const containerId = ref('')
const state = ref<'idle' | 'connecting' | 'connected' | 'closed' | 'error'>('idle')
const terminalElement = ref<HTMLElement | null>(null)
let socket: WebSocket | null = null
let terminal: Terminal | null = null
let fitAddon: FitAddon | null = null
let resizeObserver: ResizeObserver | null = null

const containers = computed(() => (systemStore.inventory?.containers ?? []).filter((item) => item.state === 'running'))
const canConnect = computed(() => target.value === 'host' || Boolean(containerId.value))
const stats = computed<WorkspaceStat[]>(() => [
  { label: '会话状态', value: state.value === 'connected' ? '已连接' : state.value === 'connecting' ? '连接中' : '未连接', tone: state.value === 'connected' ? 'success' : undefined },
  { label: '终端目标', value: target.value === 'host' ? 'NAS 主机' : 'Docker 容器' },
  { label: '身份', value: target.value === 'host' ? 'root' : '容器用户' },
])

async function connect() {
  if (!canConnect.value || state.value === 'connecting' || state.value === 'connected') return
  close()
  state.value = 'connecting'
  await nextTick()
  const element = terminalElement.value
  if (!element) return
  terminal = new Terminal({
    cursorBlink: true,
    convertEol: true,
    fontFamily: "'JetBrains Mono Variable', 'Cascadia Mono', monospace",
    fontSize: 14,
    lineHeight: 1.32,
    scrollback: 5000,
    theme: {
      background: '#101722', foreground: '#dce7f5', cursor: '#65a5ff',
      selectionBackground: '#31547a', black: '#182231', brightBlack: '#718096',
      red: '#ff6b7a', green: '#43d39e', yellow: '#f6c85f', blue: '#65a5ff',
      magenta: '#c792ea', cyan: '#54d1db', white: '#dce7f5',
    },
  })
  fitAddon = new FitAddon()
  terminal.loadAddon(fitAddon)
  terminal.open(element)
  fitAddon.fit()
  terminal.writeln('\x1b[38;5;75mNCP Root Terminal\x1b[0m  正在建立会话…')

  const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
  const params = new URLSearchParams({ target: target.value })
  if (target.value === 'container') params.set('containerId', containerId.value)
  socket = new WebSocket(`${protocol}//${location.host}/ws/terminal?${params}`)
  socket.binaryType = 'arraybuffer'
  socket.onmessage = (event) => {
    if (typeof event.data === 'string') {
      const control = JSON.parse(event.data) as { type: string }
      if (control.type === 'started') {
        state.value = 'connected'
        resize()
      } else if (control.type === 'closed') {
        state.value = 'closed'
        terminal?.writeln('\r\n\x1b[38;5;214m会话已关闭\x1b[0m')
      }
      return
    }
    terminal?.write(new Uint8Array(event.data as ArrayBuffer))
  }
  socket.onerror = () => {
    state.value = 'error'
    terminal?.writeln('\r\n\x1b[31m终端连接失败，请确认 Agent 终端服务已启用。\x1b[0m')
  }
  socket.onclose = () => { if (state.value === 'connected') state.value = 'closed' }
  terminal.onData((data) => {
    if (socket?.readyState === WebSocket.OPEN) socket.send(new TextEncoder().encode(data))
  })
  terminal.onResize(({ rows, cols }) => {
    if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ type: 'resize', rows, cols }))
  })
  resizeObserver = new ResizeObserver(resize)
  resizeObserver.observe(element)
}

function resize() {
  try { fitAddon?.fit() } catch { /* 容器尚未完成布局时等待下一次观察。 */ }
}

function close() {
  resizeObserver?.disconnect()
  resizeObserver = null
  if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ type: 'close' }))
  socket?.close()
  socket = null
  terminal?.dispose()
  terminal = null
  fitAddon = null
  if (state.value !== 'idle') state.value = 'closed'
}

onBeforeUnmount(close)
</script>

<template>
  <div class="page workspace-page terminal-page">
    <WorkspaceHeader title="终端" description="连接 NAS Root PTY 或运行中的 Docker 容器" :icon="SquareTerminal" :stats="stats">
      <template #actions>
        <ElButton v-if="state !== 'connected'" type="primary" :disabled="!canConnect" :loading="state === 'connecting'" @click="connect"><Plug :size="16" />连接终端</ElButton>
        <ElButton v-else type="danger" plain @click="close"><Unplug :size="16" />断开连接</ElButton>
      </template>
    </WorkspaceHeader>

    <section class="terminal-toolbar panel">
      <div class="target-tabs">
        <button type="button" :class="{ active: target === 'host' }" :disabled="state === 'connected'" @click="target = 'host'"><Server :size="17" /><span><strong>NAS 主机</strong><small>Root Shell</small></span></button>
        <button type="button" :class="{ active: target === 'container' }" :disabled="state === 'connected'" @click="target = 'container'"><Container :size="17" /><span><strong>Docker 容器</strong><small>容器 Shell</small></span></button>
      </div>
      <ElSelect v-if="target === 'container'" v-model="containerId" filterable :disabled="state === 'connected'" placeholder="选择运行中的容器">
        <ElOption v-for="item in containers" :key="item.id" :label="`${item.name} · ${item.image}`" :value="item.id" />
      </ElSelect>
      <span class="terminal-hint">支持 Ctrl+C、窗口自适应；离开页面时自动关闭并回收 PTY。</span>
    </section>

    <section class="terminal-frame panel">
      <header><span :class="['connection-dot', `connection-dot--${state}`]"></span><strong>{{ target === 'host' ? 'root@DH4300Plus' : containers.find(item => item.id === containerId)?.name || 'container' }}</strong><small>{{ state }}</small></header>
      <div v-if="state === 'idle' || state === 'closed'" class="terminal-placeholder"><SquareTerminal :size="34" /><strong>选择目标并连接终端</strong><span>每次只保留一个前台会话，断开后资源立即回收。</span></div>
      <div ref="terminalElement" class="terminal-canvas"></div>
    </section>
  </div>
</template>

<style scoped>
.terminal-toolbar{display:flex;min-height:66px;align-items:center;gap:12px;padding:10px 12px}.target-tabs{display:flex;gap:4px;padding:4px;border:1px solid var(--ncp-line);border-radius:11px;background:var(--ncp-surface-quiet)}.target-tabs button{display:flex;min-height:42px;align-items:center;gap:8px;padding:0 13px;border-radius:8px;color:var(--ncp-text-muted)}.target-tabs button.active{background:#fff;box-shadow:0 3px 10px rgb(24 42 72 / 8%);color:var(--ncp-primary-strong)}.target-tabs span{display:grid;text-align:left}.target-tabs strong{font-size:.76rem}.target-tabs small{font-size:.62rem}.terminal-toolbar :deep(.el-select){width:min(360px,30vw)}.terminal-toolbar :deep(.el-select__wrapper){min-height:42px;border-radius:9px}.terminal-hint{margin-left:auto;color:var(--ncp-text-subtle);font-size:.72rem}.terminal-frame{position:relative;min-height:620px;overflow:hidden;background:#101722}.terminal-frame>header{display:flex;height:44px;align-items:center;gap:8px;padding:0 14px;border-bottom:1px solid #273346;background:#182231;color:#dce7f5}.terminal-frame>header strong{font-family:var(--ncp-font-mono);font-size:.74rem}.terminal-frame>header small{margin-left:auto;color:#718096;font-family:var(--ncp-font-mono);font-size:.64rem}.connection-dot{width:8px;height:8px;border-radius:50%;background:#718096}.connection-dot--connected{background:#43d39e;box-shadow:0 0 0 4px rgb(67 211 158 / 12%)}.connection-dot--connecting{background:#f6c85f}.connection-dot--error{background:#ff6b7a}.terminal-canvas{position:absolute;inset:56px 10px 10px}.terminal-canvas :deep(.xterm){height:100%;padding:4px}.terminal-placeholder{position:absolute;z-index:2;inset:44px 0 0;display:grid;place-items:center;align-content:center;gap:7px;background:#101722;color:#718096}.terminal-placeholder strong{color:#dce7f5;font-size:.86rem}.terminal-placeholder span{font-size:.72rem}@media(max-width:760px){.terminal-toolbar{align-items:stretch;flex-direction:column}.target-tabs button{flex:1}.target-tabs{display:flex}.terminal-toolbar :deep(.el-select){width:100%}.terminal-hint{margin-left:0}.terminal-frame{min-height:calc(100dvh - 310px)}}
</style>
