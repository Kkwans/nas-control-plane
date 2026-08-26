<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { CircleHelp, Container, Plug, Server, SquareTerminal, Trash2, Unplug } from '@lucide/vue'
import { ElDialog } from 'element-plus'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'

import WorkspaceHeader, { type WorkspaceStat } from '@/components/WorkspaceHeader.vue'
import ActionButton from '@/components/ActionButton.vue'
import NcpSelect from '@/components/NcpSelect.vue'
import {
  createTerminalPastePayload,
  describeTerminalCapability,
  formatTerminalEnhancement,
  formatTerminalShell,
  getTerminalCapabilityState,
  isMultilineTerminalPaste,
  isTerminalLiteralNextShortcut,
  isTerminalPasteShortcut,
  normalizeTerminalCapabilities,
  normalizeTerminalPaste,
  supportsSafeMultilinePaste,
  terminalShortcuts,
  type TerminalCapabilities,
  type TerminalCapabilityState,
} from '@/domain/terminal'
import { useSystemStore } from '@/stores/system'

type Target = 'host' | 'container'
type PendingPaste = { text: string; lineCount: number; safe: boolean }
const terminalErrorMessages: Record<string, string> = {
  TERMINAL_UNAVAILABLE: '终端服务暂不可用，请确认 Root Agent 正常运行。',
  TERMINAL_INITIALIZATION_FAILED: '终端会话初始化失败，请检查 Agent 终端能力。',
  TERMINAL_INITIALIZATION_TIMEOUT: '终端初始化超时，请稍后重试。',
  TERMINAL_TARGET_UNAVAILABLE: '当前终端目标不可用，请确认主机或容器状态。',
  TERMINAL_INPUT_INVALID: '终端输入无效或超出允许范围。',
  TERMINAL_SESSION_FAILED: '终端会话异常结束，请重新连接。',
}
const systemStore = useSystemStore()
const target = ref<Target>('host')
const containerId = ref('')
const state = ref<'idle' | 'connecting' | 'connected' | 'closed' | 'error'>('idle')
const shellName = ref('')
const shellEnhancement = ref('')
const shellReason = ref('')
const capabilities = ref<TerminalCapabilities | null>(null)
const statusMessage = ref('')
const shortcutDialogOpen = ref(false)
const pendingPaste = ref<PendingPaste | null>(null)
const pasteConfirmRef = ref<HTMLButtonElement | null>(null)
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
const containerOptions = computed(() => containers.value.map((item) => ({ label: `${item.name} · ${item.image}`, value: item.id })))
const canConnect = computed(() => target.value === 'host' || Boolean(containerId.value))
const canClearTerminal = computed(() => state.value === 'connecting' || state.value === 'connected' || state.value === 'error')
const stateLabel = computed(() => ({
  idle: '未连接',
  connecting: '连接中',
  connected: '已连接',
  closed: '已断开',
  error: '连接失败',
}[state.value]))
const shellLabel = computed(() => formatTerminalShell(shellName.value))
const enhancementLabel = computed(() => formatTerminalEnhancement(shellEnhancement.value))
const editorCapabilityItems = computed(() => {
  const readlineState = getTerminalCapabilityState(capabilities.value, 'readline')
  let highlightingState: TerminalCapabilityState = 'unknown'
  if (shellEnhancement.value === 'blesh' && readlineState !== 'unsupported') highlightingState = 'supported'
  else if (shellEnhancement.value === 'native' || shellEnhancement.value === 'readline' || shellEnhancement.value === 'unsupported') highlightingState = 'unsupported'
  return [
    { key: 'completion', label: '命令补全', state: readlineState },
    { key: 'highlighting', label: '输入高亮', state: highlightingState },
  ]
})
const capabilityItems = computed(() => [
  { key: 'resize' as const, label: '窗口调整', value: describeTerminalCapability(capabilities.value?.resize) },
  { key: 'readline' as const, label: 'readline', value: describeTerminalCapability(capabilities.value?.readline) },
  { key: 'bracketedPaste' as const, label: 'bracketed paste', value: describeTerminalCapability(capabilities.value?.bracketedPaste) },
  { key: 'multilinePaste' as const, label: '多行粘贴', value: describeTerminalCapability(capabilities.value?.multilinePaste) },
  { key: 'ansiColors' as const, label: 'ANSI 配色', value: describeTerminalCapability(capabilities.value?.ansiColors) },
])
const capabilityWarning = computed(() => {
  if (state.value !== 'connected') return ''
  const unknown = capabilityItems.value.filter((item) => getTerminalCapabilityState(capabilities.value, item.key) === 'unknown')
  if (unknown.length === capabilityItems.value.length) return '服务端未报告终端能力，已使用兼容模式；多行粘贴会先确认并按行发送。'
  if (unknown.length > 0) return `服务端未完整报告终端能力（${unknown.map((item) => item.label).join('、')}），未报告项按不支持处理。`
  return ''
})
const pendingPasteDescription = computed(() => {
  if (!pendingPaste.value) return ''
  if (pendingPaste.value.safe) return `将把 ${pendingPaste.value.lineCount} 行内容交给当前 Shell 的 bracketed paste 处理。`
  return '当前会话未同时报告 multiline paste 与 bracketed paste；确认后将逐行发送，每行以 Enter 提交。'
})
const pendingPasteActionLabel = computed(() => pendingPaste.value?.safe ? '粘贴到终端' : '按行发送')
const stats = computed<WorkspaceStat[]>(() => [
  { label: '会话状态', value: stateLabel.value, tone: state.value === 'connected' ? 'success' : undefined },
  { label: '终端目标', value: target.value === 'host' ? 'NAS 主机' : 'Docker 容器' },
  { label: '身份', value: target.value === 'host' ? 'root' : '容器用户' },
])

