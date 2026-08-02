<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { Container, Plug, Server, SquareTerminal, Trash2, Unplug } from '@lucide/vue'
import { ElButton, ElOption, ElSelect } from 'element-plus'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'

import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import { createTerminalPastePayload, isTerminalPasteShortcut } from '@/domain/terminal'
import { useSystemStore } from '@/stores/system'

type Target = 'host' | 'container'
const systemStore = useSystemStore()
const target = ref<Target>('host')
const containerId = ref('')
const state = ref<'idle' | 'connecting' | 'connected' | 'closed' | 'error'>('idle')
const shellEnhancement = ref<'blesh' | 'native' | 'unsupported'>('native')
const shellReason = ref('')
const terminalElement = ref<HTMLElement | null>(null)
let socket: WebSocket | null = null
let terminal: Terminal | null = null
let fitAddon: FitAddon | null = null
let resizeObserver: ResizeObserver | null = null
let resizeTimer: number | undefined
let handshakeTimer: number | undefined
let pasteTarget: HTMLElement | null = null
let terminalReady = false

const containers = computed(() => (systemStore.inventory?.containers ?? []).filter((item) => item.state === 'running'))
const canConnect = computed(() => target.value === 'host' || Boolean(containerId.value))
const canClearTerminal = computed(() => state.value === 'connecting' || state.value === 'connected' || state.value === 'error')
const stateLabel = computed(() => ({
  idle: '未连接',
  connecting: '连接中',
  connected: '已连接',
  closed: '已关闭',
  error: '连接失败',
}[state.value]))
const enhancementHint = computed(() => {
  if (state.value !== 'connected') return '粘贴：Ctrl+V / ⌘V · 多行：Enter 换行，Ctrl+J 执行 · 输出保留 ANSI 配色。'
  if (shellReason.value) return shellReason.value
  if (shellEnhancement.value === 'blesh') return '命令输入高亮 + 常用输出语义配色 · ble.sh'
  if (shellEnhancement.value === 'unsupported') return '当前 Shell 不支持输入高亮，已保留 ANSI 输出配色'
  return '原生 Shell 交互 · 已保留 ANSI 输出配色'
})
const stats = computed<WorkspaceStat[]>(() => [
  { label: '会话状态', value: state.value === 'connected' ? '已连接' : state.value === 'connecting' ? '连接中' : '未连接', tone: state.value === 'connected' ? 'success' : undefined },
  { label: '终端目标', value: target.value === 'host' ? 'NAS 主机' : 'Docker 容器' },
  { label: '身份', value: target.value === 'host' ? 'root' : '容器用户' },
])

onMounted(() => void systemStore.refresh({ inventory: true }))