onMounted(() => void systemStore.refresh({ inventory: true }))

async function connect() {
  if (!canConnect.value || state.value === 'connecting' || state.value === 'connected') return
  close()
  state.value = 'connecting'
  statusMessage.value = ''
  shellName.value = ''
  shellEnhancement.value = ''
  shellReason.value = ''
  capabilities.value = null
  pendingPaste.value = null
  await nextTick()
  const element = terminalElement.value
  if (!element) {
    failTerminal('终端输入区域尚未准备好，请稍后重试。')
    return
  }
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
  terminal.attachCustomKeyEventHandler((event) => {
    if (isTerminalPasteShortcut(event)) return false
    if (isTerminalLiteralNextShortcut(event)) {
      if (event.type === 'keydown') {
        event.preventDefault()
        sendTerminalInput('\u0016')
      }
      return false
    }
    return true
  })
  terminal.writeln('\x1b[38;5;25mNCP 终端\x1b[0m  正在建立会话…')
  pasteTarget = element
  pasteTarget.addEventListener('paste', handlePaste, true)

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
        shell?: string
        enhancement?: string
        reason?: string
        message?: string
        code?: string
        capabilities?: unknown
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
        shellName.value = control.shell ?? ''
        shellEnhancement.value = control.enhancement ?? ''
        shellReason.value = control.reason ?? ''
        capabilities.value = normalizeTerminalCapabilities(control.capabilities)
        statusMessage.value = capabilityWarning.value
        terminal?.clear()
        scheduleResize()
        terminal?.focus()
      } else if (control.type === 'error') {
        failTerminal(terminalErrorMessages[control.code ?? ''] ?? control.message ?? '终端会话建立失败。')
        socket?.close()
      } else if (control.type === 'closed') {
        window.clearTimeout(handshakeTimer)
        handshakeTimer = undefined
        terminalReady = false
        state.value = 'closed'
        statusMessage.value = '终端会话已由服务端关闭，可重新连接。'
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
    else if (state.value === 'connected') {
      state.value = 'closed'
      statusMessage.value = '终端连接已断开，可重新连接。'
    }
  }
  terminal.onData((data) => {
    sendTerminalInput(data)
  })
  terminal.onResize(({ rows, cols }) => {
    if (capabilities.value?.resize === false) return
    if (terminalReady && socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ type: 'resize', rows, cols }))
  })
  resizeObserver = new ResizeObserver(scheduleResize)
  resizeObserver.observe(element)
  window.addEventListener('resize', scheduleResize)
  window.visualViewport?.addEventListener('resize', scheduleResize)
}

function handlePaste(event: ClipboardEvent) {
  const text = event.clipboardData?.getData('text/plain') ?? ''
  if (!text) return
  event.preventDefault()
  event.stopImmediatePropagation()
  if (!terminalReady || state.value !== 'connected') {
    statusMessage.value = '终端尚未就绪，未写入剪贴板内容。'
    return
  }
  if (isMultilineTerminalPaste(text)) {
    const lineCount = normalizeTerminalPaste(text).split('\n').length
    pendingPaste.value = { text, lineCount, safe: supportsSafeMultilinePaste(capabilities.value) }
    statusMessage.value = pendingPasteDescription.value
    void nextTick(() => pasteConfirmRef.value?.focus())
    return
  }
  sendPastedText(text)
}

function sendPastedText(text: string, bracketedPaste = supportsSafeMultilinePaste(capabilities.value)) {
  const sent = sendTerminalInput(createTerminalPastePayload(text, bracketedPaste))
  if (sent) {
    statusMessage.value = bracketedPaste ? '已将内容交给当前 Shell 的 bracketed paste 处理。' : '已将内容写入终端。'
    terminal?.focus()
  }
}

function confirmPaste() {
  const paste = pendingPaste.value
  if (!paste) return
  pendingPaste.value = null
  sendPastedText(paste.text, paste.safe)
}

function cancelPaste() {
  if (!pendingPaste.value) return
  pendingPaste.value = null
  statusMessage.value = '已取消多行粘贴。'
  terminal?.focus()
}

function sendTerminalInput(data: string | Uint8Array): boolean {
  if (!terminalReady || socket?.readyState !== WebSocket.OPEN) return false
  const payload = typeof data === 'string' ? new TextEncoder().encode(data) : data
  if (payload.byteLength === 0) return false
  try {
    socket.send(payload)
    return true
  } catch {
    failTerminal('终端输入发送失败，连接可能已经断开。')
    return false
  }
}

function describeTerminalCapabilityState(state: TerminalCapabilityState): string {
  if (state === 'supported') return '支持'
  if (state === 'unsupported') return '不支持'
  return '未报告'
}

function failTerminal(message: string) {
  terminalReady = false
  state.value = 'error'
  pendingPaste.value = null
  statusMessage.value = message
  terminal?.writeln(`\r\n\x1b[31m${message}\x1b[0m`)
}

function clearTerminal() {
  terminal?.clear()
  terminal?.scrollToBottom()
  terminal?.focus()
  statusMessage.value = '终端内容已清空。'
}

function resize() {
  try { fitAddon?.fit() } catch { /* 容器尚未完成布局时等待下一次观察。 */ }
}

function scheduleResize() {
  window.clearTimeout(resizeTimer)
  resizeTimer = window.setTimeout(resize, 80)
}

function close() {
  const wasActive = state.value === 'connecting' || state.value === 'connected'
  terminalReady = false
  pendingPaste.value = null
  shellName.value = ''
  shellEnhancement.value = ''
  shellReason.value = ''
  capabilities.value = null
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
  if (state.value !== 'idle') {
    state.value = 'closed'
    if (wasActive) statusMessage.value = '终端连接已断开，可重新连接。'
  }
}

onBeforeUnmount(close)
</script>