async function connect() {
  if (!canConnect.value || state.value === 'connecting' || state.value === 'connected') return
  close()
  state.value = 'connecting'
  await nextTick()
  const element = terminalElement.value
  if (!element) return
  terminal = new Terminal({
    cursorBlink: true,
    cursorStyle: 'bar',
    cursorWidth: 2,
    convertEol: true,
    fontFamily: "'JetBrains Mono Variable', 'Cascadia Mono', monospace",
    fontSize: 14,
    lineHeight: 1.32,
    scrollback: 5000,
    theme: {
      background: '#fbfcfe', foreground: '#25354b', cursor: '#3474d4', cursorAccent: '#fbfcfe',
      selectionBackground: '#dbe9fb', black: '#263548', brightBlack: '#718096',
      red: '#c95361', brightRed: '#df6673', green: '#23866f', brightGreen: '#36a287',
      yellow: '#b87622', brightYellow: '#cf933a', blue: '#3474d4', brightBlue: '#5792e5',
      magenta: '#875eae', brightMagenta: '#a477c9', cyan: '#23828d', brightCyan: '#3d9da7',
      white: '#dfe6ef', brightWhite: '#ffffff',
    },
  })
  fitAddon = new FitAddon()
  terminal.loadAddon(fitAddon)
  terminal.open(element)
  fitAddon.fit()
  terminalReady = false
  terminal.writeln('\x1b[38;5;25mNCP 终端\x1b[0m  正在建立会话…')
  pasteTarget = element
  pasteTarget.addEventListener('paste', handlePaste, true)
  terminal.attachCustomKeyEventHandler((event) => {
    if (event.type === 'keydown' && isTerminalPasteShortcut(event)) {
      // Let the browser create the native paste event. handlePaste captures
      // both keyboard and context-menu paste before xterm can add its own
      // bracketed-paste frame a second time.
      return false
    }
    return true
  })

  const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
  const params = new URLSearchParams({
    target: target.value,
    rows: String(terminal.rows),
    cols: String(terminal.cols),
  })
  if (target.value === 'container') params.set('containerId', containerId.value)
  socket = new WebSocket(`${protocol}//${location.host}/ws/terminal?${params}`)
  socket.binaryType = 'arraybuffer'
  handshakeTimer = window.setTimeout(() => {
    if (state.value !== 'connecting' || terminalReady) return
    failTerminal('终端握手超时，请检查 Agent 终端服务状态。')
    socket?.close()
  }, 10_000)
  socket.onmessage = (event) => {
    if (typeof event.data === 'string') {
      let control: {
        type: string
        enhancement?: 'blesh' | 'native' | 'unsupported'
        reason?: string
        message?: string
        rows?: number
        cols?: number
      }
      try {
        control = JSON.parse(event.data) as typeof control
      } catch {
        failTerminal('收到无法识别的终端控制帧。')
        socket?.close()
        return
      }
      if (control.type === 'started') {
        window.clearTimeout(handshakeTimer)
        handshakeTimer = undefined
        terminalReady = true
        state.value = 'connected'
        shellEnhancement.value = control.enhancement ?? 'native'
        shellReason.value = control.reason ?? ''
        terminal?.clear()
        scheduleResize()
        terminal?.focus()
      } else if (control.type === 'error') {
        failTerminal(control.message ?? '终端会话建立失败。')
        socket?.close()
      } else if (control.type === 'closed') {
        window.clearTimeout(handshakeTimer)
        handshakeTimer = undefined
        terminalReady = false
        state.value = 'closed'
        terminal?.writeln('\r\n\x1b[38;5;214m会话已关闭\x1b[0m')
      }
      return
    }
    terminal?.write(new Uint8Array(event.data as ArrayBuffer))
  }
  socket.onerror = () => {
    failTerminal('终端连接失败，请确认 Agent 终端服务已启用。')
  }
  socket.onclose = () => {
    terminalReady = false
    window.clearTimeout(handshakeTimer)
    handshakeTimer = undefined
    if (state.value === 'connecting') failTerminal('终端在握手完成前断开。')
    else if (state.value === 'connected') state.value = 'closed'
  }
  terminal.onData((data) => {
    sendTerminalInput(data)
  })
  terminal.onResize(({ rows, cols }) => {
    if (terminalReady && socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ type: 'resize', rows, cols }))
  })
  resizeObserver = new ResizeObserver(scheduleResize)
  resizeObserver.observe(element)
  window.addEventListener('resize', scheduleResize)
  window.visualViewport?.addEventListener('resize', scheduleResize)
}

function handlePaste(event: ClipboardEvent) {
  event.preventDefault()
  event.stopImmediatePropagation()
  const text = event.clipboardData?.getData('text/plain') ?? ''
  if (text) sendPastedText(text)
}

function sendPastedText(text: string) {
  if (!terminalReady) return
  sendTerminalInput(createTerminalPastePayload(text, terminal?.modes.bracketedPasteMode === true))
  terminal?.focus()
}

function sendTerminalInput(data: string | Uint8Array) {
  if (!terminalReady || socket?.readyState !== WebSocket.OPEN) return
  socket.send(typeof data === 'string' ? new TextEncoder().encode(data) : data)
}