<template>
  <div class="page workspace-page terminal-page">
    <WorkspaceHeader title="终端" description="连接 NAS Root PTY 或运行中的 Docker 容器" :icon="SquareTerminal" :stats="stats">
    </WorkspaceHeader>

    <section class="terminal-toolbar panel">
      <div class="target-tabs">
        <button type="button" :class="{ active: target === 'host' }" :disabled="state === 'connected'" @click="target = 'host'"><Server :size="17" /><span><strong>NAS 主机</strong><small>Root Shell</small></span></button>
        <button type="button" :class="{ active: target === 'container' }" :disabled="state === 'connected'" @click="target = 'container'"><Container :size="17" /><span><strong>Docker 容器</strong><small>容器 Shell</small></span></button>
      </div>
      <NcpSelect v-if="target === 'container'" v-model="containerId" :options="containerOptions" accessible-label="选择运行中的 Docker 容器" filterable :disabled="state === 'connected'" placeholder="搜索并选择运行中的容器" />
      <p v-if="statusMessage" class="terminal-live-status" role="status" aria-live="polite">{{ statusMessage }}</p>
      <div class="terminal-toolbar-actions">
        <ActionButton :icon="CircleHelp" @click="shortcutDialogOpen = true">快捷键</ActionButton>
        <ActionButton v-if="state !== 'connected'" variant="primary" :icon="Plug" :disabled="!canConnect" :loading="state === 'connecting'" @click="connect">连接终端</ActionButton>
        <ActionButton v-else variant="danger" :icon="Unplug" @click="close">断开连接</ActionButton>
      </div>
    </section>

    <ElDialog v-model="shortcutDialogOpen" title="终端快捷键与粘贴" width="min(560px, 92vw)" append-to-body>
      <div class="terminal-shortcut-dialog">
        <p>Ctrl+V / ⌘V 会把剪贴板内容写入当前 PTY；原 Ctrl+V 的 literal-next 功能改为 Ctrl+Q。多行内容始终先确认。</p>
        <ul>
          <li v-for="shortcut in terminalShortcuts" :key="shortcut.keys"><kbd>{{ shortcut.keys }}</kbd><span>{{ shortcut.action }}</span></li>
        </ul>
        <details v-if="state === 'connected'" class="terminal-capability-details">
          <summary>当前会话能力</summary>
          <div class="terminal-capabilities" aria-label="当前终端能力">
            <span class="capability-chip capability-chip--identity"><strong>{{ shellLabel }}</strong><em>{{ enhancementLabel }}</em></span>
            <span v-for="item in capabilityItems" :key="item.key" :class="['capability-chip', `capability-chip--${getTerminalCapabilityState(capabilities, item.key)}`]"><span>{{ item.label }}</span><em>{{ item.value }}</em></span>
            <span v-for="item in editorCapabilityItems" :key="item.key" :class="['capability-chip', `capability-chip--${item.state}`]"><span>{{ item.label }}</span><em>{{ describeTerminalCapabilityState(item.state) }}</em></span>
          </div>
          <p v-if="shellReason">{{ shellReason }}</p>
        </details>
      </div>
    </ElDialog>

    <section :class="['terminal-frame', 'panel', { 'terminal-frame--idle': state === 'idle' || state === 'closed' }]" role="region" aria-label="终端会话" :aria-busy="state === 'connecting'">
      <header><span :class="['connection-dot', `connection-dot--${state}`]" aria-hidden="true"></span><strong>{{ target === 'host' ? 'root@DH4300Plus' : containers.find(item => item.id === containerId)?.name || 'container' }}</strong><ActionButton v-if="canClearTerminal" class="terminal-clear-button" variant="ghost" size="sm" :icon="Trash2" aria-label="清空终端内容" @click="clearTerminal">清空内容</ActionButton><small role="status" aria-live="polite">{{ stateLabel }}</small></header>
      <div v-if="pendingPaste" class="paste-confirmation" role="dialog" aria-modal="false" aria-labelledby="terminal-paste-title" aria-describedby="terminal-paste-description" @keydown.esc.stop.prevent="cancelPaste">
        <div class="paste-confirmation-copy"><strong id="terminal-paste-title">确认多行粘贴</strong><p id="terminal-paste-description">{{ pendingPasteDescription }}</p></div>
        <div class="paste-confirmation-actions"><button ref="pasteConfirmRef" class="paste-confirmation-primary" type="button" @click="confirmPaste">{{ pendingPasteActionLabel }}</button><button class="paste-confirmation-secondary" type="button" @click="cancelPaste">取消</button></div>
      </div>
      <div v-if="state === 'idle' || state === 'closed'" class="terminal-placeholder"><SquareTerminal :size="32" /><strong>准备连接 {{ target === 'host' ? 'NAS 主机' : 'Docker 容器' }}</strong><span>连接后按会话真实能力提供补全、历史、多行粘贴与窗口自适应。</span><ActionButton variant="primary" :icon="Plug" :disabled="!canConnect" @click="connect">连接终端</ActionButton></div>
      <div ref="terminalElement" class="terminal-canvas" role="region" aria-label="终端输出和输入区域"></div>
    </section>
  </div>