function failTerminal(message: string) {
  terminalReady = false
  state.value = 'error'
  terminal?.writeln(`\r\n\x1b[31m${message}\x1b[0m`)
}

function clearTerminal() {
  terminal?.clear()
  terminal?.scrollToBottom()
  terminal?.focus()
}

function resize() {
  try { fitAddon?.fit() } catch { /* 容器尚未完成布局时等待下一次观察。 */ }
}

function scheduleResize() {
  window.clearTimeout(resizeTimer)
  resizeTimer = window.setTimeout(resize, 80)
}

function close() {
  terminalReady = false
  shellReason.value = ''
  window.clearTimeout(resizeTimer)
  resizeTimer = undefined
  window.clearTimeout(handshakeTimer)
  handshakeTimer = undefined
  window.removeEventListener('resize', scheduleResize)
  window.visualViewport?.removeEventListener('resize', scheduleResize)
  resizeObserver?.disconnect()
  resizeObserver = null
  pasteTarget?.removeEventListener('paste', handlePaste, true)
  pasteTarget = null
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
      <ElSelect v-if="target === 'container'" v-model="containerId" filterable :disabled="state === 'connected'" placeholder="搜索并选择运行中的容器">
        <ElOption v-for="item in containers" :key="item.id" :label="`${item.name} · ${item.image}`" :value="item.id" />
      </ElSelect>
      <span class="terminal-hint">{{ enhancementHint }}</span>
    </section>

    <section :class="['terminal-frame', 'panel', { 'terminal-frame--idle': state === 'idle' || state === 'closed' }]">
      <header><span :class="['connection-dot', `connection-dot--${state}`]"></span><strong>{{ target === 'host' ? 'root@DH4300Plus' : containers.find(item => item.id === containerId)?.name || 'container' }}</strong><button v-if="canClearTerminal" class="terminal-clear-button" type="button" aria-label="清空终端内容" title="清空终端内容" @click="clearTerminal"><Trash2 :size="15" /><span>清空内容</span></button><small>{{ stateLabel }}</small></header>
      <div v-if="state === 'idle' || state === 'closed'" class="terminal-placeholder"><SquareTerminal :size="32" /><strong>准备连接 {{ target === 'host' ? 'NAS 主机' : 'Docker 容器' }}</strong><span>支持 Bash 补全、历史命令、Ctrl+C 与窗口自适应。</span><ElButton type="primary" :disabled="!canConnect" @click="connect"><Plug :size="16" />连接终端</ElButton></div>
      <div ref="terminalElement" class="terminal-canvas"></div>
    </section>
  </div>
</template>

<style scoped>
.terminal-toolbar{display:flex;min-height:68px;align-items:center;gap:12px;padding:11px 14px}.target-tabs{display:flex;gap:4px;padding:4px;border:1px solid var(--ncp-line);border-radius:12px;background:var(--ncp-surface-quiet)}.target-tabs button{display:flex;min-height:42px;align-items:center;gap:8px;padding:0 14px;border-radius:9px;color:var(--ncp-text-muted)}.target-tabs button.active{background:#fff;box-shadow:0 4px 14px rgba(35,60,96,.09);color:var(--ncp-primary-strong)}.target-tabs span{display:grid;text-align:left}.target-tabs strong{font-size:.82rem}.target-tabs small{font-size:.72rem}.terminal-toolbar :deep(.el-select){width:min(380px,32vw)}.terminal-toolbar :deep(.el-select__wrapper){min-height:42px;border-radius:10px}.terminal-hint{margin-left:auto;color:var(--ncp-text-subtle);font-size:.8rem}.terminal-frame{position:relative;min-height:calc(100dvh - 286px);overflow:hidden;border-color:#d8e1ed;background:#fbfcfe;box-shadow:0 14px 36px rgba(41,68,105,.075);transition:min-height var(--ncp-duration-base) var(--ncp-ease-out)}.terminal-frame--idle{min-height:340px}.terminal-frame>header{display:flex;height:48px;align-items:center;gap:8px;padding:0 16px;border-bottom:1px solid #dce4ee;background:linear-gradient(180deg,#f8fafc,#f1f5fa);color:#24344d}.terminal-frame>header strong{font-family:var(--ncp-font-mono);font-size:.82rem}.terminal-frame>header small{margin-left:auto;color:#77869b;font-family:var(--ncp-font-mono);font-size:.74rem}.connection-dot{width:8px;height:8px;border-radius:50%;background:#8996a8}.connection-dot--connected{background:#23866f;box-shadow:0 0 0 4px var(--ncp-success-soft)}.connection-dot--connecting{background:var(--ncp-warning)}.connection-dot--error{background:var(--ncp-danger)}.terminal-canvas{position:absolute;inset:60px 13px 13px}.terminal-canvas :deep(.xterm){height:100%;padding:7px}.terminal-placeholder{position:absolute;z-index:2;inset:48px 0 0;display:grid;place-items:center;align-content:center;gap:9px;padding:24px;background:radial-gradient(circle at 50% 15%,rgba(52,116,212,.06),transparent 38%),#fbfcfe;color:#77869b}.terminal-placeholder>svg{padding:11px;border-radius:13px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong);box-sizing:content-box}.terminal-placeholder strong{color:#24344d;font-size:1rem}.terminal-placeholder span{font-size:.82rem}.terminal-placeholder :deep(.el-button){margin-top:8px}@media(max-width:760px){.terminal-toolbar{align-items:stretch;flex-direction:column}.target-tabs button{flex:1}.target-tabs{display:flex}.terminal-toolbar :deep(.el-select){width:100%}.terminal-hint{margin-left:0}.terminal-frame{min-height:calc(100dvh - 330px)}.terminal-frame--idle{min-height:310px}}
.terminal-toolbar :deep(.el-select__input),.terminal-toolbar :deep(.el-select__input-wrapper){border:0!important;outline:0!important;box-shadow:none!important}.terminal-frame{border:1px solid var(--ncp-line);background:#fbfcfe}.terminal-canvas{inset:49px 0 0;padding:12px;background:#fbfcfe}.terminal-canvas :deep(.xterm){height:100%;padding:0;background:#fbfcfe}.terminal-canvas :deep(.xterm-viewport),.terminal-canvas :deep(.xterm-screen),.terminal-canvas :deep(.xterm-screen canvas){background-color:#fbfcfe!important}.terminal-canvas :deep(.xterm-viewport){scrollbar-color:#c7d3e1 transparent}.terminal-canvas :deep(.xterm-helper-textarea){outline:0}.terminal-frame>header{background:#f6f8fb}
.terminal-page :deep(.el-button--danger.is-plain:hover),.terminal-page :deep(.el-button--danger.is-plain:focus-visible){border-color:rgba(201,83,97,.34);background:var(--ncp-danger-soft);color:var(--ncp-danger-strong)}
.terminal-frame,.terminal-frame--idle{height:clamp(578px,calc(100dvh - 320px),735px);min-height:578px}.terminal-canvas{min-height:0}.terminal-canvas :deep(.xterm-viewport){scrollbar-width:none!important;-ms-overflow-style:none}.terminal-canvas :deep(.xterm-viewport::-webkit-scrollbar){display:none}.terminal-clear-button{display:inline-flex;min-height:30px;align-items:center;gap:7px;margin-left:auto;padding:0 10px;border:1px solid var(--ncp-line);border-radius:8px;background:#fff;color:var(--ncp-text-muted);font:inherit;font-size:.75rem}.terminal-clear-button:hover,.terminal-clear-button:focus-visible{border-color:var(--ncp-primary);color:var(--ncp-primary-strong);outline:0}.terminal-frame>header small{margin-left:0}@media(max-width:760px){.terminal-frame,.terminal-frame--idle{height:calc(100dvh - 275px);min-height:485px}.terminal-clear-button span{display:none}.terminal-clear-button{padding:0 8px}}
</style>