</template>

<style scoped>
.terminal-toolbar{display:flex;min-height:68px;align-items:center;gap:12px;padding:11px 14px}.target-tabs{display:flex;gap:4px;padding:4px;border:1px solid var(--ncp-line);border-radius:12px;background:var(--ncp-surface-quiet)}.target-tabs button{display:flex;min-height:42px;align-items:center;gap:8px;padding:0 14px;border-radius:9px;color:var(--ncp-text-muted)}.target-tabs button.active{background:#fff;box-shadow:0 4px 14px rgba(35,60,96,.09);color:var(--ncp-primary-strong)}.target-tabs span{display:grid;text-align:left}.target-tabs strong{font-size:.82rem}.target-tabs small{font-size:.72rem}.terminal-toolbar :deep(.el-select){width:min(380px,32vw)}.terminal-toolbar :deep(.el-select__wrapper){min-height:42px;border-radius:10px}.terminal-hint{margin-left:auto;color:var(--ncp-text-subtle);font-size:.8rem}.terminal-frame{position:relative;min-height:calc(100dvh - 286px);overflow:hidden;border-color:#d8e1ed;background:#fbfcfe;box-shadow:0 14px 36px rgba(41,68,105,.075);transition:min-height var(--ncp-duration-base) var(--ncp-ease-out)}.terminal-frame--idle{min-height:340px}.terminal-frame>header{display:flex;height:48px;align-items:center;gap:8px;padding:0 16px;border-bottom:1px solid #dce4ee;background:linear-gradient(180deg,#f8fafc,#f1f5fa);color:#24344d}.terminal-frame>header strong{font-family:var(--ncp-font-mono);font-size:.82rem}.terminal-frame>header small{margin-left:auto;color:#77869b;font-family:var(--ncp-font-mono);font-size:.74rem}.connection-dot{width:8px;height:8px;border-radius:50%;background:#8996a8}.connection-dot--connected{background:#23866f;box-shadow:0 0 0 4px var(--ncp-success-soft)}.connection-dot--connecting{background:var(--ncp-warning)}.connection-dot--error{background:var(--ncp-danger)}.terminal-canvas{position:absolute;inset:60px 13px 13px}.terminal-canvas :deep(.xterm){height:100%;padding:7px}.terminal-placeholder{position:absolute;z-index:2;inset:48px 0 0;display:grid;place-items:center;align-content:center;gap:9px;padding:24px;background:radial-gradient(circle at 50% 15%,rgba(52,116,212,.06),transparent 38%),#fbfcfe;color:#77869b}.terminal-placeholder>svg{padding:11px;border-radius:13px;background:var(--ncp-primary-soft);color:var(--ncp-primary-strong);box-sizing:content-box}.terminal-placeholder strong{color:#24344d;font-size:1rem}.terminal-placeholder span{font-size:.82rem}.terminal-placeholder .action-button{margin-top:8px}@media(max-width:760px){.terminal-toolbar{align-items:stretch;flex-direction:column}.target-tabs button{flex:1}.target-tabs{display:flex}.terminal-toolbar :deep(.el-select){width:100%}.terminal-hint{margin-left:0}.terminal-frame{min-height:calc(100dvh - 330px)}.terminal-frame--idle{min-height:310px}}
.terminal-toolbar :deep(.el-select__input),.terminal-toolbar :deep(.el-select__input-wrapper){border:0!important;outline:0!important;box-shadow:none!important}.terminal-frame{border:1px solid var(--ncp-line);background:#fbfcfe}.terminal-canvas{inset:49px 0 0;padding:12px;background:#fbfcfe}.terminal-canvas :deep(.xterm){height:100%;padding:0;background:#fbfcfe}.terminal-canvas :deep(.xterm-viewport),.terminal-canvas :deep(.xterm-screen),.terminal-canvas :deep(.xterm-screen canvas){background-color:#fbfcfe!important}.terminal-canvas :deep(.xterm-viewport){scrollbar-color:#c7d3e1 transparent}.terminal-canvas :deep(.xterm-helper-textarea){outline:0}.terminal-frame>header{background:#f6f8fb}
.terminal-frame,.terminal-frame--idle{height:clamp(578px,calc(100dvh - 320px),735px);min-height:578px}.terminal-canvas{min-height:0}.terminal-canvas :deep(.xterm-viewport){scrollbar-width:none!important;-ms-overflow-style:none}.terminal-canvas :deep(.xterm-viewport::-webkit-scrollbar){display:none}.terminal-clear-button{margin-left:auto}.terminal-frame>header small{margin-left:0}@media(max-width:760px){.terminal-frame,.terminal-frame--idle{height:calc(100dvh - 275px);min-height:485px}.terminal-clear-button :deep(.action-button__label){display:none}.terminal-clear-button{width:40px;min-width:40px;padding:0}}
.terminal-toolbar-info{display:grid;min-width:0;flex:1;gap:6px}.terminal-hint{margin-left:0;overflow:hidden;color:var(--ncp-text-subtle);font-size:.8rem;text-overflow:ellipsis;white-space:nowrap}.terminal-capabilities{display:flex;min-width:0;flex-wrap:wrap;gap:5px}.capability-chip{display:inline-flex;align-items:center;gap:5px;padding:3px 7px;border:1px solid var(--ncp-line);border-radius:999px;background:#fff;color:var(--ncp-text-muted);font-size:.68rem;line-height:1.2}.capability-chip em{font-style:normal;color:var(--ncp-text-subtle)}.capability-chip--supported{border-color:rgba(35,134,111,.24);background:var(--ncp-success-soft);color:#176c5a}.capability-chip--supported em{color:#23866f}.capability-chip--unsupported{border-color:rgba(201,83,97,.2);background:var(--ncp-danger-soft);color:#9e3948}.capability-chip--unsupported em{color:#c95361}.capability-chip--unknown{border-color:rgba(184,118,34,.22);background:var(--ncp-warning-soft);color:#8c5a18}.capability-chip--unknown em{color:#b87622}.capability-chip--identity{border-color:rgba(52,116,212,.22);background:var(--ncp-primary-soft);color:var(--ncp-primary-strong)}.capability-chip--identity em{color:var(--ncp-primary)}.terminal-focus-state,.terminal-capability-warning,.terminal-live-status{margin:0;color:var(--ncp-text-subtle);font-size:.72rem;line-height:1.4}.terminal-focus-state--active{color:var(--ncp-success-strong)}.terminal-capability-warning{color:#8c5a18}.terminal-live-status{color:var(--ncp-text-muted)}.terminal-help{align-self:flex-start;flex:none;max-width:310px;color:var(--ncp-text-muted);font-size:.74rem}.terminal-help summary{cursor:pointer;padding:8px 6px;border-radius:7px;color:var(--ncp-primary-strong);font-weight:600;list-style-position:inside}.terminal-help summary:focus-visible{outline:2px solid var(--ncp-primary);outline-offset:2px}.terminal-help p{margin:6px 0 8px;line-height:1.5}.terminal-help code{padding:1px 4px;border-radius:4px;background:var(--ncp-surface-quiet);font-family:var(--ncp-font-mono);font-size:.7rem}.terminal-help ul{display:grid;gap:5px;margin:0;padding:0;list-style:none}.terminal-help li{display:flex;align-items:baseline;gap:7px}.terminal-help kbd{flex:none;padding:2px 5px;border:1px solid var(--ncp-line);border-bottom-width:2px;border-radius:4px;background:#fff;color:var(--ncp-text);font-family:var(--ncp-font-mono);font-size:.66rem}.terminal-help li span{line-height:1.35}.paste-confirmation{position:absolute;z-index:5;top:49px;right:0;left:0;display:flex;align-items:center;justify-content:space-between;gap:18px;padding:14px 18px;border-bottom:1px solid rgba(184,118,34,.28);background:linear-gradient(135deg,#fffaf0,#fff);box-shadow:0 10px 22px rgba(85,65,32,.12)}.paste-confirmation-copy{min-width:0}.paste-confirmation-copy strong{display:block;color:#6d4815;font-size:.86rem}.paste-confirmation-copy p{margin:4px 0 0;color:#8c6b36;font-size:.75rem;line-height:1.45}.paste-confirmation-actions{display:flex;flex:none;gap:8px}.paste-confirmation-actions button{min-height:34px;padding:0 12px;border-radius:7px;font:inherit;font-size:.74rem;cursor:pointer}.paste-confirmation-primary{border:1px solid var(--ncp-primary);background:var(--ncp-primary);color:#fff}.paste-confirmation-primary:hover,.paste-confirmation-primary:focus-visible{background:var(--ncp-primary-strong)}.paste-confirmation-secondary{border:1px solid var(--ncp-line);background:#fff;color:var(--ncp-text-muted)}.paste-confirmation-secondary:hover,.paste-confirmation-secondary:focus-visible{border-color:var(--ncp-primary);color:var(--ncp-primary-strong)}.paste-confirmation-actions button:focus-visible{outline:2px solid var(--ncp-primary);outline-offset:2px}.terminal-canvas :deep(.xterm-viewport){overflow-y:scroll!important;scrollbar-width:none!important;-ms-overflow-style:none;touch-action:pan-y;overscroll-behavior:contain;-webkit-overflow-scrolling:touch}.terminal-canvas :deep(.xterm-viewport)::-webkit-scrollbar{display:none;width:0;height:0}.terminal-canvas :deep(.xterm-helper-textarea):focus{outline:0}@media(max-width:900px){.terminal-toolbar{align-items:stretch;flex-wrap:wrap}.terminal-toolbar-info{order:3;flex-basis:100%}.terminal-help{margin-left:auto}}@media(max-width:760px){.terminal-toolbar-info{order:initial}.terminal-help{max-width:none;margin-left:0}.paste-confirmation{align-items:stretch;flex-direction:column;gap:10px}.paste-confirmation-actions{justify-content:flex-end}.terminal-hint{white-space:normal}}
.terminal-canvas :deep(.xterm-scrollable-element > .scrollbar){display:none!important}
.terminal-toolbar-actions{display:flex;flex:0 0 auto;align-items:center;gap:8px;margin-left:auto}.terminal-toolbar>.terminal-live-status{overflow:hidden;max-width:340px;margin:0;color:var(--ncp-warning-strong);font-size:.72rem;line-height:1.35;text-overflow:ellipsis;white-space:nowrap}.terminal-shortcut-dialog{display:grid;gap:16px}.terminal-shortcut-dialog>p{margin:0;color:var(--ncp-text-muted);font-size:.8rem;line-height:1.65}.terminal-shortcut-dialog>ul{display:grid;gap:0;margin:0;padding:0;overflow:hidden;border:1px solid var(--ncp-line);border-radius:12px;list-style:none}.terminal-shortcut-dialog>ul li{display:grid;grid-template-columns:150px minmax(0,1fr);align-items:center;gap:14px;min-height:46px;padding:8px 13px;border-bottom:1px solid var(--ncp-line)}.terminal-shortcut-dialog>ul li:last-child{border-bottom:0}.terminal-shortcut-dialog kbd{width:max-content;padding:3px 7px;border:1px solid var(--ncp-line-strong);border-bottom-width:2px;border-radius:6px;background:var(--ncp-surface-quiet);color:var(--ncp-text);font-family:var(--ncp-font-mono);font-size:.7rem}.terminal-shortcut-dialog li span{color:var(--ncp-text-muted);font-size:.76rem;line-height:1.4}.terminal-capability-details{overflow:hidden;border:1px solid var(--ncp-line);border-radius:12px}.terminal-capability-details summary{min-height:42px;padding:0 13px;cursor:pointer;color:var(--ncp-primary-strong);font-size:.76rem;font-weight:720;line-height:42px}.terminal-capability-details .terminal-capabilities{padding:0 13px 12px}.terminal-capability-details>p{margin:0;padding:0 13px 12px;color:var(--ncp-text-subtle);font-size:.72rem;line-height:1.45}@media(max-width:760px){.terminal-toolbar-actions{width:100%;margin-left:0}.terminal-toolbar-actions>.action-button{flex:1}.terminal-toolbar>.terminal-live-status{max-width:none;white-space:normal}.terminal-shortcut-dialog>ul li{grid-template-columns:1fr;gap:5px}}
</style>
